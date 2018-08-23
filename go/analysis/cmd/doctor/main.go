// The doctor command is prototype of an alternative to the vet tool
// that runs static checkers conforming to the
// golang.org/x/tools/go/analysis API. It is a drop-in replacement for
// vet that you can (and must) run using this command:
//
//   $ go vet -vettool $(which doctor)
//
// The tentative plan for migration is to move all this logic into the
// vet executable (using a vendored copy of the analysis package and all
// the checkers), and use an environment variable to switch between
// classic vet and doctor, initially defaulting to classic vet until we
// have ironed out any bugs. At that point we will change the default to
// doctor, and some time later remove the switch and all the logic for
// classic vet.
//
package main

// Open questions:
// - migration strategy:
//   create a temp (1 month) fork of vet (as doctor) and all its plugins
//   in subpackages, using a vendored API.
//   "go vet" is the only way to run it.  go tool vet will break.
//   Measurements show that breaking vet into separate checkers is not slower
//   if we use the inspector package for traversal.
// - how should "go vet" pass flags through to doctor?
// - with gccgo, go build does not build standard library,
//   so we will not get to analyze it. Yet we must, to create lemmas
//   for eg. printf.
// - if vet's checkers are to live in x/tools and be vendored into cmd/vet,
//   how do we deal with version skew?

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/plugin/deadcode"
	"golang.org/x/tools/go/analysis/plugin/lostcancel"
	"golang.org/x/tools/go/analysis/plugin/makecap"
	"golang.org/x/tools/go/analysis/plugin/nilness"
	"golang.org/x/tools/go/analysis/plugin/pkglemma"
	"golang.org/x/tools/go/analysis/plugin/printf"
	"golang.org/x/tools/go/analysis/plugin/vet"
	"golang.org/x/tools/go/types/objectpath"
)

// analyses is the list of analyses to run.
var analyses = append([]*analysis.Analysis{
	deadcode.Analysis,
	lostcancel.Analysis,
	makecap.Analysis,
	nilness.Analysis,
	pkglemma.Analysis,
	printf.Analysis,
}, vet.Analyses...)

func main() {
	log.SetFlags(0)
	log.SetPrefix("doctor: ")

	if err := analysis.Validate(analyses); err != nil {
		log.Fatal(err)
	}

	// TODO: "go vet" has a hardwired list of flags that it passes
	// through to vet. Obviously that list is completely wrong for
	// the set of checkers above. We need to either pass all flags
	// through from go to this command, or have the go tool query
	// this command for the set of analyses and their flags.

	if len(os.Args) < 2 {
		log.Fatalf("invalid command (want -V=full or .cfg file)")
	}

	// Comply with the -V protocol required by the build system.
	// TODO: eventually we can simply call objabi.AddVersionFlag().
	if os.Args[1] == "-V=full" {
		// Print the tool version so the build system can track changes.
		// Formats:
		//   $progname version devel ... buildID=...
		//   $progname version go1.9.1
		f, err := os.Open(os.Args[0])
		if err != nil {
			log.Fatal(err)
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			log.Fatal(err)
		}
		f.Close()
		fmt.Printf("%s version devel comments-go-here buildID=%02x\n",
			os.Args[0], string(h.Sum(nil)))
		return
	}

	if !strings.HasSuffix(os.Args[1], ".cfg") {
		log.Fatalf("expected *.cfg argument (args=%q)", os.Args)
	}

	// Read the config file.
	data, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	var cfg struct {
		Compiler                  string
		Dir                       string
		ImportPath                string
		GoFiles                   []string
		ImportMap                 map[string]string
		PackageFile               map[string]string
		Standard                  map[string]bool
		PackageVetx               map[string]string
		VetxOnly                  bool
		VetxOutput                string
		SucceedOnTypecheckFailure bool
	}
	if false {
		fmt.Printf("%s\n", data)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatal(err)
	}
	if len(cfg.GoFiles) == 0 {
		// The go command disallows packages with no files.
		// The only exception is unsafe, but the go command
		// doesn't seem to call vet on it.
		log.Fatalf("package has no files: %s", cfg.ImportPath)
	}

	// Load, parse, typecheck.
	// Parallelism makes little difference to parsing.
	// Package net (dozens of files) takes around 40ms either way.
	fset := token.NewFileSet()
	var files []*ast.File

	for _, name := range cfg.GoFiles {
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			if cfg.SucceedOnTypecheckFailure {
				os.Exit(0)
			}
			log.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Scopes:     make(map[ast.Node]*types.Scope),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	compilerImporter := importer.For(cfg.Compiler, func(path string) (io.ReadCloser, error) {
		// path is a resolved package path, not an import path.
		file, ok := cfg.PackageFile[path]
		if !ok {
			if cfg.Compiler == "gccgo" && cfg.Standard[path] {
				return nil, nil // fall back to default gccgo lookup
			}
			return nil, fmt.Errorf("no package file for %q", path)
		}
		return os.Open(file)
	})
	importer := importerFunc(func(importPath string) (*types.Package, error) {
		path, ok := cfg.ImportMap[importPath] // resolve vendoring, etc
		if !ok {
			return nil, fmt.Errorf("can't resolve import %q", path)
		}
		return compilerImporter.Import(path)
	})
	tc := &types.Config{
		Importer: importer,
		Sizes:    types.SizesFor("gc", build.Default.GOARCH), // assume gccgo ≡ gc?
	}
	pkg, err := tc.Check(cfg.ImportPath, fset, files, info)
	if err != nil {
		if cfg.SucceedOnTypecheckFailure {
			os.Exit(0)
		}
		log.Fatal(err)
	}

	// Register all LemmaTypes with encoding/gob.
	// In VetxOnly mode, analyses are only for their lemmas,
	// so we can skip any analysis that neither produces lemmas
	// nor depends on any analysis that produces lemmas.
	// Also build a map to hold working state and result.
	type action struct {
		once       sync.Once
		output     interface{}
		usesLemmas bool
	}
	actions := make(map[*analysis.Analysis]*action)
	var registerLemmas func(a *analysis.Analysis) bool
	registerLemmas = func(a *analysis.Analysis) bool {
		act, ok := actions[a]
		if !ok {
			act = new(action)
			var usesLemmas bool
			for _, lt := range a.LemmaTypes {
				gob.Register(reflect.Zero(lt).Interface())
				usesLemmas = true
			}
			for _, req := range a.Requires {
				if registerLemmas(req) {
					usesLemmas = true
				}
			}
			act.usesLemmas = usesLemmas
			actions[a] = act
		}
		return act.usesLemmas
	}
	var filtered []*analysis.Analysis
	for _, a := range analyses {
		if registerLemmas(a) || !cfg.VetxOnly {
			filtered = append(filtered, a)
		}
	}
	analyses := filtered

	// Read lemmas from imported packages.
	lemmas := readLemmas(cfg.PackageVetx, pkg)

	// In parallel, execute the DAG of analyses.
	var exec func(a *analysis.Analysis) interface{}
	var execAll func(analyses []*analysis.Analysis)
	exec = func(a *analysis.Analysis) interface{} {
		act := actions[a]
		act.once.Do(func() {
			execAll(a.Requires) // prefetch dependencies in parallel

			// The inputs to this analysis are the
			// outputs of its prerequisites.
			inputs := make(map[*analysis.Analysis]interface{})
			for _, req := range a.Requires {
				inputs[req] = exec(req)
			}

			unit := &analysis.Unit{
				Analysis: a,
				Fset:     fset,
				Syntax:   files,
				Pkg:      pkg,
				Info:     info,
				Inputs:   inputs,
				ObjectLemma: func(obj types.Object, l analysis.Lemma) bool {
					checkLemma(a, l)
					return lemmas.getObj(obj, l)
				},
				SetObjectLemma: func(obj types.Object, l analysis.Lemma) {
					checkLemma(a, l)
					lemmas.setObj(obj, l)
				},
				PackageLemma: func(pkg *types.Package, l analysis.Lemma) bool {
					checkLemma(a, l)
					return lemmas.getPkg(pkg, l)
				},
				SetPackageLemma: func(l analysis.Lemma) {
					checkLemma(a, l)
					lemmas.setPkg(l)
				},
			}

			t0 := time.Now()
			if err := a.Run(unit); err != nil {
				log.Fatal(err)
			}
			if false {
				log.Printf("analysis %s = %s", unit, time.Since(t0))
			}

			for _, f := range unit.Findings {
				fmt.Printf("%s: %s\n", fset.Position(f.Pos), f.Message)
			}

			act.output = unit.Output
		})
		return act.output
	}
	execAll = func(analyses []*analysis.Analysis) {
		var wg sync.WaitGroup
		for _, a := range analyses {
			wg.Add(1)
			go func(a *analysis.Analysis) {
				exec(a)
				wg.Done()
			}(a)
		}
		wg.Wait()
	}

	execAll(analyses)

	writeLemmas(cfg.VetxOutput, lemmas)

	// TODO: also execute special analyses: build tags, asmdecl?
}

// ---- lemma support ----

// gobLemma is the Gob declaratation of a serialized lemma.
type gobLemma struct {
	Object  objectpath.Path // path of object relative to package scope (object lemmas only)
	PkgPath string          // path of package (package lemmas only)
	Lemma   analysis.Lemma  // type and value of user-defined Lemma
}

type lemmaSet struct {
	pkg *types.Package
	mu  sync.Mutex
	m   map[lemmaKey]analysis.Lemma
}

type lemmaKey struct {
	obj     types.Object // (object lemmas only)
	pkgpath string       // (package lemmas only)
	t       reflect.Type
}

func (ls *lemmaSet) getObj(obj types.Object, ptr analysis.Lemma) bool {
	if obj == nil {
		panic("nil object")
	}
	key := lemmaKey{obj: obj, t: reflect.TypeOf(ptr)}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if v, ok := ls.m[key]; ok {
		reflect.ValueOf(ptr).Elem().Set(reflect.ValueOf(v).Elem())
		return true
	}
	return false
}

func (ls *lemmaSet) setObj(obj types.Object, lemma analysis.Lemma) {
	if obj.Pkg() != ls.pkg {
		log.Panicf("in package %s: SetLemma(%s, %T): can't set lemma on object belonging another package",
			ls.pkg, obj, lemma)
	}
	key := lemmaKey{obj: obj, t: reflect.TypeOf(lemma)}
	ls.mu.Lock()
	ls.m[key] = lemma // clobber any existing entry
	ls.mu.Unlock()
}

func (ls *lemmaSet) getPkg(pkg *types.Package, ptr analysis.Lemma) bool {
	if pkg == nil {
		panic("nil package")
	}
	key := lemmaKey{pkgpath: pkg.Path(), t: reflect.TypeOf(ptr)}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if v, ok := ls.m[key]; ok {
		reflect.ValueOf(ptr).Elem().Set(reflect.ValueOf(v).Elem())
		return true
	}
	return false
}

func (ls *lemmaSet) setPkg(lemma analysis.Lemma) {
	key := lemmaKey{pkgpath: ls.pkg.Path(), t: reflect.TypeOf(lemma)}
	ls.mu.Lock()
	ls.m[key] = lemma // clobber any existing entry
	ls.mu.Unlock()
}

func checkLemma(a *analysis.Analysis, lemma analysis.Lemma) {
	t := reflect.TypeOf(lemma)

	// Linear scan is fine:
	// the list is typically much smaller than a map bucket.
	for _, lt := range a.LemmaTypes {
		if lt == t {
			// TODO: assert that lemma *T pointer is non-nil.
			return // ok
		}
	}
	// TODO: give a more specific error for off-by-one-pointer.
	log.Panicf("internal error: type %s is not a Lemma type of analysis %s (LemmaTypes=%v)",
		t, a, a.LemmaTypes)
}

func readLemmas(inputFiles map[string]string, pkg *types.Package) *lemmaSet {
	// Read lemmas from imported packages.
	// Lemmas may describe indirectly imported packages, or their objects.
	m := make(map[lemmaKey]analysis.Lemma) // one big bucket
	for _, imp := range pkg.Imports() {
		filename, ok := inputFiles[imp.Path()]
		if !ok {
			continue // empty lemma files are discarded (TODO: check this)
		}
		data, err := ioutil.ReadFile(filename)
		if err != nil {
			log.Fatalf("reading vetx file for %s: %v", imp.Path(), err)
		}
		var lemmas []gobLemma
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&lemmas); err != nil {
			log.Fatalf("decoding vetx file %s for %s: %v", imp.Path(), filename, err)
		}
		for _, l := range lemmas {
			key := lemmaKey{t: reflect.TypeOf(l.Lemma)}
			if l.PkgPath != "" {
				if false {
					fmt.Printf("from %s read %T lemma %s for %s\n",
						imp.Path(), l.Lemma, l.Lemma, l.PkgPath)
				}
				key.pkgpath = l.PkgPath
			} else {
				obj, err := objectpath.FindObject(imp, l.Object)
				if err != nil {
					// mostly likely due to unexported object.
					// TODO: audit for other possibilities.
					// log.Printf("no object for path: %v", err)
					continue
				}
				if false {
					fmt.Printf("from %s read %T lemma %s for %s\n",
						imp.Path(), l.Lemma, l.Lemma, obj)
				}
				key.obj = obj
			}
			m[key] = l.Lemma
		}
	}

	return &lemmaSet{pkg: pkg, m: m}
}

func writeLemmas(filename string, ls *lemmaSet) {
	// Gather all lemmas, including those from imported packages.
	var gobLemmas []gobLemma

	ls.mu.Lock()
	for k, lemma := range ls.m {
		if false {
			log.Printf("%#v => %s\n", k, lemma)
		}
		if k.obj != nil {
			path, err := objectpath.Of(k.obj)
			if err != nil {
				continue // object not accessible from package API; discard lemma
			}
			gobLemmas = append(gobLemmas, gobLemma{
				Object: path,
				Lemma:  lemma,
			})
		} else {
			gobLemmas = append(gobLemmas, gobLemma{
				PkgPath: k.pkgpath,
				Lemma:   lemma,
			})
		}
	}
	ls.mu.Unlock()

	// Sort lemmas by (object, type) for determinism.
	sort.Slice(gobLemmas, func(i, j int) bool {
		x, y := gobLemmas[i], gobLemmas[j]
		if x.Object != y.Object {
			return x.Object < y.Object
		}
		tx := reflect.TypeOf(x.Lemma)
		ty := reflect.TypeOf(y.Lemma)
		if tx != ty {
			return tx.String() < ty.String()
		}
		return false // equal
	})

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(gobLemmas); err != nil {
		log.Fatal(err)
	}
	if err := ioutil.WriteFile(filename, buf.Bytes(), 0666); err != nil {
		log.Fatal(err)
	}
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

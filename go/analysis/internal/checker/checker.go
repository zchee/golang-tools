// Package checker defines the implementation of the checker commands.
// The same code drives the multi-analysis driver, the single-analysis
// driver that is conventionally provided for convenience along with
// each analysis package, and the test driver.
package checker

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"flag"
	"fmt"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

var (
	JSON = false

	// Debug is a set of single-letter flags:
	//
	//	l	show [l]emmas as they are created
	// 	p	disable [p]arallel execution of analyses
	//	s	do additional [s]anity checks on lemma types and serialization
	//	t	show [t]iming info (NB: use 'p' flag to avoid GC/scheduler noise)
	//	v	show [v]erbose logging
	//
	Debug = ""

	Context = -1 // if >=0, display offending line plus this many lines of context

	// Log files for optional performance tracing.
	CPUProfile, MemProfile, Trace string
)

// RegisterFlags registers command-line flags used the analysis driver.
func RegisterFlags() {
	flag.BoolVar(&JSON, "json", JSON, "emit JSON output")
	flag.StringVar(&Debug, "debug", Debug, `debug flags, any subset of "lpsv"`)
	flag.IntVar(&Context, "c", Context, `display offending line with this many lines of context`)

	flag.StringVar(&CPUProfile, "cpuprofile", "", "write CPU profile to this file")
	flag.StringVar(&MemProfile, "memprofile", "", "write memory profile to this file")
	flag.StringVar(&Trace, "trace", "", "write trace log to this file")
}

// Run loads the packages specified by args using go/packages,
// then applies the specified analyses to them.
// Analysis flags must already have been set.
// It provides most of the logic for the main functions of both the
// singlechecker and the multi-analysis commands.
func Run(args []string, analyses []*analysis.Analysis) error {

	if CPUProfile != "" {
		f, err := os.Create(CPUProfile)
		if err != nil {
			log.Fatal(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal(err)
		}
		// NB: profile won't be written in case of error.
		defer pprof.StopCPUProfile()
	}

	if Trace != "" {
		f, err := os.Create(Trace)
		if err != nil {
			log.Fatal(err)
		}
		if err := trace.Start(f); err != nil {
			log.Fatal(err)
		}
		// NB: trace log won't be written in case of error.
		defer func() {
			trace.Stop()
			log.Printf("To view the trace, run:\n$ go tool trace view %s", Trace)
		}()
	}

	if MemProfile != "" {
		f, err := os.Create(MemProfile)
		if err != nil {
			log.Fatal(err)
		}
		// NB: memprofile won't be written in case of error.
		defer func() {
			runtime.GC() // get up-to-date statistics
			if err := pprof.WriteHeapProfile(f); err != nil {
				log.Fatalf("Writing memory profile: %v", err)
			}
			f.Close()
		}()
	}

	// Load the packages.
	if dbg('v') {
		log.SetPrefix("")
		log.SetFlags(log.Lmicroseconds) // display timing
		log.Printf("load %s", args)
	}

	// Optimization: if the selected analyses don't produce/consume
	// lemmas, we need source only for the initial packages.
	allSyntax := needLemmas(analyses)
	initial, err := load(args, allSyntax)
	if err != nil {
		return err
	}

	roots := analyze(initial, analyses)

	// Print the results.
	printFindings(roots)

	return nil
}

// load loads the initial packages.
func load(patterns []string, allSyntax bool) ([]*packages.Package, error) {
	mode := packages.LoadSyntax
	if allSyntax {
		mode = packages.LoadAllSyntax
	}
	conf := packages.Config{
		Mode:  mode,
		Tests: true,
	}
	initial, err := packages.Load(&conf, patterns...)
	if err == nil {
		if n := packages.PrintErrors(initial); n > 0 {
			err = fmt.Errorf("%d errors during loading", n) // TODO: "1 error"
		}
	}
	return initial, err
}

// Analyze applies an analysis to a package (and their dependencies if
// necessary) and returns the graph of results.
//
// Analyze uses only the following fields of packages.Package:
// Imports, Fset, Syntax, Types, TypesInfo, IllTyped.
//
// It is exposed for use in testing.
func Analyze(pkg *packages.Package, a *analysis.Analysis) (*analysis.Unit, error) {
	act := analyze([]*packages.Package{pkg}, []*analysis.Analysis{a})[0]
	return act.unit, act.err
}

func analyze(pkgs []*packages.Package, analyses []*analysis.Analysis) []*action {
	// Construct the action graph.
	if dbg('v') {
		log.Printf("building graph of analysis units")
	}

	// Each graph node (action) is one unit of analysis.
	// Edges express package-to-package (vertical) dependencies,
	// and analysis-to-analysis (horizontal) dependencies.
	type key struct {
		*analysis.Analysis
		*packages.Package
	}
	actions := make(map[key]*action)

	var mkAction func(a *analysis.Analysis, pkg *packages.Package) *action
	mkAction = func(a *analysis.Analysis, pkg *packages.Package) *action {
		k := key{a, pkg}
		act, ok := actions[k]
		if !ok {
			act = &action{a: a, pkg: pkg}

			// Add a dependency on each required analyses.
			for _, req := range a.Requires {
				act.deps = append(act.deps, mkAction(req, pkg))
			}

			// An analysis that consumes/produces lemmas
			// must run on the package's dependencies too.
			if len(a.LemmaTypes) > 0 {
				paths := make([]string, 0, len(pkg.Imports))
				for path := range pkg.Imports {
					paths = append(paths, path)
				}
				sort.Strings(paths) // for determinism
				for _, path := range paths {
					dep := mkAction(a, pkg.Imports[path])
					act.deps = append(act.deps, dep)
				}
			}

			actions[k] = act
		}
		return act
	}

	// Build nodes for initial packages.
	var roots []*action
	for _, a := range analyses {
		for _, pkg := range pkgs {
			root := mkAction(a, pkg)
			root.isroot = true
			roots = append(roots, root)
		}
	}

	// Execute the graph in parallel.
	execAll(roots)

	return roots
}

// printFindings prints the findings for the root packages in either
// plain text or JSON format. JSON format also includes errors for any
// dependencies.
func printFindings(roots []*action) {
	// Print the output.
	//
	// Print findings only for root packages,
	// but errors for all packages.
	printed := make(map[*action]bool)
	var print func(*action)
	var visitAll func(actions []*action)
	visitAll = func(actions []*action) {
		for _, act := range actions {
			if !printed[act] {
				printed[act] = true
				visitAll(act.deps)
				print(act)
			}
		}
	}

	if JSON {
		// TODO: What should the toplevel keys be, exactly? PkgPath? Package.ID?
		// Should we denormalize the findings into flat tuples,
		// (pkg, analysis, posn, message)?
		// TODO: save timing info?
		tree := make(map[string]map[string]interface{}) // ID -> analysis -> result

		print = func(act *action) {
			m, existing := tree[act.pkg.ID]
			if !existing {
				m = make(map[string]interface{})
				// Insert m into tree later iff non-empty.
			}
			if act.err != nil {
				type jsonError struct {
					Err string `json:"error"`
				}
				m[act.a.Name] = jsonError{act.err.Error()}
			} else if act.isroot {
				type jsonFinding struct {
					Category string `json:"category,omitempty"`
					Posn     string `json:"posn"`
					Message  string `json:"message"`
				}
				var findings []jsonFinding
				for _, f := range act.unit.Findings {
					findings = append(findings, jsonFinding{
						Category: f.Category,
						Posn:     act.pkg.Fset.Position(f.Pos).String(),
						Message:  f.Message,
					})
				}
				if findings != nil {
					m[act.a.Name] = findings
				}
			}
			if !existing && len(m) > 0 {
				tree[act.pkg.ID] = m
			}
		}
		visitAll(roots)

		data, err := json.MarshalIndent(tree, "", "\t")
		if err != nil {
			log.Panicf("internal error: JSON marshalling failed: %v", err)
		}
		os.Stdout.Write(data)
		fmt.Println()
	} else {
		// plain text output

		// De-duplicate findings by position (not token.Pos) to
		// avoid double-reporting in source files that belong to
		// multiple packages, such as foo and foo.test.
		type key struct {
			token.Position
			*analysis.Analysis
			message, class string
		}
		seen := make(map[key]bool)

		print = func(act *action) {
			if act.err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", act.a.Name, act.err)
				return
			}
			if act.isroot {
				for _, f := range act.unit.Findings {
					class := act.a.Name
					if f.Category != "" {
						class += "." + f.Category
					}
					posn := act.pkg.Fset.Position(f.Pos)

					k := key{posn, act.a, class, f.Message}
					if seen[k] {
						continue // duplicate
					}
					seen[k] = true

					fmt.Printf("%s: [%s] %s\n", posn, class, f.Message)

					// -c=0: show offending line of code in context.
					if Context >= 0 {
						data, _ := ioutil.ReadFile(posn.Filename)
						lines := strings.Split(string(data), "\n")
						for i := posn.Line - Context; i <= posn.Line+Context; i++ {
							if 1 <= i && i <= len(lines) {
								fmt.Printf("%d\t%s\n", i, lines[i-1])
							}
						}
					}
				}
			}
		}
		visitAll(roots)
	}

	// Print timing info.
	if dbg('t') {
		if !dbg('p') {
			log.Println("Warning: times are mostly GC/scheduler noise; use -debug=tp to disable parallelism")
		}
		var all []*action
		var total time.Duration
		for act := range printed {
			all = append(all, act)
			total += act.duration
		}
		sort.Slice(all, func(i, j int) bool {
			return all[i].duration > all[j].duration
		})

		// Print actions accounting for 90% of the total.
		var sum time.Duration
		for _, act := range all {
			fmt.Fprintf(os.Stderr, "%s\t%s\n", act.duration, act)
			sum += act.duration
			if sum >= total*9/10 {
				break
			}
		}
	}
}

// needLemmas reports whether any analysis required by the specified set
// needs lemmas.  If so, we must load the entire program from source.
func needLemmas(analyses []*analysis.Analysis) bool {
	seen := make(map[*analysis.Analysis]bool)
	var q []*analysis.Analysis // for BFS
	q = append(q, analyses...)
	for len(q) > 0 {
		a := q[0]
		q = q[1:]
		if !seen[a] {
			seen[a] = true
			if len(a.LemmaTypes) > 0 {
				return true
			}
			q = append(q, a.Requires...)
		}
	}
	return false
}

// An action represents one unit of analysis work: the application of
// one analysis to one package. Actions form a DAG, both within a
// package (as different analyses are applied, either in sequence or
// parallel), and across packages (as dependencies are analyzed).
type action struct {
	once      sync.Once
	a         *analysis.Analysis
	pkg       *packages.Package
	unit      *analysis.Unit
	isroot    bool
	deps      []*action
	objLemmas []map[types.Object]analysis.Lemma   // indexed like a.LemmaTypes
	pkgLemmas []map[*types.Package]analysis.Lemma // indexed like a.LemmaTypes
	inputs    map[*analysis.Analysis]interface{}
	err       error
	duration  time.Duration
}

func (act *action) String() string {
	return fmt.Sprintf("%s@%s", act.a, act.pkg)
}

func execAll(actions []*action) {
	sequential := dbg('p')
	var wg sync.WaitGroup
	for _, act := range actions {
		wg.Add(1)
		work := func(act *action) {
			act.exec()
			wg.Done()
		}
		if sequential {
			work(act)
		} else {
			go work(act)
		}
	}
	wg.Wait()
}

func (act *action) exec() { act.once.Do(act.execOnce) }

func (act *action) execOnce() {
	// Analyze dependencies.
	execAll(act.deps)

	ctx, task := trace.NewTask(context.Background(), "exec")
	defer task.End()
	trace.Log(ctx, "unit", act.String())

	// Record time spent in this node but not its dependencies.
	// In parallel mode, due to GC/scheduler contention, the
	// time is 5x higher than in sequential mode, even with a
	// semaphore limiting the number of threads here.
	// So use -debug=tp.
	if dbg('t') {
		t0 := time.Now()
		defer func() { act.duration = time.Since(t0) }()
	}

	// Report an error if any dependency failed.
	var failed []string
	for _, dep := range act.deps {
		if dep.err != nil {
			failed = append(failed, dep.String())
		}
	}
	if failed != nil {
		sort.Strings(failed)
		act.err = fmt.Errorf("failed prerequisites: %s", strings.Join(failed, ", "))
		return
	}

	// Plumb the output values of the dependencies
	// into the inputs of this action.  Also lemmas.
	inputs := make(map[*analysis.Analysis]interface{})
	act.objLemmas = make([]map[types.Object]analysis.Lemma, len(act.a.LemmaTypes))
	act.pkgLemmas = make([]map[*types.Package]analysis.Lemma, len(act.a.LemmaTypes))
	for _, dep := range act.deps {
		if dep.pkg == act.pkg {
			// Same package, different analysis (horizontal edge):
			// in-memory outputs of prerequisite analyses
			// become inputs to this analysis unit.
			inputs[dep.a] = dep.unit.Output

		} else if dep.a == act.a { // (always true)
			// Same analysis, different package (vertical edge):
			// serialized lemmas produced by prerequisite analysis
			// become available to this analysis unit.
			inheritLemmas(act, dep)
		}
	}

	// Run the analysis.
	unit := &analysis.Unit{
		Analysis:        act.a,
		Fset:            act.pkg.Fset,
		Syntax:          act.pkg.Syntax,
		Pkg:             act.pkg.Types,
		Info:            act.pkg.TypesInfo,
		Inputs:          inputs,
		ObjectLemma:     act.objLemma,
		SetObjectLemma:  act.setObjLemma,
		PackageLemma:    act.pkgLemma,
		SetPackageLemma: act.setPkgLemma,
	}
	act.unit = unit

	var err error
	if act.pkg.IllTyped && !unit.Analysis.RunDespiteErrors {
		err = fmt.Errorf("analysis skipped due to errors in package")
	} else {
		err = unit.Analysis.Run(unit)
		if err == nil {
			if got, want := reflect.TypeOf(unit.Output), unit.Analysis.OutputType; got != want {
				err = fmt.Errorf(
					"internal error: on package %s, analysis %s produced Output %v, but declared OutputType %v",
					unit.Pkg.Path(), unit.Analysis, got, want)
			}
		}
	}
	act.err = err

	// disallow calls after Run
	unit.SetObjectLemma = nil
	unit.SetPackageLemma = nil
}

// inheritLemmas populates act.lemmas with
// those it obtains from its dependency, dep.
func inheritLemmas(act, dep *action) {
	serialize := dbg('s')

	for i := range dep.a.LemmaTypes {
		for obj, lemma := range dep.objLemmas[i] {
			// Filter out lemmas related to objects
			// that are irrelevant downstream
			// (equivalently: not in the compiler export data).
			if !exportedFrom(obj, dep.pkg.Types) {
				if false {
					log.Printf("%v: discarding %T lemma from %s for %s: %s", act, lemma, dep, obj, lemma)
				}
				continue
			}

			// Optionally serialize/deserialize lemma
			// to verify that it works across address spaces.
			if serialize {
				var err error
				lemma, err = codeLemma(lemma)
				if err != nil {
					log.Panicf("internal error: encoding of %T lemma failed in %v", lemma, act)
				}
			}

			if false {
				log.Printf("%v: inherited %T lemma for %s: %s", act, lemma, obj, lemma)
			}
			m := act.objLemmas[i]
			if m == nil {
				m = make(map[types.Object]analysis.Lemma)
				act.objLemmas[i] = m
			}
			m[obj] = lemma
		}

		for pkg, lemma := range dep.pkgLemmas[i] {
			// TODO: filter out lemmas that belong to
			// packages not mentioned in the export data
			// to prevent side channels.

			// Optionally serialize/deserialize lemma
			// to verify that it works across address spaces.
			if serialize {
				var err error
				lemma, err = codeLemma(lemma)
				if err != nil {
					log.Panicf("internal error: encoding of %T lemma failed in %v", lemma, act)
				}
			}

			if false {
				log.Printf("%v: inherited %T lemma for %s: %s", act, lemma, pkg, lemma)
			}
			m := act.pkgLemmas[i]
			if m == nil {
				m = make(map[*types.Package]analysis.Lemma)
				act.pkgLemmas[i] = m
			}
			m[pkg] = lemma
		}
	}
}

// codeLemma encodes then decodes a lemma,
// just to exercise that logic.
func codeLemma(lemma analysis.Lemma) (analysis.Lemma, error) {
	// We encode lemmas one at a time.
	// A real modular driver would emit all lemmas
	// into one encoder to improve gob efficiency.
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(lemma); err != nil {
		return nil, err
	}
	new := reflect.New(reflect.TypeOf(lemma).Elem()).Interface().(analysis.Lemma)
	if err := gob.NewDecoder(&buf).Decode(new); err != nil {
		return nil, err
	}
	return new, nil
}

// exportedFrom reports whether obj may be visible to a package that imports pkg.
// This includes not just the exported members of pkg, but also unexported
// constants, types, fields, and methods, perhaps belonging to oether packages,
// that find there way into the API.
// This is an overapproximation of the more accurate approach used by
// gc export data, which walks the type graph, but it's much simpler.
//
// TODO: walk the type graph.
func exportedFrom(obj types.Object, pkg *types.Package) bool {
	switch obj := obj.(type) {
	case *types.Func:
		return obj.Exported() && obj.Pkg() == pkg ||
			obj.Type().(*types.Signature).Recv() != nil
	case *types.Var:
		return obj.Exported() && obj.Pkg() == pkg ||
			obj.IsField()
	case *types.TypeName, *types.Const:
		return true
	}
	return false // Nil, Builtin, Label, or PkgName
}

// objLemma implements Analysis.ObjectLemma.
// Given a non-nil pointer ptr of type *T, where *T satisfies Lemma,
// lemma copies the lemma value to *ptr.
func (act *action) objLemma(obj types.Object, ptr analysis.Lemma) bool {
	if obj == nil {
		panic("nil object")
	}
	i := lemmaIndex(act.a, ptr)
	if v, ok := act.objLemmas[i][obj]; ok {
		reflect.ValueOf(ptr).Elem().Set(reflect.ValueOf(v).Elem())
		return true
	}
	return false
}

// setObjLemma implements Analysis.SetObjectLemma.
func (act *action) setObjLemma(obj types.Object, lemma analysis.Lemma) {
	if act.unit.SetObjectLemma == nil {
		log.Panicf("%s: Unit.SetObjectLemma(%s, %T) called after Run", act, obj, lemma)
	}

	if obj.Pkg() != act.pkg.Types {
		log.Panicf("internal error: in analysis %s of package %s: Lemma.Set(%s, %T): can't set lemmas on objects belonging another package",
			act.a, act.pkg, obj, lemma)
	}

	// TODO: assert that lemma contains a non-nil *T pointer.

	i := lemmaIndex(act.a, lemma)
	m := act.objLemmas[i]
	if m == nil {
		m = make(map[types.Object]analysis.Lemma)
		act.objLemmas[i] = m
	}
	m[obj] = lemma // clobber any existing entry
	if dbg('l') {
		objstr := types.ObjectString(obj, (*types.Package).Name)
		log.Printf("lemma %#v on %s", lemma, objstr)
	}
}

// pkgLemma implements Analysis.PackageLemma.
// Given a non-nil pointer ptr of type *T, where *T satisfies Lemma,
// lemma copies the lemma value to *ptr.
func (act *action) pkgLemma(pkg *types.Package, ptr analysis.Lemma) bool {
	if pkg == nil {
		panic("nil package")
	}
	i := lemmaIndex(act.a, ptr)
	if v, ok := act.pkgLemmas[i][pkg]; ok {
		reflect.ValueOf(ptr).Elem().Set(reflect.ValueOf(v).Elem())
		return true
	}
	return false
}

// setPkgLemma implements Analysis.SetPackageLemma.
func (act *action) setPkgLemma(lemma analysis.Lemma) {
	if act.unit.SetPackageLemma == nil {
		log.Panicf("%s: Unit.SetPackageLemma(%T) called after Run", act, lemma)
	}

	// TODO: assert that lemma contains a non-nil *T pointer.

	i := lemmaIndex(act.a, lemma)
	m := act.pkgLemmas[i]
	if m == nil {
		m = make(map[*types.Package]analysis.Lemma)
		act.pkgLemmas[i] = m
	}
	m[act.unit.Pkg] = lemma // clobber any existing entry
	if dbg('l') {
		log.Printf("lemma %#v on %s", lemma, act.unit.Pkg)
	}
}

func lemmaIndex(a *analysis.Analysis, lemma analysis.Lemma) int {
	t := reflect.TypeOf(lemma)

	// Linear scan is fine:
	// the list is typically much smaller than a map bucket.
	for i, lt := range a.LemmaTypes {
		if lt == t {
			return i
		}
	}
	// TODO: give a more specific error for off-by-one-pointer.
	log.Panicf("internal error: type %T is not a Lemma type of analysis %s (LemmaTypes=%v)",
		lemma, a, a.LemmaTypes)
	panic("unreachable")
}

func dbg(b byte) bool { return strings.IndexByte(Debug, b) >= 0 }

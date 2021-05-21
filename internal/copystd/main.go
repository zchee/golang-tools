// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command copystd copies packages from GOROOT, amending import paths for
// cross-imports within the package set.
//
// Use it like this:
//  copystd <packages...>
//
// The given set of packages must be complete: copystd is not smart enough to
// figure out additional packages that need to be copied in order to not break
// the build. For example, given import edges A->B->C, if A and C are copied C
// must be as well.
//
// Only .go files and testdata directories are copied.

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const usage = `usage: copystd <packages...>`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	if err := copyStd(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func copyStd(pkgs []string) error {
	srcdir := filepath.Join(runtime.GOROOT(), "src")

	// Step 1: load all package data into memory.
	files := make(map[string][]byte)
	for _, pkg := range pkgs {
		reldir := filepath.FromSlash(pkg)
		dir := filepath.Join(srcdir, reldir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("reading %q: %v", pkg, err)
		}
		// sanity check: ensure that reldir is actually relative.
		// (filepath.Join("/foo/", "/bar") is valid)
		reldir, err = filepath.Rel(srcdir, dir)
		if err != nil {
			return fmt.Errorf("computing relative path for %q: %v", pkg, err)
		}

		for _, entry := range entries {
			filename := filepath.Join(dir, entry.Name())
			relname := filepath.Join(reldir, entry.Name())
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					// Load everything from testdata/: we don't know what we need.
					if err := loadDir(srcdir, filename, files); err != nil {
						return fmt.Errorf("loading %q: %v", relname, err)
					}
				}
				continue
			}
			if !strings.HasSuffix(filename, ".go") {
				continue
			}
			data, err := os.ReadFile(filename)
			if err != nil {
				return fmt.Errorf("reading %q: %v", relname, err)
			}
			files[relname] = data
		}
	}

	// Step 2: compute import path replacements.
	root, err := rootPkg()
	if err != nil {
		return fmt.Errorf("finding root package: %v", err)
	}
	replace := make(map[string]string)
	for _, pkg := range pkgs {
		replace[`"`+pkg+`"`] = `"` + path.Join(root, pkg) + `"`
	}

	// Step 3: rewrite import paths.
	fset := token.NewFileSet()
	for relname, data := range files {
		if strings.Contains(relname, "/testdata/") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Base(relname), data, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parsing %q: %v", relname, err)
		}
		for _, spec := range file.Imports {
			if replacement, ok := replace[spec.Path.Value]; ok {
				spec.Path.Value = replacement
			}
		}
		var buf bytes.Buffer
		if err := format.Node(&buf, fset, file); err != nil {
			return fmt.Errorf("formatting %q: %v", relname, err)
		}
		files[relname] = buf.Bytes()
	}

	// Step 4: write files to the filesystem.
	for relname, data := range files {
		dirname := filepath.Dir(relname)
		fi, err := os.Stat(dirname)
		if os.IsNotExist(err) {
			if err := os.MkdirAll(dirname, 0755); err != nil {
				return fmt.Errorf("making %q: %v", dirname, err)
			}
		} else if err != nil {
			return err
		} else if !fi.IsDir() {
			return fmt.Errorf("path %q is not a directory", dirname)
		}
		if err := os.WriteFile(relname, data, 0644); err != nil {
			return fmt.Errorf("writing %q: %v", relname, err)
		}
	}
	return nil
}

func rootPkg() (string, error) {
	module, err := exec.Command("go", "list").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(module)), nil
}

func loadDir(base, dir string, files map[string][]byte) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		files[rel] = data
		return nil
	})
}

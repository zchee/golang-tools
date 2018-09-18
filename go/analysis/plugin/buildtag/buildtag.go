// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package buildtag checks that +build tags are valid.
//
// It cannot conform to the golang.org/x/tools/go/analysis API because
// it examines Go and non-Go files. TODO: think about that.
package buildtag

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"strings"
	"unicode"
)

// checkBuildTag checks that build tags are in the correct location and well-formed.
// It calls errorFn for each problem it finds.
// It returns an error if it could not read the file.
func Check(filename string, errorFn func(line int, msg string)) error {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}

	var fset *token.FileSet
	var f *ast.File
	if strings.HasSuffix(filename, ".go") {
		fset = token.NewFileSet()
		var err error
		f, err = parser.ParseFile(fset, filename, content, parser.ParseComments)
		if err != nil {
			return err
		}
	}

	// we must look at the raw lines, as build tags may appear in non-Go
	// files such as assembly files.
	lines := bytes.SplitAfter(content, nl)

	// lineWithComment reports whether a line corresponds to a comment in
	// the source file. If the source file wasn't Go, the function always
	// returns true.
	lineWithComment := func(line int) bool {
		if f == nil {
			// Current source file is not Go, so be conservative.
			return true
		}
		for _, group := range f.Comments {
			startLine := fset.Position(group.Pos()).Line
			endLine := fset.Position(group.End()).Line
			if startLine <= line && line <= endLine {
				return true
			}
		}
		return false
	}

	// Determine cutpoint where +build comments are no longer valid.
	// They are valid in leading // comments in the file followed by
	// a blank line.
	var cutoff int
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			cutoff = i
			continue
		}
		if bytes.HasPrefix(line, slashSlash) {
			continue
		}
		break
	}

	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, slashSlash) {
			continue
		}
		if !bytes.Contains(line, plusBuild) {
			// Check that the comment contains "+build" early, to
			// avoid unnecessary lineWithComment calls that may
			// incur linear searches.
			continue
		}
		if !lineWithComment(i + 1) {
			// This is a line in a Go source file that looks like a
			// comment, but actually isn't - such as part of a raw
			// string.
			continue
		}

		badf := func(line int, format string, args ...interface{}) {
			errorFn(line, fmt.Sprintf(format, args...))
		}

		text := bytes.TrimSpace(line[2:])
		if bytes.HasPrefix(text, plusBuild) {
			fields := bytes.Fields(text)
			if !bytes.Equal(fields[0], plusBuild) {
				// Comment is something like +buildasdf not +build.
				badf(i+1, "possible malformed +build comment")
				continue
			}
			if i >= cutoff {
				badf(i+1, "+build comment must appear before package clause and be followed by a blank line")
				continue
			}
			// Check arguments.
		Args:
			for _, arg := range fields[1:] {
				for _, elem := range strings.Split(string(arg), ",") {
					if strings.HasPrefix(elem, "!!") {
						badf(i+1, "invalid double negative in build constraint: %s", arg)
						break Args
					}
					elem = strings.TrimPrefix(elem, "!")
					for _, c := range elem {
						if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '.' {
							badf(i+1, "invalid non-alphanumeric build constraint: %s", arg)
							break Args
						}
					}
				}
			}
			continue
		}
		// Comment with +build but not at beginning.
		if i < cutoff {
			badf(i+1, "possible malformed +build comment")
			continue
		}
	}
	return nil
}

var (
	nl         = []byte("\n")
	slashSlash = []byte("//")
	plusBuild  = []byte("+build")
)

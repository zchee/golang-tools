// Package objectpath defines a stable, deterministic naming scheme for
// types.Objects, that is, named entities in Go programs.
//
// Type-checker objects are canonical, so they are usually identified by
// their address in memory (a pointer), but a pointer has meaning only
// within one address space. By contrast, objectpath names allow the
// identity of a logical object to be sent from one program to another,
// establishing a correspondance between types.Object variables that are
// distinct but logically equivalent.
//
package objectpath

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"go/types"
)

// A Path is an opaque name that identifies a types.Object within its
// enclosing package. Conceptually, the name consists of a sequence of
// destructuring operations applied to a package scope.
type Path string

// Of returns a Path that identifies obj within its package.
//
// Of returns an error if the object is not accessible from its
// enclosing Package's Scope.
// This includes universal names, package names (defined inside a file
// block), and local names (defined inside function bodies).
//
// Example: given this definition,
//
// 	type Foo interface {
// 		Method() (string, func(int) struct{ X int })
// 	}
//
// Of(X) would return a path consisting of the following components:
//
//	Foo.Method.!results.1.!results.0.X
//
func Of(obj types.Object) (Path, error) {
	path, err := pathOf(obj)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	for i, x := range path {
		if i > 0 {
			buf.WriteByte('.')
		}
		fmt.Fprint(&buf, x)
	}
	return Path(buf.String()), nil
}

const (
	opKey        = "!key"        // map key
	opValue      = "!value"      // map value
	opParams     = "!params"     // func parameters
	opResults    = "!results"    // func results
	opUnderlying = "!underlying" // underlying type
)

func pathOf(obj types.Object) ([]interface{}, error) {
	if obj.Pkg() == nil {
		// nil or builtin
		return nil, fmt.Errorf("universal objects have no path: %v", obj)
	}
	scope := obj.Pkg().Scope()

	if scope.Lookup(obj.Name()) == obj {
		return []interface{}{obj.Name()}, nil // found package-level object
	}

	// Since it's not a package-level object, it must be a
	// struct field, concrete method, or interface method.
	// Quickly reject other cases.
	switch obj := obj.(type) {
	case *types.Var:
		if !obj.IsField() {
			return nil, fmt.Errorf("var is not a field: %v", obj)
		}
	case *types.Func:
		if recv := obj.Type().(*types.Signature).Recv(); recv == nil {
			return nil, fmt.Errorf("func is not a method: %v", obj)
		}
		// TODO(adonovan): opt: if the method is concrete,
		// do a specialized version of the rest of this function so
		// that it's O(1) not O(|scope|).  Basically 'find' is needed
		// only for struct fields and interface methods.
	default:
		// pkgname, or local label/const/type
		return nil, fmt.Errorf("not a package-level object, nor a field or method: %v", obj)
	}

	// find finds obj within type T, returning the path to it, or nil if not found.
	var find func(path []interface{}, T types.Type) []interface{}
	find = func(path []interface{}, T types.Type) []interface{} {
		switch T := T.(type) {
		case *types.Basic, *types.Named:
			return nil
		case *types.Pointer:
			return find(path, T.Elem())
		case *types.Slice:
			return find(path, T.Elem())
		case *types.Array:
			return find(path, T.Elem())
		case *types.Chan:
			return find(path, T.Elem())
		case *types.Map:
			if r := find(append(path, opKey), T.Key()); r != nil {
				return r
			}
			return find(append(path, opValue), T.Elem())
		case *types.Signature:
			if r := find(append(path, opParams), T.Params()); r != nil {
				return r
			}
			return find(append(path, opResults), T.Results())
		case *types.Struct:
			for i := 0; i < T.NumFields(); i++ {
				f := T.Field(i)
				path2 := append(path, f.Id())
				if f == obj {
					return path2 // found field
				}
				if r := find(path2, f.Type()); r != nil {
					return r
				}
			}
			return nil
		case *types.Tuple:
			for i := 0; i < T.Len(); i++ {
				if r := find(append(path, i), T.At(i).Type()); r != nil {
					return r
				}
			}
			return nil
		case *types.Interface:
			for i := 0; i < T.NumMethods(); i++ {
				m := T.Method(i)
				path2 := append(path, m.Id())
				if m == obj {
					return path2 // found interface method
				}
				if r := find(path2, m.Type()); r != nil {
					return r
				}
			}
			return nil
		}
		panic(T)
	}

	// First inspect package-level named types and their declared methods.
	var space [10]interface{}
	var nontypes []types.Object
	for _, name := range scope.Names() {
		o := scope.Lookup(name)
		T, ok := o.Type().(*types.Named)
		if !ok {
			nontypes = append(nontypes, o) // save for second pass
			continue
		}
		path := append(space[:0], name)

		// Inv: T is a named type.

		// Inspect declared methods
		for i := 0; i < T.NumMethods(); i++ {
			m := T.Method(i)
			path2 := append(path, m.Id())
			if m == obj {
				return path2, nil // found declared method
			}
			if r := find(path2, m.Type()); r != nil {
				return r, nil
			}
		}

		// Inspect underlying type.
		if r := find(append(path, opUnderlying), T.Underlying()); r != nil {
			return r, nil
		}
	}

	// Then inspect everything else.
	for _, o := range nontypes {
		if r := find(append(space[:0], o.Name()), o.Type()); r != nil {
			return r, nil
		}
	}

	return nil, fmt.Errorf("can't find path for %v", obj)
}

// FindObject returns the object denoted by path p within the package pkg.
func FindObject(pkg *types.Package, p Path) (types.Object, error) {
	path, err := parse(p)
	if err != nil {
		return nil, err
	}
	if len(path) == 0 {
		return nil, fmt.Errorf("empty path")
	}
	name := path[0].(string)
	obj := pkg.Scope().Lookup(name)
	if obj == nil {
		return nil, fmt.Errorf("%s.%s not defined (path %s)", pkg.Path(), name, p)
	}
	if len(path) == 1 {
		return obj, nil
	}

	// We're looking for a field or method.

	var find func(path []interface{}, T types.Type) (types.Object, error)
	find = func(path []interface{}, T types.Type) (types.Object, error) {
		switch T := T.(type) {
		case *types.Pointer:
			return find(path, T.Elem())
		case *types.Slice:
			return find(path, T.Elem())
		case *types.Array:
			return find(path, T.Elem())
		case *types.Chan:
			return find(path, T.Elem())
		case *types.Basic:
			return nil, fmt.Errorf("unexpected basic type")
		case *types.Map:
			if len(path) == 0 {
				return nil, fmt.Errorf("in map: bad path")
			}
			switch path[0] {
			case opKey:
				return find(path[1:], T.Key())
			case opValue:
				return find(path[1:], T.Elem())
			}
			return nil, fmt.Errorf("in map: unexpected path element %v", path[0])
		case *types.Named:
			if len(path) == 0 {
				return nil, fmt.Errorf("in named: bad path")
			}
			name, ok := path[0].(string)
			if !ok {
				return nil, fmt.Errorf("in named: got %T, want method name",
					path[0])
			}
			path = path[1:]
			if name == opUnderlying {
				return find(path, T.Underlying())
			}
			for i := 0; i < T.NumMethods(); i++ {
				m := T.Method(i)
				if m.Name() == name {
					if len(path) == 0 {
						return m, nil // found concrete method
					}
					return find(path, m.Type())
				}
			}
			return nil, fmt.Errorf("named type %s has no method %s", T, name)
		case *types.Struct:
			if len(path) == 0 {
				return nil, fmt.Errorf("in struct: empty path")
			}
			id, ok := path[0].(string)
			if !ok {
				return nil, fmt.Errorf("in struct: got %T, want field name",
					path[0])
			}
			path = path[1:]
			for i := 0; i < T.NumFields(); i++ {
				fld := T.Field(i)
				if fld.Id() == id {
					if len(path) == 0 {
						return fld, nil // found field
					}
					return find(path, fld.Type())
				}
			}
			return nil, fmt.Errorf("in struct: no field %q", id)
		case *types.Tuple:
			if len(path) == 0 {
				return nil, fmt.Errorf("in map: empty path")
			}
			index, ok := path[0].(int)
			if !ok {
				return nil, fmt.Errorf("in named: got %T, want index", path[0])
			}
			if index >= T.Len() {
				return nil, fmt.Errorf("in tuple: index out of range")
			}
			return find(path[1:], T.At(index).Type())
		case *types.Interface:
			if len(path) == 0 {
				return nil, fmt.Errorf("in interface: empty path")
			}
			id, ok := path[0].(string)
			if !ok {
				return nil, fmt.Errorf("in interface: got %T, want method name",
					path[0])
			}
			path = path[1:]
			for i := 0; i < T.NumMethods(); i++ {
				m := T.Method(i)
				if m.Id() == id {
					if len(path) == 0 {
						return m, nil // found abstract method
					}
					return find(path, m.Type())
				}
			}
			return nil, fmt.Errorf("in interface: no method %q", id)
		case *types.Signature:
			if len(path) == 0 {
				return nil, fmt.Errorf("in func: empty path")
			}
			name, ok := path[0].(string)
			if !ok {
				return nil, fmt.Errorf("in func: got %T, want params/results",
					path[0])
			}
			path = path[1:]
			switch name {
			case opParams:
				return find(path, T.Params())
			case opResults:
				return find(path, T.Results())
			}
			return nil, fmt.Errorf("in signature: unexpected path element %v", name)
		}
		panic(T)
	}

	return find(path[1:], obj.Type())
}

// parse breaks a dotted path into a list of elements:
//  element = op* | identifier | int.
func parse(s Path) ([]interface{}, error) {
	words := strings.Split(string(s), ".")
	path := make([]interface{}, len(words))
	for i, word := range words {
		if n, err := strconv.Atoi(word); err == nil {
			path[i] = n
			continue
		}
		switch word {
		case opKey, opValue, opParams, opResults, opUnderlying:
			path[i] = word
			continue
		}
		if !validIdent(word) {
			return nil, fmt.Errorf("invalid path: %q is not an identifier", word)
		}
		path[i] = word
	}
	return path, nil
}

func validIdent(name string) bool {
	for i, r := range name {
		if !(r == '_' || unicode.IsLetter(r) || i > 0 && unicode.IsDigit(r)) {
			return false
		}
	}
	return name != ""
}

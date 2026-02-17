//go:build go1.26

package a

import (
	"deep/errors"
	"io"
)

var _ = errors.Errorf("foo: %w", io.EOF) // ok

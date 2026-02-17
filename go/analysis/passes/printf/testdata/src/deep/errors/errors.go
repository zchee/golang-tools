//go:build go1.26

package errors

import (
	"fmt"
	"strings"
)

// Error is a trivial implementation of error.
type Error struct {
	s string
}

// New returns an error that formats as the given text.
//
// The returned error contains a Frame set to the caller's location and
// implements Formatter to show this information when printed with details.
func New(text string) error {
	return &Error{text}
}

func (e *Error) Error() string { return e.s }

// Errorf creates new error with format.
func Errorf(format string, a ...any) error {
	if strings.Contains(format, "%w") {
		return fmt.Errorf(format, a...)
	}

	return &Error{
		s: fmt.Sprintf(format, a...),
	}
}

package libc

import "C" // purely to make this a cgo file

type CS struct {
}

type CT struct {
}

func (s *CS) Method() *CT {
	return &CT{}
}
func (t *CT) Method() {
}

func Cfoo() *CS {
	return &CS{}
}

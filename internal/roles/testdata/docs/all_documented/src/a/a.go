// Package a is documented.
package a

// F does things.
func F() {}

// T is a type.
type T struct{}

// M does things.
func (T) M() {}

// Types are grouped.
type (
	X int
	Y int
)

// Colors are colors.
const (
	Red = iota
	Blue
)

const Green = 1 // Green is a trailing comment.

type Z int // Z has a trailing comment

// Package t is documented.
package t

type Single struct{}

type (
	InBlock int
)

// Documented is fine.
type Documented int

type Trailing int // has a comment

var Global = 1

const Const = 2

var Fine = 3 // trailing

var A, B = 1, 2

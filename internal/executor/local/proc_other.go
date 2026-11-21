//go:build !unix

package local

// The local executor needs process groups and signals; it is unix-only.
func init() { panic("executor/local requires a unix platform") }

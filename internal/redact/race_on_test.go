//go:build race

package redact

// The race detector slows the stream ~40x, so timing is not asserted.
const raceEnabled = true

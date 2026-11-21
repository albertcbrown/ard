package redact

import "math"

// entropy returns the Shannon entropy of s in bits per byte.
func entropy(s []byte) float64 {
	if len(s) == 0 {
		return 0
	}
	var freq [256]int
	for _, c := range s {
		freq[c]++
	}
	n := float64(len(s))
	var h float64
	for _, f := range freq {
		if f > 0 {
			p := float64(f) / n
			h -= p * math.Log2(p)
		}
	}
	return h
}

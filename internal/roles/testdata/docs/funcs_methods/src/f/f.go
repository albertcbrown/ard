// Package f is documented.
package f

func Undoc() {}

type Exp struct{}

type hidden struct{}

// Ptr is documented.
func (e *Exp) Ptr() {}

func (e *Exp) PtrUndoc() {}

func (e Exp) ValUndoc() {}

func (h hidden) Skipped() {}

func (h *hidden) AlsoSkipped() {}

// Cache is generic.
type Cache[K comparable, V any] struct{}

func (c *Cache[K, V]) Get() {}

func (c Cache[T]) One() {}

func unexported() {}

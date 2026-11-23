package p

type List[T any] struct{ items []T }

type hidden struct{}

func Covered() int {
	used()
	return CoveredUnnamed()
}

func CoveredUnnamed() int { return 2 }

func Never() int { return 3 }

func quiet() int { return 4 }

func used() int { return 5 }

func Empty() {}

func (l *List[T]) Push(v T) { l.items = append(l.items, v); l.Len() }

func (l *List[T]) Len() int { return len(l.items) }

func (l *List[T]) Drop() { l.items = nil }

func (h hidden) Run() int { return 6 }

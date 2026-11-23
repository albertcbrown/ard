package m

type E struct{}

func (E) Error() string { return "e" }

type S struct{}

func (S) String() string { return "s" }

type U struct{}

func (U) Unwrap() error { return nil }

func (U) GoString() string { return "u" }

func (U) Other() int { return 1 }

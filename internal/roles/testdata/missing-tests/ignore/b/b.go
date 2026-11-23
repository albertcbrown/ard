package b

func FakeA() int { return 1 }

func FakeB() int { return 2 }

func Real() int { return 3 }

func (T) FakeC() int { return 4 }

type T struct{}

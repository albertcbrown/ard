package n

type T struct{}

func Named() int { return 1 }

func Unnamed() int { return 2 }

func hidden() int { return 3 }

func (T) String() string { return "t" }

func (T) Other() int { return 4 }

package github

type Writer interface{ AddLabels() error }
type Client interface{ Writer }

func New() Client { return nil }

const base = "https://api.github.com/"

func use() { _ = base }

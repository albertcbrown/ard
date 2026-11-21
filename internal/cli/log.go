package cli

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// NewLogger returns a slog text handler writing to w at the named level.
// An empty level means info; anything else is a usage error.
func NewLogger(level string, w io.Writer) (*slog.Logger, error) {
	var lv slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lv = slog.LevelDebug
	case "", "info":
		lv = slog.LevelInfo
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown log level %q (want debug, info, warn or error)", level)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lv})), nil
}

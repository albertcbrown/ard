package scanner

import (
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/config"
)

func TestCreateBodyFooter(t *testing.T) {
	parsed := Parsed{Scanner: "arch", HasSpan: true, Lines: []Line{lineFor(Item{F: finding("r:a.go", "t one two", SevLow, "")})}}
	fs := []Finding{finding("r:a.go", "t one two", SevLow, "")}
	for _, tt := range []struct {
		route config.Route
		want  bool
	}{{config.RoutePending, false}, {config.RouteNone, true}, {config.RouteNeedsHuman, true}, {config.Route("odd"), true}} {
		body := createBody(config.Role{Route: tt.route}, parsed, fs)
		if got := strings.Contains(body, llmFooter); got != tt.want {
			t.Errorf("route %q: footer = %v, want %v", tt.route, got, tt.want)
		}
		if tt.want && !strings.HasSuffix(body, llmFooter+"\n") {
			t.Errorf("route %q: footer is not last", tt.route)
		}
	}
	// A join re-renders with the existing body and must keep one footer.
	body := createBody(config.Role{Route: config.RouteNone}, parsed, fs)
	p2 := parsed
	p2.Lines = append(append([]Line(nil), parsed.Lines...), lineFor(Item{F: finding("r:b.go", "u three four", SevLow, "")}))
	joined := RenderBody(body, p2, []Finding{finding("r:b.go", "u three four", SevLow, "")})
	if n := strings.Count(joined, llmFooter); n != 1 {
		t.Errorf("footer count after join = %d", n)
	}
}

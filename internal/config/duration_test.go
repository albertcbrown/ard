package config

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseDuration(t *testing.T) {
	valid := []struct {
		in   string
		want time.Duration
	}{
		{"15m", 15 * time.Minute},
		{"4h", 4 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"1w", 7 * 24 * time.Hour},
		{"1h30m", 90 * time.Minute},
		{"90s", 90 * time.Second},
		{"1h30m10s", time.Hour + 30*time.Minute + 10*time.Second},
	}
	for _, tt := range valid {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseDuration(tt.in)
			if err != nil {
				t.Fatalf("ParseDuration(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}

	invalid := []string{"", "30", "-5m", "1x", "0s", "0", "1.5d", "2w1.5d", "500ms", "1h "}
	for _, in := range invalid {
		t.Run("invalid/"+in, func(t *testing.T) {
			_, err := ParseDuration(in)
			if err == nil {
				t.Fatalf("ParseDuration(%q) = nil error, want rejection", in)
			}
			if !strings.Contains(err.Error(), strconv.Quote(in)) {
				t.Errorf("error %q does not name the offending string %q", err, in)
			}
		})
	}
}

func TestDurationUnmarshalYAML(t *testing.T) {
	t.Run("integer is rejected with a clear message", func(t *testing.T) {
		var d Duration
		err := yaml.Unmarshal([]byte("15"), &d)
		if err == nil {
			t.Fatal("unmarshaling an integer as a duration succeeded, want rejection")
		}
		if want := `expected a duration string like "15m", got integer 15`; err.Error() != want {
			t.Errorf("error = %q, want %q", err, want)
		}
	})

	t.Run("string parses", func(t *testing.T) {
		var d Duration
		if err := yaml.Unmarshal([]byte(`"1h30m"`), &d); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if d != Duration(90*time.Minute) {
			t.Errorf("d = %s, want 1h30m", d)
		}
	})

	t.Run("mapping is rejected", func(t *testing.T) {
		var d Duration
		if err := yaml.Unmarshal([]byte("{a: 1}"), &d); err == nil {
			t.Fatal("unmarshaling a mapping as a duration succeeded, want rejection")
		}
	})
}

func TestDurationString(t *testing.T) {
	cases := map[Duration]string{
		0:                            "0s",
		Duration(5 * time.Minute):    "5m",
		Duration(90 * time.Minute):   "1h30m",
		Duration(4 * time.Hour):      "4h",
		Duration(24 * time.Hour):     "1d",
		Duration(7 * 24 * time.Hour): "1w",
		Duration(90 * time.Second):   "1m30s",
	}
	for d, want := range cases {
		if got := d.String(); got != want {
			t.Errorf("Duration(%s).String() = %q, want %q", time.Duration(d), got, want)
		}
	}
}

package effects

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// record is one ledger line.
type record struct {
	TS      time.Time      `json:"ts"`
	Op      string         `json:"op"`
	Target  string         `json:"target,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
}

// openLedger opens (creating) the JSONL ledger at path and returns the file
// and the next synthetic number, min(number)-1 over existing records (-1 for
// an empty ledger). A truncated final line (no trailing newline) is dropped;
// any other malformed line is an error, so a corrupt ledger fails closed.
func openLedger(path string) (*os.File, int, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, 0, fmt.Errorf("effects: read ledger %s: %w", path, err)
	}
	keep, addNL := len(data), false
	if i := bytes.LastIndexByte(data, '\n'); i < len(data)-1 {
		// Final line has no newline: a crash mid-write, or just no newline.
		var probe record
		if json.Unmarshal(data[i+1:], &probe) != nil {
			keep = i + 1
		} else {
			addNL = true
		}
	}
	next := -1
	for n, line := range bytes.Split(data[:keep], []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var r record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, 0, fmt.Errorf("effects: ledger %s line %d is corrupt: %w", path, n+1, err)
		}
		for _, k := range []string{"number", "id"} {
			if v, ok := r.Result[k].(float64); ok && int(v) < 0 && int(v)-1 < next {
				next = int(v) - 1
			}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("effects: open ledger %s: %w", path, err)
	}
	switch {
	case keep != len(data):
		if err := f.Truncate(int64(keep)); err != nil {
			f.Close()
			return nil, 0, fmt.Errorf("effects: trim ledger %s: %w", path, err)
		}
	case addNL:
		if _, err := f.Write([]byte{'\n'}); err != nil {
			f.Close()
			return nil, 0, fmt.Errorf("effects: repair ledger %s: %w", path, err)
		}
	}
	return f, next, nil
}

func appendRecord(f *os.File, r record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("effects: encode ledger record: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("effects: write ledger: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("effects: sync ledger: %w", err)
	}
	return nil
}

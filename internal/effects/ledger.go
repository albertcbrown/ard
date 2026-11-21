package effects

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// ledgerFile is the part of *os.File the ledger uses; tests inject faults.
type ledgerFile interface {
	Write([]byte) (int, error)
	Sync() error
	Seek(int64, int) (int64, error)
	Truncate(int64) error
	Close() error
}

// appendRecord writes one line and fsyncs. On any failure it truncates the
// file back to where it started so no partial line survives; if that also
// fails the ledger is unusable and the truncate error is returned as broken.
func appendRecord(f ledgerFile, r record) (err, broken error) {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("effects: encode ledger record: %w", err), nil
	}
	off, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("effects: seek ledger: %w", err), fmt.Errorf("effects: ledger unusable: %w", err)
	}
	werr := func() error {
		if _, err := f.Write(append(b, '\n')); err != nil {
			return fmt.Errorf("effects: write ledger: %w", err)
		}
		if err := f.Sync(); err != nil {
			return fmt.Errorf("effects: sync ledger: %w", err)
		}
		return nil
	}()
	if werr == nil {
		return nil, nil
	}
	if terr := f.Truncate(off); terr != nil {
		return werr, fmt.Errorf("effects: ledger unusable, truncate failed: %w", terr)
	}
	return werr, nil
}

// DryIssue is an issue the dry-run ledger records as (virtually) filed,
// replayed from its CreateIssue, UpdateIssue records.
type DryIssue struct {
	Number      int // negative
	Title, Body string
	Labels      []string
	Closed      bool
}

// Issues replays the ledger into the issues dry-run has created, oldest first.
// It only reads. A dry-run without a ledger file returns nil: callers then
// have only their own state to go on. Bodies are as recorded, so they are
// redacted when a Redact hook is set.
func (d *DryRun) Issues() ([]DryIssue, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.path == "" {
		return nil, nil
	}
	f, err := os.Open(d.path)
	if err != nil {
		return nil, fmt.Errorf("effects: read ledger %s: %w", d.path, err)
	}
	defer f.Close()
	var out []DryIssue
	idx := map[int]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		var r record
		if len(sc.Bytes()) == 0 || json.Unmarshal(sc.Bytes(), &r) != nil {
			continue // a torn final line is repaired when the ledger is opened
		}
		switch r.Op {
		case "CreateIssue":
			n, ok := r.Result["number"].(float64)
			if !ok || int(n) >= 0 {
				continue
			}
			is := DryIssue{Number: int(n)}
			is.Title, _ = r.Payload["title"].(string)
			is.Body, _ = r.Payload["body"].(string)
			if ls, ok := r.Payload["labels"].([]any); ok {
				for _, l := range ls {
					if s, ok := l.(string); ok {
						is.Labels = append(is.Labels, s)
					}
				}
			}
			idx[is.Number] = len(out)
			out = append(out, is)
		case "UpdateIssue":
			var n int
			if _, err := fmt.Sscanf(r.Target, "#%d", &n); err != nil {
				continue
			}
			i, ok := idx[n]
			if !ok {
				continue
			}
			if b, ok := r.Payload["body"].(string); ok {
				out[i].Body = b
			}
			switch r.Payload["state"] {
			case "closed":
				out[i].Closed = true
			case "open":
				out[i].Closed = false
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("effects: read ledger %s: %w", d.path, err)
	}
	return out, nil
}

// Package setupcmd implements `ard setup`: run every check, then apply the
// fixes only when the checks are green.
package setupcmd

import (
	"fmt"
	"io"
)

// Status is the state of one report row.
type Status string

// The four row states.
const (
	StatusOK      Status = "ok"
	StatusChanged Status = "changed"
	StatusDrift   Status = "drift"
	StatusManual  Status = "manual"
)

// Row is one line of the report. For a manual row Detail is the exact fix.
type Row struct {
	Name   string
	Status Status
	Detail string
}

// Report is the ordered list of rows.
type Report struct{ Rows []Row }

// Green reports whether no row is drift or manual.
func (r Report) Green() bool {
	for _, x := range r.Rows {
		if x.Status == StatusDrift || x.Status == StatusManual {
			return false
		}
	}
	return true
}

// HasDrift reports whether any row is drift.
func (r Report) HasDrift() bool {
	for _, x := range r.Rows {
		if x.Status == StatusDrift {
			return true
		}
	}
	return false
}

// Print writes one line per row. A manual row reads "manual: <fix>".
func Print(w io.Writer, r Report, dryRun bool) {
	if dryRun {
		fmt.Fprintln(w, "dry-run: nothing is written")
	}
	for _, x := range r.Rows {
		if x.Status == StatusManual {
			fmt.Fprintf(w, "manual: %s: %s\n", x.Name, x.Detail)
			continue
		}
		if x.Detail == "" {
			fmt.Fprintf(w, "%-8s%s\n", x.Status, x.Name)
			continue
		}
		fmt.Fprintf(w, "%-8s%s: %s\n", x.Status, x.Name, x.Detail)
	}
}

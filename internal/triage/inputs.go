package triage

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/labels"
)

// maxInputBytes caps the size of a human-provided input blob on main.
const maxInputBytes = 1 << 20

// maxInputPath caps the length of an input path.
const maxInputPath = 200

// Reasons of the inputs gate. Fixed text: never issue text.
const (
	rBadInputPath   = "bad-input-path"
	rInputTooBig    = "input-too-big"
	rWorkerNoNet    = "worker-no-network"
	rWaitingInputs  = "waiting-for-inputs"
	captureNoteHint = "Commit each file to `main` with a capture note beside it: a short `<path>.capture.md` with a table of `url`, `date` (YYYY-MM-DD), `status`, `sha256` and `verified_by`."
)

// TreeEntry is one path of main's tree at a SHA.
type TreeEntry struct {
	Mode, Type string
	Size       int64
}

// cleanInputPath checks an input path the engineer declared. Only a plain
// relative path of safe characters is allowed, so nothing in it can reach the
// gate comment as an @-mention or markdown.
func cleanInputPath(p string) error {
	switch {
	case p == "" || len(p) > maxInputPath:
		return errors.New("empty or too long")
	case path.Clean(p) != p || strings.HasPrefix(p, "/") || p == "." || strings.HasPrefix(p, "./"):
		return errors.New("not a clean relative path")
	}
	for _, r := range p {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '/' || r == '_' || r == '-'
		if !ok {
			return errors.New("forbidden character")
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == ".git" {
			return errors.New("forbidden segment")
		}
	}
	for _, pre := range []string{".ard", ".github"} {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return errors.New("protected path")
		}
	}
	return nil
}

type inputsResult struct {
	Verdict fanInVerdict
	Reason  string
	Missing []string
}

// decideInputs gates the engineer's inputs. Pure apart from lookup, which
// reports the entry of a path on main. A lookup error leaves the issue waiting.
func decideInputs(inputs []closeout.Input, worker config.Role, haveWorker bool, lookup func(path string) (TreeEntry, bool, error)) inputsResult {
	for _, in := range inputs {
		if cleanInputPath(in.Path) != nil {
			return inputsResult{Verdict: fHuman, Reason: rBadInputPath}
		}
	}
	var missing []string
	for _, in := range inputs {
		switch in.Provider {
		case "worker":
			if haveWorker && !worker.Network {
				return inputsResult{Verdict: fHuman, Reason: rWorkerNoNet}
			}
		case "human":
			e, ok, err := lookup(in.Path)
			switch {
			case err != nil:
				return inputsResult{Verdict: fWait, Reason: rWaitingInputs}
			case !ok || e.Type != "blob" || (e.Mode != "100644" && e.Mode != "100755"):
				missing = append(missing, in.Path)
			case e.Size > maxInputBytes:
				return inputsResult{Verdict: fHuman, Reason: rInputTooBig}
			}
		default:
			return inputsResult{Verdict: fHuman, Reason: rBadInputPath}
		}
	}
	if len(missing) > 0 {
		return inputsResult{Verdict: fWait, Reason: rWaitingInputs, Missing: missing}
	}
	return inputsResult{Verdict: fReady, Reason: rReady}
}

// mainView is the per-tick view of main: the SHA is resolved once per tick, and
// entries are cached for that SHA only.
func (t *tick) mainLookup(ctx context.Context) (func(string) (TreeEntry, bool, error), error) {
	l := t.l
	if l.d.MainSHA == nil || l.d.MainEntry == nil {
		return nil, errors.New("main is not readable")
	}
	if !t.mainDone {
		t.mainDone = true
		t.mainSHA, t.mainErr = l.d.MainSHA(ctx)
	}
	if t.mainErr != nil {
		return nil, t.mainErr
	}
	sha := t.mainSHA
	return func(p string) (TreeEntry, bool, error) {
		l.mu.Lock()
		if l.mainCacheSHA != sha {
			l.mainCacheSHA, l.mainCache = sha, map[string]cachedEntry{}
		}
		if c, ok := l.mainCache[p]; ok {
			l.mu.Unlock()
			return c.e, c.ok, nil
		}
		l.mu.Unlock()
		e, ok, err := l.d.MainEntry(ctx, sha, p)
		if err != nil {
			return TreeEntry{}, false, err
		}
		l.mu.Lock()
		if l.mainCacheSHA == sha {
			l.mainCache[p] = cachedEntry{e, ok}
		}
		l.mu.Unlock()
		return e, ok, nil
	}, nil
}

type cachedEntry struct {
	e  TreeEntry
	ok bool
}

// gateInputs applies the inputs gate for a network round. It returns true when
// the gate may go on to approval.
func (t *tick) gateInputs(ctx context.Context, n int, jobID, worker string, rounds int) (bool, error) {
	l := t.l
	inputs, _, err := closeout.InputsOf(ctx, l.d.Store, n)
	if err != nil {
		return false, fmt.Errorf("triage gate issue %d: inputs: %w", n, err)
	}
	if len(inputs) == 0 {
		return true, nil
	}
	lookup, merr := t.mainLookup(ctx)
	if merr != nil {
		t.log.Warn("triage.gate_main_unreadable", "issue", n, "error", merr)
		lookup = func(string) (TreeEntry, bool, error) { return TreeEntry{}, false, merr }
	}
	wr, have := workerRole(t.cfg, worker)
	res := decideInputs(inputs, wr, have, lookup)
	switch res.Verdict {
	case fReady:
		return true, nil
	case fHuman:
		return false, t.gateHuman(ctx, n, jobID, fanInResult{Verdict: fHuman, Reason: res.Reason, Worker: worker})
	}
	if len(res.Missing) == 0 {
		return false, nil
	}
	marker := fmt.Sprintf("<!-- %striage gate=inputs round=%d -->", labels.Prefix, rounds)
	var b strings.Builder
	b.WriteString("**ard · triage · waiting for inputs**\n\nThe plan needs these files on `main` before a worker starts:\n\n")
	for _, p := range res.Missing {
		fmt.Fprintf(&b, "- `%s`\n", p)
	}
	b.WriteString("\n" + captureNoteHint + " ard continues by itself once they are there.")
	return false, t.postOnce(ctx, n, marker, b.String())
}

func workerRole(cfg *config.Resolved, name string) (config.Role, bool) {
	for _, r := range cfg.RolesOf(config.KindWorker) {
		if r.Name == name {
			return r, true
		}
	}
	return config.Role{}, false
}

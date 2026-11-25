package committer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// File is one change in a Request. Exactly one of Data, Src or Delete is set.
type File struct {
	Path   string // repo-relative, slash-separated, canonical: ".ard/12/issue.md"
	Data   []byte // literal contents
	Src    string // absolute host path; read into memory before the request waits on the lane
	Delete bool   // remove the path; a path already absent is fine
	// Merge, optional, is called at apply time with the contents of the path
	// at the latest head (exists false: absent) and the file's own bytes. Its
	// result is committed instead. An error fails the commit with ErrMerge.
	// It may run again on a push retry, against a newer head.
	Merge func(current []byte, exists bool, mine []byte) ([]byte, error)
}

// Request is one commit: one issue, one stage:role, one summary, a file set.
type Request struct {
	Issue   int    // > 0; < 0 only in dry-run (synthetic issue)
	Stage   string // matches ^[a-z][a-z0-9-]*$
	Role    string // matches ^[a-z0-9][a-z0-9-]*$
	Summary string // untrusted; sanitised by Message
	JobID   string // optional; adds an "Ard-Job: <id>" trailer
	Files   []File // 1..MaxFiles entries, unique paths
}

// Result is the outcome of a Commit.
type Result struct {
	SHA       string // the new commit; for NoOp, the upstream head
	NoOp      bool   // the tree already matched: nothing was committed or pushed
	Synthetic bool   // dry-run: SHA is synthetic and nothing reached the remote
	Attempts  int    // pushes tried
	// pushed is the commit of a push that failed with ErrTransient: it may
	// have landed.
	pushed string
}

// MaxFiles bounds one request.
const MaxFiles = 256

var (
	stageRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	roleRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	jobRe   = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)
)

// snapshot validates req against issue (the number the paths must carry) and
// returns a copy whose Src files have been read into memory. Every path
// violation is reported in one *GuardError.
func snapshot(req Request, issue int) (Request, error) {
	switch {
	case !stageRe.MatchString(req.Stage):
		return req, fmt.Errorf("%w: bad stage %q", ErrInvalid, req.Stage)
	case !roleRe.MatchString(req.Role):
		return req, fmt.Errorf("%w: bad role %q", ErrInvalid, req.Role)
	case req.JobID != "" && !jobRe.MatchString(req.JobID):
		return req, fmt.Errorf("%w: bad job id", ErrInvalid)
	case len(req.Files) == 0:
		return req, fmt.Errorf("%w: no files", ErrInvalid)
	case len(req.Files) > MaxFiles:
		return req, fmt.Errorf("%w: %d files, max %d", ErrInvalid, len(req.Files), MaxFiles)
	}
	ge := &GuardError{}
	out := req
	out.Files = make([]File, len(req.Files))
	seen := map[string]bool{}
	for i, f := range req.Files {
		out.Files[i] = f
		if err := CheckPath(issue, f.Path); err != nil {
			ge.add(f.Path, err.Error())
			continue
		}
		if seen[f.Path] {
			return req, fmt.Errorf("%w: duplicate path %q", ErrInvalid, f.Path)
		}
		seen[f.Path] = true
		kinds := 0
		if f.Data != nil {
			kinds++
		}
		if f.Src != "" {
			kinds++
		}
		if f.Delete {
			kinds++
		}
		if kinds > 1 || (kinds == 0 && f.Data == nil) {
			return req, fmt.Errorf("%w: %q needs exactly one of Data, Src, Delete", ErrInvalid, f.Path)
		}
		switch {
		case len(f.Data) > MaxFileBytes:
			ge.add(f.Path, "larger than 1 MiB")
		case f.Src != "":
			b, err := readSrc(f.Src)
			var pe *guardReason
			switch {
			case errors.As(err, &pe):
				ge.add(f.Path, pe.msg)
			case err != nil:
				return req, fmt.Errorf("%w: src %q: %v", ErrInvalid, f.Src, err)
			default:
				out.Files[i].Data, out.Files[i].Src = b, ""
			}
		}
	}
	if len(ge.Paths) > 0 {
		return req, ge
	}
	return out, nil
}

type guardReason struct{ msg string }

func (g *guardReason) Error() string { return g.msg }

// readSrc reads a host file without following symlinks, in the path or at the
// end of it, and without trusting an earlier Lstat.
func readSrc(src string) ([]byte, error) {
	real, err := filepath.EvalSymlinks(src)
	if err != nil {
		return nil, err
	}
	if real != filepath.Clean(src) {
		return nil, &guardReason{"src path involves a symlink"}
	}
	f, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, &guardReason{"src is a symlink"}
		}
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, &guardReason{"src is not a regular file"}
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFileBytes {
		return nil, &guardReason{"larger than 1 MiB"}
	}
	if b == nil {
		b = []byte{}
	}
	return b, nil
}

package committer

import (
	"fmt"
	"os"
	"regexp"
)

// File is one change in a Request. Exactly one of Data, Src or Delete is set.
type File struct {
	Path   string // repo-relative, slash-separated, canonical: ".ard/12/issue.md"
	Data   []byte // literal contents
	Src    string // absolute host path; read into memory before the request waits on the lane
	Delete bool   // remove the path; a path already absent is fine
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
			fi, err := os.Lstat(f.Src)
			switch {
			case err != nil:
				return req, fmt.Errorf("%w: src %q: %v", ErrInvalid, f.Src, err)
			case !fi.Mode().IsRegular():
				ge.add(f.Path, "src is not a regular file")
			case fi.Size() > MaxFileBytes:
				ge.add(f.Path, "larger than 1 MiB")
			default:
				b, err := os.ReadFile(f.Src)
				if err != nil {
					return req, fmt.Errorf("%w: src %q: %v", ErrInvalid, f.Src, err)
				}
				out.Files[i].Data, out.Files[i].Src = b, ""
				if b == nil {
					out.Files[i].Data = []byte{}
				}
			}
		}
	}
	if len(ge.Paths) > 0 {
		return req, ge
	}
	return out, nil
}

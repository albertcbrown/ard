package servecmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/repopath"
)

// serveLogCap is the size at which serve.log is rotated to serve.log.1.
const serveLogCap = 10 << 20

// capFile is the serve.log writer: mode 0600, rotated at its cap so the log
// never grows without bound. It is safe for concurrent use.
type capFile struct {
	mu   sync.Mutex
	path string
	cap  int64
	f    *os.File
	size int64
}

// openServeLog opens ~/.ard/logs/<owner>/<repo>/serve.log and returns it with its path.
func openServeLog(home string, r github.Repo) (*capFile, string, error) {
	dir, err := repopath.Join(filepath.Join(home, ".ard", "logs"), r.String())
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	c := &capFile{path: filepath.Join(dir, "serve.log"), cap: serveLogCap}
	if err := c.open(); err != nil {
		return nil, "", err
	}
	return c, c.path, nil
}

func (c *capFile) open() error {
	f, err := os.OpenFile(c.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	c.f, c.size = f, st.Size()
	return nil
}

// Write appends p, rotating first when it would pass the cap.
func (c *capFile) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == nil {
		return 0, os.ErrClosed
	}
	if c.size > 0 && c.size+int64(len(p)) > c.cap {
		c.f.Close()
		if err := os.Rename(c.path, c.path+".1"); err != nil {
			return 0, fmt.Errorf("rotate serve.log: %w", err)
		}
		if err := c.open(); err != nil {
			c.f = nil
			return 0, err
		}
	}
	n, err := c.f.Write(p)
	c.size += int64(n)
	return n, err
}

// Close closes the file.
func (c *capFile) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == nil {
		return nil
	}
	err := c.f.Close()
	c.f = nil
	return err
}

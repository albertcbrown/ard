package tarball

const maxEntryLen = 200

// Error describes why an archive entry was refused. errors.Is(err, ErrUnsafe)
// or errors.Is(err, ErrLimit) classifies it. Why is fixed text and Entry is
// the cleaned entry name, truncated.
type Error struct {
	Entry string
	Why   string
	kind  error
}

func newError(kind error, entry, why string) *Error {
	if len(entry) > maxEntryLen {
		entry = entry[:maxEntryLen]
	}
	return &Error{Entry: entry, Why: why, kind: kind}
}

func (e *Error) Error() string {
	if e.Entry == "" {
		return "tarball: " + e.Why
	}
	return "tarball: " + e.Why + ": " + e.Entry
}

// Is reports whether target is the error's class (ErrUnsafe or ErrLimit).
func (e *Error) Is(target error) bool { return e.kind != nil && target == e.kind }

// Package tarball packs directory trees into gzip-compressed tar streams and
// unpacks them safely. It is a leaf package (standard library only).
//
// Threat model:
//   - Nothing in an archive is trusted; the unpack side runs as the
//     controller's uid and the archive comes from an agent's pod.
//   - Every write goes through os.Root, so an escape fails even if a check
//     here has a gap; explicit checks refuse hostile entries before that.
//   - Only directories, regular files and symlinks are unpacked. Modes are
//     normalised (no setuid, setgid or sticky bits), and sizes, counts, depth
//     and compression ratio are capped by [Limits].
//   - Symlink targets are not validated: they are never followed, and every
//     reader of an unpacked tree must use Lstat or os.Root.
//   - Pack stores symlinks as links and never reads through them.
package tarball

import "errors"

// ErrUnsafe classifies an entry the unpacker refuses as hostile or unsupported.
var ErrUnsafe = errors.New("tarball: unsafe entry")

// ErrLimit classifies an archive that exceeds a configured limit.
var ErrLimit = errors.New("tarball: limit exceeded")

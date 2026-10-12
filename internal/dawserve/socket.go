package dawserve

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
)

// uidFunc is a test seam for the peer check, swapped atomically because the
// server goroutine reads it while a test may replace it.
var uidFunc atomic.Pointer[func() int]

func init() { setUID(os.Getuid) }

func setUID(f func() int) { uidFunc.Store(&f) }

func currentUID() int { return (*uidFunc.Load())() }

// DefaultSocket is the serve socket inside a private per-user directory
// (~/Library/Caches/ucdaw on macOS, $XDG_RUNTIME_DIR/ucdaw on Linux), created
// 0700 and checked before use.
func DefaultSocket() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil {
			return "", fmt.Errorf("dawserve: no per-user directory: %w", err)
		}
	}
	dir := filepath.Join(base, "ucdaw")
	if err := ensurePrivateDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, "serve.sock"), nil
}

// ensurePrivateDir creates dir 0700, or checks an existing one is a real
// directory owned by this user, tightening its mode if needed.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("dawserve: creating %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("dawserve: checking %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("dawserve: %s is not a directory", dir)
	}
	if !ownedByMe(info) {
		return fmt.Errorf("dawserve: %s belongs to another user", dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("dawserve: tightening %s: %w", dir, err)
		}
	}
	return nil
}

func ownedByMe(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == currentUID()
}

// clearStaleSocket removes a leftover socket of ours; anything else at the
// path (another user's socket, a file, a symlink) is refused, never removed.
func clearStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 || !ownedByMe(info) {
		return fmt.Errorf("dawserve: %s exists and is not ucdaw's socket; refusing to replace it", path)
	}
	return os.Remove(path)
}

// checkPeer refuses a connection whose other end is not user want.
func checkPeer(conn net.Conn, want int) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("dawserve: not a Unix socket")
	}
	uid, err := peerUID(unixConn)
	if err != nil {
		return fmt.Errorf("dawserve: reading peer credentials: %w", err)
	}
	if uid != want {
		return fmt.Errorf("dawserve: socket peer is user %d, not you", uid)
	}
	return nil
}

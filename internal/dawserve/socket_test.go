package dawserve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveclarke/ucmix/internal/daw"
)

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ucdaw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestEnsurePrivateDirTightensMode(t *testing.T) {
	dir := filepath.Join(shortDir(t), "run")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestEnsurePrivateDirRefusesSymlinkAndOtherOwner(t *testing.T) {
	base := shortDir(t)
	target := filepath.Join(base, "real")
	_ = os.Mkdir(target, 0o700)
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDir(link); err == nil {
		t.Error("symlinked directory accepted")
	}
	setUID(func() int { return os.Getuid() + 1 })
	t.Cleanup(func() { setUID(os.Getuid) })
	if err := ensurePrivateDir(target); err == nil || !strings.Contains(err.Error(), "another user") {
		t.Errorf("other owner = %v", err)
	}
}

func TestServeRefusesToReplaceNonSocket(t *testing.T) {
	path := filepath.Join(shortDir(t), "s.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := &Server{}
	err := srv.Serve(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("Serve = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "not a socket" {
		t.Error("file was replaced")
	}
}

func TestClientRefusesServerOfAnotherUser(t *testing.T) {
	client, _ := startServer(t)
	setUID(func() int { return os.Getuid() + 1 })
	t.Cleanup(func() { setUID(os.Getuid) })
	if client.Available() {
		t.Error("Available trusted a server run by another user")
	}
	if _, err := client.Do(context.Background(), daw.Request{Op: "status"}); err == nil || !strings.Contains(err.Error(), "not you") {
		t.Errorf("Do = %v", err)
	}
}

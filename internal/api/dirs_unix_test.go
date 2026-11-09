//go:build unix

package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// TestRuntimeDirWithoutXDG: a Pinky started without XDG_RUNTIME_DIR (by a
// service, say) uses the user's /run/user/<uid> when it is theirs alone, so
// it takes the same lock and socket as one started in the desktop session.
func TestRuntimeDirWithoutXDG(t *testing.T) {
	base := t.TempDir()
	old := runtimeBase
	runtimeBase = base
	t.Cleanup(func() { runtimeBase = old })
	home := t.TempDir()
	state := filepath.Join(home, ".local", "state")
	mine := filepath.Join(base, strconv.Itoa(os.Getuid()))

	if got := runtimeDir(home); got != state {
		t.Errorf("without /run/user/<uid>: %s", got)
	}
	if err := os.Mkdir(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := runtimeDir(home); got != state {
		t.Errorf("with a /run/user/<uid> others can read: %s", got)
	}
	if err := os.Chmod(mine, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := runtimeDir(home); got != mine {
		t.Errorf("with the user's own /run/user/<uid>: %s", got)
	}

	if runtime.GOOS != "linux" {
		return
	}
	t.Setenv(HomeEnv, "")
	t.Setenv("HOME", home)
	t.Setenv("XDG_RUNTIME_DIR", mine)
	session, err := DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	service, err := DefaultDirs()
	if err != nil {
		t.Fatal(err)
	}
	if service.Socket() != session.Socket() || service.LockFile() != session.LockFile() {
		t.Errorf("without XDG_RUNTIME_DIR: %s, in the session: %s", service.Socket(), session.Socket())
	}
}

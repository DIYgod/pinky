//go:build !unix

package api

import (
	"net"
	"os"
	"path/filepath"
	"time"
)

// lock on systems without flock: a gateway that answers on the socket holds
// it. Windows support is untested.
func lock(path string) (*os.File, error) {
	sock := filepath.Join(filepath.Dir(path), "pinky.sock")
	if c, err := net.DialTimeout("unix", sock, time.Second); err == nil {
		c.Close()
		return nil, &RunningError{}
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
}

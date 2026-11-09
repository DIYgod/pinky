//go:build !unix

package api

import (
	"net"
	"os"
	"time"
)

// lock on systems without flock: a gateway that answers on the socket sock
// holds it. Windows support is untested.
func lock(path, sock string) (*os.File, error) {
	if c, err := net.DialTimeout("unix", sock, time.Second); err == nil {
		c.Close()
		return nil, &RunningError{}
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
}

// ownPrivateDir: no /run/user/<uid> here.
func ownPrivateDir(string) bool { return false }

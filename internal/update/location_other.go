//go:build !darwin

package update

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// locate on Windows and Linux: Pinky downloads the release for the person
// to install. On Windows that is the installer, which replaces the per-user
// install in %LOCALAPPDATA%\Programs and quits the running Pinky first;
// Linux packages are updated by the package manager.
func locate(exe string) Location {
	l := Location{Exe: exe, App: exe, Method: MethodDownload}
	switch runtime.GOOS {
	case "windows":
		l.Method = MethodInstaller
	case "linux":
		switch {
		case os.Getenv("APPIMAGE") != "":
			l.App = os.Getenv("APPIMAGE")
		case strings.HasPrefix(exe, "/usr/") || strings.HasPrefix(exe, "/opt/"):
			if owned := packageOwner(exe); owned != "" {
				l.Method, l.Manager, l.Command = MethodManager, owned, ""
			}
		case strings.Contains(exe, "/.linuxbrew/") || strings.Contains(exe, "/home/linuxbrew/"):
			l.Method, l.Manager, l.Command = MethodManager, "Homebrew", "brew upgrade pinky"
		}
	}
	return l
}

// packageOwner names the package manager that installed a file under /usr
// or /opt, as far as its databases show.
func packageOwner(exe string) string {
	base := filepath.Base(exe)
	for _, list := range []string{"/var/lib/dpkg/info/pinky.list", "/var/lib/dpkg/info/" + base + ".list"} {
		if data, err := os.ReadFile(list); err == nil && strings.Contains(string(data), exe) {
			return "your package manager"
		}
	}
	if _, err := os.Stat("/var/lib/pacman/local"); err == nil {
		if m, _ := filepath.Glob("/var/lib/pacman/local/pinky-*"); len(m) > 0 {
			return "your package manager"
		}
	}
	return ""
}

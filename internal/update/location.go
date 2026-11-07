package update

import (
	"os"
	"path/filepath"
)

// Method is how an update reaches this installation.
type Method int

const (
	// MethodSelf: Pinky replaces itself and starts again (macOS).
	MethodSelf Method = iota
	// MethodInstaller: Pinky downloads the release's installer and runs it
	// (Windows).
	MethodInstaller
	// MethodDownload: Pinky downloads the release and shows the file, to be
	// installed by hand.
	MethodDownload
	// MethodManager: what installed Pinky updates it (Homebrew, a Linux
	// package); Pinky only says so.
	MethodManager
)

// Location is where the running Pinky is installed, and how it can be
// updated there.
type Location struct {
	// Exe is the running executable, symlinks resolved.
	Exe string
	// App is what an update replaces: the app bundle on macOS, the
	// executable elsewhere.
	App string
	// Method is how this installation gets updates.
	Method Method
	// Manager names what installed Pinky, for MethodManager ("Homebrew"),
	// and Command is how it updates it ("brew upgrade --cask pinky").
	Manager, Command string
	// Problem says, in words for people, why Pinky cannot replace itself
	// here; "" with MethodSelf.
	Problem string
}

// CanReplace reports whether Pinky can replace itself here and start again.
func (l Location) CanReplace() bool { return l.Method == MethodSelf && l.Problem == "" }

// Locate finds the running installation.
func Locate() Location {
	exe, err := os.Executable()
	if err != nil {
		return Location{Method: MethodDownload, Problem: "Pinky could not find where it is installed."}
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return locate(exe)
}

// writableDir reports whether a folder can take new entries, by making
// and removing one.
func writableDir(dir string) error {
	probe, err := os.MkdirTemp(dir, ".pinky-write-")
	if err != nil {
		return err
	}
	return os.Remove(probe)
}

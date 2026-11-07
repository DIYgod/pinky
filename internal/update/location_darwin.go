package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// checkRunning checks the signature of the running bundle; tests replace
// it. Only a Pinky.app signed by Pinky's team replaces itself, so that a
// build made on this Mac (`make app`, signed ad hoc) is never overwritten
// by a release.
var checkRunning = func(bundle string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return verifySignature(ctx, bundle, false)
}

// homebrewPrefixes are where Homebrew keeps its casks.
var homebrewPrefixes = []string{"/opt/homebrew", "/usr/local"}

// locate on macOS: Pinky.app replaces itself where it is, unless macOS
// runs it from a translocated or read-only copy, Homebrew manages it, the
// folder is not writable, or the bundle is not signed by Pinky's team.
func locate(exe string) Location {
	l := Location{Exe: exe, App: exe, Method: MethodDownload}
	macOS := filepath.Dir(exe)
	contents := filepath.Dir(macOS)
	bundle := filepath.Dir(contents)
	if filepath.Base(macOS) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(bundle, ".app") {
		l.Problem = "This Pinky doesn't run from Pinky.app, so it can't replace itself."
		return l
	}
	l.App = bundle
	if cmd := homebrew(exe); cmd != "" {
		l.Method, l.Manager, l.Command = MethodManager, "Homebrew", cmd
		return l
	}
	parent := filepath.Dir(bundle)
	switch {
	case strings.Contains(bundle, "/AppTranslocation/"):
		l.Problem = "macOS runs this Pinky from a temporary copy. Move Pinky to Applications and open it from there to get updates automatically."
	case writableDir(parent) != nil:
		if strings.HasPrefix(bundle, "/Volumes/") {
			l.Problem = "Pinky runs from its disk image. Drag it to Applications and open it from there to get updates automatically."
		} else {
			l.Problem = "Pinky can't write to " + parent + ", where it is installed."
		}
	case unix.Access(bundle, unix.W_OK) != nil:
		l.Problem = "Pinky.app belongs to another user of this Mac, so Pinky can't replace it."
	case checkRunning(bundle) != nil:
		l.Problem = "This copy of Pinky isn't signed by Pinky's developer (a build made on this Mac), so it doesn't replace itself."
	default:
		l.Method = MethodSelf
	}
	return l
}

// homebrew returns the command that updates a Pinky Homebrew installed,
// or "" when Homebrew did not.
func homebrew(exe string) string {
	if strings.Contains(exe, "/Cellar/") {
		return "brew upgrade pinky"
	}
	if strings.Contains(exe, "/Caskroom/") {
		return "brew upgrade --cask pinky"
	}
	for _, prefix := range homebrewPrefixes {
		if fi, err := os.Stat(filepath.Join(prefix, "Caskroom", "pinky")); err == nil && fi.IsDir() {
			return "brew upgrade --cask pinky"
		}
	}
	return ""
}

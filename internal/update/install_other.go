//go:build !darwin

package update

import (
	"errors"
	"log/slog"
)

// CheckSwap finds nothing: only Pinky.app swaps itself.
func CheckSwap(version string) *Swapped { return nil }

// Confirm does nothing here.
func (s *Swapped) Confirm() error { return nil }

// NewInstaller: Pinky replaces itself on macOS only; elsewhere the person
// runs the installer or the package manager.
func NewInstaller(l Location, log *slog.Logger) (Installer, error) {
	if l.Problem != "" {
		return nil, errors.New(l.Problem)
	}
	return nil, errors.New("Pinky can't replace itself on this system; install the new version from the download")
}

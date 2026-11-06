//go:build !darwin

package ui

// keepAppAwake is for macOS's App Nap; other systems do not throttle a
// process whose window is out of sight.
func keepAppAwake() {}

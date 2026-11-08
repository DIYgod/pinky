//go:build !darwin

package ui

// keepAppAwake is for macOS's App Nap; other systems do not throttle a
// process whose window is out of sight.
func keepAppAwake() {}

// keepMacAwake keeps a Mac awake while agent tasks run; other systems are
// left to their own sleep settings for now.
func keepMacAwake(bool) {}

// keepMacAwakeAway keeps a Mac awake while a phone is away; other systems
// are left as they are.
func keepMacAwakeAway(bool) {}

//go:build e2e

package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/DIYgod/pinky/internal/driver"
)

// In the e2e build, PINKY_PLATFORMS ("android", "ios", or both, separated
// by commas) limits the platforms Pinky looks for phones on, so a check of
// the app with an Android emulator leaves an iPhone that is plugged in
// alone: the iOS provider would ask it about itself even with PINKY_DEVICES
// naming other phones. Release builds look on every platform.
func init() {
	v := strings.TrimSpace(os.Getenv("PINKY_PLATFORMS"))
	if v == "" {
		return
	}
	var keep []func(*slog.Logger) (driver.Provider, error)
	for _, p := range strings.Split(v, ",") {
		switch strings.TrimSpace(p) {
		case string(driver.Android):
			keep = append(keep, newAndroidProvider)
		case string(driver.IOS):
			keep = append(keep, newIOSProvider)
		}
	}
	providerFactories = keep
}

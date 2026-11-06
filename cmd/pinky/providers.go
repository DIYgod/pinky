package main

import (
	"log/slog"

	"github.com/DIYgod/pinky/internal/driver"
)

// providerFactories builds the real device providers. The Android and iOS
// drivers register here when their branches merge, for example:
//
//	providerFactories = append(providerFactories, func() (driver.Provider, error) {
//		return android.NewProvider(android.Options{})
//	})
//
// The gateway itself never branches on the platform.
var providerFactories []func() (driver.Provider, error)

// realProviders returns the providers for real phones. A provider that
// fails to start is skipped with a warning, so one missing tool (adb, a
// tunnel) does not take the others down.
func realProviders(log *slog.Logger) []driver.Provider {
	var out []driver.Provider
	for _, f := range providerFactories {
		p, err := f()
		if err != nil {
			log.Warn("device provider unavailable", "err", err)
			continue
		}
		out = append(out, p)
	}
	return out
}

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/driver/ios"
)

// providerFactories builds the real device providers. The gateway itself
// never branches on the platform.
var providerFactories = []func(log *slog.Logger) (driver.Provider, error){
	newAndroidProvider,
	newIOSProvider,
}

func newAndroidProvider(log *slog.Logger) (driver.Provider, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := &adb.Client{}
	if _, err := c.Version(ctx); err != nil {
		// No adb server yet: start the one that comes with the Android SDK
		// or platform tools, if installed.
		if err := adb.StartServer(ctx); err != nil {
			return nil, fmt.Errorf("android: no adb server, and starting one failed (install Android platform tools): %w", err)
		}
	}
	return android.NewProvider(android.Config{
		Client: c,
		Logf: func(format string, args ...any) {
			log.Debug(fmt.Sprintf(format, args...), "platform", "android")
		},
	}), nil
}

func newIOSProvider(*slog.Logger) (driver.Provider, error) {
	p := ios.NewProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Fails only when usbmuxd is missing (Linux without the package,
	// Windows without Apple Devices); phones themselves report their state.
	if _, err := p.List(ctx); err != nil {
		return nil, fmt.Errorf("ios: %w", err)
	}
	return p, nil
}

// realProviders returns the providers for real phones. A provider that
// fails to start is skipped with a warning, so one missing tool (adb,
// usbmuxd) does not take the others down. PINKY_DEVICES, a comma-separated
// list of device ids, limits Pinky to those phones (for development, when
// other tools use the rest).
func realProviders(log *slog.Logger) []driver.Provider {
	var only map[string]bool
	if v := strings.TrimSpace(os.Getenv("PINKY_DEVICES")); v != "" {
		only = map[string]bool{}
		for _, id := range strings.Split(v, ",") {
			only[strings.TrimSpace(id)] = true
		}
	}
	var out []driver.Provider
	for _, f := range providerFactories {
		p, err := f(log)
		if err != nil {
			log.Warn("device provider unavailable", "err", err)
			continue
		}
		if only != nil {
			p = &onlyProvider{Provider: p, only: only}
		}
		out = append(out, p)
	}
	return out
}

// onlyProvider hides every device not in only. It is used as a pointer,
// since the gateway compares providers.
type onlyProvider struct {
	driver.Provider
	only map[string]bool
}

func (p *onlyProvider) List(ctx context.Context) ([]driver.Info, error) {
	infos, err := p.Provider.List(ctx)
	out := infos[:0]
	for _, info := range infos {
		if p.only[info.ID] {
			out = append(out, info)
		}
	}
	return out, err
}

func (p *onlyProvider) Open(ctx context.Context, id string) (driver.Device, error) {
	if !p.only[id] {
		return nil, fmt.Errorf("%s is not in PINKY_DEVICES: %w", id, driver.ErrNotFound)
	}
	return p.Provider.Open(ctx, id)
}

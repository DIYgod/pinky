package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := &adb.Client{}
	// Pinky uses the adb server that runs, or starts one: with the adb of
	// the Android SDK, Homebrew or PATH, else its own (adb.StartServer),
	// now and whenever the server goes away.
	start := func(ctx context.Context) error {
		t, err := adb.StartServer(ctx)
		if err != nil {
			log.Warn("android: starting an adb server", "err", err)
			return err
		}
		if t.Path != "" {
			log.Info("android: started an adb server", "adb", t.Path, "from", t.Source, "version", t.Version)
		}
		return nil
	}
	if _, err := c.Version(ctx); errors.Is(err, adb.ErrServerNotRunning) {
		if err := start(ctx); errors.Is(err, adb.ErrNoADB) {
			// A build without Pinky's own adb, on a computer without one.
			return nil, fmt.Errorf("android: no adb server, and no adb to start one (install Android's platform tools, or set %s): %w", adb.EnvADB, err)
		}
	}
	return android.NewProvider(android.Config{
		Client:      c,
		StartServer: start,
		// adb's own discovery stops seeing the network after the
		// computer's network changes; the driver then reconnects the
		// phones paired over Wi-Fi itself. Not while PINKY_DEVICES limits
		// Pinky to some phones: the others belong to other tools.
		ReconnectWiFi: strings.TrimSpace(os.Getenv("PINKY_DEVICES")) == "",
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
			op := &onlyProvider{Provider: p, only: only}
			p = op
			if pr, ok := op.Provider.(driver.Pairer); ok {
				p = &onlyPairer{onlyProvider: op, pairer: pr}
			}
		}
		out = append(out, p)
	}
	return out
}

// onlyProvider hides every device not in only. It is used as a pointer,
// since the gateway compares providers.
type onlyProvider struct {
	driver.Provider

	mu   sync.Mutex
	only map[string]bool
}

func (p *onlyProvider) allowed(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.only[id]
}

func (p *onlyProvider) allow(id string) {
	p.mu.Lock()
	p.only[id] = true
	p.mu.Unlock()
}

func (p *onlyProvider) List(ctx context.Context) ([]driver.Info, error) {
	infos, err := p.Provider.List(ctx)
	out := infos[:0]
	for _, info := range infos {
		if p.allowed(info.ID) {
			out = append(out, info)
		}
	}
	return out, err
}

func (p *onlyProvider) Open(ctx context.Context, id string) (driver.Device, error) {
	if !p.allowed(id) {
		return nil, fmt.Errorf("%s is not in PINKY_DEVICES: %w", id, driver.ErrNotFound)
	}
	return p.Provider.Open(ctx, id)
}

// onlyPairer is an onlyProvider whose provider pairs phones over Wi-Fi: it
// keeps pairing available, and a phone the person pairs joins the phones
// Pinky may use.
type onlyPairer struct {
	*onlyProvider
	pairer driver.Pairer
}

func (p *onlyPairer) StartPairing(ctx context.Context) (driver.Pairing, error) {
	pg, err := p.pairer.StartPairing(ctx)
	if err != nil {
		return nil, err
	}
	return &allowOnPair{Pairing: pg, p: p.onlyProvider}, nil
}

func (p *onlyPairer) PairWithCode(ctx context.Context, addr, code string) (driver.Info, error) {
	info, err := p.pairer.PairWithCode(ctx, addr, code)
	if err == nil {
		p.allow(info.ID)
	}
	return info, err
}

// ConnectWiFi connects a paired phone at the address it shows, and lets it
// in as a pairing does.
func (p *onlyPairer) ConnectWiFi(ctx context.Context, addr string) (driver.Info, error) {
	wc, ok := p.pairer.(driver.WiFiConnector)
	if !ok {
		return driver.Info{}, fmt.Errorf("connecting by address: %w", driver.ErrUnsupported)
	}
	info, err := wc.ConnectWiFi(ctx, addr)
	if err == nil {
		p.allow(info.ID)
	}
	return info, err
}

type allowOnPair struct {
	driver.Pairing
	p *onlyProvider
}

func (a *allowOnPair) Wait(ctx context.Context) (driver.Info, error) {
	info, err := a.Pairing.Wait(ctx)
	if err == nil {
		a.p.allow(info.ID)
	}
	return info, err
}

// Progress passes the pairing's progress on (driver.PairingReporter).
func (a *allowOnPair) Progress() driver.PairingProgress {
	if r, ok := a.Pairing.(driver.PairingReporter); ok {
		return r.Progress()
	}
	return driver.PairingProgress{}
}

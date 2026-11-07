// Package android is Pinky's Android driver. It talks to phones through
// the adb server (package adb) and the bundled scrcpy server (package
// scrcpy): one long-lived scrcpy session per device carries the video
// stream, input and settle detection, and `uiautomator dump` provides the
// element tree until the companion app takes over.
package android

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
)

// Config tunes the driver. The zero value works.
type Config struct {
	// Client talks to the adb server; nil means the default server.
	Client *adb.Client

	// MaxSize and MaxFPS set the long-lived video session's stream when no
	// Stream call asked for something else. Zero means native size and
	// the device's frame rate.
	MaxSize int
	MaxFPS  int
	// VideoBitRate in bits per second; zero is scrcpy's default (8 Mbps).
	VideoBitRate int

	// SettleMax is the settle limit when WaitSettled gets none (3 s).
	SettleMax time.Duration
	// SettleQuiet is how long the video must stay still to count as
	// settled (400 ms).
	SettleQuiet time.Duration
	// SettleBytes is the packet size, in bytes, at or above which a video
	// packet counts as a change. Zero derives it from the frame size.
	SettleBytes int

	// Logf, when set, receives debug messages.
	Logf func(format string, args ...any)
}

func (c *Config) defaults() {
	if c.Client == nil {
		c.Client = &adb.Client{}
	}
	if c.SettleMax <= 0 {
		c.SettleMax = 3 * time.Second
	}
	if c.SettleQuiet <= 0 {
		c.SettleQuiet = 400 * time.Millisecond
	}
}

func (c *Config) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// Provider discovers and opens Android devices.
type Provider struct {
	cfg Config

	mu    sync.Mutex
	props map[string]deviceProps // by serial and transport id
}

// NewProvider returns a provider with the given configuration.
func NewProvider(cfg Config) *Provider {
	cfg.defaults()
	return &Provider{cfg: cfg, props: map[string]deviceProps{}}
}

// Platform implements driver.Provider.
func (p *Provider) Platform() driver.Platform { return driver.Android }

// deviceProps are the device facts read once per connection.
type deviceProps struct {
	Name, Model, OSVersion string
	SDK                    int
}

// List implements driver.Provider. It reads each online device's
// properties once per connection.
func (p *Provider) List(ctx context.Context) ([]driver.Info, error) {
	entries, err := p.cfg.Client.Devices(ctx)
	if err != nil {
		return nil, wrapErr(err)
	}
	infos := make([]driver.Info, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		infos[i] = infoFromEntry(e)
		if e.State != adb.StateDevice {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if pr, err := p.deviceProps(pctx, e); err == nil {
				infos[i].Name, infos[i].Model, infos[i].OSVersion = pr.Name, pr.Model, pr.OSVersion
			} else {
				p.cfg.logf("android: read properties of %s: %v", e.Serial, err)
			}
		}()
	}
	wg.Wait()
	return infos, nil
}

func (p *Provider) deviceProps(ctx context.Context, e adb.DeviceEntry) (deviceProps, error) {
	key := e.Serial + "#" + e.TransportID
	p.mu.Lock()
	pr, ok := p.props[key]
	p.mu.Unlock()
	if ok {
		return pr, nil
	}
	out, err := p.cfg.Client.Shell(ctx, e.Serial,
		"getprop ro.product.model; getprop ro.build.version.release; getprop ro.build.version.sdk; settings get global device_name")
	if err != nil {
		return deviceProps{}, err
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n")
	for len(lines) < 4 {
		lines = append(lines, "")
	}
	pr = deviceProps{Model: strings.TrimSpace(lines[0]), OSVersion: strings.TrimSpace(lines[1])}
	fmt.Sscan(strings.TrimSpace(lines[2]), &pr.SDK)
	pr.Name = strings.TrimSpace(lines[3])
	if pr.Name == "" || pr.Name == "null" {
		pr.Name = pr.Model
	}
	if pr.Model == "" {
		pr.Model = strings.ReplaceAll(e.Model, "_", " ")
	}
	p.mu.Lock()
	p.props[key] = pr
	p.mu.Unlock()
	return pr, nil
}

func infoFromEntry(e adb.DeviceEntry) driver.Info {
	info := driver.Info{
		ID:         e.Serial,
		Platform:   driver.Android,
		Name:       strings.ReplaceAll(e.Model, "_", " "),
		Model:      strings.ReplaceAll(e.Model, "_", " "),
		Connection: "usb",
		Backend:    "scrcpy",
	}
	if e.Wireless() {
		info.Connection = "wifi"
	}
	if info.Name == "" {
		info.Name = e.Serial
	}
	switch e.State {
	case adb.StateDevice:
		info.State = driver.StateOnline
	case adb.StateUnauthorized, adb.StateAuthorizing:
		info.State = driver.StateUnauthorized
		info.Detail = "Unlock the phone and allow USB debugging for this computer."
	case adb.StateNoPerm:
		info.State = driver.StateNeedsSetup
		info.Detail = "This computer has no permission to use the phone over USB"
		if e.Detail != "" {
			info.Detail += " (" + e.Detail + ")"
		}
		info.Detail += "."
	case adb.StateRecovery, adb.StateBootloader, adb.StateSideload, adb.StateRescue:
		info.State = driver.StateOffline
		info.Detail = "The phone is in " + e.State + " mode."
	default:
		info.State = driver.StateOffline
		info.Detail = "adb reports the phone as " + e.State + "; reconnect the cable or turn USB debugging off and on."
	}
	return info
}

// Open implements driver.Provider. Open each device once and share the
// result: every open device runs its own scrcpy session.
func (p *Provider) Open(ctx context.Context, id string) (driver.Device, error) {
	return p.OpenDevice(ctx, id)
}

// OpenDevice is Open with the concrete type, for callers that use the
// Android-only features (background mode).
func (p *Provider) OpenDevice(ctx context.Context, id string) (*Device, error) {
	entries, err := p.cfg.Client.Devices(ctx)
	if err != nil {
		return nil, wrapErr(err)
	}
	for _, e := range entries {
		if e.Serial != id {
			continue
		}
		info := infoFromEntry(e)
		if info.State != driver.StateOnline {
			return nil, fmt.Errorf("android: %s is %s: %s: %w", id, info.State, info.Detail, driver.ErrOffline)
		}
		pr, err := p.deviceProps(ctx, e)
		if err != nil {
			return nil, wrapErr(err)
		}
		info.Name, info.Model, info.OSVersion = pr.Name, pr.Model, pr.OSVersion
		return newDevice(p.cfg, info, pr), nil
	}
	return nil, fmt.Errorf("android: device %q: %w", id, driver.ErrNotFound)
}

// wrapErr marks errors that mean the phone is gone as driver.ErrOffline.
func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, driver.ErrOffline) {
		return err
	}
	if errors.Is(err, adb.ErrDeviceNotFound) || errors.Is(err, adb.ErrDeviceOffline) ||
		errors.Is(err, adb.ErrServerNotRunning) || errors.Is(err, adb.ErrUnauthorized) {
		return fmt.Errorf("%w: %v", driver.ErrOffline, err)
	}
	return err
}

var (
	_ driver.Provider = (*Provider)(nil)
)

// Package fake is an in-memory driver for tests and UI development.
package fake

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// Provider serves a fixed set of fake devices.
type Provider struct {
	mu      sync.Mutex
	devices map[string]*Device
	order   []string
}

// NewProvider returns a provider with the given devices.
func NewProvider(devices ...*Device) *Provider {
	p := &Provider{devices: map[string]*Device{}}
	for _, d := range devices {
		p.devices[d.info.ID] = d
		p.order = append(p.order, d.info.ID)
	}
	return p
}

// Platform implements driver.Provider.
func (p *Provider) Platform() driver.Platform { return driver.Android }

// List implements driver.Provider.
func (p *Provider) List(context.Context) ([]driver.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]driver.Info, 0, len(p.order))
	for _, id := range p.order {
		out = append(out, p.devices[id].info)
	}
	return out, nil
}

// Open implements driver.Provider.
func (p *Provider) Open(_ context.Context, id string) (driver.Device, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d, ok := p.devices[id]
	if !ok {
		return nil, fmt.Errorf("fake device %q: %w", id, driver.ErrNotFound)
	}
	return d, nil
}

// Action is one recorded input.
type Action struct {
	Kind string // tap, swipe, pinch, type, key, launch, touch, touch2
	P    screen.Point
	To   screen.Point
	Text string
	// Pinch is the gesture of a pinch.
	Pinch driver.Pinch
}

// Device is a fake phone. Its screen is whatever SetScreen last set, and
// every input is recorded.
type Device struct {
	mu      sync.Mutex
	info    driver.Info
	scr     *screen.Screen
	actions []Action
	onInput func(Action)
}

// NewDevice returns an online fake Android device.
func NewDevice(id, name string) *Device {
	return &Device{
		info: driver.Info{ID: id, Platform: driver.Android, Name: name, Model: name, OSVersion: "16",
			Connection: "usb", Backend: "fake", State: driver.StateOnline},
		scr: &screen.Screen{Width: 1080, Height: 2400, Orientation: "portrait"},
	}
}

// SetScreen replaces what Observe returns.
func (d *Device) SetScreen(s *screen.Screen) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.scr = s
}

// OnInput sets a hook run after each recorded input, to script screens.
func (d *Device) OnInput(f func(Action)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onInput = f
}

// Actions returns the inputs recorded so far.
func (d *Device) Actions() []Action {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Action(nil), d.actions...)
}

func (d *Device) record(a Action) {
	d.mu.Lock()
	d.actions = append(d.actions, a)
	f := d.onInput
	d.mu.Unlock()
	if f != nil {
		f(a)
	}
}

// Info implements driver.Device.
func (d *Device) Info() driver.Info { return d.info }

// Observe implements driver.Device. It returns a copy of the current screen.
func (d *Device) Observe(context.Context, driver.ObserveOptions) (*screen.Screen, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := *d.scr
	s.Elements = append([]screen.Element(nil), d.scr.Elements...)
	s.Events = append([]screen.Event(nil), d.scr.Events...)
	s.CapturedAt = time.Now()
	return &s, nil
}

// Screenshot implements driver.Device with a plain gray image.
func (d *Device) Screenshot(context.Context) (image.Image, error) {
	d.mu.Lock()
	w, h := d.scr.Width, d.scr.Height
	d.mu.Unlock()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xEE
	}
	img.Set(0, 0, color.Black)
	return img, nil
}

// Tap implements driver.Device.
func (d *Device) Tap(_ context.Context, p screen.Point, _ driver.TapOptions) error {
	d.record(Action{Kind: "tap", P: p})
	return nil
}

// Swipe implements driver.Device.
func (d *Device) Swipe(_ context.Context, from, to screen.Point, _ time.Duration) error {
	d.record(Action{Kind: "swipe", P: from, To: to})
	return nil
}

// Pinch implements driver.Pincher: P and To are where the fingers touch
// down.
func (d *Device) Pinch(_ context.Context, g driver.Pinch) error {
	a, b := g.At(0)
	d.record(Action{Kind: "pinch", P: a, To: b, Pinch: g})
	return nil
}

// Type implements driver.Device.
func (d *Device) Type(_ context.Context, text string, _ driver.TypeOptions) error {
	d.record(Action{Kind: "type", Text: text})
	return nil
}

// Key implements driver.Device.
func (d *Device) Key(_ context.Context, k driver.Key) error {
	d.record(Action{Kind: "key", Text: string(k)})
	return nil
}

// Launch implements driver.Device.
func (d *Device) Launch(_ context.Context, app string) error {
	d.record(Action{Kind: "launch", Text: app})
	return nil
}

// Apps implements driver.Device.
func (d *Device) Apps(context.Context) ([]driver.App, error) {
	return []driver.App{{ID: "com.android.settings", Name: "Settings", System: true}}, nil
}

// Stream implements driver.Device. Fake devices have no video.
func (d *Device) Stream(context.Context, driver.StreamOptions) (driver.Stream, error) {
	return nil, driver.ErrUnsupported
}

// Touch implements driver.Toucher.
func (d *Device) Touch(_ context.Context, a driver.TouchAction, p screen.Point) error {
	d.record(Action{Kind: "touch:" + string(a), P: p})
	return nil
}

// Touch2 implements driver.MultiToucher.
func (d *Device) Touch2(_ context.Context, a driver.TouchAction, p, q screen.Point) error {
	d.record(Action{Kind: "touch2:" + string(a), P: p, To: q})
	return nil
}

// Close implements driver.Device.
func (d *Device) Close() error { return nil }

var (
	_ driver.Provider     = (*Provider)(nil)
	_ driver.Device       = (*Device)(nil)
	_ driver.Toucher      = (*Device)(nil)
	_ driver.Pincher      = (*Device)(nil)
	_ driver.MultiToucher = (*Device)(nil)
)

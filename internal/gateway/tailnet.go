package gateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/DIYgod/pinky/internal/driver"
)

// Phones away from the computer's network, through Tailscale
// (driver.Tailnet): what Tailscale on this computer reports and the phones
// on its tailnet, connecting to one there, and setting a phone up to be
// reached that way.

// ErrNoTailnet is returned when no provider reaches phones through
// Tailscale.
var ErrNoTailnet = errors.New("no connected platform reaches phones through Tailscale")

// tailnet returns the first provider that reaches phones through
// Tailscale.
func (g *Gateway) tailnet() (driver.Tailnet, error) {
	for _, p := range g.opt.Providers {
		if t, ok := p.(driver.Tailnet); ok {
			return t, nil
		}
	}
	return nil, ErrNoTailnet
}

// tailnetOf returns a phone and its provider, which must reach phones
// through Tailscale.
func (g *Gateway) tailnetOf(deviceID string) (*device, driver.Tailnet, error) {
	d, err := g.device(deviceID)
	if err != nil {
		return nil, nil, err
	}
	g.mu.Lock()
	p := d.provider
	g.mu.Unlock()
	t, ok := p.(driver.Tailnet)
	if !ok {
		return nil, nil, fmt.Errorf("%s cannot be reached through Tailscale: %w", d.name(), driver.ErrUnsupported)
	}
	return d, t, nil
}

// Tailnet says what Tailscale on this computer reports, and lists the
// phones on its tailnet.
func (g *Gateway) Tailnet(ctx context.Context) (driver.TailnetStatus, error) {
	t, err := g.tailnet()
	if err != nil {
		return driver.TailnetStatus{}, err
	}
	return t.Tailnet(ctx)
}

// ConnectTailnet connects to a phone at its Tailscale address. A phone the
// person removed is back once it connects.
func (g *Gateway) ConnectTailnet(ctx context.Context, addr string) (driver.Info, error) {
	t, err := g.tailnet()
	if err != nil {
		return driver.Info{}, err
	}
	info, err := t.ConnectTailnet(ctx, addr)
	if err == nil {
		g.unremove(info.ID)
		g.discover(ctx)
	}
	return info, err
}

// Away says whether a phone is set up to be reached through Tailscale,
// away from the computer's network.
func (g *Gateway) Away(ctx context.Context, deviceID string) (driver.Away, error) {
	d, t, err := g.tailnetOf(deviceID)
	if err != nil {
		return driver.Away{}, err
	}
	return t.Away(ctx, d.id)
}

// SetAway sets a phone up to be reached through Tailscale, or stops that.
// Setting it up restarts the phone's adb, which drops its connections for
// a moment: a task on it survives that like a short disconnection
// (Options.ReconnectGrace).
func (g *Gateway) SetAway(ctx context.Context, deviceID string, on bool) (driver.Away, error) {
	d, t, err := g.tailnetOf(deviceID)
	if err != nil {
		return driver.Away{}, err
	}
	a, err := t.SetAway(ctx, d.id, on)
	g.discover(ctx)
	g.emit(Event{Kind: EventDevices, DeviceID: d.id})
	return a, err
}

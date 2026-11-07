package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
)

// pairingProvider lists the phones it paired.
type pairingProvider struct {
	listed []driver.Info
}

func (p *pairingProvider) Platform() driver.Platform { return driver.Android }

func (p *pairingProvider) List(context.Context) ([]driver.Info, error) {
	return append([]driver.Info(nil), p.listed...), nil
}

func (p *pairingProvider) Open(context.Context, string) (driver.Device, error) {
	return nil, driver.ErrUnsupported
}

func (p *pairingProvider) pair(id string) driver.Info {
	info := driver.Info{ID: id, Platform: driver.Android, State: driver.StateOnline, Connection: "wifi"}
	p.listed = append(p.listed, info)
	return info
}

func (p *pairingProvider) StartPairing(context.Context) (driver.Pairing, error) {
	return &fakePairing{p: p}, nil
}

func (p *pairingProvider) PairWithCode(_ context.Context, addr, _ string) (driver.Info, error) {
	return p.pair("code-" + addr), nil
}

type fakePairing struct{ p *pairingProvider }

func (f *fakePairing) QR() string                                { return "WIFI:T:ADB;S:pinky-test;P:secret;;" }
func (f *fakePairing) Wait(context.Context) (driver.Info, error) { return f.p.pair("qr-phone"), nil }
func (f *fakePairing) Close() error                              { return nil }

// TestDeviceFilterKeepsPairing checks that PINKY_DEVICES keeps Wi-Fi
// pairing available and lets the phones paired through it in.
func TestDeviceFilterKeepsPairing(t *testing.T) {
	under := &pairingProvider{listed: []driver.Info{{ID: "usb-phone"}, {ID: "other-phone"}}}
	t.Setenv("PINKY_DEVICES", "usb-phone")
	saved := providerFactories
	t.Cleanup(func() { providerFactories = saved })
	providerFactories = providerFactories[:0:0]
	providerFactories = append(providerFactories, func(*slog.Logger) (driver.Provider, error) { return under, nil })

	ps := realProviders(slog.New(slog.DiscardHandler))
	if len(ps) != 1 {
		t.Fatalf("%d providers", len(ps))
	}
	pr, ok := ps[0].(driver.Pairer)
	if !ok {
		t.Fatalf("%T hides driver.Pairer", ps[0])
	}
	ctx := context.Background()
	ids := func() []string {
		infos, _ := ps[0].List(ctx)
		var out []string
		for _, i := range infos {
			out = append(out, i.ID)
		}
		return out
	}
	if got := ids(); len(got) != 1 || got[0] != "usb-phone" {
		t.Fatalf("listed %v", got)
	}
	pg, err := pr.StartPairing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.PairWithCode(ctx, "10.0.0.2:37000", "123456"); err != nil {
		t.Fatal(err)
	}
	got := ids()
	want := map[string]bool{"usb-phone": true, "qr-phone": true, "code-10.0.0.2:37000": true}
	if len(got) != len(want) {
		t.Fatalf("listed %v", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("listed %q", id)
		}
	}
}

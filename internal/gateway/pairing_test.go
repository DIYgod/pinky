package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
)

// wifiProvider pairs phones over Wi-Fi and connects them by address.
type wifiProvider struct {
	*demo.Provider

	mu     sync.Mutex
	listed []driver.Info
}

func (p *wifiProvider) List(context.Context) ([]driver.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]driver.Info(nil), p.listed...), nil
}

func (p *wifiProvider) add(id string) driver.Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	info := driver.Info{ID: id, Platform: driver.Android, Name: "Pixel 9", State: driver.StateOffline, Connection: "wifi"}
	p.listed = append(p.listed, info)
	return info
}

func (p *wifiProvider) StartPairing(context.Context) (driver.Pairing, error) {
	return reportingPairing{}, nil
}

func (p *wifiProvider) PairWithCode(context.Context, string, string) (driver.Info, error) {
	return driver.Info{}, driver.ErrPairedNotFound
}

func (p *wifiProvider) ConnectWiFi(_ context.Context, addr string) (driver.Info, error) {
	return p.add("R5CN20ABC"), nil
}

type reportingPairing struct{}

func (reportingPairing) QR() string { return "WIFI:T:ADB;S:pinky-x;P:y;;" }
func (reportingPairing) Wait(context.Context) (driver.Info, error) {
	return driver.Info{}, context.Canceled
}
func (reportingPairing) Close() error { return nil }
func (reportingPairing) Progress() driver.PairingProgress {
	return driver.PairingProgress{Found: true, NotAllowed: true}
}

// TestPairingExtras: the gateway passes a pairing's progress on, and
// connects a phone by the address it shows, which then shows in Devices
// at once.
func TestPairingExtras(t *testing.T) {
	p := &wifiProvider{Provider: demo.NewProvider(driver.Android)}
	g, err := New(Options{Providers: []driver.Provider{p}, DataDir: t.TempDir(), NoScreenshots: true, DiscoverEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	ctx := context.Background()
	pg, err := g.StartPairing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := pg.(driver.PairingReporter)
	if !ok {
		t.Fatalf("%T hides the progress", pg)
	}
	if pr := r.Progress(); !pr.Found || !pr.NotAllowed {
		t.Errorf("progress = %+v", pr)
	}
	info, err := g.ConnectWiFi(ctx, "192.168.1.42:41000")
	if err != nil || info.ID != "R5CN20ABC" {
		t.Fatalf("ConnectWiFi = %+v, %v", info, err)
	}
	found := false
	for _, d := range g.Devices() {
		found = found || d.Info.ID == "R5CN20ABC"
	}
	if !found {
		t.Errorf("the phone is not in Devices: %+v", g.Devices())
	}
}

//go:build device

package android

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// TestDeviceWiFiSwitch moves a real phone to Wi-Fi (adb tcpip, then a
// connection at its Wi-Fi address), drops that connection and has the
// reconnect pass bring it back, then gives the phone a wrong address and
// has the pass find the right one with mDNS. With PINKY_WIFI_OFF=1 it ends
// with the Wi-Fi connection stopped (adb usb).
//
//	PINKY_DEVICE=<phone id> go test -tags device -run TestDeviceWiFiSwitch
//
// On macOS the test binary needs the Local Network for the mDNS step:
// sign it with an identifier, as docs/notes/android.md says.
func TestDeviceWiFiSwitch(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p := NewProvider(Config{Logf: t.Logf, NetworkFile: filepath.Join(t.TempDir(), "network-phones.json")})
	if _, err := p.List(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := p.WiFi(ctx, id)
	t.Logf("before: %+v, %v", st, err)
	start := time.Now()
	st, err = p.SetWiFi(ctx, id, true)
	if err != nil || !st.On || !st.Connected {
		t.Fatalf("on: %+v, %v", st, err)
	}
	t.Logf("on in %v: %+v", time.Since(start).Round(time.Millisecond), st)
	n, _ := p.net.get(id)

	// Dropped: the pass connects again.
	if err := p.cfg.Client.Disconnect(ctx, n.lanSerial()); err != nil {
		t.Fatal(err)
	}
	entries, _ := p.cfg.Client.Devices(ctx)
	start = time.Now()
	if missing := p.link.reconnect(ctx, p.net.list(), entries); missing {
		t.Fatalf("not reconnected at %s", n.lanSerial())
	}
	t.Logf("reconnected at %s in %v", n.lanSerial(), time.Since(start).Round(time.Millisecond))

	// A wrong address: mDNS finds the right one.
	if err := p.cfg.Client.Disconnect(ctx, n.lanSerial()); err != nil {
		t.Fatal(err)
	}
	p.net.update(id, func(m *netPhone) { m.LAN = "192.168.5.250" })
	entries, _ = p.cfg.Client.Devices(ctx)
	start = time.Now()
	missing := p.link.reconnect(ctx, p.net.list(), entries)
	got, _ := p.net.get(id)
	t.Logf("after a wrong address: missing %v, kept %s, in %v", missing, got.LAN, time.Since(start).Round(time.Millisecond))
	if missing || got.LAN != n.LAN {
		t.Errorf("not found again: missing %v, kept %+v", missing, got)
	}
	infos, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if in.ID == id {
			t.Logf("listed: %s %s via=%q state=%s", in.ID, in.Connection, in.Via, in.State)
			if in.State != driver.StateOnline {
				t.Errorf("listed as %s", in.State)
			}
		}
	}
	if os.Getenv("PINKY_WIFI_OFF") == "1" {
		st, err = p.SetWiFi(ctx, id, false)
		t.Logf("off: %+v, %v", st, err)
		if err != nil || st.On {
			t.Errorf("off: %+v, %v", st, err)
		}
	}
}

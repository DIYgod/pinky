package ios

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/xpc"
)

func entry(udid string, deviceID int, conn string) goios.DeviceEntry {
	return goios.DeviceEntry{DeviceID: deviceID, Properties: goios.DeviceProperties{SerialNumber: udid, ConnectionType: conn, DeviceID: deviceID}}
}

func TestProviderEntries(t *testing.T) {
	list := []goios.DeviceEntry{
		entry("b", 3, "Network"), entry("b", 4, "USB"), entry("a", 5, "USB"), entry("ipad", 6, "Network"),
	}
	p := NewProvider()
	got := p.entries(list)
	if len(got) != 2 || got[0].Properties.SerialNumber != "a" || got[1].DeviceID != 4 {
		t.Fatalf("entries without Wireless = %+v", got)
	}
	p.Wireless = true
	got = p.entries(list)
	if len(got) != 3 || got[1].DeviceID != 4 || got[2].Properties.SerialNumber != "ipad" {
		t.Fatalf("entries with Wireless = %+v", got)
	}
}

func TestProviderCachesInspect(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	state := map[string]driver.State{"on": driver.StateOnline, "setup": driver.StateNeedsSetup}
	now := time.Unix(1000, 0)
	p := NewProvider()
	p.now = func() time.Time { return now }
	p.inspectFn = func(_ context.Context, e goios.DeviceEntry) driver.Info {
		mu.Lock()
		defer mu.Unlock()
		id := e.Properties.SerialNumber
		calls[id]++
		return driver.Info{ID: id, State: state[id]}
	}
	ctx := context.Background()
	on, setup := entry("on", 1, "USB"), entry("setup", 2, "USB")
	ask := func() {
		for _, e := range []goios.DeviceEntry{on, setup} {
			if got := p.info(ctx, e); got.State != state[e.Properties.SerialNumber] {
				t.Fatalf("info(%s) = %+v", e.Properties.SerialNumber, got)
			}
		}
	}
	ask()
	now = now.Add(10 * time.Second)
	ask()
	if calls["on"] != 1 || calls["setup"] != 1 {
		t.Fatalf("after 10 s: calls %v, want one each", calls)
	}
	now = now.Add(25 * time.Second)
	ask()
	if calls["on"] != 1 || calls["setup"] != 2 {
		t.Fatalf("after 35 s: calls %v, want the online phone cached and the other asked again", calls)
	}
	on = entry("on", 7, "USB") // replugged
	ask()
	if calls["on"] != 2 {
		t.Fatalf("after a new entry: calls %v, want the phone asked again", calls)
	}
	p.forgetExcept([]goios.DeviceEntry{setup})
	ask()
	if calls["on"] != 3 {
		t.Fatalf("after it was gone: calls %v, want it asked again", calls)
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	p.info(cctx, entry("new", 9, "USB"))
	p.info(ctx, entry("new", 9, "USB"))
	if calls["new"] != 2 {
		t.Fatalf("calls %v: an inspection cut short by its context must not be cached", calls)
	}
}

func TestFromVideo(t *testing.T) {
	for _, c := range []struct {
		p              screen.Point
		vw, vh, sw, sh int
		want           screen.Point
	}{
		{screen.Point{X: 100, Y: 200}, 1136, 2464, 1125, 2436, screen.Point{X: 100, Y: 200}},     // padded: unchanged
		{screen.Point{X: 1130, Y: 2450}, 1136, 2464, 1125, 2436, screen.Point{X: 1130, Y: 2450}}, // in the padding: panel clamps
		{screen.Point{X: 100, Y: 200}, 0, 0, 1125, 2436, screen.Point{X: 100, Y: 200}},           // no video yet
		{screen.Point{X: 500, Y: 1000}, 645, 1398, 1290, 2796, screen.Point{X: 1000, Y: 2000}},   // scaled down by half
	} {
		if got := fromVideo(c.p, c.vw, c.vh, c.sw, c.sh); got != c.want {
			t.Errorf("fromVideo(%v, %dx%d, %dx%d) = %v, want %v", c.p, c.vw, c.vh, c.sw, c.sh, got, c.want)
		}
	}
	if x, y := scaleAxis(1130, 1125), scaleAxis(2450, 2436); x != scaleAxis(1124, 1125) || y != scaleAxis(2435, 2436) {
		t.Errorf("a point in the padding maps to %d,%d, want the screen's last row and column", x, y)
	}
}

func TestCDServiceRedials(t *testing.T) {
	dials := 0
	boom := errors.New("dial failed")
	s := &cdService{name: "com.apple.coredevice.deviceinfo", dial: func(goios.DeviceEntry, string) (*xpc.Connection, error) {
		dials++
		return nil, boom
	}}
	// A dropped connection is dialled again on the next request.
	if _, err := s.invoke(context.Background(), "f", "", nil); !errors.Is(err, boom) || dials != 1 {
		t.Fatalf("after a drop: %v, %d dials", err, dials)
	}
	if _, err := s.invoke(context.Background(), "f", "", nil); !errors.Is(err, boom) || dials != 2 {
		t.Fatalf("again: %v, %d dials", err, dials)
	}
	// After Close it stays closed.
	s.Close()
	if _, err := s.invoke(context.Background(), "f", "", nil); err == nil || !strings.Contains(err.Error(), "connection closed") || dials != 2 {
		t.Fatalf("after Close: %v, %d dials", err, dials)
	}
}

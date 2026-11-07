//go:build device

package ios

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// TestDeviceConnection measures one connection, chosen by PINKY_CONN ("usb"
// or "wifi"), in a session of its own: run it alone (-run
// TestDeviceConnection), so the shared USB session does not run beside it.
func TestDeviceConnection(t *testing.T) {
	conn := os.Getenv("PINKY_CONN")
	if conn == "" {
		t.Skip("PINKY_CONN not set")
	}
	id := os.Getenv("PINKY_DEVICE")
	ctx := ctxFor(t, 4*time.Minute)
	start := time.Now()
	d, err := Open(ctx, driver.Info{ID: id, Platform: driver.IOS, Connection: conn})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	t.Logf("%s: entry %s, session open in %v", conn, d.l.base.Properties.ConnectionType, time.Since(start).Round(time.Millisecond))
	defer home(ctx, d)
	home(ctx, d)
	stopRate := logStreamRate(t, d)
	defer stopRate()

	lat, err := d.SwipeLatency(ctx, 6, false)
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	t.Logf("touch-down to changed frame: %v (%v)", lat, err)

	time.Sleep(2 * time.Second) // let the remoteAXService connection settle
	for i := 0; i < 3; i++ {
		t0 := time.Now()
		s, st, err := d.ObserveWithStats(ctx, driver.ObserveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("home screen observe %d: %v, %d elements, source %s", i, time.Since(t0).Round(time.Millisecond), len(s.Elements), st.Source)
		pause()
	}
	for i := 0; i < 2; i++ {
		t0 := time.Now()
		img, err := d.Screenshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("screenshot %d: %v (%v)", i, time.Since(t0).Round(time.Millisecond), img.Bounds().Size())
		pause()
	}
	// The screen capture service takes a format; PNGs are 16 bits a channel
	// and 5 to 7 MB, which costs seconds over Wi-Fi.
	for _, format := range []string{"jpeg", "heic"} {
		t0 := time.Now()
		b, err := d.captureFormat(ctx, format)
		t.Logf("capture as %s: %d bytes in %v, %v", format, len(b), time.Since(t0).Round(time.Millisecond), err)
		pause()
	}
	if err := d.Launch(ctx, "com.apple.Preferences"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	t0 := time.Now()
	s, err := d.Observe(ctx, driver.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Settings observe after launch: %v, %d elements", time.Since(t0).Round(time.Millisecond), len(s.Elements))
	var field screen.Element
	for _, e := range s.Elements {
		if e.Role == screen.RoleInput && e.Label == "Search" && !e.Bounds.Empty() {
			field = e
		}
	}
	if !field.Bounds.Empty() {
		d.Tap(ctx, field.Bounds.Center(), driver.TapOptions{})
		time.Sleep(1200 * time.Millisecond)
		if err := d.Type(ctx, "clock", driver.TypeOptions{}); err != nil {
			t.Error(err)
		}
		st := d.LastTypeStats()
		t.Logf("type: %s in %v (saved %s, %s)", st.Method, st.Total.Round(time.Millisecond), st.Saved, st.Restore)
		time.Sleep(time.Second)
		leaveSearch(ctx, t, d)
	}
	ms, _ := d.StreamStats()
	elapsed := time.Since(ms.Since).Seconds()
	t.Logf("stream: %d frames, %d keyframes, %d packets, %d lost, %d dropped, %d keyframe requests, %.1f Mbit/s over %.0f s, delay %v (known %v)",
		ms.Frames, ms.KeyFrames, ms.Packets, ms.Lost, ms.Dropped, ms.KeyRequests, float64(ms.Bytes)*8/1e6/elapsed, elapsed, ms.Delay, ms.DelayKnown)
}

// captureFormat asks CoreDevice's screen capture service for one format.
func (d *Device) captureFormat(ctx context.Context, format string) ([]byte, error) {
	d.svcMu.Lock()
	defer d.svcMu.Unlock()
	if d.shot == nil {
		svc, err := dialCoreDevice(d.l.entry, screenCaptureService)
		if err != nil {
			return nil, err
		}
		d.shot = svc
	}
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := d.shot.invoke(c, featureCaptureScreenshot, actionCaptureScreenshot, map[string]any{"requestedFormat": format})
	if err != nil {
		d.shot.Close()
		d.shot = nil
		return nil, err
	}
	img, _ := out["image"].([]byte)
	return img, nil
}

// logStreamRate logs the stream's bitrate, loss and key frame requests every
// second, to see whether the phone's encoder follows the loss it is told.
func logStreamRate(t *testing.T, d *Device) func() {
	done := make(chan struct{})
	go func() {
		prev, _ := d.StreamStats()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
			}
			cur, ok := d.StreamStats()
			if !ok {
				continue
			}
			t.Logf("rate: %.2f Mbit/s, %d frames, %d lost, %d key requests", float64(cur.Bytes-prev.Bytes)*8/1e6, cur.Frames-prev.Frames, cur.Lost-prev.Lost, cur.KeyRequests-prev.KeyRequests)
			prev = cur
		}
	}()
	return func() { close(done) }
}

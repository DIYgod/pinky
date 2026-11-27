//go:build device

package harmony

import (
	"context"
	"image"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// This test reads connection metadata only; it never reads or controls the
// screen, installs apps, or accepts a debugging authorization prompt.
func TestDeviceDiscovery(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("set PINKY_DEVICE; run through scripts/devlock.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	infos, err := NewProvider().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range infos {
		if info.ID == id {
			if info.Platform != driver.HarmonyOS || info.Backend != "hdc" {
				t.Fatalf("info = %+v", info)
			}
			t.Logf("platform=%s connection=%s state=%s setup=%s", info.Platform, info.Connection, info.State, info.Setup)
			return
		}
	}
	t.Fatal("connected test phone was not discovered")
}

// Real-device smoke test: only Calculator and Home, with no installs,
// settings changes, credentials, or personal applications.
func TestDeviceNativeControl(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("set PINKY_DEVICE; use scripts/devlock.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p := NewProvider()
	dev, err := p.Open(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	d := dev.(*Device)
	defer d.Close()
	locked, err := d.Locked(ctx)
	if err != nil || locked {
		t.Fatalf("phone locked or state unknown: %v", err)
	}
	start := time.Now()
	apps, err := d.Apps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range apps {
		if a.ID == "com.huawei.hmos.calculator" {
			found = true
		}
	}
	if !found {
		t.Fatal("Calculator not installed; no app opened")
	}
	t.Logf("Listed %d apps in %v", len(apps), time.Since(start))
	if err := d.Launch(ctx, "com.huawei.hmos.calculator"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	s, err := d.Observe(ctx, driver.ObserveOptions{MayCapture: func(s *screen.Screen) bool { return s.App.ID == "com.huawei.hmos.calculator" && !s.Locked }})
	if err != nil {
		t.Fatal(err)
	}
	if s.Locked || s.App.ID != "com.huawei.hmos.calculator" {
		t.Fatal("unexpected foreground; stopped")
	}
	for _, e := range s.Elements {
		if strings.Contains(e.Label, "同意") || strings.Contains(e.Label, "允许") || strings.Contains(strings.ToLower(e.Label), "agree") {
			t.Fatal("unexpected dialog; stopped")
		}
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cur, err := d.Observe(clean, driver.ObserveOptions{MayCapture: func(*screen.Screen) bool { return false }})
		if err == nil && !cur.Locked && cur.App.ID == "com.huawei.hmos.calculator" {
			_ = d.Key(clean, driver.KeyHome)
		}
	}()
	t.Logf("Observe: %dx%d, %d elements", s.Width, s.Height, len(s.Elements))
	img, err := d.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != s.Width || img.Bounds().Dy() != s.Height {
		t.Fatal("screenshot and input coordinate spaces differ")
	}
	// Confirm a visible change after a tap; no guessed coordinates.
	var digit *screen.Element
	for _, candidate := range []string{"2", "3", "4", "5"} {
		count := 0
		var key *screen.Element
		for i := range s.Elements {
			if strings.HasSuffix(s.Elements[i].Label, candidate) {
				count++
			}
			if s.Elements[i].Label == candidate {
				key = &s.Elements[i]
			}
		}
		if count == 1 && key != nil {
			digit = key
			break
		}
	}
	if digit == nil {
		t.Fatal("Calculator digit not identified; no input sent")
	}
	if err := d.Tap(ctx, digit.Bounds.Center(), driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(350 * time.Millisecond)
	after, err := d.Observe(ctx, driver.ObserveOptions{MayCapture: func(s *screen.Screen) bool { return s.App.ID == "com.huawei.hmos.calculator" && !s.Locked }})
	if err != nil || after.App.ID != "com.huawei.hmos.calculator" {
		t.Fatalf("after tap: %v", err)
	}
	beforeCount, afterCount := 0, 0
	for _, e := range s.Elements {
		if strings.HasSuffix(e.Label, digit.Label) {
			beforeCount++
		}
	}
	for _, e := range after.Elements {
		if strings.HasSuffix(e.Label, digit.Label) {
			afterCount++
		}
	}
	if afterCount <= beforeCount {
		t.Fatalf("tap not verified: before=%d after=%d", beforeCount, afterCount)
	}
	t.Log("Calculator tap changed the displayed value")
	stream, err := d.Stream(ctx, driver.StreamOptions{MaxFPS: 2})
	if err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	frames := 0
read:
	for frames < 2 {
		select {
		case pkt, ok := <-stream.Packets():
			if !ok {
				t.Fatalf("mirror ended: %v", stream.Err())
			}
			if !pkt.Config && len(pkt.Data) > 0 {
				frames++
			}
		case <-timer.C:
			break read
		}
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if frames < 2 {
		t.Fatal("live preview did not produce two frames")
	}
	t.Log("Screenshot-based live preview produced H.264 frames")
}

// Measure only Calculator and Home, without storing any image or tree.
func TestDevicePreviewLatency(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("set PINKY_DEVICE; use scripts/devlock.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dev, err := NewProvider().Open(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	d := dev.(*Device)
	defer d.Close()
	if err := d.Launch(ctx, "com.huawei.hmos.calculator"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(350 * time.Millisecond)
	s, err := d.Observe(ctx, driver.ObserveOptions{MayCapture: func(*screen.Screen) bool { return false }})
	if err != nil || s.Locked || s.App.ID != "com.huawei.hmos.calculator" {
		t.Fatalf("unexpected foreground; stopped: %v", err)
	}
	for _, e := range s.Elements {
		if strings.Contains(e.Label, "同意") || strings.Contains(e.Label, "允许") || strings.Contains(strings.ToLower(e.Label), "agree") {
			t.Fatal("unexpected dialog; stopped")
		}
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cur, err := d.Observe(clean, driver.ObserveOptions{MayCapture: func(*screen.Screen) bool { return false }})
		if err == nil && !cur.Locked && cur.App.ID == "com.huawei.hmos.calculator" {
			_ = d.Key(clean, driver.KeyHome)
		}
	}()
	originalRun := d.run
	d.run = func(ctx context.Context, path string, args ...string) ([]byte, error) {
		start := time.Now()
		out, err := originalRun(ctx, path, args...)
		step := "guards"
		if strings.Contains(args[len(args)-1], "'snapshot_display'") {
			step = "JPEG"
		}
		t.Logf("capture step %s: %v", step, time.Since(start))
		return out, err
	}
	for i := range 3 {
		start := time.Now()
		if _, err := d.Screenshot(ctx); err != nil {
			t.Fatal(err)
		}
		t.Logf("capture %d: %v", i+1, time.Since(start))
	}
	for i := range 2 {
		start := time.Now()
		d.mu.Lock()
		img, err := d.capturePreview(ctx, image.Pt(s.Width, s.Height))
		d.mu.Unlock()
		if err != nil || img.Bounds().Size() != image.Pt(s.Width, s.Height) {
			t.Fatalf("preview capture failed: %v", err)
		}
		t.Logf("scaled preview capture %d: %v", i+1, time.Since(start))
	}
	d.run = originalRun
	start := time.Now()
	st, err := d.Stream(ctx, driver.StreamOptions{MaxFPS: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	frames := 0
	for frames < 2 {
		select {
		case pkt, ok := <-st.Packets():
			if !ok {
				t.Fatalf("stream ended: %v", st.Err())
			}
			if !pkt.Config {
				frames++
				t.Logf("preview frame %d after %v", frames, time.Since(start))
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	// Tap only the Calculator key identified from this run's tree.
	for _, e := range s.Elements {
		if e.Label == "2" {
			time.Sleep(350 * time.Millisecond)
			start := time.Now()
			if err := d.Tap(ctx, e.Bounds.Center(), driver.TapOptions{}); err != nil {
				t.Fatal(err)
			}
			t.Logf("tap while mirroring: %v", time.Since(start))
			return
		}
	}
	t.Fatal("Calculator digit not identified; no input sent")
}

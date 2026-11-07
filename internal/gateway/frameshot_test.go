package gateway

import (
	"context"
	"image"
	"os/exec"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/video"
)

// noShot is a phone whose driver cannot take screenshots, as an Android
// background display cannot (screencap reaches physical displays only).
type noShot struct{ *demo.Device }

func (noShot) Screenshot(context.Context) (image.Image, error) { return nil, driver.ErrUnsupported }

type noShotProvider struct {
	*demo.Provider
	d noShot
}

func (p noShotProvider) Open(context.Context, string) (driver.Device, error) { return p.d, nil }

// TestScreenshotFromVideo gets a screenshot from the phone's video when
// its driver cannot take one, and it shows the screen.
func TestScreenshotFromVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs ffmpeg for the demo phone's video")
	}
	if err := video.Load(); err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	dev := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	dev.SetVideo(true)
	want, err := dev.Screenshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	g, err := New(Options{Providers: []driver.Provider{noShotProvider{demo.NewProvider(driver.Android, dev), noShot{dev}}},
		TrustNewAgents: true, NoScreenshots: true, DiscoverEvery: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for deadline := time.Now().Add(5 * time.Second); len(g.Devices()) == 0 || g.Devices()[0].Status == DeviceOffline; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the phone did not come online")
		}
	}
	d, err := g.device("pixel")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	img, err := g.shotOf(ctx, d, noShot{dev}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("a picture from the video in %v", time.Since(start).Round(time.Millisecond))
	wb, gb := want.Bounds(), img.Bounds()
	if gb.Dx() != wb.Dx()&^1 || gb.Dy() != wb.Dy()&^1 {
		t.Fatalf("picture %v, screen %v", gb, wb)
	}
	// The video is compressed: compare colors loosely at a few points.
	var diff, n int
	for _, f := range [][2]float64{{0.25, 0.2}, {0.5, 0.5}, {0.75, 0.8}, {0.5, 0.1}, {0.2, 0.9}} {
		x, y := int(f[0]*float64(gb.Dx())), int(f[1]*float64(gb.Dy()))
		r1, g1, b1, _ := want.At(wb.Min.X+x, wb.Min.Y+y).RGBA()
		r2, g2, b2, _ := img.At(x, y).RGBA()
		diff += absDiff(r1, r2) + absDiff(g1, g2) + absDiff(b1, b2)
		n += 3
	}
	if avg := diff / n >> 8; avg > 40 {
		t.Errorf("the picture differs from the screen by %d on average", avg)
	}
}

func absDiff(a, b uint32) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

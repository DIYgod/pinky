package harmonyos

import (
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

func TestMatePadDesktopPointMapsToNativeDisplay(t *testing.T) {
	f := mirrorFrame{width: 736, height: 1120, surface: surface{width: 1840, height: 2800, top: 87, bottom: 63}}
	if p, ok := nativeMirrorPoint(f, screen.Point{X: 368, Y: 560}); !ok || p != (screen.Point{X: 921, Y: 1401}) {
		t.Fatalf("preview point mapped to %v, allowed=%v", p, ok)
	}
	for _, point := range []screen.Point{{X: 1, Y: 1}, {X: 400, Y: 22}, {X: 400, Y: 1114}, {X: 736, Y: 560}} {
		if _, ok := nativeMirrorPoint(f, point); ok {
			t.Fatalf("sent input outside the visible Settings surface: %v", point)
		}
	}
}

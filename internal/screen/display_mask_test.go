package screen

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestPhysicalDisplayMask(t *testing.T) {
	const model = "iPhone 13 Pro"
	if PhysicalDisplayMask("iPhone 13 Pro Max", 0) != nil {
		t.Fatal("another model inherited this mask")
	}
	for turn, notch := range []Point{{585, 50}, {2481, 585}, {584, 2481}, {50, 584}} {
		m := PhysicalDisplayMask(model, turn)
		b := image.Rect(7, 9, 7+m.Width, 9+m.Height)
		plain := image.NewRGBA(b)
		draw.Draw(plain, b, image.White, image.Point{}, draw.Src)
		covered := m.Cover(plain)
		if covered.Bounds() != b {
			t.Fatalf("turn %d changed screenshot bounds", turn)
		}
		for _, point := range []Point{{1, 1}, notch, {m.Width - 1, m.Height - 1}} {
			if m.Visible(Rect{X: point.X, Y: point.Y, W: 1, H: 1}, m.Width, m.Height) ||
				covered.At(b.Min.X+point.X, b.Min.Y+point.Y) != (color.RGBA{A: 255}) {
				t.Errorf("turn %d left hidden point %+v visible", turn, point)
			}
		}
		center := Point{m.Width / 2, m.Height / 2}
		if !m.Visible(Rect{X: center.X, Y: center.Y, W: 25, H: 35}, m.Width, m.Height) ||
			covered.At(b.Min.X+center.X, b.Min.Y+center.Y) != (color.RGBA{255, 255, 255, 255}) {
			t.Errorf("turn %d obscured the center", turn)
		}
	}
	m := PhysicalDisplayMask(model, 0)
	if !m.Visible(Rect{X: 230, Y: 20, W: 50, H: 20}, 1170, 2532) ||
		m.Visible(Rect{X: 315, Y: 25, W: 60, H: 10}, 1170, 2532) ||
		!m.Visible(Rect{X: 560, Y: 103, W: 50, H: 20}, 1170, 2532) {
		t.Fatal("the status area, notch transition, or area just below the notch is wrong")
	}
}

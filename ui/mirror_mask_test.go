package ui

import (
	"image/color"
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
	mg "github.com/egoist/mygo/ui"
)

func TestDrawIPhone13ProMask(t *testing.T) {
	for turn, notch := range []screen.Point{{X: 117, Y: 9}, {X: 496, Y: 117}, {X: 116, Y: 496}, {X: 9, Y: 116}} {
		mask := screen.PhysicalDisplayMask("iPhone 13 Pro", turn)
		w, h := mask.Width/5, mask.Height/5
		m := new(mirror)
		tt := mg.NewTester(func(c *mg.Context) {
			mg.Box(c).Fill().Background(hex("#000000")).Draw(func(pt *mg.Painter, r mg.Rect) {
				m.clipMirror(pt, r, 0, mask, func() { pt.Fill(r, hex("#ffffff"), 0) })
			})
		}, w, h)
		pic := tt.Image()
		if pic.RGBAAt(notch.X, notch.Y) != (color.RGBA{A: 255}) ||
			pic.RGBAAt(1, 1) != (color.RGBA{A: 255}) ||
			pic.RGBAAt(w/2, h/2) != (color.RGBA{255, 255, 255, 255}) {
			t.Errorf("rotation %d: notch %v, corner %v, center %v", turn,
				pic.RGBAAt(notch.X, notch.Y), pic.RGBAAt(1, 1), pic.RGBAAt(w/2, h/2))
		}
	}
}

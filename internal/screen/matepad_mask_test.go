package screen

import "testing"

func TestMatePadVisibleOutline(t *testing.T) {
	for _, size := range [][2]int{{1840, 2800}, {2800, 1840}, {736, 1120}} {
		w, h := size[0], size[1]
		m := UprightDisplayMask("BKY-W20", 0, w, h)
		if !m.Fits(w, h) {
			t.Fatal("outline has the wrong orientation")
		}
		if m.Visible(Rect{W: 1, H: 1}, w, h) {
			t.Fatal("outside the rounded corner was visible")
		}
		if !m.Visible(Rect{X: w / 2, W: 1, H: 1}, w, h) {
			t.Fatal("tablet acquired a false notch")
		}
		if !m.Visible(Rect{X: w / 40, Y: h / 50, W: 1, H: 1}, w, h) {
			t.Fatal("the tablet outline obscures the status bar")
		}
	}
}

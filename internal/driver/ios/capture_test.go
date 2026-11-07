package ios

import (
	"image"
	"image/color"
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

// shotWithText is a white screenshot with dark "text" drawn in each of the
// given rectangles.
func shotWithText(w, h int, text ...screen.Rect) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	for _, r := range text {
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				if (x/3+y/5)%2 == 0 {
					img.Set(x, y, color.Black)
				}
			}
		}
	}
	return img
}

func TestUncaptured(t *testing.T) {
	const w, h = 375, 812 // a screenshot at one pixel per point
	title := screen.Element{Role: screen.RoleText, Label: "Enter passcode", Bounds: screen.Rect{X: 100, Y: 100, W: 175, H: 40}, Source: screen.SourceTree}
	var keys []screen.Element
	for i, l := range []string{"1", "2", "3", "4", "5", "6"} {
		keys = append(keys, screen.Element{Role: screen.RoleButton, Label: l, Source: screen.SourceTree,
			Bounds: screen.Rect{X: 40 + (i%3)*100, Y: 400 + (i/3)*100, W: 80, H: 80}})
	}
	els := append([]screen.Element{title}, keys...)
	img := shotWithText(w, h, title.Bounds) // the keys are in a layer the screenshot leaves out
	got := uncaptured(els, img, nil, w, h)
	if len(got) != len(keys) || got[0] != 1 {
		t.Fatalf("uncaptured %v; want the six keys", got)
	}
	// Drawn keys show.
	drawn := shotWithText(w, h, title.Bounds, keys[0].Bounds, keys[1].Bounds, keys[2].Bounds, keys[3].Bounds)
	if got := uncaptured(els, drawn, nil, w, h); got != nil {
		t.Errorf("two blank keys were marked: %v", got)
	}
	// Text OCR read inside counts as shown.
	lines := []ocrLine{}
	for _, k := range keys {
		c := k.Bounds.Center()
		lines = append(lines, ocrLine{Text: k.Label, X: float64(c.X-5) / w, Y: float64(c.Y-5) / h, W: 10.0 / w, H: 10.0 / h})
	}
	if got := uncaptured(els, img, lines, w, h); got != nil {
		t.Errorf("keys OCR read were marked: %v", got)
	}
}

func TestCaptureMarks(t *testing.T) {
	var c captureMarks
	s := &screen.Screen{App: screen.App{ID: "sg.trust"}, Elements: []screen.Element{
		{Role: screen.RoleButton, Label: "1"}, {Role: screen.RoleButton, Label: "2"}, {Role: screen.RoleText, Label: "Enter passcode"},
	}}
	c.mark(s, []int{0, 1})
	if !s.Elements[0].Has(stateNotInScreenshot) || s.Elements[2].Has(stateNotInScreenshot) {
		t.Fatalf("marked %+v", s.Elements)
	}
	// A read without a screenshot keeps the marks.
	next := &screen.Screen{App: screen.App{ID: "sg.trust"}, Elements: []screen.Element{
		{Role: screen.RoleButton, Label: "1"}, {Role: screen.RoleText, Label: "Enter passcode"},
	}}
	c.apply(next)
	if !next.Elements[0].Has(stateNotInScreenshot) || next.Elements[1].Has(stateNotInScreenshot) {
		t.Errorf("applied %+v", next.Elements)
	}
	// Another app does not get them.
	other := &screen.Screen{App: screen.App{ID: "com.apple.Preferences"}, Elements: []screen.Element{{Role: screen.RoleButton, Label: "1"}}}
	c.apply(other)
	if other.Elements[0].Has(stateNotInScreenshot) {
		t.Error("marks carried over to another app")
	}
	// A screenshot that shows the element clears its mark.
	again := &screen.Screen{App: screen.App{ID: "sg.trust"}, Elements: []screen.Element{{Role: screen.RoleButton, Label: "1"}}}
	c.mark(again, nil)
	later := &screen.Screen{App: screen.App{ID: "sg.trust"}, Elements: []screen.Element{{Role: screen.RoleButton, Label: "1"}}}
	c.apply(later)
	if later.Elements[0].Has(stateNotInScreenshot) {
		t.Error("a mark stayed after a screenshot showed the element")
	}
}

// Calculator's keys: orange circles in square frames on black, with thin
// symbols. They show.
func TestUncapturedThinSymbols(t *testing.T) {
	const w, h = 375, 812
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var els []screen.Element
	for i := 0; i < 4; i++ {
		r := screen.Rect{X: 20 + i*90, Y: 600, W: 80, H: 80}
		els = append(els, screen.Element{Role: screen.RoleButton, Label: "Add", Bounds: r, Source: screen.SourceTree})
		for y := r.Y; y < r.Y+r.H; y++ {
			for x := r.X; x < r.X+r.W; x++ {
				dx, dy := x-(r.X+40), y-(r.Y+40)
				switch {
				case dx*dx+dy*dy > 38*38:
					img.Set(x, y, color.Black)
				case dy >= -1 && dy <= 1 && dx > -15 && dx < 15:
					img.Set(x, y, color.White) // a minus sign, 3 pixels high
				default:
					img.Set(x, y, color.RGBA{255, 159, 10, 255})
				}
			}
		}
	}
	if got := uncaptured(els, img, nil, w, h); got != nil {
		t.Errorf("drawn keys marked: %v", got)
	}
}

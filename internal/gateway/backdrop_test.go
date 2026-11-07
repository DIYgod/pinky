package gateway

import (
	"context"
	"sync"
	"testing"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// notesMenu is Notes with a note's context menu open, as an iPhone lists
// it: the note list under the menu, a full-screen "Dismiss context menu"
// button, the note's preview and the menu, whose "Attachment View" lies at
// the middle of the screen. backdropFirst lists the button before the
// preview and the menu, else after them.
func notesMenu(backdropFirst bool) *screen.Screen {
	el := func(role, label string, r screen.Rect) screen.Element {
		return screen.Element{Role: role, Label: label, Bounds: r, Source: screen.SourceTree, Path: "notes", ID: label}
	}
	s := &screen.Screen{Width: 1080, Height: 2400, App: screen.App{Name: "Notes", ID: "com.apple.mobilenotes"}}
	s.Elements = append(s.Elements,
		el(screen.RoleText, "9:41", screen.Rect{X: 40, Y: 24, W: 120, H: 56}),
		el(screen.RoleButton, "Groceries", screen.Rect{X: 40, Y: 300, W: 1000, H: 160}),
		el(screen.RoleButton, "New Note", screen.Rect{X: 900, Y: 2250, W: 140, H: 100}))
	backdrop := el(screen.RoleButton, "Dismiss context menu", screen.Rect{X: 0, Y: 0, W: 1080, H: 2400})
	menu := []screen.Element{
		el(screen.RoleImage, "Groceries preview", screen.Rect{X: 100, Y: 250, W: 880, H: 600}),
		el(screen.RoleOther, "", screen.Rect{X: 240, Y: 900, W: 600, H: 800}),
		el(screen.RoleButton, "Pin Note", screen.Rect{X: 240, Y: 950, W: 600, H: 100}),
		el(screen.RoleButton, "Share Note", screen.Rect{X: 240, Y: 1050, W: 600, H: 100}),
		el(screen.RoleButton, "Attachment View", screen.Rect{X: 240, Y: 1150, W: 600, H: 100}),
		el(screen.RoleButton, "Delete", screen.Rect{X: 240, Y: 1250, W: 600, H: 100}),
	}
	if backdropFirst {
		s.Elements = append(append(s.Elements, backdrop), menu...)
	} else {
		s.Elements = append(append(s.Elements, menu...), backdrop)
	}
	return s
}

// menuDev shows Notes' context menu until a tap lands on the backdrop
// outside the menu and its preview.
type menuDev struct {
	*demo.Device
	backdropFirst bool
	mu            sync.Mutex
	open          bool
	taps          []screen.Point
}

func (d *menuDev) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := notesMenu(d.backdropFirst)
	if !d.open {
		s.Elements = s.Elements[:3]
	}
	return s, nil
}

func (d *menuDev) Tap(ctx context.Context, p screen.Point, opt driver.TapOptions) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.taps = append(d.taps, p)
	if d.open && !onMenu(notesMenu(d.backdropFirst), p) {
		d.open = false
	}
	return nil
}

// onMenu reports whether p lies on the preview or the menu.
func onMenu(s *screen.Screen, p screen.Point) bool {
	for _, e := range s.Elements {
		switch e.Label {
		case "Groceries preview", "", "Pin Note", "Share Note", "Attachment View", "Delete":
			if e.Bounds.Contains(p) {
				return true
			}
		}
	}
	return false
}

// A tap by ref on a full-screen backdrop lands where nothing else lies on
// it, so it closes the menu instead of opening the menu item at the middle
// of the screen (field test #29), whichever place the tree gives it.
func TestTapOnABackdropMissesWhatLiesOnIt(t *testing.T) {
	for _, first := range []bool{true, false} {
		base := demo.NewDevice("iphone", "iPhone 13 mini", driver.IOS)
		dev := &menuDev{Device: base, backdropFirst: first, open: true}
		g := newDevGateway(t, base, dev)
		s, res := connect(t, g, "Tidy the notes")
		res = tap(s, ref(t, res, "Dismiss context menu"))
		mustStatus(t, res, StatusSettled)
		dev.mu.Lock()
		taps, open := dev.taps, dev.open
		dev.mu.Unlock()
		if len(taps) != 1 || open {
			t.Fatalf("backdrop first %v: taps %v, menu still open %v", first, taps, open)
		}
		if p := taps[0]; p.Y < 2400*6/100 || onMenu(notesMenu(first), p) {
			t.Fatalf("backdrop first %v: tapped %v", first, p)
		}
	}
}

func TestTapPoint(t *testing.T) {
	s := notesMenu(true)
	screen.NewRefs().Assign(s)
	find := func(s *screen.Screen, label string) screen.Element {
		for _, e := range s.Elements {
			if e.Label == label {
				return e
			}
		}
		t.Fatalf("no %q", label)
		return screen.Element{}
	}
	// A button at the middle of the backdrop: nothing lies on it.
	if p, want := tapPoint(s, find(s, "Attachment View")), (screen.Point{X: 540, Y: 1200}); p != want {
		t.Errorf("Attachment View: %v, want its centre %v", p, want)
	}
	// The backdrop: below the menu, clear of the New Note button under it.
	p := tapPoint(s, find(s, "Dismiss context menu"))
	if onMenu(s, p) || find(s, "New Note").Bounds.Contains(p) || p.Y < 1700 {
		t.Errorf("backdrop: %v", p)
	}

	// A button with its own label at its centre is tapped there.
	row := &screen.Screen{Width: 1080, Height: 2400, Elements: []screen.Element{
		{Role: screen.RoleButton, Label: "Wi-Fi", Bounds: screen.Rect{X: 0, Y: 300, W: 1080, H: 140}, Source: screen.SourceTree},
		{Role: screen.RoleText, Label: "Wi-Fi", Bounds: screen.Rect{X: 40, Y: 340, W: 1000, H: 60}, Source: screen.SourceTree},
		{Role: screen.RoleSwitch, Label: "Wi-Fi", Bounds: screen.Rect{X: 900, Y: 330, W: 140, H: 80}, Source: screen.SourceTree},
	}}
	screen.NewRefs().Assign(row)
	if p, want := tapPoint(row, row.Elements[0]), row.Elements[0].Bounds.Center(); p != want {
		t.Errorf("a row with its label: %v, want %v", p, want)
	}
	// A card with another control at its centre is tapped beside it.
	card := &screen.Screen{Width: 1080, Height: 2400, Elements: []screen.Element{
		{Role: screen.RoleButton, Label: "Chicken rice bowl", Bounds: screen.Rect{X: 40, Y: 400, W: 1000, H: 400}, Source: screen.SourceTree},
		{Role: screen.RoleButton, Label: "Add to cart", Bounds: screen.Rect{X: 340, Y: 550, W: 400, H: 100}, Source: screen.SourceTree},
	}}
	screen.NewRefs().Assign(card)
	if p := tapPoint(card, card.Elements[0]); card.Elements[1].Bounds.Contains(p) || !card.Elements[0].Bounds.Contains(p) {
		t.Errorf("a card: %v", p)
	}
	// A list is tapped at its centre, whatever lies there.
	list := &screen.Screen{Width: 1080, Height: 2400, Elements: []screen.Element{
		{Role: screen.RoleList, Label: "Notes", Bounds: screen.Rect{X: 0, Y: 200, W: 1080, H: 2000}, Source: screen.SourceTree},
		{Role: screen.RoleButton, Label: "Groceries", Bounds: screen.Rect{X: 0, Y: 1150, W: 1080, H: 140}, Source: screen.SourceTree},
	}}
	screen.NewRefs().Assign(list)
	if p, want := tapPoint(list, list.Elements[0]), list.Elements[0].Bounds.Center(); p != want {
		t.Errorf("a list: %v, want %v", p, want)
	}
	// Covered all over: the centre, as before.
	full := &screen.Screen{Width: 1080, Height: 2400, Elements: []screen.Element{
		{Role: screen.RoleButton, Label: "Close", Bounds: screen.Rect{X: 0, Y: 0, W: 1080, H: 2400}, Source: screen.SourceTree},
		{Role: screen.RoleOther, Label: "", Bounds: screen.Rect{X: 0, Y: 0, W: 1080, H: 2399}, Source: screen.SourceTree},
	}}
	screen.NewRefs().Assign(full)
	if p, want := tapPoint(full, full.Elements[0]), full.Elements[0].Bounds.Center(); p != want {
		t.Errorf("covered all over: %v, want %v", p, want)
	}
}

package ios

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// sbEl is an element of SpringBoard (pid 37) in an overlay's subtree.
func sbEl(id uint64, parent, typ int, traits uint64, label string, f axpRect) raxElement {
	return raxElement{Object: &axpObject{PID: 37, ID: id}, Parent: parent, Type: typ, Traits: traits, Label: label,
		Frame: f, HasFrame: true, IsElement: true, Visible: true, Enabled: true}
}

func timerAlert() sbOverlay {
	els := []raxElement{
		sbEl(500, -1, xcAlert, 0, "", axpRect{0, 0, 375, 812}),
		sbEl(501, 0, xcStaticText, traitStaticText, "Timer", axpRect{40, 300, 295, 30}),
		sbEl(502, 0, xcButton, traitButton, "Stop", axpRect{40, 380, 140, 44}),
		sbEl(503, 0, xcButton, traitButton, "Repeat", axpRect{195, 380, 140, 44}),
	}
	els[0].IsElement = false
	return sbOverlay{key: [2]uint64{37, 500}, els: els}
}

func banner() sbOverlay {
	return sbOverlay{key: [2]uint64{37, 600}, els: []raxElement{
		sbEl(600, -1, 0, traitButton, "Messages, Anna, Your code is 123456", axpRect{8, 10, 359, 80}),
	}}
}

func TestOverlayKind(t *testing.T) {
	if k := overlayKind(timerAlert().els); k != screen.EventDialog {
		t.Errorf("an alert is %s", k)
	}
	if k := overlayKind(banner().els); k != screen.EventNotification {
		t.Errorf("a banner is %s", k)
	}
	hud := []raxElement{sbEl(700, -1, xcStaticText, traitStaticText, "Silent Mode", axpRect{100, 10, 175, 36})}
	if k := overlayKind(hud); k != screen.EventToast {
		t.Errorf("a status banner is %s", k)
	}
}

func TestOverlayText(t *testing.T) {
	if got := overlayText(timerAlert().els); got != "Timer, Stop, Repeat" {
		t.Errorf("alert text %q", got)
	}
	long := []raxElement{sbEl(1, -1, xcStaticText, 0, strings.Repeat("a", 400), axpRect{0, 0, 10, 10})}
	if got := []rune(overlayText(long)); len(got) != maxEventText+1 {
		t.Errorf("long text kept %d characters", len(got))
	}
}

func TestOverlayEvents(t *testing.T) {
	ctx := context.Background()
	d := &Device{apps: &appCatalog{apps: []driver.App{{ID: "com.apple.MobileSMS", Name: "Messages"}}, at: time.Now()}}
	d.sb.app = &axpObject{PID: 37, ID: 1, IsApp: true}
	over := []sbOverlay{timerAlert(), banner()}
	evs, covered, dlg := d.overlayEvents(ctx, over, 3, 1125, 2436)
	if len(evs) != 2 || evs[0].Kind != screen.EventDialog || evs[0].Text != "Timer, Stop, Repeat" {
		t.Fatalf("events %+v", evs)
	}
	if evs[1].Kind != screen.EventNotification || evs[1].App != "com.apple.MobileSMS" || evs[1].Text != "Anna, Your code is 123456" {
		t.Errorf("banner event %+v; want the app's bundle id and the rest of the text", evs[1])
	}
	if len(covered) != 2 || covered[1] != (screen.Rect{X: 24, Y: 30, W: 1077, H: 240}) {
		t.Errorf("covered %v", covered)
	}
	els := dialogElements(d.springBoardElement(), dlg, 3, 1125, 2436)
	var labels []string
	for _, e := range els {
		labels = append(labels, e.Role+" "+e.Label)
		if !strings.HasPrefix(e.Path, springBoard) {
			t.Errorf("%q has path %q", e.Label, e.Path)
		}
	}
	if strings.Join(labels, "; ") != "text Timer; button Stop; button Repeat" {
		t.Errorf("dialog elements: %v", labels)
	}
	// While they stay, they are not reported again; once gone and back, they are.
	if evs, _, _ := d.overlayEvents(ctx, over, 3, 1125, 2436); len(evs) != 0 {
		t.Errorf("reported again: %+v", evs)
	}
	d.overlayEvents(ctx, nil, 3, 1125, 2436)
	if evs, _, _ := d.overlayEvents(ctx, over[:1], 3, 1125, 2436); len(evs) != 1 {
		t.Errorf("an alert that came back: %+v", evs)
	}
}

// The app's elements under a banner are left out; an alert's buttons are
// listed after the app's.
func TestRAXScreenElementsUnderBanner(t *testing.T) {
	els := []raxElement{
		{Object: &axpObject{PID: 5, ID: 100, IsApp: true}, Parent: -1, Label: "Notes", Frame: axpRect{0, 0, 375, 812}, HasFrame: true, Type: 1},
		{Object: &axpObject{PID: 5, ID: 1}, Parent: 0, Type: xcButton, Label: "Back", Frame: axpRect{8, 50, 60, 30}, HasFrame: true, IsElement: true, Visible: true, Enabled: true},
		{Object: &axpObject{PID: 5, ID: 2}, Parent: 0, Type: xcTextView, Label: "Note", Frame: axpRect{0, 120, 375, 600}, HasFrame: true, IsElement: true, Visible: true, Enabled: true},
	}
	got, _ := raxScreenElements(els, 3, 1125, 2436, "com.apple.mobilenotes", screen.Rect{X: 24, Y: 30, W: 1077, H: 240})
	if len(got) != 1 || got[0].Label != "Note" {
		t.Errorf("elements %+v; want the Back button under the banner left out", got)
	}
}

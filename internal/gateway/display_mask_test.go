package gateway

import (
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

func TestAgentViewRespectsPhysicalMask(t *testing.T) {
	g, _ := newTestGateway(t)
	raw := &screen.Screen{
		Width: 1170, Height: 2532, Mask: screen.PhysicalDisplayMask("iPhone 13 Pro", 0),
		Elements: []screen.Element{
			{Label: "behind the notch", Bounds: screen.Rect{X: 480, Y: 35, W: 120, H: 30}},
			{Label: "at the bottom corner", Bounds: screen.Rect{X: 5, Y: 2485, W: 50, H: 30}},
			{Label: "visible below the notch", Bounds: screen.Rect{X: 480, Y: 106, W: 120, H: 30}},
		},
	}
	view, _ := g.agentView(nil, raw)
	if len(view.Elements) != 1 || view.Elements[0].Label != "visible below the notch" || len(raw.Elements) != 3 {
		t.Fatalf("unexpected agent view: %+v", view.Elements)
	}
}

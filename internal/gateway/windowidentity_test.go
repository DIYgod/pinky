package gateway

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

func TestScreenshotDropsChangedWindowIdentity(t *testing.T) {
	for _, after := range []string{"wm:0:different", ""} {
		t.Run(after, func(t *testing.T) {
			g, hd := newHookGateway(t)
			s, _ := connect(t, g, "Read a public article")
			var taken atomic.Bool
			hd.setObserve(func(sc *screen.Screen) {
				sc.CaptureIdentity = "wm:0:original"
				if taken.Load() {
					sc.CaptureIdentity = after
				}
			})
			hd.setShot(func() { taken.Store(true) })
			res := s.Observe(context.Background(), ObserveRequest{Screenshot: true})
			if res.Screenshot != nil {
				t.Fatal("returned a picture after verified window changed")
			}
		})
	}
}

func TestShotKeyIncludesWindowIdentity(t *testing.T) {
	a := &screen.Screen{App: screen.App{ID: "org.example.reader"}, CaptureIdentity: "wm:0:one"}
	b := *a
	b.CaptureIdentity = "wm:0:two"
	if shotKey(a) == shotKey(&b) {
		t.Fatal("history reused a different window")
	}
}

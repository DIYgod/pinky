package harmonyos

import (
	"context"
	"fmt"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

type mirrorGesture struct {
	frame mirrorFrame
	from  screen.Point
	start time.Time
	moved bool
}

// Touch is used only by the desktop owner's input path. Buffer the
// gesture until release, so a mouse move never starts a device command.
func (d *device) Touch(ctx context.Context, action driver.TouchAction, point screen.Point) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch action {
	case driver.TouchDown:
		d.gesture = nil
		d.previewMu.Lock()
		s := d.preview
		d.previewMu.Unlock()
		if s == nil {
			return nil
		}
		s.mu.Lock()
		frame := s.frame
		s.mu.Unlock()
		if frame.surface.id == "" || frame.at.IsZero() || time.Since(frame.at) > 3500*time.Millisecond {
			return nil
		}
		if _, ok := nativeMirrorPoint(frame, point); !ok {
			return nil
		}
		d.gesture = &mirrorGesture{frame: frame, from: point, start: time.Now()}
		return nil
	case driver.TouchMove:
		if g := d.gesture; g != nil {
			from, _ := nativeMirrorPoint(g.frame, g.from)
			to, valid := nativeMirrorPoint(g.frame, point)
			if !valid || abs(to.X-from.X)+abs(to.Y-from.Y) >= 35 {
				g.moved = true
			}
		}
		return nil
	case driver.TouchUp:
		gesture := d.gesture
		d.gesture = nil
		if gesture == nil || time.Since(gesture.start) > 5*time.Second {
			return nil
		}
		return d.sendMirrorGesture(ctx, gesture, point)
	default:
		return driver.ErrUnsupported
	}
}

func nativeMirrorPoint(frame mirrorFrame, p screen.Point) (screen.Point, bool) {
	if p.X < 0 || p.Y < 0 || p.X >= frame.width || p.Y >= frame.height {
		return screen.Point{}, false
	}
	s := frame.surface
	if s.width <= 0 || s.height <= 0 {
		return screen.Point{}, false
	}
	native := screen.Point{X: (2*p.X + 1) * s.width / (2 * frame.width), Y: (2*p.Y + 1) * s.height / (2 * frame.height)}
	// The status, navigation and back-gesture areas are controlled by
	// SceneBoard and are handled separately from the mirror pointer.
	mask := screen.UprightDisplayMask("BKY-W20", 0, s.width, s.height)
	return native, mask.Visible(screen.Rect{X: native.X, Y: native.Y, W: 1, H: 1}, s.width, s.height) && native.X >= 24 && native.X < s.width-24 && native.Y >= s.top && native.Y < s.height-s.bottom
}

func (d *device) sendMirrorGesture(ctx context.Context, g *mirrorGesture, end screen.Point) error {
	from, ok := nativeMirrorPoint(g.frame, g.from)
	to, valid := nativeMirrorPoint(g.frame, end)
	if !ok || !valid {
		return nil
	}
	current, err := d.focusedOwnerSurface(ctx)
	if err != nil {
		return err
	}
	if current != g.frame.surface {
		return fmt.Errorf("the MatePad window changed during the mouse gesture: %w", driver.ErrUnsupported)
	}
	dist := abs(to.X-from.X) + abs(to.Y-from.Y)
	held := time.Since(g.start)
	d.last, d.panes = surface{}, nil
	if dist < 35 {
		if g.moved {
			return nil
		}
		cmd := "click"
		if held > 550*time.Millisecond {
			cmd = "longClick"
		}
		_, err = d.shell(ctx, fmt.Sprintf("uitest uiInput %s %d %d", cmd, to.X, to.Y))
		return err
	}
	speed := min(40000, max(200, int(float64(dist)/max(held, 120*time.Millisecond).Seconds())))
	_, err = d.shell(ctx, fmt.Sprintf("uitest uiInput swipe %d %d %d %d %d", from.X, from.Y, to.X, to.Y, speed))
	return err
}

var (
	_ driver.Toucher     = (*device)(nil)
	_ driver.MirrorKeyer = (*device)(nil)
)

package harmony

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

func (d *Device) input(ctx context.Context, args ...string) error {
	if err := d.requireUnlocked(ctx); err != nil {
		return err
	}
	out, err := d.shell(ctx, append([]string{"uitest", "uiInput"}, args...)...)
	if err != nil {
		return err
	}
	if text := strings.TrimSpace(string(out)); text != "" && !strings.EqualFold(text, "No Error") && (strings.HasPrefix(text, "Missing parameter") || containsFailure(text)) {
		return fmt.Errorf("harmony: input rejected: %s", out)
	}
	return nil
}

func (d *Device) Tap(ctx context.Context, p screen.Point, opt driver.TapOptions) error {
	done := d.preferInput()
	defer done()
	d.mu.Lock()
	defer d.mu.Unlock()
	if p.X < 0 || p.Y < 0 {
		return fmt.Errorf("harmony: negative tap coordinates")
	}
	cmd := "click"
	if opt.LongPress {
		cmd = "longClick"
	}
	return d.input(ctx, cmd, strconv.Itoa(p.X), strconv.Itoa(p.Y))
}

func (d *Device) Swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error {
	done := d.preferInput()
	defer done()
	d.mu.Lock()
	defer d.mu.Unlock()
	if min(from.X, from.Y, to.X, to.Y) < 0 {
		return fmt.Errorf("harmony: negative swipe coordinates")
	}
	if dur <= 0 {
		dur = 300 * time.Millisecond
	}
	distance := math.Hypot(float64(to.X-from.X), float64(to.Y-from.Y))
	velocity := max(200, min(40000, int(distance/dur.Seconds())))
	return d.input(ctx, "swipe", strconv.Itoa(from.X), strconv.Itoa(from.Y), strconv.Itoa(to.X), strconv.Itoa(to.Y), strconv.Itoa(velocity))
}

var keys = map[driver.Key]string{
	driver.KeyHome: "Home", driver.KeyBack: "Back", driver.KeyEnter: "2054", driver.KeyTab: "2049",
	driver.KeyDelete: "2055", driver.KeyForwardDelete: "2071", driver.KeyEscape: "2070",
	driver.KeyDpadUp: "2012", driver.KeyDpadDown: "2013", driver.KeyDpadLeft: "2014", driver.KeyDpadRight: "2015",
	driver.KeyPageUp: "2068", driver.KeyPageDown: "2069",
}

func (d *Device) Key(ctx context.Context, key driver.Key) error {
	code, ok := keys[key]
	if !ok {
		return fmt.Errorf("harmony: key %s: %w", key, driver.ErrUnsupported)
	}
	done := d.preferInput()
	defer done()
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.input(ctx, "keyEvent", code)
}

// UITest does not reliably expose input/password metadata on every OS.
// Keep text injection unavailable until that can be verified, rather than
// treating an unknown input as a non-password field.
func (*Device) Type(context.Context, string, driver.TypeOptions) error {
	return fmt.Errorf("harmony: typing is not supported yet; enter text on the phone: %w", driver.ErrUnsupported)
}

// Touch coalesces the desktop mirror's raw gesture into one UITest action.
func (d *Device) Touch(ctx context.Context, action driver.TouchAction, p screen.Point) error {
	d.touchMu.Lock()
	switch action {
	case driver.TouchDown:
		d.down, d.downAt = p, time.Now()
		d.touchMu.Unlock()
		return nil
	case driver.TouchMove:
		d.touchMu.Unlock()
		return nil
	case driver.TouchUp:
		from, at := d.down, d.downAt
		d.downAt = time.Time{}
		d.touchMu.Unlock()
		if at.IsZero() {
			return nil
		}
		dur := time.Since(at)
		if math.Hypot(float64(p.X-from.X), float64(p.Y-from.Y)) < 12 {
			return d.Tap(ctx, p, driver.TapOptions{LongPress: dur > 600*time.Millisecond})
		}
		return d.Swipe(ctx, from, p, dur)
	default:
		d.touchMu.Unlock()
		return fmt.Errorf("harmony: unknown touch action")
	}
}

var _ driver.Toucher = (*Device)(nil)

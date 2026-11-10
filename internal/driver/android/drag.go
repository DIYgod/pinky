package android

import (
	"context"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/scrcpy"
)

// A drag goes through the scrcpy session like a swipe, so it works on the
// main display and on background displays alike.

// Drag implements driver.Dragger.
func (d *Device) Drag(ctx context.Context, g driver.Drag) error {
	d.used()
	err := d.withInput(ctx, true, func(l *live) error { return l.drag(ctx, g) })
	if err == nil {
		d.markAwake()
	}
	return err
}

// Drag implements driver.Dragger.
func (b *Background) Drag(ctx context.Context, g driver.Drag) error {
	b.d.used()
	if err := b.ready(ctx); err != nil {
		return err
	}
	return b.l.input(func() error { return b.l.drag(ctx, g) })
}

// drag moves one finger through g, a move every swipeStep and no events
// while it rests. Each step keeps to the drag's own clock, so a long drag
// takes as long as it says. The finger always lifts, also when ctx ends
// during the drag.
func (l *live) drag(ctx context.Context, g driver.Drag) error {
	steps := g.Steps(swipeStep)
	finger := scrcpy.PointerGenericFinger
	at, err := l.position(ctx, steps[0].P)
	if err != nil {
		return err
	}
	if err := l.send(scrcpy.InjectTouch(scrcpy.ActionDown, finger, at, 1, 0, 0)); err != nil {
		return err
	}
	next := time.Now()
	var werr error
	for _, s := range steps[1:] {
		next = next.Add(s.Wait)
		if werr = sleepCtx(ctx, time.Until(next)); werr != nil {
			break
		}
		p, err := l.position(ctx, s.P)
		if err != nil {
			werr = err
			break
		}
		if p == at {
			continue
		}
		at = p
		if err := l.send(scrcpy.InjectTouch(scrcpy.ActionMove, finger, at, 1, 0, 0)); err != nil {
			return err
		}
	}
	if err := l.send(scrcpy.InjectTouch(scrcpy.ActionUp, finger, at, 0, 0, 0)); err != nil {
		return err
	}
	return werr
}

var (
	_ driver.Dragger = (*Device)(nil)
	_ driver.Dragger = (*Background)(nil)
)

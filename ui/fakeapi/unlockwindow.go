package fakeapi

import (
	"fmt"
	"slices"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// Unlock without asking (gateway/unlockwindow.go): for a while, Pinky
// enters a phone's saved PIN when an agent's step finds it locked. The fake
// keeps when each phone's window ends; it asks no one to confirm unless a
// test sets SetUnlockWindowConfirm, and never enters a PIN.

// unlockWindows is the fake's part of unlock without asking. Guarded by
// API.mu.
type unlockWindows struct {
	// confirm stands for the person's Touch ID: it runs without the lock,
	// and its error is OpenUnlockWindow's.
	confirm func(deviceID string, length time.Duration) error
	// epoch counts ended windows, so one ended while the person confirmed
	// does not open, as in the gateway.
	epoch int
	opens []time.Duration // the lengths OpenUnlockWindow opened
}

// SetUnlockWindowConfirm sets what OpenUnlockWindow calls before it opens a
// window, in place of the person's confirmation; nil opens at once.
func (a *API) SetUnlockWindowConfirm(fn func(deviceID string, length time.Duration) error) {
	a.mu.Lock()
	a.uw.confirm = fn
	a.mu.Unlock()
}

// UnlockWindowsOpened returns the lengths of the windows OpenUnlockWindow
// opened, in order.
func (a *API) UnlockWindowsOpened() []time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.uw.opens)
}

// OpenUnlockWindow opens an unlock window of length for the phone, as the
// gateway's does once the person confirms.
func (a *API) OpenUnlockWindow(deviceID string, length time.Duration) error {
	if !slices.Contains(gateway.UnlockWindowLengths, length) {
		return fmt.Errorf("unlock without asking lasts 1, 4 or 8 hours, not %s", length)
	}
	a.mu.Lock()
	d := a.device(deviceID)
	if d == nil {
		a.mu.Unlock()
		return fmt.Errorf("fakeapi: no device %q: %w", deviceID, driver.ErrNotFound)
	}
	if !a.features(d).Unlock {
		a.mu.Unlock()
		return gateway.ErrNoUnlock
	}
	confirm, epoch := a.uw.confirm, a.uw.epoch
	a.mu.Unlock()
	if confirm != nil {
		if err := confirm(deviceID, length); err != nil {
			return err
		}
	}
	a.mu.Lock()
	if a.device(deviceID) != d || a.uw.epoch != epoch {
		a.mu.Unlock()
		return gateway.ErrUnlockWindowEnded
	}
	d.unlockUntil, d.unlockRefused = a.opt.Now().Add(length), false
	a.uw.opens = append(a.uw.opens, length)
	a.mu.Unlock()
	a.emit(gateway.Event{Kind: gateway.EventDevices, DeviceID: deviceID})
	return nil
}

// EndUnlockWindow ends the phone's unlock window, also one being confirmed.
func (a *API) EndUnlockWindow(deviceID string) error {
	a.mu.Lock()
	a.uw.epoch++
	if d := a.device(deviceID); d != nil {
		d.unlockUntil, d.unlockRefused = time.Time{}, false
	}
	a.mu.Unlock()
	a.emit(gateway.Event{Kind: gateway.EventDevices, DeviceID: deviceID})
	return nil
}

// RefusePIN does what the gateway does when the phone does not accept the
// PIN its unlock window entered: the window ends, the phone stays locked,
// and the phone shows UnlockRefused until it is unlocked.
func (a *API) RefusePIN(deviceID string) {
	a.mu.Lock()
	if d := a.device(deviceID); d != nil {
		d.unlockUntil, d.unlockRefused, d.locked = time.Time{}, true, true
	}
	a.mu.Unlock()
	a.emit(gateway.Event{Kind: gateway.EventDevices, DeviceID: deviceID})
}

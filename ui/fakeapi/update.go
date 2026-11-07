package fakeapi

import (
	"context"
	"fmt"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// SetCompanion sets a fake Android phone's companion app, such as
// driver.CompanionOutdated for an older build.
func (a *API) SetCompanion(deviceID string, s driver.CompanionState) {
	a.mu.Lock()
	if d := a.device(deviceID); d != nil {
		d.companion = s
	}
	a.mu.Unlock()
	a.emit(gateway.Event{Kind: gateway.EventDevices, DeviceID: deviceID})
}

// UpdateCompanion updates an older fake companion app without asking, as
// Gateway.UpdateCompanion does, taking a second; it refuses while an agent
// task runs on the phone.
func (a *API) UpdateCompanion(ctx context.Context, deviceID string) (driver.CompanionUpdate, error) {
	cs, err := a.CompanionState(ctx, deviceID)
	if err != nil {
		return driver.CompanionUpdate{}, err
	}
	res := driver.CompanionUpdate{From: "0.1.0 (75)", To: "0.1.0 (130)", ServiceOn: true}
	if cs != driver.CompanionOutdated {
		res.From = res.To
		return res, nil
	}
	a.mu.Lock()
	for _, t := range a.tasks {
		if t.view.DeviceID == deviceID && t.view.Ended.IsZero() {
			a.mu.Unlock()
			return driver.CompanionUpdate{}, gateway.ErrPhoneInUse
		}
	}
	a.mu.Unlock()
	select {
	case <-ctx.Done():
		return driver.CompanionUpdate{}, ctx.Err()
	case <-time.After(time.Duration(float64(time.Second) / a.opt.Speed)):
	}
	a.mu.Lock()
	d := a.device(deviceID)
	if d == nil {
		a.mu.Unlock()
		return driver.CompanionUpdate{}, fmt.Errorf("fakeapi: no device %q: %w", deviceID, driver.ErrNotFound)
	}
	d.companion = driver.CompanionOn
	a.updates = append(a.updates, deviceID)
	a.mu.Unlock()
	a.emit(gateway.Event{Kind: gateway.EventDevices, DeviceID: deviceID})
	res.Updated = true
	return res, nil
}

// CompanionUpdates lists the phones UpdateCompanion updated.
func (a *API) CompanionUpdates() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.updates...)
}

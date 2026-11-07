package fakeapi

import (
	"fmt"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// iphoneSteps are the states a new iPhone goes through in Add phone, as the
// iOS driver reports them: it asks to be trusted, needs Developer Mode,
// then Pinky prepares it (mounts the developer disk image), and it comes
// online.
var iphoneSteps = []driver.Info{
	{State: driver.StateUnauthorized, Setup: driver.SetupTrust,
		Detail: "Unlock the iPhone and tap Trust on the \"Trust This Computer?\" prompt, then enter its passcode."},
	{State: driver.StateNeedsSetup, Setup: driver.SetupDeveloperMode,
		Detail: "Turn on Developer Mode: on the iPhone, open Settings > Privacy & Security > Developer Mode, turn it on and tap Restart. After the restart, unlock the iPhone, tap Turn On and enter its passcode."},
	{State: driver.StateNeedsSetup, Setup: driver.SetupPreparing,
		Detail: "Pinky is preparing the iPhone: it mounts Apple's developer disk image, which takes a few seconds."},
	{State: driver.StateOnline},
}

// ConnectIPhone plugs in a new iPhone, as Add phone › iPhone waits for. It
// goes through iphoneSteps a few seconds per step at Speed, or one step per
// AdvanceIPhone with Options.HoldSetup.
func (a *API) ConnectIPhone() {
	a.mu.Lock()
	gen := a.gen
	a.nextID++
	id := fmt.Sprintf("iphone-16-%d", a.nextID)
	d := &device{
		info: driver.Info{ID: id, Platform: driver.IOS, Name: "iPhone 16", Model: "iPhone 16", OSVersion: "27.1",
			Connection: "usb", Backend: "coredevice"},
		width: 1179, height: 2556, codec: driver.HEVC, source: "testsrc",
	}
	setStep(&d.info, iphoneSteps[0])
	a.devices = append(a.devices, d)
	a.mu.Unlock()
	a.emit(gateway.Event{Kind: gateway.EventDevices, DeviceID: id})
	if a.opt.HoldSetup {
		return
	}
	go func() {
		for i := 1; i < len(iphoneSteps); i++ {
			if !a.sleepGen(gen, 4*time.Second) {
				return
			}
			a.SetIPhone(id, iphoneSteps[i])
		}
	}()
}

// AdvanceIPhone moves every iPhone ConnectIPhone plugged in that is not
// online yet to its next step.
func (a *API) AdvanceIPhone() {
	a.mu.Lock()
	var next []struct {
		id   string
		step driver.Info
	}
	for _, d := range a.devices {
		if d.info.Platform != driver.IOS || d.info.State == driver.StateOnline {
			continue
		}
		for i, st := range iphoneSteps[:len(iphoneSteps)-1] {
			if st.State == d.info.State && st.Setup == d.info.Setup {
				next = append(next, struct {
					id   string
					step driver.Info
				}{d.info.ID, iphoneSteps[i+1]})
				break
			}
		}
	}
	a.mu.Unlock()
	for _, n := range next {
		a.SetIPhone(n.id, n.step)
	}
}

// SetIPhone puts a phone at a step: its State, Setup and Detail from step.
func (a *API) SetIPhone(id string, step driver.Info) {
	a.mu.Lock()
	d := a.device(id)
	if d == nil {
		a.mu.Unlock()
		return
	}
	setStep(&d.info, step)
	a.mu.Unlock()
	a.emit(gateway.Event{Kind: gateway.EventDevices, DeviceID: id})
}

func setStep(info *driver.Info, step driver.Info) {
	info.State, info.Setup, info.Detail = step.State, step.Setup, step.Detail
}

// HostChecks implements the client's host check (gateway.HostChecks):
// what SetHostCheck set for iPhones, nothing by default.
func (a *API) HostChecks() map[driver.Platform]driver.HostCheck {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[driver.Platform]driver.HostCheck{}
	if a.iosHost.Missing != "" {
		out[driver.IOS] = a.iosHost
	}
	return out
}

// SetHostCheck says what the fake computer lacks for iPhones, such as
// Apple's iPhone driver on Windows.
func (a *API) SetHostCheck(c driver.HostCheck) {
	a.mu.Lock()
	a.iosHost = c
	a.mu.Unlock()
	a.emit(gateway.Event{Kind: gateway.EventDevices})
}

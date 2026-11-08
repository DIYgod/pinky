//go:build darwin

package ui

import (
	"runtime"
	"sync"

	"github.com/ebitengine/purego/objc"
)

// Activity options of NSProcessInfo.beginActivityWithOptions:reason:.
const (
	// NSActivityIdleSystemSleepDisabled: the Mac does not sleep when idle.
	activityIdleSystemSleepDisabled = 1 << 20
	// NSActivityUserInitiated: no App Nap, no timer coalescing and no idle
	// system sleep. The display may still sleep.
	activityUserInitiated = 0x00FFFFFF | activityIdleSystemSleepDisabled
	// NSActivityUserInitiatedAllowingIdleSystemSleep: the same, but the
	// Mac may sleep when idle, so a laptop with Pinky in its menu bar
	// sleeps as usual.
	activityUserInitiatedAllowingSleep = activityUserInitiated &^ activityIdleSystemSleepDisabled
	// NSActivityLatencyCritical: no throttling of timers and I/O.
	activityLatencyCritical = 0xFF00000000
)

// awakeReason names Pinky's assertion while agent tasks run, as
// `pmset -g assertions` lists it; awayReason the one while a phone is
// connected through Tailscale.
const (
	awakeReason = "Pinky: an agent task is running"
	awayReason  = "Pinky: a phone is connected through Tailscale"
)

var (
	appNapOnce  sync.Once
	appNapToken objc.ID

	awakeMu    sync.Mutex
	awakeToken objc.ID
	awayToken  objc.ID
)

// keepAppAwake holds a process activity for the app's lifetime, so macOS
// does not App Nap the gateway while its window is hidden or covered and
// the screen is locked: agents' calls, video and timers keep their pace.
// It lets the Mac sleep when idle; keepMacAwake stops that while tasks run.
func keepAppAwake() {
	appNapOnce.Do(func() {
		appNapToken = beginActivity(activityUserInitiatedAllowingSleep|activityLatencyCritical, "Pinky is connected to phones")
	})
}

// keepMacAwake holds NSActivityUserInitiated while on: the Mac does not
// sleep when idle while an agent task runs, and may again once none does.
// Phones are never kept awake; the display may sleep.
func keepMacAwake(on bool) {
	awakeMu.Lock()
	defer awakeMu.Unlock()
	switch {
	case on && awakeToken == 0:
		awakeToken = beginActivity(activityUserInitiated|activityLatencyCritical, awakeReason)
	case !on && awakeToken != 0:
		endActivity(awakeToken)
		awakeToken = 0
	}
}

// keepMacAwakeAway holds NSActivityUserInitiated while on, as keepMacAwake
// does, for a phone away from the Mac's network: agents reach it through
// the Mac. A closed lid still sleeps the Mac, unless it runs in clamshell
// mode (power and an external display).
func keepMacAwakeAway(on bool) {
	awakeMu.Lock()
	defer awakeMu.Unlock()
	switch {
	case on && awayToken == 0:
		awayToken = beginActivity(activityUserInitiated|activityLatencyCritical, awayReason)
	case !on && awayToken != 0:
		endActivity(awayToken)
		awayToken = 0
	}
}

// beginActivity starts a process activity and returns its token, retained:
// the activity lasts until endActivity.
func beginActivity(options uint64, reason string) objc.ID {
	// Autorelease pools belong to a thread, and the goroutine may move to
	// another one between calls unless it is locked (as the UI's is).
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	info := objc.ID(objc.GetClass("NSProcessInfo")).Send(objc.RegisterName("processInfo"))
	r := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), reason)
	token := info.Send(objc.RegisterName("beginActivityWithOptions:reason:"), options, r)
	return token.Send(objc.RegisterName("retain"))
}

func endActivity(token objc.ID) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	info := objc.ID(objc.GetClass("NSProcessInfo")).Send(objc.RegisterName("processInfo"))
	info.Send(objc.RegisterName("endActivity:"), token)
	token.Send(objc.RegisterName("release"))
}

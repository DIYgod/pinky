//go:build darwin

package ui

import (
	"sync"

	"github.com/ebitengine/purego/objc"
)

// Activity options of NSProcessInfo.beginActivityWithOptions:reason:.
const (
	// NSActivityUserInitiatedAllowingIdleSystemSleep: no App Nap, no timer
	// coalescing; the Mac may still sleep when idle, so a laptop with
	// Pinky in its menu bar sleeps as usual.
	activityUserInitiatedAllowingSleep = 0x00FFFFFF &^ (1 << 20)
	// NSActivityLatencyCritical: no throttling of timers and I/O.
	activityLatencyCritical = 0xFF00000000
)

var (
	appNapOnce  sync.Once
	appNapToken objc.ID
)

// keepAppAwake holds a process activity for the app's lifetime, so macOS
// does not App Nap the gateway while its window is hidden or covered and
// the screen is locked: agents' calls, video and timers keep their pace.
func keepAppAwake() {
	appNapOnce.Do(func() {
		pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
		defer pool.Send(objc.RegisterName("drain"))
		info := objc.ID(objc.GetClass("NSProcessInfo")).Send(objc.RegisterName("processInfo"))
		reason := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), "Pinky is connected to phones")
		token := info.Send(objc.RegisterName("beginActivityWithOptions:reason:"),
			uint64(activityUserInitiatedAllowingSleep|activityLatencyCritical), reason)
		// The token is autoreleased; keep it, as ending it would bring App
		// Nap back.
		appNapToken = token.Send(objc.RegisterName("retain"))
	})
}

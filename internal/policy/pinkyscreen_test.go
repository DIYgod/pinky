package policy

import (
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

// The screens of Pinky's Android app hold the person's approvals, Stop and
// rules: they are blocked, no task can lift that, and the agent can neither
// open them nor act on them, nor stop or uninstall the app. Pressing home
// still works, so the agent can leave. Other apps are not blocked by it.
func TestPinkyScreenBlocked(t *testing.T) {
	e := Default()
	for _, pkg := range []string{"app.pinky.companion", "app.pinky.companion.dev"} {
		pinky := screen.App{ID: pkg, Name: "Pinky"}
		if rule, blocked := e.Blocked(pinky); !blocked || rule != RulePinkyApps {
			t.Fatalf("Blocked(%s) = %q, %v", pkg, rule, blocked)
		}
		if rule, blocked := e.Blocked(screen.App{ID: pkg + "@10"}); !blocked || rule != RulePinkyApps {
			t.Fatalf("Blocked(%s in a work profile) = %q, %v", pkg, rule, blocked)
		}

		for _, in := range []Input{
			{Op: OpLaunch, Launch: pinky},
			{Op: OpLaunch, Launch: pinky, AllowBlocked: true},
			{Op: OpTap, App: pinky, Target: &screen.Element{Role: screen.RoleButton, Label: "Allow once"}},
			{Op: OpTap, App: pinky, AllowBlocked: true},
			{Op: OpKey, Key: "back", App: pinky},
			{Op: OpStop, Launch: pinky},
			{Op: OpUninstall, Launch: pinky},
		} {
			if v := e.Evaluate(in); v.Decision != Deny || v.Rule != RulePinkyApps {
				t.Errorf("%s %s: %+v", pkg, in.Op, v)
			}
		}
		if v := e.Evaluate(Input{Op: OpKey, Key: "home", App: pinky}); v.Decision == Deny {
			t.Errorf("home on %s: %+v", pkg, v)
		}
	}
	for _, other := range []string{"app.pinky.companionx", "app.pinky.standalone", "com.android.settings"} {
		if _, blocked := e.Blocked(screen.App{ID: other}); blocked {
			t.Errorf("%s is blocked", other)
		}
	}
}

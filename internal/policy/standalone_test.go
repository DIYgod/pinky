package policy

import (
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

// The standalone prototype's screen holds the person's approvals and Stop:
// it is blocked, no task can lift that, and the agent can neither open it
// nor act on it, nor stop or uninstall it. Pressing home still works, so
// the agent can leave. Pinky Companion's screen is not blocked.
func TestStandaloneBlocked(t *testing.T) {
	e := Default()
	pinky := screen.App{ID: StandaloneApp, Name: "Pinky Standalone"}
	if rule, blocked := e.Blocked(pinky); !blocked || rule != RulePinkyApps {
		t.Fatalf("Blocked(standalone) = %q, %v", rule, blocked)
	}
	if rule, blocked := e.Blocked(screen.App{ID: StandaloneApp + "@10"}); !blocked || rule != RulePinkyApps {
		t.Fatalf("Blocked(standalone in a work profile) = %q, %v", rule, blocked)
	}
	if _, blocked := e.Blocked(screen.App{ID: "app.pinky.companion"}); blocked {
		t.Fatal("Pinky Companion's screen is blocked")
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
			t.Errorf("%s: %+v", in.Op, v)
		}
	}
	if v := e.Evaluate(Input{Op: OpKey, Key: "home", App: pinky}); v.Decision == Deny {
		t.Errorf("home on Pinky's screen: %+v", v)
	}
}

// Pinky's iPhone app is blocked the same way, under each id it gets: its
// own builds, desktop Pinky's builds with the person's team, and SideStore's.
// Other apps whose ids merely start alike are not.
func TestIPhoneAppBlocked(t *testing.T) {
	e := Default()
	for _, id := range []string{"cc.diygod.pinky", "cc.diygod.pinky.standalone", "cc.diygod.pinky.standalone.tunnel",
		"cc.diygod.pinky.ABCDE12345", "pinky.abcde12345", "pinky.abcde12345.activity", "CC.DIYGOD.PINKY.APP"} {
		app := screen.App{ID: id, Name: "Pinky"}
		if rule, blocked := e.Blocked(app); !blocked || rule != RulePinkyApps {
			t.Errorf("Blocked(%s) = %q, %v", id, rule, blocked)
		}
		if v := e.Evaluate(Input{Op: OpTap, App: app, AllowBlocked: true}); v.Decision != Deny || v.Rule != RulePinkyApps {
			t.Errorf("tap on %s: %+v", id, v)
		}
		if v := e.Evaluate(Input{Op: OpUninstall, Launch: app}); v.Decision != Deny || v.Rule != RulePinkyApps {
			t.Errorf("uninstall %s: %+v", id, v)
		}
	}
	for _, id := range []string{"cc.diygod.pinkyclone", "com.example.pinky", "app.pinky.companion"} {
		if _, blocked := e.Blocked(screen.App{ID: id}); blocked {
			t.Errorf("%s is blocked", id)
		}
	}
}

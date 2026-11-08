package ui

import (
	"strings"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// TestAddTailnet: Add phone › Android links to the Tailscale steps, which
// show this computer's tailnet and the phone on it. Connecting finds the
// phone's adb not listening and says how to set it up; the port Wireless
// debugging shows then connects it, and the phone shows.
func TestAddTailnet(t *testing.T) {
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.sync()
	ta.page = pageAddPhone
	ta.add.reset(ta.app)
	ta.add.step = "android"
	tt := mg.NewTester(ta.view, 1280, 1000)
	tt.Frame()
	if err := tt.Click("Phone not on this network? Use Tailscale"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the tailnet", func() bool { tt.Frame(); return ta.tn.have })
	tt.Frame()
	for _, want := range []string{"Connect through Tailscale", "Connected to someone@example.com as Someone's MacBook.", "pixel-8a", "100.101.2.2 · online"} {
		if !hasText(tt, want) {
			t.Fatalf("no %q on the page: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Connect"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the answer", func() bool {
		tt.Frame()
		return !ta.tn.connecting["100.101.2.2"] && ta.tn.connErr["100.101.2.2"] != ""
	})
	tt.Frame()
	if !hasText(tt, "pixel-8a doesn't take connections from Pinky yet") || !hasText(tt, "Use away from home") {
		t.Fatalf("no set-up steps: %q", tt.Texts())
	}
	ta.tn.port["100.101.2.2"] = "37123"
	tt.Frame()
	if err := tt.Click("Connect at this port"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the phone", func() bool {
		return ta.page == pagePhones && strings.HasPrefix(ta.sel, "pixel-8a-tailnet-")
	})
	if d := ta.device(ta.sel); d == nil || connectionLine(*d) != "Android · Tailscale" {
		t.Fatalf("header line of %+v", d)
	}
}

// TestAddTailnetHere: without Tailscale running here, the first step says
// what to do and offers the app or its download.
func TestAddTailnetHere(t *testing.T) {
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.sync()
	ta.page, ta.add.step = pageAddPhone, "tailnet"
	tt := mg.NewTester(ta.view, 1280, 1000)
	for _, c := range []struct {
		state, text, button string
	}{
		{driver.TailnetNotInstalled, "Install Tailscale on this computer and sign in.", "Get Tailscale"},
		{driver.TailnetStopped, "Tailscale is turned off on this computer. Open it and turn it on.", "Open Tailscale"},
		{driver.TailnetNeedsLogin, "Sign in to Tailscale on this computer.", "Open Tailscale"},
	} {
		ta.api.SetTailnet(driver.TailnetStatus{State: c.state})
		ta.tn.at = ta.tn.at.AddDate(0, 0, -1)
		ta.waitFor(t, c.state, func() bool { tt.Frame(); return !ta.tn.busy && ta.tn.st.State == c.state })
		tt.Frame()
		if !hasText(tt, c.text) {
			t.Errorf("%s: no %q: %q", c.state, c.text, tt.Texts())
		}
		if _, ok := tt.Find(c.button); !ok {
			t.Errorf("%s: no %q button", c.state, c.button)
		}
	}
}

// TestUseAwayFromHome turns Use away from home on for a phone in Settings ›
// Phones, through its dialog, and off again; a phone connected through
// Tailscale keeps the computer awake while the setting says so.
func TestUseAwayFromHome(t *testing.T) {
	ta := newTestApp(t, fakeapi.OnePhone)
	var awake []bool
	ta.awayAwake = func(on bool) { awake = append(awake, on) }
	ta.sync()
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 1000)
	ta.waitFor(t, "the row", func() bool { tt.Frame(); return hasText(tt, "Use away from home") })
	tt.Frame()
	if !hasText(tt, "Reach Pixel 9 through Tailscale when it's away from this computer's network.") {
		t.Fatalf("row: %q", tt.Texts())
	}
	if !hasText(tt, "Keep this computer awake for phones away") {
		t.Errorf("no keep-awake switch: %q", tt.Texts())
	}
	if err := tt.Click("Turn on…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !ta.tn.askOpen || !hasText(tt, "Use Pixel 9 away from home?") || !hasText(tt, "tap Cancel") {
		t.Fatalf("dialog: %q", tt.Texts())
	}
	if err := tt.Click("Turn on"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "on", func() bool { tt.Frame(); return hasText(tt, "On. When Pixel 9 is away from this computer's network") })
	if !hasText(tt, "Pixel 9 can now be used away from home") {
		t.Errorf("no toast: %q", tt.Texts())
	}
	if len(awake) != 0 {
		t.Errorf("awake %v with no phone away", awake)
	}
	// A phone connected through Tailscale: the computer stays awake, and
	// not once the setting is off.
	if _, err := ta.api.ConnectTailnet(ta.ctx, "100.101.2.2:37123"); err != nil {
		t.Fatal(err)
	}
	ta.sync()
	if len(awake) != 1 || !awake[0] {
		t.Fatalf("awake %v with a phone away", awake)
	}
	ta.settings.KeepAwakeAway = false
	ta.sync()
	if len(awake) != 2 || awake[1] {
		t.Fatalf("awake %v with the setting off", awake)
	}
	// Off again.
	ta.settings.KeepAwakeAway = true
	ta.sync()
	tt.Frame()
	if err := tt.Click("Turn off"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "off", func() bool { tt.Frame(); return hasText(tt, "Use away from home is off for Pixel 9") })
}

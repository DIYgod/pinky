package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/secret"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// --- unlock without asking (#231) ---

// The Locked scenario's phone, whose unlock window the tests open.
const uwPhone, uwName = "pixel-9", "Pixel 9"

// newWindowApp is the Locked scenario's client at 14:30 by the tests'
// clock, with a made-up PIN saved for Pixel 9: its unlock window can be
// turned on.
func newWindowApp(t *testing.T) *testApp {
	t.Helper()
	clockAt(t, wallTime(2026, 11, 26, 14, 30))
	ta := newTestApp(t, fakeapi.Locked)
	if err := ta.opt.Secrets.Save(secret.PINAccount(uwPhone), "0000"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the saved PIN", func() bool { return ta.pin.ready && ta.pin.saved[uwPhone] && ta.device(uwPhone) != nil })
	return ta
}

// uwSwitch returns the first Unlock without asking switch of the last
// frame.
func uwSwitch(t *testing.T, tt *mg.Tester) mg.AccessInfo {
	t.Helper()
	sw, ok := tt.AccessByRole(mg.RoleSwitch, locales.UnlockWindowTitle())
	if !ok {
		t.Fatalf("no Unlock without asking switch in %q", tt.Texts())
	}
	return sw
}

// clickMid clicks the middle of r.
func clickMid(tt *mg.Tester, r mg.Rect) { tt.ClickAt(r.X+r.W/2, r.Y+r.H/2) }

// windowSettled waits until Pixel 9's window is open, or not, and the
// client is done opening or ending it.
func (ta *testApp) windowSettled(t *testing.T, what string, open bool) {
	t.Helper()
	ta.waitFor(t, what, func() bool {
		d := ta.device(uwPhone)
		return d != nil && !ta.uw.busy[uwPhone] && windowOn(*d) == open
	})
}

// openWindowNow opens Pixel 9's window for length in the fake, as the
// gateway does once the person confirms, and has the client read it.
func (ta *testApp) openWindowNow(t *testing.T, length time.Duration) {
	t.Helper()
	if err := ta.api.OpenUnlockWindow(uwPhone, length); err != nil {
		t.Fatal(err)
	}
	ta.sync()
}

// TestUnlockWindowOpenAndEnd turns unlock without asking on in Settings ›
// Phones: the lengths show, 4 hours has the gateway open the window, and
// the switch says until when and how long is left. The strip under the
// mirror says it too, and its End ends the window.
func TestUnlockWindowOpenAndEnd(t *testing.T) {
	ta := newWindowApp(t)
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if sw := uwSwitch(t, tt); sw.Checked || sw.Disabled {
		t.Fatalf("the switch starts %+v, not off and enabled", sw)
	}
	if !hasText(tt, locales.UnlockWindowDesc()) {
		t.Errorf("no word on what it does: %q", tt.Texts())
	}
	if err := tt.Click(locales.UnlockWindowTitle()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{locales.UnlockWindowHowLong(), locales.UnlockWindowFor(1), locales.UnlockWindowFor(4), locales.UnlockWindowFor(8)} {
		if !hasText(tt, want) {
			t.Errorf("no %q once it is turned on: %q", want, tt.Texts())
		}
	}
	if got := ta.api.UnlockWindowsOpened(); len(got) != 0 {
		t.Fatalf("a window opened before a length was picked: %v", got)
	}
	if err := tt.Click(locales.UnlockWindowFor(4)); err != nil {
		t.Fatal(err)
	}
	ta.windowSettled(t, "the window to open", true)
	if got := ta.api.UnlockWindowsOpened(); !slices.Equal(got, []time.Duration{4 * time.Hour}) {
		t.Fatalf("opened %v, want 4h", got)
	}
	if want := locales.UnlockWindowOnToast("18:30", uwName); !slices.Contains(ta.toasts, want) {
		t.Errorf("no %q in %q", want, ta.toasts)
	}
	tt.Frame()
	if want := locales.UnlockWindowUntil("18:30", "4 h"); !hasText(tt, want) {
		t.Errorf("Settings › Phones does not say %q: %q", want, tt.Texts())
	}
	if sw := uwSwitch(t, tt); !sw.Checked || sw.Disabled {
		t.Errorf("the switch is %+v while the window is open", sw)
	}
	if hasText(tt, locales.UnlockWindowHowLong()) {
		t.Errorf("the lengths stay once the window is open: %q", tt.Texts())
	}

	ta.selectPhone(uwPhone)
	tt.Frame()
	strip := locales.UnlockWindowBanner("4 h")
	if !hasText(tt, strip) {
		t.Fatalf("no %q under the mirror: %q", strip, tt.Texts())
	}
	tt.Advance(time.Minute) // the toast, at the bottom of the window, goes
	if err := tt.Click(locales.UnlockWindowEndLabel(uwName)); err != nil {
		t.Fatal(err)
	}
	ta.windowSettled(t, "the window to end", false)
	if want := locales.UnlockWindowEndedToast(uwName); !slices.Contains(ta.toasts, want) {
		t.Errorf("no %q in %q", want, ta.toasts)
	}
	tt.Frame()
	if hasText(tt, strip) {
		t.Errorf("the strip stays after End: %q", tt.Texts())
	}
}

// TestUnlockWindowSwitchOff: turning the switch off, or Cancel, before a
// length is picked puts the lengths away and opens nothing; turning it off
// while the window is open ends the window.
func TestUnlockWindowSwitchOff(t *testing.T) {
	ta := newWindowApp(t)
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	clickMid(tt, uwSwitch(t, tt).Bounds)
	tt.Frame()
	if sw := uwSwitch(t, tt); !sw.Checked || !hasText(tt, locales.UnlockWindowHowLong()) {
		t.Fatalf("the switch is %+v, with no lengths: %q", sw, tt.Texts())
	}
	clickMid(tt, uwSwitch(t, tt).Bounds)
	tt.Frame()
	if sw := uwSwitch(t, tt); sw.Checked || hasText(tt, locales.UnlockWindowHowLong()) || ta.uw.choosing != "" {
		t.Errorf("after the switch went off again it is %+v: %q", sw, tt.Texts())
	}
	if err := tt.Click(locales.UnlockWindowTitle()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(locales.UnlockWindowCancel()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if sw := uwSwitch(t, tt); sw.Checked || hasText(tt, locales.UnlockWindowHowLong()) {
		t.Errorf("after Cancel the switch is %+v: %q", sw, tt.Texts())
	}
	if got := ta.api.UnlockWindowsOpened(); len(got) != 0 {
		t.Fatalf("a window opened: %v", got)
	}

	ta.openWindowNow(t, time.Hour)
	tt.Frame()
	if want := locales.UnlockWindowUntil("15:30", "1 h"); !hasText(tt, want) {
		t.Fatalf("no %q: %q", want, tt.Texts())
	}
	clickMid(tt, uwSwitch(t, tt).Bounds)
	ta.windowSettled(t, "the window to end", false)
	if want := locales.UnlockWindowEndedToast(uwName); !slices.Contains(ta.toasts, want) {
		t.Errorf("no %q in %q", want, ta.toasts)
	}
}

// TestUnlockWindowExpires: once the window's time is up, the switch is off
// and the strip gone, whether or not the gateway has said so yet.
func TestUnlockWindowExpires(t *testing.T) {
	ta := newWindowApp(t)
	ta.openWindowNow(t, time.Hour)
	ta.selectPhone(uwPhone)
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if !hasText(tt, locales.UnlockWindowBanner("1 h")) {
		t.Fatalf("no strip: %q", tt.Texts())
	}
	clockAt(t, wallTime(2026, 11, 26, 15, 29))
	tt.Frame()
	if !hasText(tt, locales.UnlockWindowBanner("1 min")) {
		t.Errorf("a minute before the end: %q", tt.Texts())
	}
	clockAt(t, wallTime(2026, 11, 26, 15, 30))
	tt.Frame()
	if hasText(tt, locales.UnlockWindowTitle()) {
		t.Errorf("the strip stays once the time is up: %q", tt.Texts())
	}
	ta.page, ta.set.sec = pageSettings, secPhones
	tt.Frame()
	if sw := uwSwitch(t, tt); sw.Checked || sw.Disabled || !hasText(tt, locales.UnlockWindowDesc()) {
		t.Errorf("once the time is up the switch is %+v: %q", sw, tt.Texts())
	}
}

// TestUnlockWindowConfirm: while the person confirms, the switch stays on,
// takes no flip and says it waits. Cancelling Touch ID leaves it off
// without a word; a window ended while the person confirmed does not open,
// and the client says so.
func TestUnlockWindowConfirm(t *testing.T) {
	ta := newWindowApp(t)
	asked := make(chan string, 1)
	answer := make(chan error)
	t.Cleanup(func() { close(answer) })
	ta.api.SetUnlockWindowConfirm(func(id string, length time.Duration) error {
		asked <- fmt.Sprint(id, " ", length)
		return <-answer
	})
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if err := tt.Click(locales.UnlockWindowTitle()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(locales.UnlockWindowFor(1)); err != nil {
		t.Fatal(err)
	}
	if got := <-asked; got != uwPhone+" 1h0m0s" {
		t.Errorf("asked to confirm %q", got)
	}
	tt.Frame()
	if !hasText(tt, locales.UnlockWindowConfirming()) {
		t.Errorf("no word that it waits for the person: %q", tt.Texts())
	}
	if sw := uwSwitch(t, tt); !sw.Checked || !sw.Disabled {
		t.Errorf("while the person confirms the switch is %+v, want on and disabled", sw)
	}
	_ = tt.Click(locales.UnlockWindowTitle()) // takes no flip meanwhile
	if ta.uw.choosing != "" || !ta.uw.want[uwPhone] {
		t.Errorf("a click while confirming: choosing %q, want %v", ta.uw.choosing, ta.uw.want[uwPhone])
	}
	toasts := len(ta.toasts)
	answer <- fmt.Errorf("touch id: %w", secret.ErrCancelled)
	ta.windowSettled(t, "the cancel", false)
	if len(ta.toasts) != toasts {
		t.Errorf("a cancel says %q", ta.toasts[toasts:])
	}
	if got := ta.api.UnlockWindowsOpened(); len(got) != 0 {
		t.Errorf("a cancelled window opened: %v", got)
	}
	tt.Frame()
	if sw := uwSwitch(t, tt); sw.Checked || sw.Disabled {
		t.Errorf("after a cancel the switch is %+v", sw)
	}

	// Ended while the person confirmed: Unlock from this computer was
	// turned off, or the phone's PIN changed, meanwhile.
	ta.api.SetUnlockWindowConfirm(func(id string, _ time.Duration) error { return ta.api.EndUnlockWindow(id) })
	if err := tt.Click(locales.UnlockWindowTitle()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(locales.UnlockWindowFor(8)); err != nil {
		t.Fatal(err)
	}
	ta.windowSettled(t, "the window that ended meanwhile", false)
	if want := locales.UnlockWindowChanged(uwName); !slices.Contains(ta.toasts, want) {
		t.Errorf("no %q in %q", want, ta.toasts)
	}
	if got := ta.api.UnlockWindowsOpened(); len(got) != 0 {
		t.Errorf("a window ended meanwhile opened: %v", got)
	}
}

// TestUnlockWindowNeeds: the switch is off, takes no flip and says why
// while Unlock from this computer is off, the system keeps no PINs, the
// phone's PIN is not saved, or the phone refused it. A phone that Pinky
// cannot unlock from here has no row in Settings › Phones, and its row in
// ⋯ says so.
func TestUnlockWindowNeeds(t *testing.T) {
	ta := newWindowApp(t)
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	for _, c := range []struct {
		name      string
		set, undo func()
		why       string
	}{
		{"unlocking from this computer off", func() { ta.settings.UnlockFromComputer = false }, func() { ta.settings.UnlockFromComputer = true }, locales.UnlockWindowOff()},
		{"no keychain", func() { ta.pin.supported = false }, func() { ta.pin.supported = true }, locales.UnlockNotAvailable()},
		{"no PIN", func() { ta.pin.saved[uwPhone] = false }, func() { ta.pin.saved[uwPhone] = true }, locales.UnlockWindowNoPIN()},
		{"a refused PIN", func() { ta.pin.wrong[uwPhone] = true }, func() { delete(ta.pin.wrong, uwPhone) }, locales.UnlockWindowWrongPIN()},
	} {
		c.set()
		tt.Frame()
		if sw := uwSwitch(t, tt); sw.Checked || !sw.Disabled || !hasText(tt, c.why) {
			t.Errorf("%s: the switch is %+v, with %q", c.name, sw, tt.Texts())
		}
		ta.flipWindow(*ta.device(uwPhone), true, "settings")
		if ta.uw.choosing != "" {
			t.Errorf("%s: the lengths show", c.name)
			ta.uw.choosing = ""
		}
		c.undo()
	}
	tt.Frame()
	if sw := uwSwitch(t, tt); sw.Disabled {
		t.Fatalf("with all it needs the switch is %+v", sw)
	}

	d := ta.devices[0]
	d.Features.Unlock = false
	ta.devices[0] = d
	tt.Frame()
	if _, ok := tt.AccessByRole(mg.RoleSwitch, locales.UnlockWindowTitle()); ok {
		t.Errorf("Settings › Phones offers it for a phone Pinky cannot unlock from here: %q", tt.Texts())
	}
	ta.selectPhone(uwPhone)
	tt.Frame()
	if err := tt.Click(locales.PhoneMoreControls()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if sw := uwSwitch(t, tt); sw.Checked || !sw.Disabled || !hasText(tt, locales.UnlockWindowNoUnlock()) {
		t.Errorf("⋯ has the switch %+v: %q", sw, tt.Texts())
	}
}

// TestUnlockWindowInMenu turns it on in ⋯ under the mirror, where the
// lengths show under the switch and go when the menu closes, and turns it
// off there.
func TestUnlockWindowInMenu(t *testing.T) {
	ta := newWindowApp(t)
	ta.selectPhone(uwPhone)
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if err := tt.Click(locales.PhoneMoreControls()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if sw := uwSwitch(t, tt); sw.Checked || sw.Disabled || !hasText(tt, locales.UnlockWindowDesc()) {
		t.Fatalf("⋯ has the switch %+v: %q", sw, tt.Texts())
	}
	clickMid(tt, uwSwitch(t, tt).Bounds)
	tt.Frame()
	if ta.uw.choosing != uwPhone || ta.uw.where != "menu" || !hasText(tt, locales.UnlockWindowHowLong()) {
		t.Fatalf("no lengths in ⋯: %q", tt.Texts())
	}
	tt.Key(0, mg.KeyEscape) // the menu
	tt.Frame()
	if ta.more[uwPhone] || ta.uw.choosing != "" {
		t.Fatalf("after the menu closed: open %v, choosing %q", ta.more[uwPhone], ta.uw.choosing)
	}
	if err := tt.Click(locales.PhoneMoreControls()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if hasText(tt, locales.UnlockWindowHowLong()) {
		t.Errorf("the lengths are back with the menu: %q", tt.Texts())
	}
	clickMid(tt, uwSwitch(t, tt).Bounds)
	tt.Frame()
	if err := tt.Click(locales.UnlockWindowFor(1)); err != nil {
		t.Fatal(err)
	}
	ta.windowSettled(t, "the window to open", true)
	tt.Frame()
	if want := locales.UnlockWindowUntil("15:30", "1 h"); !hasText(tt, want) {
		t.Errorf("⋯ does not say %q: %q", want, tt.Texts())
	}
	if !hasText(tt, locales.UnlockWindowBanner("1 h")) {
		t.Errorf("no strip under the mirror: %q", tt.Texts())
	}
	clickMid(tt, uwSwitch(t, tt).Bounds)
	ta.windowSettled(t, "the window to end", false)
	if got := ta.api.UnlockWindowsOpened(); !slices.Equal(got, []time.Duration{time.Hour}) {
		t.Errorf("opened %v, want 1h", got)
	}
}

// TestUnlockWindowEndsWithPIN: changing or removing the phone's PIN ends
// its window first, as the gateway would go on entering the PIN it took;
// with no window open the PIN changes without a word about one.
func TestUnlockWindowEndsWithPIN(t *testing.T) {
	ta := newWindowApp(t)
	store := ta.opt.Secrets.(*secret.Memory)
	if end, open := ta.windowEnder(uwPhone); end != nil || open {
		t.Errorf("with no window windowEnder gives %v, %v", end != nil, open)
	}
	ended := locales.UnlockWindowEndedToast(uwName)
	ta.openWindowNow(t, time.Hour)
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if err := tt.Click(locales.PinChangeLabel(uwName)); err != nil {
		t.Fatal(err)
	}
	tt.Type("9999")
	if err := tt.Click(locales.PinSave()); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the new PIN", func() bool { return !ta.pin.busy && !windowOn(*ta.device(uwPhone)) })
	if pin, err := store.Reveal(context.Background(), secret.PINAccount(uwPhone), "test"); err != nil || pin != "9999" {
		t.Fatalf("the keychain has %q, %v", pin, err)
	}
	if !slices.Contains(ta.toasts, ended) {
		t.Errorf("no %q in %q", ended, ta.toasts)
	}

	ta.openWindowNow(t, time.Hour)
	tt.Frame() // shows the toasts so far, which leave ta.toasts
	if err := tt.Click(locales.PinRemoveLabel(uwName)); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the PIN to go", func() bool { return !ta.pin.saved[uwPhone] && !windowOn(*ta.device(uwPhone)) })
	if store.Has(secret.PINAccount(uwPhone)) {
		t.Error("the keychain still has the PIN")
	}
	if !slices.Contains(ta.toasts, ended) {
		t.Errorf("no %q in %q", ended, ta.toasts)
	}

	// Without a window, a new PIN says nothing of one.
	tt.Frame()
	ta.pin.value = "2468"
	ta.savePIN(uwPhone, uwName)
	ta.waitFor(t, "the PIN", func() bool { return ta.pin.saved[uwPhone] && !ta.pin.busy })
	if slices.Contains(ta.toasts, ended) {
		t.Errorf("a PIN saved with no window says it ended: %q", ta.toasts)
	}
	if got := ta.api.UnlockWindowsOpened(); len(got) != 2 {
		t.Errorf("opened %v", got)
	}
}

// TestUnlockWindowRefusedPIN: when the phone refuses the PIN its window
// entered, the gateway ends the window; the client takes it as a refused
// PIN, so the unlock card offers Try again rather than Unlock and the
// switch stays off until the PIN changes. A PIN saved since is not taken
// for refused again.
func TestUnlockWindowRefusedPIN(t *testing.T) {
	ta := newWindowApp(t)
	ta.openWindowNow(t, 4*time.Hour)
	ta.api.RefusePIN(uwPhone)
	ta.waitFor(t, "the refusal", func() bool { return ta.pin.wrong[uwPhone] })
	if windowOn(*ta.device(uwPhone)) {
		t.Fatal("the window stays open after the phone refused its PIN")
	}
	ta.selectPhone(uwPhone)
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if label, _ := unlockWords(); hasText(tt, label) {
		t.Errorf("Unlock offered after the phone refused the PIN: %q", tt.Texts())
	}
	if !hasText(tt, locales.UnlockTryAgain()) {
		t.Errorf("no Try again: %q", tt.Texts())
	}
	if hasText(tt, locales.UnlockWindowTitle()) {
		t.Errorf("the strip stays: %q", tt.Texts())
	}
	ta.page, ta.set.sec = pageSettings, secPhones
	tt.Frame()
	if sw := uwSwitch(t, tt); sw.Checked || !sw.Disabled || !hasText(tt, locales.UnlockWindowWrongPIN()) {
		t.Errorf("after the refusal the switch is %+v: %q", sw, tt.Texts())
	}
	if err := tt.Click(locales.PinChangeLabel(uwName)); err != nil {
		t.Fatal(err)
	}
	tt.Type("1357")
	if err := tt.Click(locales.PinSave()); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the new PIN", func() bool { return !ta.pin.busy && !ta.pin.wrong[uwPhone] })
	ta.sync()
	if d := ta.device(uwPhone); ta.pin.wrong[uwPhone] || d.UnlockRefused {
		t.Errorf("the new PIN is taken for refused: wrong %v, the gateway's %v", ta.pin.wrong[uwPhone], d.UnlockRefused)
	}
	if slices.Contains(ta.toasts, locales.UnlockWindowEndedToast(uwName)) {
		t.Errorf("a window that had ended is said to end: %q", ta.toasts)
	}
	tt.Frame()
	if sw := uwSwitch(t, tt); sw.Disabled {
		t.Errorf("with the new PIN the switch is %+v: %q", sw, tt.Texts())
	}
}

// TestUnlockWindowNeedsTheGateway: a client of a gateway elsewhere, whose
// API has no unlock windows, shows no switch and no strip.
func TestUnlockWindowNeedsTheGateway(t *testing.T) {
	ta := newWindowApp(t)
	ta.openWindowNow(t, time.Hour)
	ta.app.api = struct{ gateway.API }{ta.api}
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if hasText(tt, locales.UnlockWindowTitle()) {
		t.Errorf("Settings › Phones has it: %q", tt.Texts())
	}
	ta.selectPhone(uwPhone)
	tt.Frame()
	if err := tt.Click(locales.PhoneMoreControls()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if hasText(tt, locales.UnlockWindowTitle()) {
		t.Errorf("the phone's page has it: %q", tt.Texts())
	}
}

// TestUnlockWindowForgottenWithPhone: a removed phone's window state goes
// with it, as the gateway ends its window.
func TestUnlockWindowForgottenWithPhone(t *testing.T) {
	ta := newWindowApp(t)
	ta.uw.busy[uwPhone], ta.uw.want[uwPhone], ta.uw.refused[uwPhone] = true, true, true
	ta.uw.choosing, ta.uw.where = uwPhone, "menu"
	ta.forgetPhone(uwPhone, false)
	if len(ta.uw.busy)+len(ta.uw.want)+len(ta.uw.refused) != 0 || ta.uw.choosing != "" {
		t.Errorf("after the phone went: %+v", ta.uw)
	}
}

// TestUnlockWindowLeft says how long a window has left in minutes rounded
// up, in hours and minutes.
func TestUnlockWindowLeft(t *testing.T) {
	for mins, want := range map[int]string{
		1:   "1 min",
		42:  "42 min",
		60:  "1 h",
		192: "3 h 12 min",
		240: "4 h",
		479: "7 h 59 min",
		480: "8 h",
	} {
		if got := leftWords(mins); got != want {
			t.Errorf("leftWords(%d) = %q, want %q", mins, got, want)
		}
	}
	clockAt(t, wallTime(2026, 11, 26, 14, 30))
	var d gateway.DeviceView
	var got string
	mt := mg.NewTester(func(c *mg.Context) { got = windowLeft(c, d) }, 100, 100)
	for _, c := range []struct {
		left time.Duration
		want string
	}{
		{3*time.Hour + 11*time.Minute + 30*time.Second, "3 h 12 min"},
		{4 * time.Hour, "4 h"},
		{10 * time.Second, "1 min"},
	} {
		d.UnlockUntil = testNow().Add(c.left)
		mt.Frame()
		if got != c.want {
			t.Errorf("%v left: %q, want %q", c.left, got, c.want)
		}
	}
	if d.UnlockUntil = testNow(); windowOn(d) {
		t.Error("a window is on at its end")
	}
}

// TestUnlockWindowErrors says why a window did not open, and nothing when
// the person cancelled.
func TestUnlockWindowErrors(t *testing.T) {
	const name = "Mi 10 Pro"
	for _, c := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{fmt.Errorf("touch id: %w", secret.ErrCancelled), ""},
		{gateway.ErrUnlockWindowEnded, locales.UnlockWindowChanged(name)},
		{gateway.ErrNoUnlock, locales.UnlockWindowFailed(name, locales.UnlockWindowNoUnlock())},
		{fmt.Errorf("keychain: %w", secret.ErrNotFound), locales.UnlockErrNoPIN(name)},
		{secret.ErrUnsupported, locales.UnlockNotAvailable()},
		{fmt.Errorf("pixel: %w", driver.ErrOffline), locales.UnlockErrOffline(name)},
		{errors.New("the keyring is locked"), locales.UnlockWindowFailed(name, "the keyring is locked")},
	} {
		if got := windowError(c.err, name); got != c.want {
			t.Errorf("windowError(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// TestUnlockWindowFits: in each language, the lengths and Cancel stay in
// ⋯, and the strip under the mirror shows its words whole beside End, with
// the most it says, at the widths the window has.
func TestUnlockWindowFits(t *testing.T) {
	for _, lang := range testLanguages() {
		t.Run(langShort(lang), func(t *testing.T) {
			ta := newWindowApp(t)
			ta.selectPhone(uwPhone)
			tt := mg.NewTester(ta.view, 1280, 820)
			inLanguage(t, lang, tt)
			tt.Frame()
			if err := tt.Click(locales.PhoneMoreControls()); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			sw := uwSwitch(t, tt)
			clickMid(tt, sw.Bounds)
			tt.Frame()
			right := sw.Bounds.X + sw.Bounds.W + 1
			var last mg.Rect
			for i, name := range []string{locales.UnlockWindowCancel(), locales.UnlockWindowFor(1), locales.UnlockWindowFor(4), locales.UnlockWindowFor(8)} {
				b, ok := tt.AccessByRole(mg.RoleButton, name)
				if !ok {
					t.Fatalf("no %q in ⋯: %q", name, tt.Texts())
				}
				if b.Bounds.X+b.Bounds.W > right {
					t.Errorf("%q %v goes past the switch's edge %.1f", name, b.Bounds, right)
				}
				if i > 1 && b.Bounds.Y == last.Y && b.Bounds.X < last.X+last.W {
					t.Errorf("%q %v overlaps %v", name, b.Bounds, last)
				}
				last = b.Bounds
			}
			if r, ok := tt.Find(locales.UnlockWindowHowLong()); !ok || r.H > 2*fsSmall {
				t.Errorf("%q is %v, not on a line", locales.UnlockWindowHowLong(), r)
			}
			tt.Key(0, mg.KeyEscape)

			ta.openWindowNow(t, 8*time.Hour)
			clockAt(t, wallTime(2026, 11, 26, 14, 31))
			words := locales.UnlockWindowBanner(leftWords(8*60 - 1))
			whole := textWidth(t, lang, words)
			for _, w := range []int{960, 1280} {
				tt.SetSize(w, 820)
				tt.Frame()
				r, ok := tt.Find(words)
				if !ok {
					t.Fatalf("width %d: no %q in %q", w, words, tt.Texts())
				}
				if r.W < whole-0.5 {
					t.Errorf("width %d: %q is %.1f wide, cut from %.1f", w, words, r.W, whole)
				}
				end, ok := tt.AccessByRole(mg.RoleButton, locales.UnlockWindowEndLabel(uwName))
				if !ok || end.Bounds.X < r.X+r.W {
					t.Errorf("width %d: End %v overlaps the words %v", w, end.Bounds, r)
				}
			}
		})
	}
}

// textWidth returns how wide the strip's words are, whole, in the
// language lang.
func textWidth(t *testing.T, lang, words string) float32 {
	t.Helper()
	var w float32
	mt := mg.NewTester(func(c *mg.Context) {
		applyTheme(c)
		w, _ = c.MeasureText(0, mg.Span{Text: words, Size: fsSmall})
	}, 200, 100)
	inLanguage(t, lang, mt)
	mt.Frame()
	return w
}

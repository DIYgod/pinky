package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// frameUntil builds frames and syncs until cond holds.
func (ta *testApp) frameUntil(t *testing.T, tt *mg.Tester, what string, cond func() bool) {
	t.Helper()
	ta.waitFor(t, what, func() bool { tt.Frame(); return cond() })
	tt.Frame()
}

func TestCheckupOfXiaomiPhone(t *testing.T) {
	ta := newTestApp(t, fakeapi.Checkup)
	ta.sync()
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if err := tt.Click("More phone controls"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Checkup"); err != nil {
		t.Fatal(err)
	}
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf("redmi-k80"); return r != nil && r.have })
	for _, want := range []string{"Checkup: Redmi K80", "2 items to fix", "System: HyperOS 2.0",
		"Tapping and typing", locales.CheckInputRefused(), locales.CheckInputXiaomi(), locales.CheckInputXiaomiNote(),
		"Pinky Companion", locales.CheckCompanionMissing(), "Seeing the screen", "Working in the background", "Wi-Fi"} {
		if !hasText(tt, want) {
			t.Errorf("the checkup lacks %q: %q", want, tt.Texts())
		}
	}
	// The way to Developer options is Xiaomi's.
	if !hasText(tt, "Additional settings") {
		t.Errorf("no Xiaomi path: %q", tt.Texts())
	}
	// Installing fails as on a Xiaomi phone with Install via USB off: the
	// note says what to turn on there.
	if err := tt.Click("Install"); err != nil {
		t.Fatal(err)
	}
	ta.frameUntil(t, tt, "the install to fail", func() bool { return ta.comp.err["redmi-k80"] != "" })
	if got := ta.comp.err["redmi-k80"]; got != locales.CompanionErrRestrictedXiaomi() {
		t.Errorf("the install error = %q", got)
	}
	if !hasText(tt, locales.CompanionErrRestrictedXiaomi()) {
		t.Errorf("the checkup does not say why the install failed: %q", tt.Texts())
	}
	// Once the phone allows installs, installing again passes the item:
	// the checkup runs again by itself.
	ta.api.SetInstallError("redmi-k80", nil)
	if err := tt.Click("Install"); err != nil {
		t.Fatal(err)
	}
	ta.frameUntil(t, tt, "the install", func() bool {
		r := ta.checkupOf("redmi-k80")
		return ta.comp.installing["redmi-k80"].IsZero() && ta.comp.err["redmi-k80"] == "" && r != nil && !r.running
	})
	if !hasText(tt, locales.CheckCompanionPass()) || !hasText(tt, "1 item to fix") {
		t.Errorf("after the install: %q", tt.Texts())
	}
	if err := tt.Click("Back to the phone"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if hasText(tt, "Checkup: Redmi K80") {
		t.Errorf("the checkup still shows")
	}
}

func TestCheckupAllGood(t *testing.T) {
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.sync()
	tt := mg.NewTester(ta.view, 1280, 820)
	d := ta.device("pixel-9")
	ta.openCheckup(*d)
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf("pixel-9"); return r != nil && r.have && !r.running })
	if !hasText(tt, "Everything works.") || !hasText(tt, locales.CheckInputPass()) || hasText(tt, "items to fix") {
		t.Errorf("a phone that has everything: %q", tt.Texts())
	}
	// The checkup of a phone that is gone says how to connect it again.
	ta.api.Unplug("pixel-9")
	ta.frameUntil(t, tt, "the phone to go", func() bool { return ta.device("pixel-9").Info.State == driver.StateOffline })
	ta.frameUntil(t, tt, "the checkup to run again", func() bool {
		r := ta.checkupOf("pixel-9")
		return r != nil && !r.running && r.state == driver.StateOffline
	})
	for _, want := range []string{"Pixel 9 isn't connected.", locales.CheckConnectionAndroidStepCable(), locales.CheckupWaitOnline()} {
		if !hasText(tt, want) {
			t.Errorf("an offline phone's checkup lacks %q: %q", want, tt.Texts())
		}
	}
}

func TestCheckupFromOfflinePhone(t *testing.T) {
	ta := newTestApp(t, fakeapi.ThreePhones)
	ta.sync()
	ta.sel = "galaxy-s25"
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if err := tt.Click("Checkup"); err != nil {
		t.Fatalf("no Checkup on an offline phone's page: %v %q", err, tt.Texts())
	}
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf("galaxy-s25"); return r != nil && r.have })
	if !hasText(tt, "Galaxy S25 isn't connected.") {
		t.Errorf("the offline phone's checkup: %q", tt.Texts())
	}
}

func TestCheckupFromSettings(t *testing.T) {
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.sync()
	ta.page, ta.set.sec = pageSettings, secPhones
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	if err := tt.Click("Checkup of Pixel 9"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ta.page != pagePhones || ta.sel != "pixel-9" || ta.chk.showing != "pixel-9" {
		t.Errorf("page %v, phone %q, checkup %q", ta.page, ta.sel, ta.chk.showing)
	}
}

func TestAddAndroidSteps(t *testing.T) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.NoPhones, Speed: 50, HoldSetup: true})
	ta.sync()
	ta.page = pageAddPhone
	ta.add.reset(ta.app)
	ta.add.step = "android"
	tt := mg.NewTester(ta.view, 1280, 820)
	tt.Frame()
	for _, want := range []string{"Which phone is it?", "Xiaomi · Redmi · POCO", "Samsung", "Other",
		"Turn on Developer options", "Build number", "Turn on USB debugging", "Plug in the phone and allow this computer",
		"Allow USB debugging?", "Looking for your phone…"} {
		if !hasText(tt, want) {
			t.Errorf("the Android steps lack %q: %q", want, tt.Texts())
		}
	}
	// Each maker's steps: Xiaomi's tap OS version and turn on its two
	// switches; Samsung's turn Auto Blocker off.
	if err := tt.Click("Xiaomi · Redmi · POCO"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{locales.GuideXiaomiTap(), locales.AddAndroidTurnOn(locales.GuideXiaomiSwitchSecurity()),
		locales.AddAndroidTurnOn(locales.GuideXiaomiSwitchInstall())} {
		if !hasText(tt, want) {
			t.Errorf("Xiaomi's steps lack %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Samsung"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !hasText(tt, locales.AddAndroidTurnOff(locales.GuideSamsungAutoBlocker())) || hasText(tt, locales.GuideXiaomiSwitchSecurity()) {
		t.Errorf("Samsung's steps: %q", tt.Texts())
	}

	// A Redmi turns up and waits to be allowed, then comes online: the
	// steps switch to Xiaomi's, and its checkup says what to fix.
	cu := fakeapi.XiaomiCheckup()
	id := ta.api.ConnectPhoneAs("Redmi K80", &cu)
	ta.frameUntil(t, tt, "the phone", func() bool { return ta.device(id) != nil })
	if !hasText(tt, "Found Redmi K80. Unlock it and tap Allow on the phone.") {
		t.Errorf("waiting for Allow: %q", tt.Texts())
	}
	ta.droid.waiting[id] = time.Now().Add(-time.Minute)
	tt.Frame()
	if !hasText(tt, locales.AddAndroidNoPrompt()) {
		t.Errorf("no help when the phone shows no prompt: %q", tt.Texts())
	}
	ta.api.FinishSetup()
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf(id); return r != nil && r.have && !r.running })
	if ta.droid.brand != "xiaomi" {
		t.Errorf("the steps did not switch to Xiaomi's: %q", ta.droid.brand)
	}
	for _, want := range []string{"Pinky recognized HyperOS 2.0 on Redmi K80: these are its steps.", "Check the phone",
		locales.CheckInputXiaomi(), "Done", "Check again"} {
		if !hasText(tt, want) {
			t.Errorf("after the phone came online, no %q: %q", want, tt.Texts())
		}
	}
	// The finished steps' marks are named Done too: Done is the button
	// left of Check again.
	again, ok := tt.Find("Check again")
	if !ok {
		t.Fatal("no Check again")
	}
	tt.ClickAt(again.X-30, again.Y+again.H/2)
	tt.Frame()
	if ta.page != pagePhones || ta.sel != id {
		t.Errorf("Done: page %v, phone %q", ta.page, ta.sel)
	}
}

func TestAddAndroidUSBPermission(t *testing.T) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.NoPhones, Speed: 50, HoldSetup: true})
	ta.sync()
	ta.page = pageAddPhone
	ta.add.reset(ta.app)
	ta.add.step = "android"
	ta.devices = append(ta.devices, gateway.DeviceView{Info: driver.Info{ID: "usb:1-2", Platform: driver.Android, Name: "usb:1-2",
		State: driver.StateNeedsSetup, Detail: "This computer has no permission to use the phone over USB (missing udev rules? user is in the plugdev group)."}})
	tt := mg.NewTester(func(c *mg.Context) { ta.addPhonePage(c) }, 1280, 820)
	tt.Frame()
	if !hasText(tt, locales.CheckUSBPermission()) || !hasText(tt, locales.CheckUSBPermissionNote()) {
		t.Errorf("no udev help: %q", tt.Texts())
	}
}

func TestCompanionErrors(t *testing.T) {
	restricted := errors.New("adb: Failure [INSTALL_FAILED_USER_RESTRICTED: Install canceled by user]")
	for _, c := range []struct {
		err    error
		family string
		want   string
	}{
		{errors.New("context deadline exceeded: x"), "", locales.CompanionErrOther("context deadline exceeded: x")},
		{restricted, "xiaomi", locales.CompanionErrRestrictedXiaomi()},
		{restricted, "", locales.CompanionErrRestricted()},
		{restricted, "vivo", locales.CompanionErrRestricted()},
		{errors.New("adb: Failure [INSTALL_FAILED_ABORTED: User rejected permissions]"), "vivo", locales.CompanionErrDeclined()},
		{errors.New("adb: Failure [INSTALL_FAILED_VERIFICATION_FAILURE]"), "", locales.CompanionErrPlayProtect()},
		{errors.New("adb shell settings: exit status 255: java.lang.SecurityException: Permission denial, must have one of: [android.permission.WRITE_SECURE_SETTINGS]"),
			"xiaomi", locales.CompanionErrSettingsXiaomi()},
		{errors.New("Permission denial: writing to settings requires:android.permission.WRITE_SECURE_SETTINGS"), "oppo", locales.CompanionErrSettingsOppo()},
		{driver.ErrOffline, "", locales.CompanionErrOffline()},
	} {
		if got := companionError(c.err, c.family); got != c.want {
			t.Errorf("companionError(%v, %q) = %q, want %q", c.err, c.family, got, c.want)
		}
	}
}

func TestMenuPathParts(t *testing.T) {
	if got := strings.Join(pathParts(" Settings › About phone ›OS version "), "|"); got != "Settings|About phone|OS version" {
		t.Errorf("pathParts = %q", got)
	}
	for _, f := range []string{"xiaomi", "oppo", "vivo", "honor", "huawei", "samsung", "pixel"} {
		if brandOfFamily(f) != f || brandByID(f).id != f {
			t.Errorf("no steps for %q", f)
		}
	}
	if brandOfFamily("") != "other" || brandByID("nope").id != "other" {
		t.Error("stock Android has no steps of its own")
	}
	for _, b := range brands() {
		if len(pathParts(b.about())) < 1 || b.tap() == "" || len(pathParts(b.dev())) < 2 || len(b.switches()) == 0 {
			t.Errorf("%s's steps are incomplete", b.id)
		}
	}
}

func TestCheckupInChinese(t *testing.T) {
	inLanguage(t, "zh-Hans")
	ta := newTestApp(t, fakeapi.Checkup)
	ta.sync()
	tt := mg.NewTester(ta.view, 1280, 820)
	inLanguage(t, "zh-Hans", tt)
	d := ta.device("redmi-k80")
	ta.openCheckup(*d)
	ta.frameUntil(t, tt, "the checkup", func() bool { r := ta.checkupOf("redmi-k80"); return r != nil && r.have && !r.running })
	for _, want := range []string{"Redmi K80 体检", "点按和输入", "USB调试（安全设置）", "更多设置"} {
		if !hasText(tt, want) {
			t.Errorf("the Chinese checkup lacks %q: %q", want, tt.Texts())
		}
	}
}

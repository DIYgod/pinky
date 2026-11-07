package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

func TestIPhoneStep(t *testing.T) {
	dev := func(state driver.State, setup string) *gateway.DeviceView {
		return &gateway.DeviceView{Info: driver.Info{Platform: driver.IOS, State: state, Setup: setup}}
	}
	for _, c := range []struct {
		d    *gateway.DeviceView
		host driver.HostCheck
		want int
	}{
		{nil, driver.HostCheck{Missing: "apple_devices"}, iphoneHost},
		{nil, driver.HostCheck{Missing: "usbmuxd_stopped"}, iphonePlug}, // installed: it starts with the phone
		{nil, driver.HostCheck{}, iphonePlug},
		{dev(driver.StateOffline, ""), driver.HostCheck{}, iphonePlug},
		{dev(driver.StateUnauthorized, driver.SetupTrust), driver.HostCheck{}, iphoneTrust},
		{dev(driver.StateNeedsSetup, driver.SetupDeveloperMode), driver.HostCheck{}, iphoneDevMode},
		{dev(driver.StateNeedsSetup, driver.SetupPreparing), driver.HostCheck{}, iphonePrepare},
		{dev(driver.StateNeedsSetup, driver.SetupNoDiskImage), driver.HostCheck{}, iphonePrepare},
		{dev(driver.StateNeedsSetup, ""), driver.HostCheck{}, iphonePrepare},
		{dev(driver.StateOnline, ""), driver.HostCheck{}, iphoneReady},
	} {
		if got := iphoneStep(c.d, c.host); got != c.want {
			t.Errorf("iphoneStep(%+v, %+v) = %d, want %d", c.d, c.host, got, c.want)
		}
	}
}

// addIPhoneApp opens Add phone › iPhone on a fake that holds a new iPhone
// at each step until the test moves it on.
func addIPhoneApp(t *testing.T) (*testApp, *mg.Tester) {
	ta := newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.OnePhone, Speed: 50, HoldSetup: true})
	ta.sync()
	ta.page = pageAddPhone
	ta.add.reset(ta.app)
	ta.add.step = "iphone"
	ta.add.snapshot(ta.app)
	return ta, mg.NewTester(ta.view, 1280, 820)
}

// TestAddIPhoneSteps walks a new iPhone through Add phone: each step says
// what to do on the phone, and the next one shows once the phone gets there.
func TestAddIPhoneSteps(t *testing.T) {
	ta, tt := addIPhoneApp(t)
	tt.Frame()
	for _, want := range []string{"Connect your iPhone", "Plug in the iPhone", "Looking for your iPhone…", "Trust this computer", "Turn on Developer Mode", "Prepare the iPhone"} {
		if !hasText(tt, want) {
			t.Fatalf("before the phone: no %q in %q", want, tt.Texts())
		}
	}
	ta.api.ConnectIPhone()
	for _, step := range []struct{ what, text string }{
		{"the Trust step", "Waiting for the iPhone to trust this computer…"},
		{"the Developer Mode step", "Settings › Privacy & Security › Developer Mode"},
		{"the preparation step", "Preparing the iPhone…"},
		{"the phone ready", "iPhone 16 is ready"},
	} {
		ta.waitFor(t, step.what, func() bool { tt.Frame(); return hasText(tt, step.text) })
		if step.text != "Waiting for the iPhone to trust this computer…" && !hasText(tt, "Found iPhone 16 · iOS 27.1") {
			t.Errorf("%s: the found phone is not named in %q", step.what, tt.Texts())
		}
		ta.api.AdvanceIPhone()
	}
	if _, ok := tt.Find("Done"); !ok {
		t.Fatal("no Done button once the phone is ready")
	}
}

// TestAddIPhoneStuck shows why the preparation step is stuck, in words for
// the case of a computer without the developer disk image.
func TestAddIPhoneStuck(t *testing.T) {
	ta, tt := addIPhoneApp(t)
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the phone", func() bool { tt.Frame(); return ta.add.iphone(ta.app) != nil })
	id := ta.add.iphone(ta.app).Info.ID
	defer func(os string) { guideOS = os }(guideOS)

	guideOS = "darwin"
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupNoDiskImage, Detail: "Pinky needs Apple's developer disk image."})
	ta.waitFor(t, "the missing image", func() bool { tt.Frame(); return hasText(tt, "This computer has no developer disk image yet") })
	if !hasText(tt, "Install Xcode from the App Store and open it once") {
		t.Errorf("no word of Xcode in %q", tt.Texts())
	}
	if _, ok := tt.Find("Get Xcode in the App Store"); !ok {
		t.Error("no App Store link")
	}

	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDiskImage, Detail: "Pinky could not reach Apple's signing server (gs.apple.com)."})
	ta.waitFor(t, "the failed mount", func() bool { tt.Frame(); return hasText(tt, "Pinky could not finish this step.") })
	if !hasText(tt, "gs.apple.com") || !hasText(tt, "Pinky tries again by itself.") {
		t.Errorf("the reason or the retry note is missing: %q", tt.Texts())
	}

	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupUnlock})
	ta.waitFor(t, "the unlock step", func() bool { tt.Frame(); return hasText(tt, "Unlock the iPhone so Pinky can finish.") })

	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupUpdate, OSVersion: "16.7"})
	ta.waitFor(t, "an old iOS", func() bool { tt.Frame(); return hasText(tt, "needs a newer iOS") })
}

// TestAddIPhoneDriver asks Windows for Apple's iPhone driver first.
func TestAddIPhoneDriver(t *testing.T) {
	defer func(os string) { guideOS = os }(guideOS)
	guideOS = "windows"
	ta, tt := addIPhoneApp(t)
	ta.api.SetHostCheck(driver.HostCheck{Missing: "apple_devices", Detail: "install Apple Devices"})
	ta.waitFor(t, "the driver step", func() bool { tt.Frame(); return hasText(tt, "Waiting for it to be installed…") })
	if !hasText(tt, "Install Apple's iPhone driver") || !hasText(tt, "Microsoft Store") {
		t.Fatalf("no driver step in %q", tt.Texts())
	}
	ta.api.SetHostCheck(driver.HostCheck{})
	ta.add.hostAt = ta.add.hostAt.AddDate(0, 0, -1) // ask again now
	ta.waitFor(t, "the plug-in step", func() bool { tt.Frame(); return hasText(tt, "Looking for your iPhone…") })
}

// TestAddIPhoneScreenshots draws each step in light and dark, and in
// Chinese, into $PINKY_SCREENSHOTS, for the notes.
func TestAddIPhoneScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS to a folder to write the screenshots")
	}
	ta, tt := addIPhoneApp(t)
	shoot := func(name string) {
		t.Helper()
		for _, dark := range []bool{false, true} {
			st := mg.NewTester(ta.view, 1280, 820)
			st.SetScale(2)
			st.SetDark(dark)
			st.Frame()
			st.Frame()
			file := name + "-light.png"
			if dark {
				file = name + "-dark.png"
			}
			f, err := os.Create(filepath.Join(dir, file))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, st.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
	tt.Frame()
	shoot("iphone-plug")
	ta.api.ConnectIPhone()
	ta.waitFor(t, "the phone", func() bool { tt.Frame(); return ta.add.iphone(ta.app) != nil })
	id := ta.add.iphone(ta.app).Info.ID
	for _, st := range []struct {
		name string
		info driver.Info
	}{
		{"iphone-trust", driver.Info{State: driver.StateUnauthorized, Setup: driver.SetupTrust}},
		{"iphone-devmode", driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDeveloperMode}},
		{"iphone-preparing", driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupPreparing}},
		{"iphone-noimage", driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupNoDiskImage, Detail: "Pinky needs Apple's developer disk image, which comes with Xcode."}},
		{"iphone-failed", driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDiskImage, Detail: "Pinky could not reach Apple's signing server (gs.apple.com), which signs the developer disk image for each iPhone. Check the internet connection."}},
		{"iphone-ready", driver.Info{State: driver.StateOnline}},
	} {
		ta.api.SetIPhone(id, st.info)
		ta.waitFor(t, st.name, func() bool { tt.Frame(); d := ta.add.iphone(ta.app); return d != nil && d.Info.Setup == st.info.Setup })
		shoot(st.name)
	}
	inLanguage(t, "zh-Hans", tt)
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupDeveloperMode})
	ta.waitFor(t, "Developer Mode in Chinese", func() bool { tt.Frame(); return hasText(tt, "开发者模式") })
	shoot("iphone-devmode-zh")
	ta.api.SetIPhone(id, driver.Info{State: driver.StateNeedsSetup, Setup: driver.SetupNoDiskImage})
	ta.waitFor(t, "the missing image in Chinese", func() bool { tt.Frame(); return hasText(tt, "开发者磁盘映像") })
	shoot("iphone-noimage-zh")
}

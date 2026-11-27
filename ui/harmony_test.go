package ui

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

func TestHarmonyOSIsNotLabelledAndroid(t *testing.T) {
	d := gateway.DeviceView{Info: driver.Info{Platform: driver.HarmonyOS, Connection: "usb", State: driver.StateUnauthorized}, Status: gateway.DeviceNeedsSetup}
	if got := connectionLine(d); got != "HarmonyOS · USB" {
		t.Fatalf("connectionLine = %q", got)
	}
	if got := osLine(d); got != "HarmonyOS · USB" {
		t.Fatalf("osLine = %q", got)
	}
	app := &app{}
	for _, tc := range []struct{ step, text string }{
		{"hdc", locales.HarmonyHDCMissing()},
		{"harmony_backend", locales.HarmonyBackendMissing()},
		{driver.FixAuthorize, locales.HarmonyAuthorize()},
	} {
		w := app.checkWords(d, gateway.CheckupView{}, driver.Check{ID: driver.CheckAuthorized, Fix: tc.step, Status: driver.CheckFail})
		if w.text != tc.text || w.detail {
			t.Errorf("%s: %+v", tc.step, w)
		}
	}
}

type nativeReadyDevice struct{ *demo.Device }

func (*nativeReadyDevice) Checkup(context.Context) (driver.Checkup, error) {
	return driver.Checkup{Family: "huawei", Checks: []driver.Check{{ID: "harmony_control", Status: driver.CheckPass}, {ID: driver.CheckScreen, Status: driver.CheckPass}}}, nil
}

type nativeReadyProvider struct{ d *nativeReadyDevice }

func (*nativeReadyProvider) Platform() driver.Platform { return driver.HarmonyOS }
func (p *nativeReadyProvider) List(context.Context) ([]driver.Info, error) {
	return []driver.Info{p.d.Info()}, nil
}
func (p *nativeReadyProvider) Open(context.Context, string) (driver.Device, error) { return p.d, nil }

func TestNativeHarmonyCheckupShowsAuthorizedAndReady(t *testing.T) {
	p := &nativeReadyProvider{d: &nativeReadyDevice{demo.NewDevice("native-ready", "Huawei native test", driver.HarmonyOS)}}
	ga := newStepsApp(t, p)
	ga.waitFor(t, "native device online", func() bool { return ga.device("native-ready").Status == gateway.DeviceIdle })
	d := ga.device("native-ready")
	ga.selectPhone(d.Info.ID)
	ga.openCheckup(d)
	tt := mg.NewTester(ga.view, 1280, 820)
	ga.waitFor(t, "native checkup", func() bool { tt.Frame(); r := ga.checkupOf(d.Info.ID); return r != nil && r.have && !r.running })
	r := ga.checkupOf(d.Info.ID)
	for _, c := range r.view.Checks {
		if c.ID == driver.CheckAuthorized && c.Status != driver.CheckPass {
			t.Fatalf("authorization is misreported: %+v", c)
		}
	}
	for _, lang := range testLanguages() {
		inLanguage(t, lang, tt)
		tt.Frame()
		tt.Frame()
		for _, want := range []string{locales.CheckAuthorizedPass(), locales.HarmonyControlTitle(), locales.HarmonyReady(), locales.HarmonyMirrorReady()} {
			if !hasText(tt, want) {
				t.Errorf("%s lacks %q: %q", lang, want, tt.Texts())
			}
		}
		if hasText(tt, locales.HarmonyBackendMissing()) {
			t.Fatal("old unsupported-control error remains")
		}
		if dir := os.Getenv("PINKY_SCREENSHOTS"); dir != "" {
			f, err := os.Create(filepath.Join(dir, "native-control-checkup-"+langShort(lang)+".png"))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, tt.Image())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Exercise the real gateway's setup state and the actual phone page in
// every UI language. These are synthetic phones, not real screen dumps.
func TestHarmonySetupPages(t *testing.T) {
	p := &stepsProvider{platform: driver.HarmonyOS}
	info := driver.Info{ID: "harmony-fixture", Platform: driver.HarmonyOS, Name: "HarmonyOS phone", Connection: "usb", Backend: "hdc", State: driver.StateNeedsSetup}
	p.list(info)
	ga := newStepsApp(t, p)
	ga.waitFor(t, "HarmonyOS phone", func() bool { return ga.device(info.ID).Info.ID == info.ID })
	for _, step := range []string{"hdc", driver.FixAuthorize, "harmony_backend"} {
		info.Setup, info.State = step, driver.StateNeedsSetup
		if step == driver.FixAuthorize {
			info.State = driver.StateUnauthorized
		}
		p.list(info)
		ga.waitFor(t, step, func() bool { return ga.device(info.ID).Info.Setup == step })
		ga.selectPhone(info.ID)
		for _, lang := range testLanguages() {
			tt := mg.NewTester(ga.view, 1280, 820)
			inLanguage(t, lang, tt)
			tt.Frame()
			tt.Frame()
			want := locales.HarmonyHDCMissing()
			if step == driver.FixAuthorize {
				want = locales.HarmonyAuthorize()
			}
			if step == "harmony_backend" {
				want = locales.HarmonyBackendMissing()
			}
			if !hasText(tt, want) || !hasText(tt, "HarmonyOS · USB") || hasText(tt, locales.PhoneOffline(info.Name)) {
				t.Errorf("%s/%s: page = %q", step, lang, tt.Texts())
			}
			if dir := os.Getenv("PINKY_SCREENSHOTS"); dir != "" {
				f, err := os.Create(filepath.Join(dir, "harmony-"+step+"-"+langShort(lang)+".png"))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, tt.Image())
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

// A native Huawei phone must not be left on the ADB setup instructions
// or the Android pairing spinner, including when other phones are listed.
func TestAddHarmonyReplacesADBGuide(t *testing.T) {
	p := &stepsProvider{platform: driver.HarmonyOS}
	droid := &stepsProvider{platform: driver.Android}
	droid.list(driver.Info{ID: "other-android", Platform: driver.Android, Name: "Other Android", State: driver.StateUnauthorized})
	ga := newStepsApp(t, p, droid)
	ga.waitFor(t, "existing Android", func() bool { return ga.device("other-android").Info.ID == "other-android" })
	ga.page = pageAddPhone
	ga.add.reset(ga.app)
	ga.add.step, ga.droid.brand = "android", "huawei"
	info := driver.Info{ID: "native-huawei", Platform: driver.HarmonyOS, Name: "HarmonyOS phone", Connection: "usb", Backend: "hdc", State: driver.StateUnauthorized, Setup: driver.FixAuthorize}
	p.list(info)
	ga.waitFor(t, "native phone", func() bool { return ga.device(info.ID).Info.ID == info.ID })
	for _, step := range []string{driver.FixAuthorize, "harmony_backend", "hdc"} {
		info.Setup, info.State = step, driver.StateNeedsSetup
		if step == driver.FixAuthorize {
			info.State = driver.StateUnauthorized
		}
		p.list(info)
		ga.waitFor(t, step, func() bool { return ga.device(info.ID).Info.Setup == step })
		for _, lang := range testLanguages() {
			tt := mg.NewTester(ga.view, 1280, 820)
			inLanguage(t, lang, tt)
			tt.Frame()
			tt.Frame()
			for _, want := range []string{locales.HarmonyGuideTitle(), locales.HarmonyGuideDetail(), locales.AddPhoneFound(info.Name)} {
				if !hasText(tt, want) {
					t.Errorf("%s/%s: missing %q: %q", step, lang, want, tt.Texts())
				}
			}
			for _, unwanted := range []string{locales.AddPhoneLooking(), locales.AddAndroidStepDev(), locales.GuideHuaweiSwitchCharge(), locales.AddPhoneOrWiFi()} {
				if hasText(tt, unwanted) {
					t.Errorf("%s/%s: wrong ADB instruction %q", step, lang, unwanted)
				}
			}
			if step == driver.FixAuthorize && !hasText(tt, locales.HarmonyHandshakeNote()) {
				t.Error("missing handshake explanation")
			}
			if dir := os.Getenv("PINKY_SCREENSHOTS"); dir != "" {
				f, err := os.Create(filepath.Join(dir, "add-harmony-"+step+"-"+langShort(lang)+".png"))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, tt.Image())
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	// Opening Add phone after discovery must still explain this phone.
	ga.add.reset(ga.app)
	ga.add.step, ga.droid.brand = "android", "huawei"
	tt := mg.NewTester(ga.view, 1280, 820)
	inLanguage(t, "en", tt)
	tt.Frame()
	tt.Frame()
	if !hasText(tt, locales.HarmonyGuideTitle()) {
		t.Fatal("known native phone lost in Android guide")
	}
	// Disconnecting the followed phone must not silently switch to ADB.
	info.State, info.Detail = driver.StateOffline, "The phone disconnected."
	p.list(info)
	ga.waitFor(t, "offline native phone", func() bool { return ga.device(info.ID).Info.State == driver.StateOffline })
	tt.Frame()
	tt.Frame()
	if !hasText(tt, locales.PhoneOffline(info.Name)) || hasText(tt, locales.AddPhoneLooking()) {
		t.Fatalf("offline page = %q", tt.Texts())
	}
	// A different brand remains on its normal Android instructions.
	ga.droid.brand = "pixel"
	tt.Frame()
	tt.Frame()
	if hasText(tt, locales.HarmonyGuideTitle()) || !hasText(tt, locales.AddAndroidStepDev()) {
		t.Fatalf("other brand page = %q", tt.Texts())
	}
}

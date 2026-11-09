package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
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

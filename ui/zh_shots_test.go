package ui

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/secret"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// TestChineseScreenshots draws every screen in Simplified Chinese into
// $PINKY_SCREENSHOTS_ZH, to look for text that is cut or overflows. Tests
// have no video, so the mirrors show stills of the fake's test source and
// of a demo phone's drawn app. PINKY_SCREENSHOTS_DARK=1 draws the dark
// appearance too.
func TestChineseScreenshots(t *testing.T) {
	dir := os.Getenv("PINKY_SCREENSHOTS_ZH")
	if dir == "" {
		t.Skip("set PINKY_SCREENSHOTS_ZH to a folder to write the Chinese screenshots")
	}
	inLanguage(t, "zh-Hans")
	_, devs := demo.World()
	drawn, err := devs[0].Screenshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	still := func(ta *testApp, key, id string, img image.Image, agent bool) {
		b := img.Bounds()
		ta.mirrors[key] = &mirror{a: ta.app, id: id, agent: agent, state: "playing", bitmap: mg.NewBitmap(img),
			picW: b.Dx(), picH: b.Dy(), used: ta.buildSeq, retry: time.Now().Add(time.Hour)}
	}
	source := func(ta *testApp, id string) image.Image {
		path, err := ta.api.MediaPath(context.Background(), "shot-"+id)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	themes := []bool{false}
	if os.Getenv("PINKY_SCREENSHOTS_DARK") != "" {
		themes = append(themes, true)
	}
	write := func(name string, dark bool, img image.Image) {
		file := name + "-zh-light.png"
		if dark {
			file = name + "-zh-dark.png"
		}
		f, err := os.Create(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	// shoot draws view at w × h; before runs before each frame, to put the
	// stills back that a build swept.
	shoot := func(ta *testApp, name string, view func(*mg.Context), w, h int, before func()) {
		t.Helper()
		for _, dark := range themes {
			ta.sync()
			if before != nil {
				before()
			}
			tt := mg.NewTester(view, w, h)
			inLanguage(t, "zh-Hans", tt)
			tt.SetScale(2)
			tt.SetDark(dark)
			tt.Frame()
			if before != nil {
				before()
			}
			tt.Frame()
			write(name, dark, tt.Image())
		}
	}
	main := func(ta *testApp, name string, before func()) {
		t.Helper()
		shoot(ta, name, ta.view, 1280, 820, before)
	}
	pixel := func(ta *testApp) func() {
		img := source(ta, "pixel-9")
		return func() { still(ta, "pixel-9", "pixel-9", img, false) }
	}

	// One phone, the payment waiting.
	ta := newTestAppTimeout(t, fakeapi.OnePhone, 10*time.Minute)
	ta.waitFor(t, "the approval", func() bool { _, ok := ta.approval(); return ok })
	main(ta, "phone-one", pixel(ta))
	ta.more["pixel-9"] = true
	main(ta, "phone-more", pixel(ta))
	ta.more["pixel-9"] = false
	n, _ := ta.approval()
	ta.noting[n.ID] = true
	main(ta, "phone-note", pixel(ta))
	shoot(ta, "panel-one", ta.panelView, panelW, panelH, nil)

	// Three phones, and All phones.
	ta = newTestAppTimeout(t, fakeapi.ThreePhones, 10*time.Minute)
	ta.waitFor(t, "two approvals", func() bool { return len(ta.approvals()) == 2 })
	main(ta, "phone-three", pixel(ta))
	ta.sel = allPhones
	main(ta, "all-phones", nil)
	shoot(ta, "panel-three", ta.panelView, panelW, panelH, nil)
	shoot(ta, "flyout", ta.flyoutView, 320, ta.flyoutHeight(), nil)
	ta.remember(ta.approvals()[0], "allow_once", "")
	shoot(ta, "panel-allowed", ta.panelView, panelW, panelH, nil)

	// The person in control, a handoff, a new agent.
	ta = newTestAppTimeout(t, fakeapi.Takeover, 10*time.Minute)
	ta.waitFor(t, "the takeover", func() bool { return len(ta.devices) > 0 && ta.devices[0].Status == "in_control" })
	main(ta, "takeover", pixel(ta))
	ta = newTestAppTimeout(t, fakeapi.Handoff, 10*time.Minute)
	ta.waitFor(t, "the handoff", func() bool { return len(ta.needs) > 0 })
	main(ta, "handoff", pixel(ta))
	shoot(ta, "panel-handoff", ta.panelView, panelW, panelH, nil)
	ta = newTestAppTimeout(t, fakeapi.NewAgent, 10*time.Minute)
	ta.waitFor(t, "the new agent", func() bool { return len(ta.needs) > 0 })
	main(ta, "new-agent", pixel(ta))
	ta.page, ta.set.sec = pageSettings, secAgents
	main(ta, "settings-agents-request", nil)

	// A locked phone: the PIN form, then Touch ID.
	ta = newTestApp(t, fakeapi.Locked)
	ta.waitFor(t, "the unlock card", func() bool { return len(ta.needs) == 1 && ta.pin.ready })
	main(ta, "locked", pixel(ta))
	ta.pin.editing, ta.pin.where, ta.pin.value = "pixel-9", "card", "2468"
	main(ta, "unlock-pin", pixel(ta))
	ta.pin.editing, ta.pin.value = "", ""
	ta.opt.Secrets.Save(secret.PINAccount("pixel-9"), "2468")
	ta.pin.saved["pixel-9"] = true
	main(ta, "unlock-touch-id", pixel(ta))

	// Settings, every section.
	for _, s := range []struct {
		sec  settingsSection
		name string
	}{{secSafety, "settings-safety"}, {secPhones, "settings-phones"}, {secAgents, "settings-agents"},
		{secPrivacy, "settings-privacy"}, {secAdvanced, "settings-advanced"}} {
		ta.page, ta.set.sec = pageSettings, s.sec
		main(ta, s.name, nil)
	}
	ta.opt.AgentToken = "demo-0f3c9a7e51b24d6c8e2a"
	ta.settings.RemoteAgents = true
	ta.page, ta.set.sec = pageSettings, secAgents
	main(ta, "settings-remote", nil)

	// The companion app, the agent's screen.
	ta = newTestApp(t, fakeapi.Companion)
	ta.sync()
	img := source(ta, "pixel-9")
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the companion state", func() bool { tt.Frame(); return ta.comp.state["pixel-9"] != "" })
	main(ta, "companion", func() { still(ta, "pixel-9", "pixel-9", img, false) })
	ta = newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.Background, Speed: 0.5, ApprovalTimeout: time.Minute})
	ta.waitFor(t, "the task", func() bool { return ta.anyRunning() && len(ta.tasks[0].Steps) >= 2 })
	main(ta, "background", func() { still(ta, "agent:pixel-9", "pixel-9", drawn, true) })

	// Add phone: the choice, Android, pairing.
	ta = newTestAppWith(t, fakeapi.Options{Scenario: fakeapi.NoPhones, Speed: 1, PairDelay: -1})
	ta.sync()
	ta.page = pageAddPhone
	ta.add.reset(ta.app)
	main(ta, "add-phone", nil)
	ta.add.step = "android"
	main(ta, "add-android", nil)
	ta.add.step = "iphone"
	main(ta, "add-iphone", nil)
	ta.add.step = "qr"
	tt = mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the QR code", func() bool { tt.Frame(); return ta.add.pair.code != nil })
	main(ta, "pair-qr", nil)
	ta.stopPairing()
	ta.add.pair.form, ta.add.pair.addr, ta.add.pair.pin = true, "192.168.1.20:37123", "000000"
	ta.add.pair.formErr = pairError(errorString("adb: failed to pair: Wrong password or connection was dropped"))
	main(ta, "pair-code", nil)

	// Activity, a task, its recording.
	ta = newTestApp(t, fakeapi.OnePhone)
	ta.page = pageActivity
	tt = mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "Activity", func() bool { tt.Frame(); return ta.act.loaded })
	main(ta, "activity", nil)
	ta.act.sel = "h-lunch"
	main(ta, "activity-task", nil)
	ta.waitFor(t, "the recording", func() bool { tt.Frame(); return ta.recordingOf("h-lunch") != nil })
	rec := *ta.recordingOf("h-lunch")
	ta.act.replay = true
	pl := &recPlayer{a: ta.app, task: "h-lunch", rec: rec, state: "paused", pos: rec.Marks[4].At + time.Second}
	b := drawn.Bounds()
	pl.bitmap, pl.picW, pl.picH = mg.NewBitmap(drawn), b.Dx(), b.Dy()
	ta.rec.pl = pl
	main(ta, "recording", nil)
}

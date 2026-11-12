package ui

import (
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// TestPhoneTasksCard says, once Pinky's app is on an Android phone, that
// it can do tasks there by itself, with how to set it up, until Got it
// puts the card away for every phone; Settings › Phones keeps saying so.
func TestPhoneTasksCard(t *testing.T) {
	ta := newTestApp(t, fakeapi.Companion)
	ta.sync()
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "the companion state", func() bool { tt.Frame(); return ta.comp.state["pixel-9"] == driver.CompanionMissing })
	tt.Frame()
	if hasText(tt, "Pinky on the phone can work by itself") {
		t.Errorf("the card shows before the app is installed: %q", tt.Texts())
	}
	if err := tt.Click("Install Pinky on Pixel 9"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the install", func() bool { return ta.comp.state["pixel-9"] == driver.CompanionReady })
	tt.Frame()
	for _, want := range []string{"Pinky on the phone can work by itself", "Open Pinky on Pixel 9", "How to set it up", "Got it"} {
		if !hasText(tt, want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Got it"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if hasText(tt, "Pinky on the phone can work by itself") || !ta.tasksTip.seen {
		t.Errorf("after Got it: %q", tt.Texts())
	}
	ta.page, ta.set.sec = pageSettings, secPhones
	tt.Frame()
	if !hasText(tt, "Tasks on the phone") || !hasText(tt, "Open Pinky on Pixel 9") || !hasText(tt, "How to set it up") {
		t.Errorf("Settings › Phones: %q", tt.Texts())
	}
}

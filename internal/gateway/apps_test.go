package gateway

import (
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
)

// TestMatchAppByID finds apps by English names on a phone in another
// language, through their package or bundle ids.
func TestMatchAppByID(t *testing.T) {
	apps := []driver.App{
		{ID: "com.google.android.calculator", Name: "计算器", System: true},
		{ID: "com.google.android.deskclock", Name: "时钟", System: true},
		{ID: "com.android.settings", Name: "设置", System: true},
		{ID: "com.apple.mobilecal", Name: "日历", System: true},
		{ID: "com.example.cal", Name: "Cal"},
	}
	for q, want := range map[string]string{
		"Calculator": "com.google.android.calculator",
		"Clock":      "com.google.android.deskclock",
		"Settings":   "com.android.settings",
		"Cal":        "com.example.cal", // a name that matches wins
		"设置":         "com.android.settings",
	} {
		got, ok := matchApp(apps, q)
		if !ok || got.ID != want {
			t.Errorf("matchApp(%q) = %q, %v; want %q", q, got.ID, ok, want)
		}
	}
	if _, ok := matchApp(apps, "Maps"); ok {
		t.Error("matched an app the phone lacks")
	}
}

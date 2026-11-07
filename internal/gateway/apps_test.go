package gateway

import (
	"context"
	"strings"
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

// A task app list with a name no installed app has (天气 for an English
// Weather app) is pointed out on connect, and connecting again with the
// same task takes the corrected list.
func TestConnectPointsOutUnknownTaskApps(t *testing.T) {
	g, _ := newTestGateway(t)
	s := g.OpenSession(agent)
	t.Cleanup(s.Close)
	ctx := context.Background()
	res := s.Connect(ctx, ConnectRequest{Task: "Lunch", Apps: []string{"天气", "Dinely"}})
	mustStatus(t, res, StatusSettled)
	if !strings.Contains(res.Message, "“天气”") || strings.Contains(res.Message, "“Dinely”") {
		t.Fatalf("connect message: %s", res.Message)
	}
	res = s.Connect(ctx, ConnectRequest{Task: "Lunch", Apps: []string{"Dinely"}})
	mustStatus(t, res, StatusSettled)
	if strings.Contains(res.Message, "No installed app matches") {
		t.Fatalf("still warned: %s", res.Message)
	}
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "com.dinely.app"}), StatusSettled)
}

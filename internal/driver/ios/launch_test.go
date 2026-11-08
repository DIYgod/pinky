package ios

import (
	"errors"
	"reflect"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
)

func TestLinkTarget(t *testing.T) {
	for _, c := range []struct {
		app    string
		link   string
		isLink bool
		err    error
	}{
		{"Settings", "", false, nil},
		{"com.apple.Preferences", "", false, nil},
		{"https://example.com/login", "https://example.com/login", true, nil},
		{"  http://example.com  ", "http://example.com", true, nil},
		{"tel://5551234", "", true, driver.ErrUnsupported},
		{"https://", "", true, driver.ErrUnsupported},
		{"://nothing", "", true, driver.ErrUnsupported},
	} {
		link, isLink, err := linkTarget(c.app)
		if link != c.link || isLink != c.isLink || !errors.Is(err, c.err) {
			t.Errorf("linkTarget(%q) = %q, %v, %v; want %q, %v, %v", c.app, link, isLink, err, c.link, c.isLink, c.err)
		}
	}
}

func TestLaunchInput(t *testing.T) {
	opts := func(in map[string]any) map[string]any { return in["options"].(map[string]any) }
	app, err := launchInput("com.apple.Preferences", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opts(app)["payloadURL"]; ok {
		t.Error("an app launch carries a payload URL")
	}
	if got := app["applicationSpecifier"]; !reflect.DeepEqual(got, map[string]any{"bundleIdentifier": map[string]any{"_0": "com.apple.Preferences"}}) {
		t.Errorf("applicationSpecifier = %v", got)
	}
	link, err := launchInput(safariID, "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if got := opts(link)["payloadURL"]; !reflect.DeepEqual(got, map[string]any{"relative": "https://example.com/"}) {
		t.Errorf("payloadURL = %v", got)
	}
}

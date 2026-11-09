package harmony

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

const unlockedState = "* deviceLocked false 100\n* screenLocked false 100\n"

func TestJPEGTransportAndCleanup(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 8, 16)), nil); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []bool{false, true} {
		cleaned, captured := false, false
		d := &Device{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			cmd := args[len(args)-1]
			switch {
			case strings.HasPrefix(cmd, "'snapshot_display' "):
				captured = strings.HasSuffix(cmd, "'-t' 'jpeg'")
				return []byte("success: snapshot display"), nil
			case strings.HasPrefix(cmd, "'base64' "):
				if corrupt {
					return []byte("invalid!"), nil
				}
				value := base64.StdEncoding.EncodeToString(encoded.Bytes())
				return []byte(value[:40] + "\r\n" + value[40:] + "\r\n"), nil
			case strings.HasPrefix(cmd, "'rm' '-f' "):
				cleaned = true
				return nil, nil
			default:
				t.Fatalf("unexpected command: %s", cmd)
				return nil, nil
			}
		}}
		raw, err := d.tempOutput(context.Background(), "snapshot_display", ".jpeg")
		if !captured || !cleaned {
			t.Fatalf("capture=%v cleanup=%v", captured, cleaned)
		}
		if corrupt {
			if err == nil {
				t.Fatal("corrupt shell transport accepted")
			}
			continue
		}
		if err != nil || !bytes.Equal(raw, encoded.Bytes()) {
			t.Fatalf("JPEG bytes changed: %v", err)
		}
	}
}

func TestLockStateFailsClosed(t *testing.T) {
	for _, out := range []string{"", "* deviceLocked false\n", "* deviceLocked bad\n* screenLocked false"} {
		if _, err := parseLock(out); err == nil {
			t.Errorf("unknown lock state accepted: %q", out)
		}
	}
	if v, err := parseLock(unlockedState); err != nil || v {
		t.Fatalf("unlocked: %v %v", v, err)
	}
	if v, err := parseLock(strings.Replace(unlockedState, "screenLocked false", "screenLocked true", 1)); err != nil || !v {
		t.Fatalf("locked: %v %v", v, err)
	}
}

func TestInputNeverRunsOnLockedOrUnknownPhone(t *testing.T) {
	for _, state := range []string{"unknown", strings.Replace(unlockedState, "deviceLocked false", "deviceLocked true", 1)} {
		d := &Device{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			cmd := args[len(args)-1]
			if strings.Contains(cmd, "'uiInput'") {
				t.Fatal("input injected without known unlocked state")
			}
			return []byte(state), nil
		}}
		if err := d.Tap(context.Background(), screen.Point{X: 10, Y: 10}, driver.TapOptions{}); err == nil {
			t.Fatal("tap allowed")
		}
		if err := d.Key(context.Background(), driver.KeyHome); err == nil {
			t.Fatal("key allowed")
		}
	}
	d := &Device{}
	if err := d.Type(context.Background(), "secret", driver.TypeOptions{}); !errors.Is(err, driver.ErrUnsupported) {
		t.Fatalf("unverified text input: %v", err)
	}
}

func TestLayoutExcludesOtherWindows(t *testing.T) {
	node := func(attrs map[string]string, children ...uiNode) uiNode {
		return uiNode{Attrs: attrs, Children: children}
	}
	root := node(map[string]string{"bounds": "[0,0][100,200]"},
		node(map[string]string{"bundleName": "com.test.bank", "hostWindowId": "8", "bounds": "[0,0][100,200]", "text": "private"}),
		node(map[string]string{"bundleName": "com.test.calculator", "hostWindowId": "7", "bounds": "[0,0][100,200]"},
			node(map[string]string{"type": "Text", "text": "2", "bounds": "[10,20][30,40]", "hostWindowId": "7"}),
			node(map[string]string{"type": "TextInput", "focused": "true", "password": "true", "enabled": "false", "bounds": "[10,50][90,70]", "hostWindowId": "7"}),
		),
	)
	raw, _ := json.Marshal(root)
	s, err := parseLayout(raw, "7")
	if err != nil {
		t.Fatal(err)
	}
	if s.App.ID != "com.test.calculator" || s.Width != 100 || s.Height != 200 || len(s.Elements) != 2 {
		t.Fatalf("screen=%+v", s)
	}
	for _, e := range s.Elements {
		if e.Label == "private" {
			t.Fatal("another window leaked")
		}
	}
	input := s.Elements[1]
	if !input.Has(screen.StatePassword) || !input.Has(screen.StateFocused) || !input.Has(screen.StateDisabled) {
		t.Fatalf("input=%+v", input)
	}
	if _, err := parseLayout(raw, "99"); err == nil {
		t.Fatal("unknown foreground accepted")
	}
}

func TestShellArgumentsAreQuoted(t *testing.T) {
	got := shellCommand("aa", "start", "-b", "test';$(id)")
	if got != "'aa' 'start' '-b' 'test'\\'';$(id)'" {
		t.Fatalf("unsafe command: %q", got)
	}
}

func TestUITestNoErrorIsSuccess(t *testing.T) {
	d := &Device{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if strings.Contains(args[len(args)-1], "'uiInput'") {
			return []byte("No Error\n"), nil
		}
		return []byte(unlockedState), nil
	}}
	if err := d.Tap(context.Background(), screen.Point{X: 10, Y: 10}, driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeNotificationCaptureFailsClosed(t *testing.T) {
	base := "calculator 0 100 7 1 1 0 10 0 [ 0 0 100 200 ]\nFocus window: 7\n"
	if err := safeWindow(base, "7"); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{"SCBBannerNotificatio 0 10 14 2106 1 0 2000 0", "SCBDropdownPanel 0 10 21 2109 1 0 2001 0"} {
		if err := safeWindow(base+extra, "7"); err == nil {
			t.Fatal("notification capture allowed")
		}
	}
	if err := safeWindow("SCBGlobalSearch 0 10 7 2125 1 0 50 0", "7"); err == nil {
		t.Fatal("unknown system panel allowed")
	}
}

func TestMainAbilityRequiresPublicEntry(t *testing.T) {
	out := []byte(`{"name":"com.test.app","hapModuleInfos":[{"name":"entry","mainElementName":"com.test.app.MainAbility","abilityInfos":[{"name":"Background","visible":true},{"name":"MainAbility","visible":true}]}]}`)
	m, a, err := mainAbility(out)
	if err != nil || m != "entry" || a != "MainAbility" {
		t.Fatalf("entry=%q %q %v", m, a, err)
	}
	if _, _, err := mainAbility([]byte(strings.Replace(string(out), `"name":"MainAbility","visible":true`, `"name":"MainAbility","visible":false`, 1))); err == nil {
		t.Fatal("private entry allowed")
	}
}

func TestNativeProviderOpensConnectedDevice(t *testing.T) {
	p := NewProvider()
	p.find = func() (string, error) { return "hdc", nil }
	p.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "list" {
			return []byte("test-native USB Connected localhost\n"), nil
		}
		return []byte("TestModel\nOpenHarmony-7.0\n7.0.0.1\n"), nil
	}
	infos, err := p.List(context.Background())
	if err != nil || len(infos) != 1 {
		t.Fatalf("list: %+v %v", infos, err)
	}
	if infos[0].State != driver.StateOnline || infos[0].Name != "Huawei TestModel" {
		t.Fatalf("info=%+v", infos[0])
	}
	d, err := p.Open(context.Background(), "test-native")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Info().Backend != "hdc" {
		t.Fatal("wrong backend")
	}
}

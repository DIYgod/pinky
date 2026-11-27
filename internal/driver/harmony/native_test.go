package harmony

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

const unlockedState = "* deviceLocked false 100\n* screenLocked false 100\n"

func TestPreviewKeepsNativeCoordinatesAndRejectsRotation(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 80, 1600))
	for y := range 1600 {
		for x := range 80 {
			c := color.RGBA{R: 255, A: 255}
			if y >= 800 {
				c = color.RGBA{B: 255, A: 255}
			}
			src.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	for _, rotated := range []bool{false, true} {
		d := &Device{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			cmd := args[len(args)-1]
			if strings.Contains(cmd, "'ScreenlockService'") {
				return []byte(unlockedState + "calculator 0 100 7 1 1 0 10 0\nFocus window: 7\n"), nil
			}
			if strings.Contains(cmd, "'snapshot_display'") {
				if !strings.Contains(cmd, "'-w' '80' '-h' '1600'") {
					t.Fatal("preview was not scaled on the phone")
				}
				header := "process: display 0, file type: jpeg, width: 100, height: 2000\n"
				if rotated {
					header = "process: display 0, file type: jpeg, width: 2000, height: 100\n"
				}
				return []byte(header + "success: snapshot display\n__PINKY_JPEG__\n" + base64.StdEncoding.EncodeToString(buf.Bytes())), nil
			}
			if strings.HasPrefix(cmd, "'rm' ") {
				return nil, nil
			}
			t.Fatalf("unexpected command: %s", cmd)
			return nil, nil
		}}
		img, err := d.capturePreview(context.Background(), image.Pt(100, 2000))
		if rotated {
			if !errors.Is(err, errDisplayChanged) || img != nil {
				t.Fatalf("rotation accepted: %v", err)
			}
			continue
		}
		if err != nil || img.Bounds().Size() != image.Pt(100, 2000) {
			t.Fatalf("native coordinates changed: %v", err)
		}
		r, _, b, _ := img.At(50, 500).RGBA()
		if r < 60000 || b > 5000 {
			t.Fatal("top of image mapped incorrectly")
		}
		r, _, b, _ = img.At(50, 1500).RGBA()
		if b < 60000 || r > 5000 {
			t.Fatal("bottom of image mapped incorrectly")
		}
	}
}

func TestInputPreemptsPreview(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	d := &Device{previewCancel: cancel, run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if strings.Contains(args[len(args)-1], "'uiInput'") {
			return []byte("No Error"), nil
		}
		return []byte(unlockedState), nil
	}}
	d.mu.Lock() // The mirror is in a slow capture.
	go func() { <-ctx.Done(); d.mu.Unlock() }()
	start := time.Now()
	if err := d.Tap(context.Background(), screen.Point{X: 10, Y: 10}, driver.TapOptions{}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("input waited for the preview instead of interrupting it")
	}
	if d.inputs != 0 {
		t.Fatal("input priority was not released")
	}
}

func TestJPEGTransportAndCleanup(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 8, 16)), nil); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []bool{false, true} {
		trapped, fallback, captured := false, false, false
		d := &Device{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			cmd := args[len(args)-1]
			switch {
			case strings.Contains(cmd, "'snapshot_display' "):
				trapped = strings.HasPrefix(cmd, "'trap' ") && strings.Contains(cmd, "rm") && strings.Contains(cmd, "'EXIT'")
				captured = strings.Contains(cmd, "'-t' 'jpeg'") && strings.Contains(cmd, "'base64' ")
				if corrupt {
					return []byte("success: snapshot display\n__PINKY_JPEG__\ninvalid!"), nil
				}
				value := base64.StdEncoding.EncodeToString(encoded.Bytes())
				return []byte("success: snapshot display\r\n__PINKY_JPEG__\r\n" + value[:40] + "\r\n" + value[40:] + "\r\n"), nil
			case strings.HasPrefix(cmd, "'rm' '-f' "):
				fallback = true
				return nil, nil
			default:
				t.Fatalf("unexpected command: %s", cmd)
				return nil, nil
			}
		}}
		raw, err := d.tempOutput(context.Background(), "snapshot_display", ".jpeg")
		if !captured || !trapped || fallback != corrupt {
			t.Fatalf("capture=%v trap=%v fallback=%v", captured, trapped, fallback)
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

func TestBatchedCaptureGuards(t *testing.T) {
	window := "calculator 0 100 7 1 1 0 10 0\nFocus window: 7\n"
	good := unlockedState + window
	for _, tc := range []struct {
		name, before, after string
		captured            bool
	}{
		{"locked before", strings.Replace(good, "deviceLocked false", "deviceLocked true", 1), good, false},
		{"unknown before", window, good, false},
		{"locked after", good, strings.Replace(good, "screenLocked false", "screenLocked true", 1), true},
		{"unknown after", good, window, true},
		{"changed window", good, strings.ReplaceAll(good, "7", "8"), true},
		{"notification after", good, good + "SCBBannerNotificatio 0 10 14 2106 1 0 2000 0\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guards, captured := 0, false
			d := &Device{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				cmd := args[len(args)-1]
				if strings.Contains(cmd, "'ScreenlockService'") {
					guards++
					if !strings.Contains(cmd, "'WindowManagerService'") {
						t.Fatal("missing window guard")
					}
					if guards == 1 {
						return []byte(tc.before), nil
					}
					return []byte(tc.after), nil
				}
				if strings.Contains(cmd, "'snapshot_display'") {
					captured = true
					return []byte("success: snapshot display\n__PINKY_JPEG__\nAA=="), nil
				}
				t.Fatalf("unexpected command: %s", cmd)
				return nil, nil
			}}
			img, err := d.Screenshot(context.Background())
			if err == nil || img != nil || captured != tc.captured {
				t.Fatalf("image=%v error=%v captured=%v", img, err, captured)
			}
		})
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

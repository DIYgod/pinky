//go:build device

package ios

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/danielpaulus/go-ios/ios/nskeyedarchiver"
)

// TestAXProbeDump dumps what the inspector sends for the first elements of
// the app in front (no actions). Run alone with PINKY_AXPROBE=dump and
// PINKY_AXAPP=<bundle id>.
func TestAXProbeDump(t *testing.T) {
	if os.Getenv("PINKY_AXPROBE") != "dump" {
		t.Skip("PINKY_AXPROBE=dump not set")
	}
	id := os.Getenv("PINKY_DEVICE")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	l, err := dialLink(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var names []string
	for name := range l.rsd.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	out := "/tmp/pinky-axprobe"
	os.MkdirAll(out, 0o700)
	os.WriteFile(filepath.Join(out, "rsd.txt"), []byte(joinLines(names)), 0o600)

	d := &Device{l: l}
	if app := os.Getenv("PINKY_AXAPP"); app != "" {
		if err := d.Launch(ctx, app); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
	}
	ax, err := dialAX(ctx, l.base)
	if err != nil {
		t.Fatal(err)
	}
	defer ax.Close()

	ax.drain(ctx)
	ax.ch.MethodCallAsync("deviceInspectorFocusOnElement:", nskeyedarchiver.NewNSNull())
	msg, err := ax.recvFocus(ctx, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dump(t, filepath.Join(out, "focus0.json"), unarchiveArg(msg))
	ax.ch.MethodCallAsync("deviceInspectorMoveWithOptions:", moveOptions(axFirst))
	for i := 1; i <= 6; i++ {
		msg, err := ax.recvFocus(ctx, 3*time.Second)
		if err != nil {
			t.Logf("move %d: %v", i, err)
			break
		}
		v := unarchiveArg(msg)
		n, _ := parseFocus(msg)
		t.Logf("element %d: caption %q ident %q handle %x", i, n.caption, n.ident, n.handle)
		dump(t, filepath.Join(out, "focus"+string(rune('0'+i))+".json"), v)
		ax.ch.MethodCallAsync("deviceInspectorMoveWithOptions:", moveOptions(axNext))
	}
	ax.ch.MethodCallAsync("deviceInspectorPreviewOnElement:", nskeyedarchiver.NewNSNull())
	nodes, complete, err := ax.walk(ctx, observeLimits)
	t.Logf("walk: %d nodes, complete %v, err %v", len(nodes), complete, err)
	for i, n := range nodes {
		t.Logf("%2d %q ident %q pid %d", i, n.caption, n.ident, handlePID(n.handle))
	}
}

func joinLines(s []string) string {
	b := ""
	for _, x := range s {
		b += x + "\n"
	}
	return b
}

// dump writes v as JSON, with byte slices as base64.
func dump(t *testing.T, path string, v any) {
	b, err := json.MarshalIndent(jsonable(v), "", " ")
	if err != nil {
		t.Logf("dump %s: %v", path, err)
		return
	}
	os.WriteFile(path, b, 0o600)
}

func jsonable(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, e := range x {
			m[k] = jsonable(e)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = jsonable(e)
		}
		return out
	case []byte:
		return "b64:" + base64.StdEncoding.EncodeToString(x)
	default:
		return x
	}
}

// TestAXProbeActivate tries the inspector's actions, the only input the
// path below iOS 27 has without the helper: Activate on a Calculator key
// and its Clear key, then Activate on Settings' search field and setting
// its value. It never presses anything but those: no switches. Run alone
// with PINKY_AXPROBE=act; it leaves the phone on its home screen.
func TestAXProbeActivate(t *testing.T) {
	if os.Getenv("PINKY_AXPROBE") != "act" {
		t.Skip("PINKY_AXPROBE=act not set")
	}
	id := os.Getenv("PINKY_DEVICE")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	l, err := dialLink(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	d := &Device{l: l}
	ax, err := dialAX(ctx, l.base)
	if err != nil {
		t.Fatal(err)
	}
	defer ax.Close()
	walk := func(what string) []axNode {
		t0 := time.Now()
		nodes, complete, err := ax.walk(ctx, observeLimits)
		t.Logf("%s: walk %v, %d nodes, complete %v, err %v", what, time.Since(t0).Round(time.Millisecond), len(nodes), complete, err)
		for i, n := range nodes {
			t.Logf("  %2d %q ident %q", i, n.caption, n.ident)
		}
		return nodes
	}
	find := func(nodes []axNode, ok func(cp captionParts, n axNode) bool) (axNode, bool) {
		for _, n := range nodes {
			if ok(parseCaption(n.caption), n) {
				return n, true
			}
		}
		return axNode{}, false
	}
	// Calculator's keys may read as just their label; nothing pressed here
	// is ever a switch.
	isButton := func(cp captionParts) bool { return !has(cp.traits, "toggle") && !has(cp.traits, "switch") }

	// Calculator: 7, then Clear.
	if err := d.Launch(ctx, "com.apple.calculator"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	nodes := walk("Calculator")
	seven, ok := find(nodes, func(cp captionParts, n axNode) bool { return cp.first == "7" && isButton(cp) })
	if !ok {
		t.Fatal("no 7 key")
	}
	t0 := time.Now()
	err = ax.activate(ctx, seven.handle)
	t.Logf("activate 7: %v in %v", err, time.Since(t0).Round(time.Millisecond))
	time.Sleep(time.Second)
	nodes = walk("Calculator after 7")
	if clr, ok := find(nodes, func(cp captionParts, n axNode) bool {
		f := strings.ToLower(cp.first)
		return (f == "clear" || f == "all clear" || f == "delete") && isButton(cp)
	}); ok {
		err = ax.activate(ctx, clr.handle)
		t.Logf("activate %q: %v", clr.caption, err)
		time.Sleep(time.Second)
		walk("Calculator after clear")
	}

	// Settings: the search field, then its value.
	if err := d.Launch(ctx, "com.apple.Preferences"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	nodes = walk("Settings")
	field, ok := find(nodes, func(cp captionParts, n axNode) bool { return cp.first == "Search" && has(cp.traits, "search field") })
	if !ok {
		t.Log("no search field; skipping")
		return
	}
	err = ax.activate(ctx, field.handle)
	t.Logf("activate the search field: %v", err)
	time.Sleep(1500 * time.Millisecond)
	for _, attr := range []string{"Value", "AXValue"} {
		c, cc := context.WithTimeout(ctx, 3*time.Second)
		_, err := ax.ch.MethodCallWithContext(c, "deviceElement:setValue:attribute:", elementArg(field.handle), "clock", attributeArg(attr))
		cc()
		t.Logf("setValue %q: %v", attr, err)
		time.Sleep(time.Second)
		nodes = walk("Settings after setValue " + attr)
	}
	if cancelBtn, ok := find(nodes, func(cp captionParts, n axNode) bool {
		return (cp.first == "Cancel" || cp.first == "close") && isButton(cp)
	}); ok {
		t.Logf("activate %q: %v", cancelBtn.caption, ax.activate(ctx, cancelBtn.handle))
		time.Sleep(time.Second)
	}
	// Home through the iOS 27 path, as every device test ends.
	full, err := Open(ctx, driver.Info{ID: id, Platform: driver.IOS})
	if err == nil {
		full.Key(ctx, driver.KeyHome)
		time.Sleep(time.Second)
		full.Close()
	}
}

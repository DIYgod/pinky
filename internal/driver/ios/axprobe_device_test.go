//go:build device

package ios

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

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

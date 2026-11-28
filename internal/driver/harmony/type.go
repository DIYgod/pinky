package harmony

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/DIYgod/pinky/internal/driver"
)

// ArkUI's native RichEditor is a rich-text composer. Its input configuration
// uses UNSPECIFIED. Ordinary TextInput nodes can be password or OTP fields,
// and are deliberately not accepted here.
var nativeLeaf = regexp.MustCompile(`^\|-> ([A-Za-z0-9_]+)\(Node\*\) id:([0-9]+)(?: idstr:[^[:space:]]+)? childSize:[0-9]+$`)

func richEditorID(dump, window, app string) string {
	var win, bundle, leaf string
	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "WinId:"):
			if win != "" {
				return ""
			}
			win = strings.TrimSpace(strings.TrimPrefix(line, "WinId:"))
		case strings.HasPrefix(line, "bundleName:"):
			if bundle != "" {
				return ""
			}
			bundle = strings.TrimSpace(strings.TrimPrefix(line, "bundleName:"))
		case strings.Contains(line, "(Node*)"):
			match := nativeLeaf.FindStringSubmatch(line)
			if leaf != "" || match == nil || match[1] != "RichEditor" {
				return ""
			}
			leaf = match[2]
		}
	}
	if app == "" || win != window || bundle != app {
		return ""
	}
	return leaf
}

func (d *Device) Type(ctx context.Context, text string, opt driver.TypeOptions) error {
	if opt.ForegroundID == "" || !opt.Append || opt.Submit || text == "" || strings.IndexFunc(text, unicode.IsControl) >= 0 {
		return fmt.Errorf("harmony: use append without submit in a focused native rich editor: %w", driver.ErrUnsupported)
	}
	done := d.preferInput()
	defer done()
	d.mu.Lock()
	defer d.mu.Unlock()
	win, err := d.screenState(ctx)
	if err != nil {
		return err
	}
	args := []string{"hidumper", "-s", "WindowManagerService", "-a", "-w " + win + " -focus"}
	dump, err := d.shell(ctx, args...)
	if err != nil {
		return err
	}
	id := richEditorID(string(dump), win, opt.ForegroundID)
	if id == "" {
		return fmt.Errorf("harmony: no verified native rich editor: %w", driver.ErrUnsupported)
	}
	// The foreground window and editor may change during the diagnostic.
	current, err := d.screenState(ctx)
	if err != nil {
		return err
	}
	if current != win {
		return fmt.Errorf("harmony: foreground changed before typing")
	}
	dump, err = d.shell(ctx, args...)
	if err != nil {
		return err
	}
	if richEditorID(string(dump), win, opt.ForegroundID) != id {
		return fmt.Errorf("harmony: editor changed before typing")
	}
	// A failing tool can echo its arguments. Keep the text out of errors.
	out, err := d.shell(ctx, "uitest", "uiInput", "text", text)
	if err != nil || (strings.TrimSpace(string(out)) != "" && !strings.EqualFold(strings.TrimSpace(string(out)), "No Error")) {
		return fmt.Errorf("harmony: text input outcome unknown; observe the draft before retrying")
	}
	return nil
}

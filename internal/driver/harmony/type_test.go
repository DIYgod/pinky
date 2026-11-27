package harmony

import (
	"context"
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
)

func TestTypeBindsToFocusedRichEditor(t *testing.T) {
	const focus = "WinId: 7\nbundleName:example.chat\n |-> RichEditor(Node*) id:41 childSize:0"
	for _, test := range []struct {
		name, second string
		allowed      bool
	}{
		{"rich editor", focus, true},
		{"different app", strings.ReplaceAll(focus, "example.chat", "other"), false},
		{"password-capable field", strings.ReplaceAll(focus, "RichEditor", "TextInput"), false},
		{"focus moved", strings.ReplaceAll(focus, "id:41", "id:42"), false},
		{"different window", strings.ReplaceAll(focus, "WinId: 7", "WinId: 8"), false},
		{"ambiguous", focus + "\n |-> Button(Node*) id:44 childSize:0", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			reads, writes := 0, 0
			d := Device{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				cmd := args[len(args)-1]
				switch {
				case strings.Contains(cmd, "ScreenlockService"):
					return []byte(unlockedState + "app 0 1 7 1 1 0 10\nFocus window: 7"), nil
				case strings.Contains(cmd, "-focus"):
					reads++
					if reads == 1 {
						return []byte(focus), nil
					}
					return []byte(test.second), nil
				case strings.Contains(cmd, "'uiInput' 'text'"):
					writes++
					return []byte("No Error"), nil
				}
				t.Fatal(cmd)
				return nil, nil
			}}
			err := d.Type(context.Background(), "测试", driver.TypeOptions{ForegroundID: "example.chat", Append: true})
			if (err == nil) != test.allowed || (writes == 1) != test.allowed {
				t.Fatalf("err=%v writes=%d", err, writes)
			}
		})
	}
}

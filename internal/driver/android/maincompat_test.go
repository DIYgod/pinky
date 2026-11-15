package android

import (
	"context"
	"errors"
	"testing"

	"github.com/DIYgod/pinky/internal/driver"
)

func TestWeChatMainDisplayCompatibility(t *testing.T) {
	for _, tt := range []struct {
		model, pkg string
		sdk        int
		blocked    bool
	}{
		{"Mi Note 3", "com.tencent.mm", 35, true},
		{"Mi Note 3", "com.example.reader", 35, false},
		{"Pixel", "com.tencent.mm", 35, false},
		{"Mi Note 3", "com.tencent.mm", 34, false},
	} {
		b := &Background{d: &Device{props: deviceProps{Model: tt.model, SDK: tt.sdk}}}
		called := false
		err := b.startWith(context.Background(), launchTarget{pkg: tt.pkg}, func() error { called = true; return nil })
		if tt.blocked {
			if called || !errors.Is(err, driver.ErrUnsupported) {
				t.Fatalf("moved incompatible app before fallback: %v", err)
			}
		} else if !called || err != nil {
			t.Fatalf("changed another app/phone: %v", err)
		}
	}
}

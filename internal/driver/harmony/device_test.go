//go:build device

package harmony

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// This test reads connection metadata only; it never reads or controls the
// screen, installs apps, or accepts a debugging authorization prompt.
func TestDeviceDiscovery(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("set PINKY_DEVICE; run through scripts/devlock.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	infos, err := NewProvider().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range infos {
		if info.ID == id {
			if info.Platform != driver.HarmonyOS || info.Backend != "hdc" {
				t.Fatalf("info = %+v", info)
			}
			t.Logf("platform=%s connection=%s state=%s setup=%s", info.Platform, info.Connection, info.State, info.Setup)
			return
		}
	}
	t.Fatal("connected test phone was not discovered")
}

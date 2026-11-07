//go:build device

package android

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// TestDeviceBackgroundScreenOff opens a background display while the
// phone's own screen is off, in H.265 and in H.264, scrolls Settings on it
// and logs the frames that arrive. Let the screen time out first.
func TestDeviceBackgroundScreenOff(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	d, err := NewProvider(Config{Logf: t.Logf}).OpenDevice(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	on, err := d.ScreenOn(ctx)
	t.Logf("the phone's screen is on: %v (%v)", on, err)
	for _, hevc := range []bool{true, false} {
		d.mu.Lock()
		d.noHEVC = !hevc
		d.mu.Unlock()
		b, err := d.OpenDisplay(ctx, DisplayOptions{App: "com.android.settings"})
		if err != nil {
			t.Errorf("hevc %v: %v", hevc, err)
			continue
		}
		if lock := os.Getenv("PINKY_BG_WAKELOCK"); lock != "" {
			out, err := d.client.Shell(ctx, d.info.ID, fmt.Sprintf("cmd power set-wakelock acquire -d %d %s", b.DisplayID(), lock))
			t.Logf("hevc %v: wakelock %s on display %d: %q %v", hevc, lock, b.DisplayID(), strings.TrimSpace(string(out)), err)
			defer d.client.Shell(context.Background(), d.info.ID, fmt.Sprintf("cmd power set-wakelock release -d %d %s", b.DisplayID(), lock))
		}
		st, err := b.Stream(ctx, driverStreamOptions())
		if err != nil {
			b.Close()
			t.Errorf("hevc %v: stream: %v", hevc, err)
			continue
		}
		if os.Getenv("PINKY_BG_DUMP") != "" {
			time.Sleep(time.Second)
			out, _ := d.client.Shell(ctx, d.info.ID, "dumpsys power | grep -E 'groupId:|mWakefulness=|mIsDefaultGroupAdjacent|powerGroupId=[1-9]|lastUserActivityTime=' | cut -c1-160; dumpsys display | grep -E 'displayId "+fmt.Sprint(b.DisplayID())+",|mDisplayGroupId=[1-9]|Display "+fmt.Sprint(b.DisplayID())+":|mState=' | cut -c1-200")
			t.Logf("hevc %v: power and display state:\n%s", hevc, out)
		}
		frames, bytes := 0, 0
		done := make(chan struct{})
		go func() {
			defer close(done)
			for p := range st.Packets() {
				if !p.Config {
					frames++
				}
				bytes += len(p.Data)
			}
		}()
		for i := 0; i < 4; i++ {
			from, to := screen.Point{X: 540, Y: 1700}, screen.Point{X: 540, Y: 700}
			if i%2 == 1 {
				from, to = to, from
			}
			b.Swipe(ctx, from, to, 300*time.Millisecond)
			time.Sleep(700 * time.Millisecond)
		}
		s, oerr := b.Observe(ctx, driverObserveOptions())
		elements := 0
		if s != nil {
			elements = len(s.Elements)
		}
		st.Close()
		<-done
		on, _ := d.ScreenOn(ctx)
		t.Logf("hevc %v: %d frames, %d KB; the display read %d elements (%v); the phone's screen on: %v", hevc, frames, bytes/1024, elements, oerr, on)
		b.Close()
	}
}

func driverStreamOptions() driver.StreamOptions   { return driver.StreamOptions{} }
func driverObserveOptions() driver.ObserveOptions { return driver.ObserveOptions{} }

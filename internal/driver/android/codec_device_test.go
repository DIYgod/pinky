//go:build device

package android

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestDeviceCodecs streams the main display in H.264 and in H.265 for a few
// seconds each, at the given bit rate, and logs what arrives.
func TestDeviceCodecs(t *testing.T) {
	id := os.Getenv("PINKY_DEVICE")
	if id == "" {
		t.Skip("PINKY_DEVICE not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	d, err := NewProvider(Config{Logf: t.Logf}).OpenDevice(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Wake(ctx); err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{"h264", "h265"} {
		opt := d.baseOptions()
		opt.VideoCodec = codec
		if codec == "h264" {
			opt.VideoCodec = ""
		}
		l, err := d.sessionWith(ctx, &opt)
		if err != nil {
			t.Errorf("%s: %v", codec, err)
			continue
		}
		st, err := l.attach(ctx)
		if err != nil {
			t.Errorf("%s: attach: %v", codec, err)
			continue
		}
		var frames, keys, bytes int
		deadline := time.After(4 * time.Second)
	loop:
		for {
			select {
			case p, ok := <-st.Packets():
				if !ok {
					break loop
				}
				if !p.Config {
					frames++
				}
				if p.KeyFrame {
					keys++
				}
				bytes += len(p.Data)
			case <-deadline:
				break loop
			}
		}
		w, h := st.Size()
		t.Logf("%s at %d bit/s, %.0f fps max: %dx%d, %d frames (%d key), %d KB in 4 s", codec, opt.VideoBitRate, opt.MaxFPS, w, h, frames, keys, bytes/1024)
		st.Close()
	}
}

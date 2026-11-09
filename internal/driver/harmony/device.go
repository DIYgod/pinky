package harmony

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/ocr"
	"github.com/DIYgod/pinky/internal/screen"
)

type Device struct {
	info    driver.Info
	path    string
	run     func(context.Context, string, ...string) ([]byte, error)
	mu      sync.Mutex // UITest is single-session; serialize reads, input and screenshots.
	closed  bool
	labels  map[string]string
	apps    []driver.App
	appsAt  time.Time
	cancel  context.CancelFunc
	touchMu sync.Mutex
	down    screen.Point
	downAt  time.Time
}

func (d *Device) Info() driver.Info { return d.info }

func (d *Device) shell(ctx context.Context, args ...string) ([]byte, error) {
	if d.closed {
		return nil, driver.ErrOffline
	}
	return d.run(ctx, d.path, "-t", d.info.ID, "shell", shellCommand(args...))
}

func (d *Device) locked(ctx context.Context) (bool, error) {
	out, err := d.shell(ctx, "hidumper", "-s", "ScreenlockService", "-a", "-all")
	if err != nil {
		return false, err
	}
	return parseLock(string(out))
}

func (d *Device) Locked(ctx context.Context) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.locked(ctx)
}

func (d *Device) requireUnlocked(ctx context.Context) error {
	locked, err := d.locked(ctx)
	if err != nil {
		return err
	}
	if locked {
		return driver.ErrLocked
	}
	return nil
}

// tempOutput reads an explicitly named UITest output and removes it before
// returning. Images are never written to the host filesystem or cached.
func (d *Device) tempOutput(ctx context.Context, tool, ext string, extra ...string) ([]byte, error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	remote := "/data/local/tmp/pinky-" + hex.EncodeToString(nonce[:]) + ext
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = d.shell(clean, "rm", "-f", remote)
	}()
	args := append([]string{"uitest", tool, "-p", remote}, extra...)
	if tool == "snapshot_display" {
		args = []string{tool, "-f", remote, "-t", "jpeg"}
	}
	out, err := d.shell(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(out, []byte("saved to")) && !(tool == "snapshot_display" && bytes.Contains(out, []byte("success: snapshot display"))) {
		return nil, fmt.Errorf("harmony: %s did not produce output: %s", tool, strings.TrimSpace(string(out)))
	}
	if ext == ".png" || ext == ".jpeg" {
		encoded, err := d.shell(ctx, "base64", remote)
		if err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(encoded)), ""))
	}
	return d.shell(ctx, "cat", remote)
}

func (d *Device) tree(ctx context.Context) (*screen.Screen, error) {
	locked, err := d.locked(ctx)
	if err != nil {
		return nil, err
	}
	if locked {
		return &screen.Screen{Locked: true, CapturedAt: time.Now()}, nil
	}
	out, err := d.shell(ctx, "hidumper", "-s", "WindowManagerService", "-a", "-a")
	if err != nil {
		return nil, err
	}
	focus, err := focusedWindow(string(out))
	if err != nil {
		return nil, err
	}
	if err := safeWindow(string(out), focus); err != nil {
		return nil, err
	}
	raw, err := d.tempOutput(ctx, "dumpLayout", ".json", "-w", focus)
	if err != nil {
		return nil, err
	}
	s, err := parseLayout(raw, focus)
	if err != nil {
		return nil, err
	}
	if err := d.requireUnlocked(ctx); err != nil {
		return nil, err
	}
	s.App.Name = d.label(ctx, s.App.ID)
	return s, nil
}

func (d *Device) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, err := d.tree(ctx)
	if err != nil || s.Locked {
		return s, err
	}
	missing := false
	for _, e := range s.Elements {
		if e.Role == screen.RoleButton && e.Label == "" {
			missing = true
			break
		}
	}
	if (opt.OCR || missing) && ocr.Supported() == nil && (opt.MayCapture == nil || opt.MayCapture(s)) {
		img, err := d.capture(ctx)
		if err != nil {
			return nil, err
		}
		if img.Bounds().Dx() != s.Width || img.Bounds().Dy() != s.Height {
			return nil, fmt.Errorf("harmony: screenshot coordinates changed")
		}
		lines, err := ocr.RecognizeImage(img, ocr.Options{Languages: ocr.Languages})
		if err != nil {
			return nil, err
		}
		after, err := d.tree(ctx)
		if err != nil {
			return nil, err
		}
		if after.Locked || after.App.ID != s.App.ID {
			return nil, fmt.Errorf("harmony: screen changed during OCR")
		}
		s.Elements = append(s.Elements, ocr.Extras(s.Elements, lines, s.Width, s.Height)...)
	}
	return s, nil
}

func (d *Device) capture(ctx context.Context) (image.Image, error) {
	if err := d.requireUnlocked(ctx); err != nil {
		return nil, err
	}
	before, err := d.shell(ctx, "hidumper", "-s", "WindowManagerService", "-a", "-a")
	if err != nil {
		return nil, err
	}
	focus, err := focusedWindow(string(before))
	if err != nil {
		return nil, err
	}
	if err := safeWindow(string(before), focus); err != nil {
		return nil, err
	}
	// JPEG keeps a busy home screen small enough for HDC's shell channel.
	// Raw binary shell output is unsafe: HDC transforms control bytes, so
	// even compressed images are transported as base64 and decoded here.
	raw, err := d.tempOutput(ctx, "snapshot_display", ".jpeg")
	if err != nil {
		return nil, err
	}
	if err := d.requireUnlocked(ctx); err != nil {
		return nil, err
	}
	after, err := d.shell(ctx, "hidumper", "-s", "WindowManagerService", "-a", "-a")
	if err != nil {
		return nil, err
	}
	focusAfter, err := focusedWindow(string(after))
	if err != nil {
		return nil, err
	}
	if focusAfter != focus {
		return nil, fmt.Errorf("harmony: window changed during screenshot")
	}
	if err := safeWindow(string(after), focusAfter); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}

func (d *Device) Screenshot(ctx context.Context) (image.Image, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.capture(ctx)
}
func (*Device) ReadsText() bool { return ocr.Supported() == nil }

func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	return nil
}

var _ driver.Device = (*Device)(nil)
var _ driver.LockReporter = (*Device)(nil)
var _ driver.TextReader = (*Device)(nil)

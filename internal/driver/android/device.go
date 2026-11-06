package android

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/driver/android/scrcpy"
	"github.com/DIYgod/pinky/internal/screen"
)

// Device is an open Android phone. It keeps one scrcpy session for the
// main display, started on first use and restarted if it dies, which
// carries input, the video stream and settle detection.
type Device struct {
	cfg    Config
	client *adb.Client
	info   driver.Info
	props  deviceProps

	mu     sync.Mutex
	main   *live
	closed bool

	dumpMu   sync.Mutex // uiautomator allows one dump at a time
	dumpFile bool       // this phone cannot dump to /dev/tty

	stateMu   sync.Mutex
	natW      int // display size in the natural orientation, from `wm size`
	natH      int
	sizeAt    time.Time
	awakeAt   time.Time // when the screen was last seen on
	longPress time.Duration
	imeKeys   bool      // ASCII may be typed as key events
	imeAt     time.Time // when imeKeys was decided

	appsMu sync.Mutex
	apps   []driver.App
	appsAt time.Time
}

func newDevice(cfg Config, info driver.Info, props deviceProps) *Device {
	return &Device{cfg: cfg, client: cfg.Client, info: info, props: props}
}

// Info implements driver.Device.
func (d *Device) Info() driver.Info { return d.info }

// Serial returns the adb serial.
func (d *Device) Serial() string { return d.info.ID }

// Close implements driver.Device. It ends the scrcpy session; the server
// exits and removes itself from the phone.
func (d *Device) Close() error {
	d.mu.Lock()
	d.closed = true
	l := d.main
	d.main = nil
	d.mu.Unlock()
	if l != nil {
		l.close(errors.New("android: device closed"))
	}
	return nil
}

// baseOptions are the session options when no stream asked for others.
func (d *Device) baseOptions() scrcpy.Options {
	return scrcpy.Options{
		MaxSize:      d.cfg.MaxSize,
		MaxFPS:       float64(d.cfg.MaxFPS),
		VideoBitRate: d.cfg.VideoBitRate,
		StayAwake:    d.cfg.StayAwake,
	}
}

// session returns the running main-display session, starting one if
// needed.
func (d *Device) session(ctx context.Context) (*live, error) {
	return d.sessionWith(ctx, nil)
}

// sessionWith returns a running session; with want set, one started with
// exactly those options, restarting the current one if it differs.
func (d *Device) sessionWith(ctx context.Context, want *scrcpy.Options) (*live, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errors.New("android: device closed")
	}
	if l := d.main; l != nil && !l.ended() {
		if want == nil || sameVideo(l.opt, *want) {
			return l, nil
		}
		d.cfg.logf("android: %s: restarting the session for new stream options", d.info.ID)
		l.close(errors.New("android: session restarted with new options"))
	}
	opt := d.baseOptions()
	if want != nil {
		opt = *want
	}
	natW, natH, err := d.naturalSize(ctx)
	if err != nil {
		return nil, err
	}
	l, err := startLive(ctx, &d.cfg, d.client, d.info.ID, opt, func(vw, vh int) (int, int) {
		return orient(natW, natH, vw, vh)
	})
	if err != nil {
		return nil, wrapErr(err)
	}
	d.main = l
	return l, nil
}

func sameVideo(a, b scrcpy.Options) bool {
	return a.MaxSize == b.MaxSize && a.MaxFPS == b.MaxFPS && a.VideoBitRate == b.VideoBitRate &&
		a.VideoCodec == b.VideoCodec && a.StayAwake == b.StayAwake
}

// naturalSize returns the display size in the natural orientation,
// cached for a minute.
func (d *Device) naturalSize(ctx context.Context) (int, int, error) {
	d.stateMu.Lock()
	if d.natW > 0 && time.Since(d.sizeAt) < time.Minute {
		w, h := d.natW, d.natH
		d.stateMu.Unlock()
		return w, h, nil
	}
	d.stateMu.Unlock()
	out, err := d.client.Shell(ctx, d.info.ID, "wm size")
	if err != nil {
		return 0, 0, wrapErr(err)
	}
	w, h, ok := parseWMSize(string(out))
	if !ok {
		return 0, 0, fmt.Errorf("android: cannot read the display size from %q", out)
	}
	d.stateMu.Lock()
	d.natW, d.natH, d.sizeAt = w, h, time.Now()
	d.stateMu.Unlock()
	return w, h, nil
}

// live is one running scrcpy session and the goroutine that reads its
// video.
type live struct {
	cfg    *Config
	s      *scrcpy.Session
	opt    scrcpy.Options
	codec  driver.Codec
	serial string
	// screenSize maps the video size to the size of the screen that
	// agents' coordinates use.
	screenSize func(vw, vh int) (int, int)

	mu        sync.Mutex
	vw, vh    int
	sized     chan struct{} // closed at the first session header
	sizedOnce sync.Once
	sub       *stream

	settle settleTracker
	stats  streamStats

	done chan struct{}
	once sync.Once
	err  error
}

func startLive(ctx context.Context, cfg *Config, c *adb.Client, serial string, opt scrcpy.Options, screenSize func(vw, vh int) (int, int)) (*live, error) {
	start := time.Now()
	s, err := scrcpy.Start(ctx, c, serial, opt)
	if err != nil {
		return nil, err
	}
	l := &live{
		cfg: cfg, s: s, opt: opt, serial: serial, screenSize: screenSize,
		codec: driver.H264,
		sized: make(chan struct{}),
		done:  make(chan struct{}),
	}
	if s.Codec == scrcpy.CodecH265 {
		l.codec = driver.HEVC
	}
	l.settle.fixed = cfg.SettleBytes
	cfg.logf("android: %s: scrcpy session started in %v", serial, time.Since(start).Round(time.Millisecond))
	go l.run()
	return l, nil
}

func (l *live) run() {
	for {
		p, err := l.s.ReadPacket()
		if err != nil {
			l.close(err)
			return
		}
		now := time.Now()
		if p.Session {
			l.mu.Lock()
			l.vw, l.vh = p.Width, p.Height
			l.mu.Unlock()
			l.sizedOnce.Do(func() { close(l.sized) })
			l.settle.resize(now, p.Width, p.Height)
			continue
		}
		change := l.settle.packet(now, p.PTS, len(p.Data), p.Config, p.KeyFrame)
		l.stats.add(now, p.PTS, len(p.Data), p.Config, p.KeyFrame, change)
		l.mu.Lock()
		sub := l.sub
		l.mu.Unlock()
		if sub != nil {
			sub.push(p)
		}
	}
}

// close ends the session and any stream on it.
func (l *live) close(err error) {
	l.once.Do(func() {
		l.mu.Lock()
		l.err = err
		sub := l.sub
		l.sub = nil
		l.mu.Unlock()
		close(l.done)
		l.s.Close()
		if sub != nil {
			sub.end(fmt.Errorf("%w: %v", driver.ErrOffline, err))
		}
	})
}

func (l *live) ended() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

// failure is the error to return for a session that ended.
func (l *live) failure() error {
	l.mu.Lock()
	err := l.err
	l.mu.Unlock()
	if errors.Is(err, scrcpy.ErrClosed) {
		return err
	}
	return fmt.Errorf("%w: scrcpy session ended: %v", driver.ErrOffline, err)
}

// videoSize waits for the first session header and returns the current
// video size.
func (l *live) videoSize(ctx context.Context) (int, int, error) {
	select {
	case <-l.sized:
	case <-l.done:
		return 0, 0, l.failure()
	case <-ctx.Done():
		return 0, 0, ctx.Err()
	case <-time.After(5 * time.Second):
		return 0, 0, errors.New("android: no video from the scrcpy session")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.vw, l.vh, nil
}

// position maps a screen point to the video frame the server expects.
func (l *live) position(ctx context.Context, p screen.Point) (scrcpy.Position, error) {
	vw, vh, err := l.videoSize(ctx)
	if err != nil {
		return scrcpy.Position{}, err
	}
	sw, sh := l.screenSize(vw, vh)
	x, y := p.X, p.Y
	if sw > 0 && sh > 0 && (sw != vw || sh != vh) {
		x = (x*vw + sw/2) / sw
		y = (y*vh + sh/2) / sh
	}
	x = min(max(x, 0), vw-1)
	y = min(max(y, 0), vh-1)
	return scrcpy.Position{X: int32(x), Y: int32(y), Width: uint16(vw), Height: uint16(vh)}, nil
}

// send writes a control message, mapping failures to driver errors.
func (l *live) send(msg []byte) error {
	if err := l.s.Send(msg); err != nil {
		return l.failure()
	}
	return nil
}

var (
	_ driver.Device       = (*Device)(nil)
	_ driver.Toucher      = (*Device)(nil)
	_ driver.LockReporter = (*Device)(nil)
	_ driver.Clipboard    = (*Device)(nil)
	_ driver.Settler      = (*Device)(nil)
)

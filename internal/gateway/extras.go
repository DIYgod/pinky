package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// Client features beyond API: pairing phones over Wi-Fi, the Android
// companion app, background displays, screen recordings, and history
// retention and export. The client finds them with type assertions, so
// fakes and older gateways may leave them out.

// ErrNoPairing is returned when no provider can pair phones over Wi-Fi.
var ErrNoPairing = errors.New("no connected platform can pair phones over Wi-Fi")

// StartPairing begins pairing a phone over Wi-Fi by QR code, with the first
// provider that can (driver.Pairer). The phone shows up in Devices once
// Wait returns.
func (g *Gateway) StartPairing(ctx context.Context) (driver.Pairing, error) {
	for _, p := range g.opt.Providers {
		if pr, ok := p.(driver.Pairer); ok {
			pg, err := pr.StartPairing(ctx)
			if err != nil {
				return nil, err
			}
			return &discoverOnPair{Pairing: pg, g: g}, nil
		}
	}
	return nil, ErrNoPairing
}

// PairWithCode pairs a phone with the address and six-digit code it shows
// under Pair device with pairing code.
func (g *Gateway) PairWithCode(ctx context.Context, addr, code string) (driver.Info, error) {
	for _, p := range g.opt.Providers {
		if pr, ok := p.(driver.Pairer); ok {
			info, err := pr.PairWithCode(ctx, addr, code)
			if err == nil {
				g.discover(ctx)
			}
			return info, err
		}
	}
	return driver.Info{}, ErrNoPairing
}

// discoverOnPair looks for devices as soon as a pairing finishes, rather
// than at the next discovery tick.
type discoverOnPair struct {
	driver.Pairing
	g *Gateway
}

func (p *discoverOnPair) Wait(ctx context.Context) (driver.Info, error) {
	info, err := p.Pairing.Wait(ctx)
	if err == nil {
		p.g.discover(ctx)
	}
	return info, err
}

// CompanionState reports whether a phone's companion app is ready, or
// driver.ErrUnsupported for phones without one (iPhones).
func (g *Gateway) CompanionState(ctx context.Context, deviceID string) (driver.CompanionState, error) {
	ci, err := g.companionInstaller(deviceID)
	if err != nil {
		return "", err
	}
	return ci.CompanionState(ctx)
}

// InstallCompanion installs or updates a phone's companion app from the APK
// bundled with Pinky. Only the person asks for it, from the client.
func (g *Gateway) InstallCompanion(ctx context.Context, deviceID string) error {
	ci, err := g.companionInstaller(deviceID)
	if err != nil {
		return err
	}
	if err := ci.InstallCompanion(ctx); err != nil {
		return err
	}
	g.emit(Event{Kind: EventDevices, DeviceID: deviceID})
	return nil
}

func (g *Gateway) companionInstaller(deviceID string) (driver.CompanionInstaller, error) {
	d, err := g.device(deviceID)
	if err != nil {
		return nil, err
	}
	dev := g.driverOf(d)
	if dev == nil {
		return nil, driver.ErrOffline
	}
	ci, ok := dev.(driver.CompanionInstaller)
	if !ok {
		return nil, fmt.Errorf("%s has no companion app: %w", d.name(), driver.ErrUnsupported)
	}
	return ci, nil
}

// RecordingView is a task's screen recording.
type RecordingView struct {
	TaskID string
	Path   string // an MP4 file, in the codec the phone sent (H.264 or HEVC)
	Start  time.Time
	Length time.Duration
	Marks  []RecordingMark
}

// RecordingMark is a step's place in a recording.
type RecordingMark struct {
	At   time.Duration // from Start
	Step int           // StepView.Index
	Text string
}

// Recording returns a task's screen recording, or driver.ErrNotFound when
// it has none (recording was off, the phone was in a blocked app, or the
// retention limit removed it).
func (g *Gateway) Recording(ctx context.Context, taskID string) (RecordingView, error) {
	return RecordingView{}, fmt.Errorf("recordings: %w", driver.ErrNotFound)
}

// SetRecording turns screen recording of agent tasks on or off. Recording
// pauses while a blocked app is in front.
func (g *Gateway) SetRecording(on bool) {}

// SetRetention keeps finished tasks, their screenshots and recordings for
// days days and deletes older ones now and once a day; 0 keeps everything.
func (g *Gateway) SetRetention(days int) {}

// ExportHistory writes a self-contained HTML report and a JSON file of the
// tasks (all finished tasks when taskIDs is empty) into dir and returns the
// HTML file's path.
func (g *Gateway) ExportHistory(ctx context.Context, taskIDs []string, dir string) (string, error) {
	return "", fmt.Errorf("export: %w", driver.ErrUnsupported)
}

// SetBackground sets whether agents work on a phone's background display
// (driver.Backgrounder) instead of its main screen. It applies from the
// next task.
func (g *Gateway) SetBackground(deviceID string, on bool) error {
	return fmt.Errorf("background displays: %w", driver.ErrUnsupported)
}

// Background reports whether agents work on a phone's background display.
func (g *Gateway) Background(deviceID string) bool { return false }

// AgentStream returns the live video of the background display an agent is
// working on, or driver.ErrNotFound when none is open.
func (g *Gateway) AgentStream(ctx context.Context, deviceID string, opt driver.StreamOptions) (driver.Stream, error) {
	return nil, fmt.Errorf("background displays: %w", driver.ErrNotFound)
}

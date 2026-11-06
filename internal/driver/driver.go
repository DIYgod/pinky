// Package driver defines what a platform driver provides: device discovery,
// observation, input and a video stream. The gateway never branches on the
// platform; it only talks to these interfaces.
package driver

import (
	"context"
	"errors"
	"image"
	"time"

	"github.com/DIYgod/pinky/internal/screen"
)

// Platform is a phone platform.
type Platform string

// Platforms.
const (
	Android Platform = "android"
	IOS     Platform = "ios"
)

// State is a device's connection state.
type State string

// Device states.
const (
	StateOnline       State = "online"
	StateOffline      State = "offline"
	StateUnauthorized State = "unauthorized" // the phone has not trusted this computer yet
	StateNeedsSetup   State = "needs_setup"  // e.g. Developer Mode off; Detail says what to do
)

// Info describes a device.
type Info struct {
	ID         string // adb serial or iOS UDID; stable across reconnects
	Platform   Platform
	Name       string // "Pixel 9", or the name the owner gave the iPhone
	Model      string
	OSVersion  string
	Connection string // "usb" or "wifi"
	Backend    string // "scrcpy", "coredevice", "wda", "mirroring", "fake"
	State      State
	Detail     string // a plain sentence when State is not online
}

// Provider discovers and opens the devices of one platform.
type Provider interface {
	Platform() Platform
	List(ctx context.Context) ([]Info, error)
	Open(ctx context.Context, id string) (Device, error)
}

// Device is an open, long-lived session with one phone. Methods must be safe
// to call from several goroutines; implementations serialize as needed.
type Device interface {
	Info() Info
	// Observe reads the current screen. Refs are assigned by the gateway.
	Observe(ctx context.Context, opt ObserveOptions) (*screen.Screen, error)
	// Screenshot returns the screen in the same pixel space as Observe.
	Screenshot(ctx context.Context) (image.Image, error)
	Tap(ctx context.Context, p screen.Point, opt TapOptions) error
	Swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error
	// Type enters text into the focused field.
	Type(ctx context.Context, text string, opt TypeOptions) error
	Key(ctx context.Context, k Key) error
	// Launch starts an app by package name, bundle id or display name.
	Launch(ctx context.Context, app string) error
	Apps(ctx context.Context) ([]App, error)
	// Stream starts the live video stream. One stream per device at a time.
	Stream(ctx context.Context, opt StreamOptions) (Stream, error)
	Close() error
}

// ObserveOptions tune Observe.
type ObserveOptions struct {
	// OCR asks the driver to add OCR elements for areas the tree misses.
	OCR bool
}

// TapOptions tune Tap.
type TapOptions struct {
	LongPress bool
}

// TypeOptions tune Type.
type TypeOptions struct {
	Append bool // keep the field's current text
	Submit bool // press Enter afterwards
}

// App is an installed app.
type App struct {
	ID     string // package name or bundle id
	Name   string
	System bool
}

// Key is a hardware or navigation key.
type Key string

// Keys agents may press. Drivers return ErrUnsupported for keys their
// platform lacks (for example KeyRecents on iPhone).
const (
	KeyBack          Key = "back"
	KeyHome          Key = "home"
	KeyRecents       Key = "recents"
	KeyEnter         Key = "enter"
	KeyTab           Key = "tab"
	KeyDelete        Key = "delete"
	KeyForwardDelete Key = "forward_delete"
	KeyEscape        Key = "escape"
	KeyDpadUp        Key = "dpad_up"
	KeyDpadDown      Key = "dpad_down"
	KeyDpadLeft      Key = "dpad_left"
	KeyDpadRight     Key = "dpad_right"
	KeyPageUp        Key = "page_up"
	KeyPageDown      Key = "page_down"
)

// StreamOptions tune the video stream.
type StreamOptions struct {
	MaxSize int // longest side in pixels; 0 = native
	MaxFPS  int // 0 = device default
}

// Codec is a video codec.
type Codec string

// Codecs.
const (
	H264 Codec = "h264"
	HEVC Codec = "hevc"
)

// Packet is one encoded unit of the video stream.
type Packet struct {
	Data     []byte // Annex B
	PTS      time.Duration
	Config   bool // codec configuration (SPS/PPS/VPS)
	KeyFrame bool
}

// Stream is a live encoded video stream.
type Stream interface {
	Codec() Codec
	Size() (width, height int)
	Packets() <-chan Packet // closed when the stream ends; Err says why
	Err() error
	Close() error
}

// Optional capabilities. The gateway checks for them with type assertions.

// TouchAction is a raw touch phase, used for the person's input on the mirror.
type TouchAction string

// Touch actions.
const (
	TouchDown TouchAction = "down"
	TouchMove TouchAction = "move"
	TouchUp   TouchAction = "up"
)

// Toucher injects raw touches, for drags on the mirror.
type Toucher interface {
	Touch(ctx context.Context, a TouchAction, p screen.Point) error
}

// LockReporter reports whether the phone is locked.
type LockReporter interface {
	Locked(ctx context.Context) (bool, error)
}

// Clipboard reads and writes the phone's clipboard.
type Clipboard interface {
	SetClipboard(ctx context.Context, text string) error
	Clipboard(ctx context.Context) (string, error)
}

// Errors drivers return. Wrap them with fmt.Errorf("...: %w", err).
var (
	ErrUnsupported = errors.New("not supported by this device or backend")
	ErrOffline     = errors.New("device offline")
	ErrLocked      = errors.New("device locked")
	ErrNotFound    = errors.New("not found")
)

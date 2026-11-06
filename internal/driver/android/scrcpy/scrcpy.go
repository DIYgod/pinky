// Package scrcpy runs the bundled scrcpy server on an Android phone and
// speaks its protocol directly: key=value options at start, the video
// stream with its packet headers, control messages to the phone and device
// messages back. It does not use the scrcpy client.
//
// The protocol follows the server's sources at the bundled version:
// Options.java, DesktopConnection.java, Streamer.java,
// ControlMessageReader.java and DeviceMessageWriter.java.
package scrcpy

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

// Version is the version of the bundled server. The server refuses a
// client that announces any other version.
const Version = "5.0.1"

//go:embed server/scrcpy-server-v5.0.1
var serverJar []byte

// ServerJar returns the bundled server (a jar holding classes.dex).
func ServerJar() []byte { return serverJar }

// Video codec ids, as sent in the stream header.
const (
	CodecH264 uint32 = 0x68323634 // "h264"
	CodecH265 uint32 = 0x68323635 // "h265"
	CodecAV1  uint32 = 0x00617631 // "av1"
)

// CodecName returns the server's name for a codec id.
func CodecName(id uint32) string {
	switch id {
	case CodecH264:
		return "h264"
	case CodecH265:
		return "h265"
	case CodecAV1:
		return "av1"
	}
	return fmt.Sprintf("0x%08x", id)
}

// VirtualDisplay asks the server to create a new display instead of
// mirroring an existing one.
type VirtualDisplay struct {
	// Width, Height and DPI of the display; zero means the main display's.
	Width, Height, DPI int
	// KeepContent moves the display's apps to the main display when the
	// session ends, instead of closing them.
	KeepContent bool
	// NoSystemDecorations hides the status bar and launcher on the display.
	NoSystemDecorations bool
}

// Options configure one server run. The zero value mirrors the main
// display at native size, with control and without audio, and never
// syncs the clipboard on its own.
type Options struct {
	MaxSize      int     // longest video side in pixels; 0 = native
	MaxFPS       float64 // 0 = device default
	VideoBitRate int     // bits per second; 0 = server default (8 Mbps)
	VideoCodec   string  // "h264" (default), "h265" or "av1"
	VideoEncoder string  // a specific MediaCodec encoder name
	NoVideo      bool
	NoControl    bool

	DisplayID int             // display to mirror; 0 is the main display
	Virtual   *VirtualDisplay // create a new display instead

	StayAwake        bool   // keep the phone awake while plugged in
	ShowTouches      bool   // show touches on the phone
	PowerOn          bool   // wake the screen at start (scrcpy presses POWER; off by default)
	PowerOffOnClose  bool   // turn the screen off when the session ends
	DisplayIMEPolicy string // "local", "fallback" or "hide" (Android 10+)
	LogLevel         string // "verbose", "debug", "info" (default), "warn" or "error"

	// Extra holds raw key=value options for anything not covered above.
	Extra []string
}

// Args returns the server's command-line arguments for a run with the
// given session id: the version first, then key=value options.
func (o Options) Args(scid uint32) []string {
	level := o.LogLevel
	if level == "" {
		level = "info"
	}
	args := []string{
		Version,
		fmt.Sprintf("scid=%08x", scid&0x7fffffff),
		"log_level=" + level,
		"audio=false",
		"tunnel_forward=true",
		// Pinky reads the clipboard only when asked; never stream it.
		"clipboard_autosync=false",
	}
	if !o.PowerOn {
		// scrcpy would press POWER to wake a sleeping screen; Pinky wakes
		// the screen itself with WAKEUP, which can never turn it off.
		args = append(args, "power_on=false")
	}
	if o.NoVideo {
		args = append(args, "video=false")
	}
	if o.NoControl {
		args = append(args, "control=false")
	}
	if o.MaxSize > 0 {
		args = append(args, "max_size="+strconv.Itoa(o.MaxSize))
	}
	if o.MaxFPS > 0 {
		args = append(args, "max_fps="+strconv.FormatFloat(o.MaxFPS, 'f', -1, 32))
	}
	if o.VideoBitRate > 0 {
		args = append(args, "video_bit_rate="+strconv.Itoa(o.VideoBitRate))
	}
	if o.VideoCodec != "" && o.VideoCodec != "h264" {
		args = append(args, "video_codec="+o.VideoCodec)
	}
	if o.VideoEncoder != "" {
		args = append(args, "video_encoder="+o.VideoEncoder)
	}
	if v := o.Virtual; v != nil {
		spec := ""
		if v.Width > 0 && v.Height > 0 {
			spec = fmt.Sprintf("%dx%d", v.Width, v.Height)
		}
		if v.DPI > 0 {
			spec += "/" + strconv.Itoa(v.DPI)
		}
		args = append(args, "new_display="+spec)
		if v.KeepContent {
			args = append(args, "vd_destroy_content=false")
		}
		if v.NoSystemDecorations {
			args = append(args, "vd_system_decorations=false")
		}
	} else if o.DisplayID != 0 {
		args = append(args, "display_id="+strconv.Itoa(o.DisplayID))
	}
	if o.StayAwake {
		args = append(args, "stay_awake=true")
	}
	if o.ShowTouches {
		args = append(args, "show_touches=true")
	}
	if o.PowerOffOnClose {
		args = append(args, "power_off_on_close=true")
	}
	if o.DisplayIMEPolicy != "" {
		args = append(args, "display_ime_policy="+o.DisplayIMEPolicy)
	}
	for _, kv := range o.Extra {
		if strings.Contains(kv, "=") {
			args = append(args, kv)
		}
	}
	return args
}

// socketName is the abstract socket the server listens on for a session id.
func socketName(scid uint32) string {
	return fmt.Sprintf("scrcpy_%08x", scid&0x7fffffff)
}

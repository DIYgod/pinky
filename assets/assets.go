// Package assets holds Pinky's brand files: the app icons, the tray images
// and the logo the client shows. See BRAND.md.
package assets

import _ "embed"

// Logo is the 32-point logo, redrawn on the pixel grid, as an SVG.
//
//go:embed svg/pinky-32.svg
var Logo []byte

// TrayTemplate is the macOS menu bar image, black on transparent for the
// system to tint, at 36 × 36 pixels (18 points at 2×).
//
//go:embed tray/pinkyTemplate@2x.png
var TrayTemplate []byte

// TrayColor is the 32-pixel color icon for the Windows and Linux trays.
//
//go:embed icon/hicolor/32x32/apps/pinky.png
var TrayColor []byte

// Icon256 is the app icon at 256 pixels, for window icons on Windows and Linux.
//
//go:embed icon/hicolor/256x256/apps/pinky.png
var Icon256 []byte

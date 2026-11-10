package ui

import (
	"strings"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// The bezel Pinky draws around a phone's screen takes the screen's shape
// the right way up, which the picture gives (mirror.aspect): portrait, or
// landscape when the phone is turned, an iPad's either way. Its corners
// round with the screen's short side, so they stay the same turned, and
// less on an iPad, whose corners are rounded less for its size. Pinky
// draws no notch or Dynamic Island: the picture shows the phone's own top,
// which turns with it.

// bezelKind is the kind of phone a bezel is drawn for.
type bezelKind int

const (
	bezelAndroid bezelKind = iota
	bezelIPhone
	bezelIPad
	bezelHarmony
)

// bezelOf returns the kind of a device's bezel.
func bezelOf(d gateway.DeviceView) bezelKind {
	switch {
	case d.Info.Platform == driver.HarmonyOS:
		return bezelHarmony
	case d.Info.Platform != driver.IOS:
		return bezelAndroid
	case strings.HasPrefix(d.Info.Model, "iPad"):
		return bezelIPad
	}
	return bezelIPhone
}

// defaultAspect is a screen's width over its height before its picture
// comes: a phone's usual portrait.
func (k bezelKind) defaultAspect() float32 {
	switch k {
	case bezelIPhone:
		return 1206.0 / 2622
	case bezelIPad:
		return 1668.0 / 2388
	case bezelHarmony:
		return 1840.0 / 2800
	}
	return 9.0 / 20
}

// cornerShare is a screen's corner radius as a share of its short side,
// after the prototype's phones: 42 for an iPhone's screen 282 wide, 38 for
// an Android phone's 276.
func (k bezelKind) cornerShare() float32 {
	switch k {
	case bezelIPhone:
		return 0.15
	case bezelIPad:
		return 0.045
	}
	return 0.137
}

// bezelRadius returns the radius of a screen sw×sh DIPs, the right way up,
// and of the bezel pad DIPs thick around it.
func bezelRadius(k bezelKind, sw, sh, pad float32) (outer, inner float32) {
	inner = max(4, min(sw, sh)*k.cornerShare())
	return inner + pad - 1, inner
}

// fitBezel returns the size of a phone, its bezel pad DIPs thick around a
// screen of aspect (width over height), that fits in w×h DIPs, and the
// screen's size.
func fitBezel(aspect, w, h, pad float32) (pw, ph, sw, sh float32) {
	if aspect <= 0 {
		aspect = 9.0 / 20
	}
	sw, sh = w-2*pad, (w-2*pad)/aspect
	if sh > h-2*pad {
		sh = h - 2*pad
		sw = sh * aspect
	}
	sw, sh = max(sw, 1), max(sh, 1)
	return sw + 2*pad, sh + 2*pad, sw, sh
}

// deviceBezel scales the physical border with the display's short edge.
// Its width is part of the fit, so maximizing or resizing never changes
// the proportion of the phone or tablet occupied by the border.
func deviceBezel(d gateway.DeviceView, aspect, w, h, fallback float32) (pw, ph, pad, outer, inner float32) {
	ratio, outerCorner := float32(0), float32(0)
	switch d.Info.Model {
	case "iPhone 13 Pro":
		// Apple body width (71.5 mm) and the 1170-pixel, 460-ppi panel.
		ratio, outerCorner = 62.5/1170, 202.4/1170
	case "BKY-W20":
		// BKY product face: approximately 9 pixels of bezel per edge
		// for an active short edge of 213 pixels in the reference image.
		ratio, outerCorner = 9.0/213, 11.5/213
	}
	if ratio == 0 {
		pw, ph, sw, sh := fitBezel(aspect, w, h, fallback)
		outer, inner := bezelRadius(bezelOf(d), sw, sh, fallback)
		return pw, ph, fallback, outer, inner
	}
	if aspect <= 0 {
		aspect = bezelOf(d).defaultAspect()
	}
	extra := 2 * ratio * min(aspect, 1)
	sh := min(w/(aspect+extra), h/(1+extra))
	sw := aspect * sh
	pad = ratio * min(sw, sh)
	return sw + 2*pad, sh + 2*pad, pad, outerCorner * min(sw, sh), 0
}

package ios

import (
	"context"
	"fmt"
)

const (
	deviceInfoService     = "com.apple.coredevice.deviceinfo"
	featureGetDisplayInfo = "com.apple.coredevice.feature.getdisplayinfo"
)

// screenInfo describes the main display.
type screenInfo struct {
	// Width and Height are the size UIKit renders at (points times scale),
	// which is also the screenshot size and Pinky's coordinate space. The
	// panel itself may be slightly smaller (1080x2340 on an iPhone 13 mini,
	// against 1125x2436 rendered).
	Width, Height int
	Scale         float64 // pixels per point
	Orientation   string  // rot0, rot90, rot180, rot270
	Backlight     string  // activeOn, off, ...
}

func readScreenInfo(ctx context.Context, svc *cdService) (screenInfo, error) {
	out, err := svc.invoke(ctx, featureGetDisplayInfo, "", nil)
	if err != nil {
		return screenInfo{}, err
	}
	return parseScreenInfo(out)
}

// parseScreenInfo reads the main display from a getdisplayinfo reply.
func parseScreenInfo(out map[string]any) (screenInfo, error) {
	displays, _ := out["displays"].([]any)
	for _, d := range displays {
		m, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if p, _ := m["primary"].(bool); !p {
			continue
		}
		si := screenInfo{Scale: 1}
		if b, ok := m["bounds"].([]any); ok && len(b) == 2 {
			if wh, ok := b[1].([]any); ok && len(wh) == 2 {
				si.Width, si.Height = int(toFloat(wh[0])), int(toFloat(wh[1]))
			}
		}
		if s := toFloat(m["pointScale"]); s > 0 {
			si.Scale = s
		}
		si.Orientation, _ = m["currentOrientation"].(string)
		si.Backlight, _ = m["backlightState"].(string)
		if si.Backlight == "" {
			// iOS 27.2 moved it next to "displays".
			si.Backlight, _ = out["backlightState"].(string)
		}
		if si.Width <= 0 || si.Height <= 0 {
			return si, fmt.Errorf("display info: no size for the main display")
		}
		return si, nil
	}
	return screenInfo{}, fmt.Errorf("display info: no main display")
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	default:
		return float64(toInt64(v))
	}
}

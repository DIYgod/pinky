package harmony

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/screen"
)

type uiNode struct {
	Attrs    map[string]string `json:"attributes"`
	Children []uiNode          `json:"children"`
}

func bounds(s string) screen.Rect {
	var x, y, x2, y2 int
	if _, err := fmt.Sscanf(s, "[%d,%d][%d,%d]", &x, &y, &x2, &y2); err != nil {
		return screen.Rect{}
	}
	return screen.Rect{X: x, Y: y, W: x2 - x, H: y2 - y}
}

// parseLayout uses the focused window only, not another application's
// notifications or task snapshots. Unknown foreground identity fails closed.
func parseLayout(raw []byte, focus string) (*screen.Screen, error) {
	var root uiNode
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("harmony: UITest layout: %w", err)
	}
	s := &screen.Screen{CapturedAt: time.Now(), Orientation: "portrait"}
	r := bounds(root.Attrs["bounds"])
	s.Width, s.Height = r.X+r.W, r.Y+r.H
	var main *uiNode
	var find func(*uiNode)
	find = func(n *uiNode) {
		if n.Attrs["hostWindowId"] == focus && n.Attrs["bundleName"] != "" && main == nil {
			main = n
		}
		for i := range n.Children {
			find(&n.Children[i])
		}
	}
	find(&root)
	if main == nil {
		return nil, fmt.Errorf("harmony: cannot identify focused window %s", focus)
	}
	s.App.ID = main.Attrs["bundleName"]
	if s.Width <= 0 || s.Height <= 0 {
		return nil, fmt.Errorf("harmony: invalid screen size")
	}
	if s.Width > s.Height {
		s.Orientation = "landscape"
	}
	var walk func(*uiNode, string)
	walk = func(n *uiNode, path string) {
		a := n.Attrs
		if a["visible"] == "false" {
			return
		}
		if id := a["hostWindowId"]; id != "" && id != focus {
			return
		}
		r := bounds(a["bounds"])
		label := strings.TrimSpace(a["description"])
		if label == "" {
			label = strings.TrimSpace(a["text"])
		}
		if label == "" {
			label = strings.TrimSpace(a["hint"])
		}
		typ := a["type"]
		role := screen.RoleOther
		switch strings.ToLower(typ) {
		case "button":
			role = screen.RoleButton
		case "textinput", "textarea", "search":
			role = screen.RoleInput
		case "text", "span":
			role = screen.RoleText
		case "image":
			role = screen.RoleImage
		case "list", "scroll", "grid", "swiper":
			role = screen.RoleList
		case "toggle", "switch":
			role = screen.RoleSwitch
		case "checkbox":
			role = screen.RoleCheckbox
		default:
			if a["clickable"] == "true" {
				role = screen.RoleButton
			}
		}
		if !r.Empty() && (label != "" || role == screen.RoleInput || a["clickable"] == "true" || a["scrollable"] == "true") {
			e := screen.Element{Role: role, Label: label, Bounds: r, ID: a["id"], Path: path, Source: screen.SourceTree}
			for _, st := range [][2]string{{"focused", screen.StateFocused}, {"selected", screen.StateSelected}, {"checked", screen.StateChecked}, {"scrollable", screen.StateScrollable}, {"password", screen.StatePassword}} {
				if a[st[0]] == "true" {
					e.States = append(e.States, st[1])
				}
			}
			if a["enabled"] == "false" {
				e.States = append(e.States, screen.StateDisabled)
			}
			if role == screen.RoleInput {
				e.States = append(e.States, screen.StateEditable)
			}
			s.Elements = append(s.Elements, e)
		}
		path += "/" + typ + ":" + a["id"]
		for i := range n.Children {
			walk(&n.Children[i], path)
		}
	}
	walk(main, "")
	return s, nil
}

func parseLock(out string) (locked bool, err error) {
	values := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "*" && (f[1] == "deviceLocked" || f[1] == "screenLocked") {
			v, e := strconv.ParseBool(f[2])
			if e != nil {
				return false, e
			}
			values[f[1]] = v
		}
	}
	if len(values) != 2 {
		return false, fmt.Errorf("harmony: cannot determine lock state")
	}
	return values["deviceLocked"] || values["screenLocked"], nil
}

func focusedWindow(out string) (string, error) {
	for _, line := range strings.Split(out, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "Focus window:"); ok {
			value = strings.TrimSpace(value)
			if id, err := strconv.Atoi(value); err == nil && id >= 0 {
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("harmony: cannot determine focused window")
}

// UITest can isolate the focused tree, but screenshots include overlays.
// Refuse unknown system panels and visible notification surfaces instead
// of leaking an unrelated app's notification through OCR or a screenshot.
func safeWindow(out, focus string) error {
	found := false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 {
			continue
		}
		if _, err := strconv.Atoi(f[3]); err != nil {
			continue
		}
		z, err := strconv.Atoi(f[7])
		if err != nil || z < 0 {
			continue
		}
		name := f[0]
		lower := strings.ToLower(name)
		if strings.Contains(lower, "banner") || strings.Contains(lower, "notific") || strings.Contains(lower, "dropdown") {
			return fmt.Errorf("harmony: a notification surface is visible; wait until it closes")
		}
		if f[3] == focus {
			found = true
			if strings.HasPrefix(name, "SCB") && !strings.HasPrefix(name, "SCBDesktop") {
				return fmt.Errorf("harmony: this system panel is not supported yet")
			}
		}
	}
	if !found {
		return fmt.Errorf("harmony: focused window is not visible")
	}
	return nil
}

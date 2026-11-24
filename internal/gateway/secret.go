package gateway

import (
	"image"
	"strings"

	"github.com/DIYgod/pinky/internal/history"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

// What an agent types into a password or one-time-code field, and the keys
// it taps on an app's hidden passcode keypad, are never kept or shown in
// plain text, wherever the person allowed the typing (Settings › Safety,
// on virtual phones and on phones) and when Pinky refused it. prep.secret
// marks such an action, and then:
//
//   - its step in Activity, live and in history, its approval card and the
//     step an approval holds say "Typed “••••••” into “Password”", and a
//     keypad tap "Tapped “•”" without where it went;
//   - its arguments in history (and so in exports) carry the mask for the
//     text, and its intent has the text taken out;
//   - the screen text history keeps after a step shows a password or code
//     field's content as the mask (hideTyped), and history screenshots
//     cover such fields where they show text (credentialPlaces).
//
// The agent's own result keeps the screen as it reads, field included: the
// agent gave the text, and checks the field with it. Screen recordings show
// the screen as the phone drew it, as for one-time codes (codes.go): they
// stay on this computer and reach an agent only when the person allows it.

// secretMask stands for the text typed into a password or code field: of
// one length, whatever the text's.
const secretMask = history.SecretMask

// secretKey stands for the key tapped on an app's hidden passcode keypad.
const secretKey = "•"

// hideSecret returns a with the text it types masked.
func hideSecret(a Action) Action {
	if a.Tool == "type" && a.Text != "" {
		a.Text = secretMask
	}
	if a.Tool == "tap" {
		// Which key of the passcode keypad.
		a.Ref, a.Point = 0, nil
	}
	return a
}

// shown returns the action and call as Pinky keeps and shows them: for a
// secret action (prep.secret), the text masked and taken out of the intent.
func shown(a Action, c Call, p *prep) (Action, Call) {
	if p == nil || !p.secret {
		return a, c
	}
	if a.Tool == "type" {
		c.Intent = scrubSecret(c.Intent, a.Text)
	}
	return hideSecret(a), c
}

// scrubSecret takes secret out of s. Text this short also stands in
// ordinary words, so one or two characters stay.
func scrubSecret(s, secret string) string {
	secret = strings.TrimSpace(secret)
	if s == "" || len([]rune(secret)) < 3 {
		return s
	}
	return strings.ReplaceAll(s, secret, secretMask)
}

// typedSecret reports whether e is a text field that holds a password or
// code: a credential field with text in it.
func typedSecret(e *screen.Element) bool {
	return e.Content != "" && (e.Role == screen.RoleInput || e.Has(screen.StateEditable)) && policy.IsCredentialField(e)
}

// hideTyped returns s with what its password and code fields hold masked,
// for the screen text history keeps; s itself when there is none.
func hideTyped(s *screen.Screen) *screen.Screen {
	if s == nil {
		return nil
	}
	var out *screen.Screen
	for i := range s.Elements {
		e := &s.Elements[i]
		if !typedSecret(e) {
			continue
		}
		if out == nil {
			c := *s
			c.Elements = append([]screen.Element(nil), s.Elements...)
			out = &c
		}
		m := &out.Elements[i]
		m.Label = maskContent(m.Label, m.Content)
		m.Content = secretMask
	}
	if out == nil {
		return s
	}
	return out
}

// maskContent replaces the content a label shows (all of it, or its end
// after the field's name) with the mask.
func maskContent(label, content string) string {
	if label == content {
		return secretMask
	}
	if rest, ok := strings.CutSuffix(label, content); ok {
		return rest + secretMask
	}
	return strings.ReplaceAll(label, content, secretMask)
}

// credentialPlaces returns where reads show password and code fields with
// text in them, in the pixels of a w x h picture of the screen, and
// whether one could not be placed.
func credentialPlaces(w, h int, reads ...*screen.Screen) (boxes []image.Rectangle, unplaced bool) {
	for _, s := range reads {
		if s == nil {
			continue
		}
		for i := range s.Elements {
			e := &s.Elements[i]
			if !typedSecret(e) {
				continue
			}
			if e.Bounds.Empty() || s.Width <= 0 || s.Height <= 0 {
				unplaced = true
				continue
			}
			if r := scaleBox(e.Bounds, s.Width, s.Height, w, h); !r.Empty() {
				boxes = append(boxes, r)
			}
		}
	}
	return boxes, unplaced
}

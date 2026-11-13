package standalone

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo/i18n"

	"github.com/DIYgod/pinky/internal/gwtext"
)

// The gateway words steps, approvals and status lines in English, for
// agents, logs and the history alike. The screens read them in the
// person's language: each command may name the language the screens show
// ("locale": "zh-Hans", "ja", "en"), and the state and the history then
// carry the gateway's words in it beside the English (the *Shown fields),
// as desktop Pinky says them (internal/gwtext). A core serves the screens
// of one app, so the language is the core's, the last one a command named.

// screenLocale is the language the screens last named; "" is English.
var (
	screenLocale atomic.Pointer[string]
	localeOnce   sync.Once
)

// setLocale makes locale the language of the screens. The words of
// internal/gwtext follow i18n.Locale, which nothing else in a core sets.
func setLocale(locale string) {
	if locale == "" {
		return
	}
	if p := screenLocale.Load(); p != nil && *p == locale {
		return
	}
	screenLocale.Store(&locale)
	localeOnce.Do(func() {
		i18n.SetLocaleFunc(func() string {
			if p := screenLocale.Load(); p != nil {
				return *p
			}
			return ""
		})
	})
}

// shown is s said in the screens' language, or "" when that says the same.
func shown(s, said string) string {
	if said == s {
		return ""
	}
	return said
}

// stepShown says a step's text in the screens' language: its first line
// and, after a newline, its second; and for a step that waited for the
// person, what was asked. Both are "" in English.
func stepShown(text string) (step, asked string) {
	if !gwtext.Translated() {
		return "", ""
	}
	first, sub, _ := strings.Cut(text, "\n")
	sub = strings.TrimSpace(sub)
	said := gwtext.Step(first)
	if sub != "" {
		said += "\n" + gwtext.StepSub(sub)
	}
	if action, ok := strings.CutPrefix(first, "Waiting for you: "); ok {
		asked = gwtext.ActionAsked(action)
	}
	return shown(text, said), asked
}

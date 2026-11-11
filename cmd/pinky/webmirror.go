package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/tailnet"
	"github.com/DIYgod/pinky/internal/webmirror"
	"github.com/DIYgod/pinky/ui"
)

// newWebMirror returns the web mirror of the gateway's phones, off until
// Settings › Phones › Web mirror turns it on. Its tokens are kept apart
// from the agent token: one can only watch, the other can also use the
// phone while the person allows control.
func newWebMirror(dirs api.Dirs, g *gateway.Gateway, log *slog.Logger) *webMirror {
	return &webMirror{webmirror.New(webmirror.Options{
		Source:           g,
		TokenFile:        filepath.Join(dirs.Config, "mirror-token"),
		ControlTokenFile: filepath.Join(dirs.Config, "mirror-control-token"),
		Log:              log,
		OwnNames:         tailnetNames,
		Text:             webMirrorText,
	})}
}

// webMirror is the web mirror as Settings sees it (ui.WebMirror).
type webMirror struct{ *webmirror.Server }

// Tailnet says where `tailscale serve` gives the web mirror to the tailnet.
// It only reads Tailscale's state.
func (w *webMirror) Tailnet(ctx context.Context) ui.RemoteTailnet {
	st, _ := tailnet.Read(ctx)
	if !st.Running() || st.Self.DNSName == "" {
		return ui.RemoteTailnet{}
	}
	out := ui.RemoteTailnet{Name: st.Self.DNSName}
	if s, err := tailnet.ReadServe(ctx); err == nil {
		if u := s.Forwarding(webmirror.Addr); u != "" {
			out.URL = u + "/"
		} else {
			out.ServePort = webmirror.ServePort(s)
		}
	}
	return out
}

// webMirrorText is the page's words in the language of the browser's
// Accept-Language that suits best, as "zh-CN,zh;q=0.9,en;q=0.8".
func webMirrorText(acceptLanguage string) (string, map[string]string) {
	want := ""
	if first, _, _ := strings.Cut(acceptLanguage, ","); first != "" {
		want, _, _ = strings.Cut(strings.TrimSpace(first), ";")
	}
	m := locales.In(want)
	_, lang, ok := locales.Catalog.Message(want, "webMirrorPageTitle")
	if !ok || lang == "" {
		lang = "en"
	}
	return lang, map[string]string{
		"title":        m.WebMirrorPageTitle(),
		"phone":        m.WebMirrorPagePhone(),
		"connecting":   m.WebMirrorPageConnecting(),
		"reconnecting": m.WebMirrorPageReconnecting(),
		"noToken":      m.WebMirrorPageNoToken(),
		"badToken":     m.WebMirrorPageBadToken(),
		"insecure":     m.WebMirrorPageInsecure(),
		"noWebCodecs":  m.WebMirrorPageNoWebCodecs(),
		"unsupported":  m.WebMirrorPageUnsupported("{codec}"),
		"noPhone":      m.WebMirrorPageNoPhone(),
		"ended":        m.WebMirrorPageEnded(),
		"streamFailed": m.WebMirrorPageStreamFailed("{detail}"),
		"back":         m.WebMirrorPageBack(),
		"home":         m.WebMirrorPageHome(),
		"fullScreen":   m.WebMirrorPageFullScreen(),
		"inControl":    m.WebMirrorPageInControl(),
		"controlOff":   m.WebMirrorPageControlOff(),
		"tailnetUser":  m.WebMirrorPageTailnetUser(),
	}
}

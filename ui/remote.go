package ui

import (
	"net"
	"strconv"
	"strings"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
)

// Remote agents: MCP over Streamable HTTP, for agents that cannot start
// `pinky mcp` on this computer. Off by default, at DefaultRemoteAddr unless
// the person changes it. The agent token is the bearer token; the first
// connection asks the person as over stdio.

// RemotePath is the path of the MCP endpoint Settings tells remote agents
// to use: http://<RemoteAddr>/mcp.
const RemotePath = "/mcp"

// RemoteURL is the URL agents connect to over HTTP.
func RemoteURL(addr string) string { return "http://" + addr + RemotePath }

// remoteState is Settings › Agents › Remote agents'.
type remoteState struct {
	addr      string // the address being typed
	editing   bool
	showToken bool
	err       string
}

// checkRemoteAddr says what is wrong with an address to listen on, and
// whether other computers can reach it.
func checkRemoteAddr(addr string) (msg string, others bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return locales.RemoteAddrInvalid(), false
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return locales.RemotePortInvalid(), false
	}
	switch {
	case host == "localhost":
		return "", false
	case host == "":
		return "", true // every address of this computer
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return locales.RemoteHostInvalid(), false
	}
	return "", !ip.IsLoopback()
}

// remoteAgents is the Remote agents part of Settings › Agents.
func (a *app) remoteAgents(c *mg.Context) {
	p := pal(c)
	st := &a.settings
	r := &a.remote
	if st.RemoteAddr == "" {
		st.RemoteAddr = DefaultRemoteAddr
	}
	mg.Column(c).Gap(8).Label(locales.RemoteTitle()).Children(func() {
		mg.Column(c).Children(func() {
			mg.Text(c, locales.RemoteTitle()).FontSize(15).FontWeight(650)
			mg.Text(c, locales.RemoteIntro()).
				FontSize(13.5).TextColor(p.ink2).Margin(2, 0, 0, 0)
		})
		mg.Column(c).Children(func() {
			a.toggleRow(c, "remote", locales.RemoteToggle(), "", !st.RemoteAgents, &st.RemoteAgents)
			if !st.RemoteAgents {
				return
			}
			_, others := checkRemoteAddr(st.RemoteAddr)
			settingRow(c, locales.RemoteAddress(), locales.RemoteAddressDesc(), false, func() {
				if !r.editing {
					mg.Row(c).Gap(10).AlignItems(mg.Center).Children(func() {
						mg.Text(c, st.RemoteAddr).Font("monospace").FontSize(13).Selectable()
						if button(c, locales.SettingsChange(), btnOpt{small: true}).Label(locales.RemoteChangeAddress()).Clicked() {
							r.editing, r.addr, r.err = true, st.RemoteAddr, ""
						}
					})
					return
				}
				mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
					in := mg.TextInput(c.Key("remote-addr"), &r.addr).Label(locales.RemoteAddress()).Placeholder(DefaultRemoteAddr).Width(170).AutoFocus()
					save := button(c, locales.SettingsSave(), btnOpt{small: true, kind: btnPrimary})
					if save.Clicked() || in.Submitted() {
						addr := strings.TrimSpace(r.addr)
						if msg, _ := checkRemoteAddr(addr); msg != "" {
							r.err = msg
						} else {
							st.RemoteAddr, r.editing, r.err = addr, false, ""
							a.changed()
						}
					}
					if button(c, locales.SettingsCancel(), btnOpt{small: true}).Clicked() {
						r.editing, r.err = false, ""
					}
				})
			})
			if r.err != "" {
				mg.Text(c, r.err).FontSize(13).TextColor(p.red).Role(mg.RoleStatus).Margin(0, 0, 6, 0)
			}
			if others {
				mg.Row(c).Gap(8).AlignItems(mg.Start).Padding(10, 12).Radius(10).Background(p.amberTint).Border(1, p.amberLine).Margin(0, 0, 6, 0).
					Children(func() {
						mg.Icon(c, icAlert).FontSize(16).TextColor(p.amber)
						mg.Text(c, locales.RemoteOthersWarning()).
							FontSize(13).Grow(1).MinWidth(0)
					})
			}
			url := RemoteURL(st.RemoteAddr)
			a.copyRow(c, locales.RemoteURLTitle(), url, url, locales.RemoteCopyURL(), locales.RemoteCopiedURL(), false)
			tok := a.opt.AgentToken
			if tok == "" {
				settingRow(c, locales.RemoteToken(), locales.RemoteTokenMissing(), false, nil)
			} else {
				shown := strings.Repeat("•", 16)
				if r.showToken {
					shown = tok
				}
				settingRow(c, locales.RemoteToken(), locales.RemoteTokenDesc(), false, func() {
					mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
						mg.Text(c, shown).Font("monospace").FontSize(12.5).MaxWidth(180).SingleLine()
						word, label := locales.RemoteShow(), locales.RemoteShowToken()
						if r.showToken {
							word, label = locales.RemoteHide(), locales.RemoteHideToken()
						}
						if button(c, word, btnOpt{small: true}).Label(label).Clicked() {
							r.showToken = !r.showToken
						}
						if button(c, "", btnOpt{small: true, icon: icCopy, label: locales.RemoteCopyToken()}).Clicked() {
							a.copy(c, tok, locales.RemoteCopiedToken())
						}
					})
				})
			}
			token := "<agent token>"
			if tok != "" {
				token = tok
			}
			cmd := `claude mcp add --transport http pinky ` + url + ` --header "Authorization: Bearer ` + token + `"`
			shownCmd := cmd
			if tok != "" && !r.showToken {
				shownCmd = strings.Replace(cmd, tok, "…", 1)
			}
			mg.Column(c).Gap(6).Padding(10, 0).Children(func() {
				mg.Row(c).Gap(12).AlignItems(mg.Center).Children(func() {
					mg.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						mg.Text(c, "Claude Code").FontWeight(600)
						mg.Text(c, locales.RemoteRunHint()).FontSize(13.5).TextColor(p.ink2)
					})
					if button(c, "", btnOpt{small: true, icon: icCopy, label: locales.RemoteCopyCommand()}).Clicked() {
						a.copy(c, cmd, locales.RemoteCopiedCommand())
					}
				})
				mg.Text(c, shownCmd).Font("monospace").FontSize(12).Padding(10, 12).Radius(8).Background(p.bg2).Border(1, p.line).
					Selectable().Label(locales.RemoteCommandLabel())
			})
		})
	})
}

// copyRow is a setting row whose value has a Copy button, named copyLabel,
// which says copied once it copies.
func (a *app) copyRow(c *mg.Context, title, shown, value, copyLabel, copied string, last bool) {
	settingRow(c, title, "", last, func() {
		mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
			mg.Text(c, shown).Font("monospace").FontSize(12.5).Selectable()
			if button(c, "", btnOpt{small: true, icon: icCopy, label: copyLabel}).Clicked() {
				a.copy(c, value, copied)
			}
		})
	})
}

// copy puts text on the clipboard and says so.
func (a *app) copy(c *mg.Context, text, said string) {
	c.WriteClipboard(text)
	c.Toast(said)
}

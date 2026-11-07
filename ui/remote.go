package ui

import (
	"net"
	"strconv"
	"strings"

	mg "github.com/egoist/mygo/ui"
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
		return "Enter an address and port, like 127.0.0.1:7465.", false
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "The port is a number from 1 to 65535.", false
	}
	switch {
	case host == "localhost":
		return "", false
	case host == "":
		return "", true // every address of this computer
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "Enter an IP address of this computer, like 127.0.0.1, or localhost.", false
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
	mg.Column(c).Gap(8).Label("Remote agents").Children(func() {
		mg.Column(c).Children(func() {
			mg.Text(c, "Remote agents").FontSize(15).FontWeight(650)
			mg.Text(c, "For agents that can't start `pinky mcp` on this computer, such as one in a container. They connect over HTTP with the agent token, and you allow each one, as above.").
				FontSize(13.5).TextColor(p.ink2).Margin(2, 0, 0, 0)
		})
		mg.Column(c).Children(func() {
			a.toggleRow(c, "remote", "Let agents connect over HTTP", "", !st.RemoteAgents, &st.RemoteAgents)
			if !st.RemoteAgents {
				return
			}
			_, others := checkRemoteAddr(st.RemoteAddr)
			settingRow(c, "Address", "Where Pinky listens. 127.0.0.1 is this computer only.", false, func() {
				if !r.editing {
					mg.Row(c).Gap(10).AlignItems(mg.Center).Children(func() {
						mg.Text(c, st.RemoteAddr).Font("monospace").FontSize(13).Selectable()
						if button(c, "Change", btnOpt{small: true}).Label("Change the address").Clicked() {
							r.editing, r.addr, r.err = true, st.RemoteAddr, ""
						}
					})
					return
				}
				mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
					in := mg.TextInput(c.Key("remote-addr"), &r.addr).Label("Address").Placeholder(DefaultRemoteAddr).Width(170).AutoFocus()
					save := button(c, "Save", btnOpt{small: true, kind: btnPrimary})
					if save.Clicked() || in.Submitted() {
						addr := strings.TrimSpace(r.addr)
						if msg, _ := checkRemoteAddr(addr); msg != "" {
							r.err = msg
						} else {
							st.RemoteAddr, r.editing, r.err = addr, false, ""
							a.changed()
						}
					}
					if button(c, "Cancel", btnOpt{small: true}).Clicked() {
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
						mg.Text(c, "Other computers on your network can reach Pinky at this address. Anyone with the agent token can ask to use your phones.").
							FontSize(13).Grow(1).MinWidth(0)
					})
			}
			url := RemoteURL(st.RemoteAddr)
			a.copyRow(c, "URL", url, url, false)
			tok := a.opt.AgentToken
			if tok == "" {
				settingRow(c, "Agent token", "This Pinky didn't give the client its agent token.", false, nil)
			} else {
				shown := strings.Repeat("•", 16)
				if r.showToken {
					shown = tok
				}
				settingRow(c, "Agent token", "Agents send it as their bearer token. Keep it private.", false, func() {
					mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
						mg.Text(c, shown).Font("monospace").FontSize(12.5).MaxWidth(180).SingleLine()
						word := "Show"
						if r.showToken {
							word = "Hide"
						}
						if button(c, word, btnOpt{small: true}).Label(word + " the agent token").Clicked() {
							r.showToken = !r.showToken
						}
						if button(c, "", btnOpt{small: true, icon: icCopy, label: "Copy the agent token"}).Clicked() {
							a.copy(c, tok, "Copied the agent token")
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
						mg.Text(c, "Run this where the agent runs.").FontSize(13.5).TextColor(p.ink2)
					})
					if button(c, "", btnOpt{small: true, icon: icCopy, label: "Copy the command"}).Clicked() {
						a.copy(c, cmd, "Copied the command")
					}
				})
				mg.Text(c, shownCmd).Font("monospace").FontSize(12).Padding(10, 12).Radius(8).Background(p.bg2).Border(1, p.line).
					Selectable().Label("Command to add Pinky to Claude Code")
			})
		})
	})
}

// copyRow is a setting row whose value has a Copy button.
func (a *app) copyRow(c *mg.Context, title, shown, value string, last bool) {
	settingRow(c, title, "", last, func() {
		mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
			mg.Text(c, shown).Font("monospace").FontSize(12.5).Selectable()
			if button(c, "", btnOpt{small: true, icon: icCopy, label: "Copy the " + strings.ToLower(title)}).Clicked() {
				a.copy(c, value, "Copied the "+strings.ToLower(title))
			}
		})
	})
}

// copy puts text on the clipboard and says so.
func (a *app) copy(c *mg.Context, text, said string) {
	c.WriteClipboard(text)
	c.Toast(said)
}

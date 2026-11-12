package ui

import (
	"errors"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/webapproval"
)

// WebApprovals lets the person answer approvals in a browser on their
// phone, through the web mirror and Tailscale (internal/webapproval). A
// phone is added only here: its browser opens a link made here, makes a
// passkey and shows a code, and the person allows it here after comparing
// the code. Everything is kept in memory only.
type WebApprovals interface {
	// NewLink makes the token of the link that adds a phone, good once
	// and for a few minutes.
	NewLink() string
	// Requests are the browsers waiting for the person to allow them.
	Requests() []webapproval.Request
	Allow(id string) error
	Deny(id string)
	// Phones are the browsers that may answer approvals.
	Phones() []webapproval.Phone
	Remove(id string)
}

// webApprovalsLinkLife is how long the link that adds a phone works.
const webApprovalsLinkLife = 5 * time.Minute

// webApprovalsSettings is the Answer approvals in the browser part of
// Settings › Phones › Web mirror; tn says where Tailscale serves the
// mirror.
func (a *app) webApprovalsSettings(c *mg.Context, tn RemoteTailnet) {
	wa := a.opt.WebApprovals
	if wa == nil {
		return
	}
	p := pal(c)
	st := &a.settings
	a.toggleRow(c, "web-approvals", locales.WebApprovalsToggle(), locales.WebApprovalsDesc(), false, &st.WebApprovals)
	if !st.WebApprovals {
		return
	}
	if tn.URL == "" {
		mg.Text(c, locales.WebApprovalsNeedsTailnet()).FontSize(fsSmall).TextColor(p.ink2).Margin(0, sp4, sp2, sp4)
		return
	}
	r := &a.webMirror
	reqs := wa.Requests()
	if len(reqs) > 0 || time.Since(r.approvalLinkAt) >= webApprovalsLinkLife {
		// Used, or too old to work.
		r.approvalLink = ""
	}
	settingRow(c.Key("wa-add"), locales.WebApprovalsAdd(), locales.WebApprovalsAddDesc(), false, func() {
		if button(c.Key("wa-new"), locales.WebApprovalsAddButton(), btnOpt{small: true}).Clicked() {
			r.approvalLink, r.approvalLinkAt = wa.NewLink(), time.Now()
		}
	})
	if r.approvalLink != "" {
		// The token is a secret: never a key, and only after "#" on screen.
		link := tn.URL + "approvals#pair=" + r.approvalLink
		settingRow(c.Key("wa-link"), locales.WebApprovalsLink(), locales.WebApprovalsLinkDesc(), false, func() {
			mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
				mg.Text(c, tn.URL+"approvals#…").Font("monospace").FontSize(12.5)
				if button(c.Key("wa-copy"), "", btnOpt{small: true, icon: icCopy, label: locales.WebApprovalsCopyLink()}).Clicked() {
					a.copy(c, link, locales.WebMirrorCopiedLink())
				}
			})
		})
	}
	for _, q := range reqs {
		code := q.Code
		if len(code) == 6 {
			code = code[:3] + " " + code[3:]
		}
		kc := c.Key("wa-req-" + q.ID)
		mg.Row(kc).Gap(sp2).AlignItems(mg.Center).Padding(10, sp3).Radius(rControl).Background(p.amberTint).Border(1, p.amberLine).
			Margin(0, sp4, sp2, sp4).Role(mg.RoleStatus).Children(func() {
			mg.Icon(kc, icAlert).FontSize(16).TextColor(p.amber)
			mg.Column(kc).Gap(2).Grow(1).MinWidth(0).Children(func() {
				mg.Text(kc, locales.WebApprovalsRequest()).FontWeight(fwMedium)
				mg.Text(kc, locales.WebApprovalsRequestDesc(q.Browser, q.Login, code)).FontSize(fsSmall).LineHeight(1.4)
			})
			if button(kc.Key("deny"), locales.WebApprovalsDeny(), btnOpt{small: true}).Clicked() {
				wa.Deny(q.ID)
			}
			if button(kc.Key("allow"), locales.WebApprovalsAllow(), btnOpt{small: true, kind: btnPrimary}).Clicked() {
				if errors.Is(wa.Allow(q.ID), webapproval.ErrTooMany) {
					c.Toast(locales.WebApprovalsTooMany(webapproval.MaxPhones))
				}
			}
		})
	}
	phones := wa.Phones()
	if len(phones) == 0 {
		settingRow(c.Key("wa-phones"), locales.WebApprovalsPhones(), locales.WebApprovalsNoPhones(), false, nil)
	}
	for _, ph := range phones {
		kc := c.Key("wa-phone-" + ph.ID)
		settingRow(kc, locales.WebApprovalsPhones(), locales.WebApprovalsPhone(ph.Browser, ph.Login), false, func() {
			if button(kc.Key("remove"), locales.WebApprovalsRemove(), btnOpt{small: true}).Clicked() {
				wa.Remove(ph.ID)
				c.Toast(locales.WebApprovalsRemoved())
			}
		})
	}
	// Requests come from the phone.
	c.After(time.Second)
}

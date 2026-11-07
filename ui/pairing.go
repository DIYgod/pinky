package ui

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"
	"rsc.io/qr"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// Pairing an Android phone over Wi-Fi, by QR code or by pairing code
// (Developer options › Wireless debugging). The gateway's StartPairing
// gives the text of the QR code and waits for the phone; PairWithCode
// pairs with the address and code the phone shows.

// pairState is a pairing under way on the Add phone page.
type pairState struct {
	seq    int
	cancel context.CancelFunc
	state  string // "", "starting", "waiting", "failed"
	text   string // what the QR code says
	code   *qr.Code
	err    string
	since  time.Time // when the code showed

	// The pairing-code form.
	form     bool
	addr     string
	pin      string
	formBusy bool
	formErr  string
}

// startPairing asks the gateway for a QR code and waits for the phone in
// the background, until it pairs or the person leaves the step.
func (a *app) startPairing() {
	st := &a.add.pair
	pr, ok := a.api.(pairer)
	if !ok {
		return
	}
	a.stopPairing()
	st.seq++
	seq := st.seq
	ctx, cancel := context.WithCancel(a.ctx)
	st.cancel = cancel
	st.state, st.err, st.text, st.code = "starting", "", "", nil
	go func() {
		pg, err := pr.StartPairing(ctx)
		if err != nil {
			a.post(func() {
				if st.seq == seq {
					st.state, st.err = "failed", pairError(err)
				}
			})
			return
		}
		defer pg.Close()
		text := pg.QR()
		code, qerr := qr.Encode(text, qr.M)
		a.post(func() {
			if st.seq != seq {
				return
			}
			if qerr != nil {
				st.state, st.err = "failed", locales.PairingQRDrawError(qerr.Error())
				return
			}
			st.state, st.text, st.code, st.since = "waiting", text, code, time.Now()
		})
		if qerr != nil {
			return
		}
		info, err := pg.Wait(ctx)
		if ctx.Err() != nil {
			return // the person left
		}
		a.paired(seq, info, err, false)
	}()
}

// stopPairing ends a QR pairing the person walked away from.
func (a *app) stopPairing() {
	st := &a.add.pair
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	st.seq++
	st.state, st.err = "", ""
}

// pairWithCode pairs with the address and code the phone shows under Pair
// device with pairing code.
func (a *app) pairWithCode() {
	st := &a.add.pair
	pr, ok := a.api.(pairer)
	if !ok || st.formBusy {
		return
	}
	addr := strings.TrimSpace(st.addr)
	if msg := checkPairAddr(addr); msg != "" {
		st.formErr = msg
		return
	}
	code := keepDigits(st.pin, 6)
	if len(code) != 6 {
		st.formErr = locales.PairingCodeIncomplete()
		return
	}
	st.formBusy, st.formErr = true, ""
	seq := st.seq
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
		defer cancel()
		info, err := pr.PairWithCode(ctx, addr, code)
		a.paired(seq, info, err, true)
	}()
}

// paired finishes a pairing: on success it reads the devices again and
// shows the new phone.
func (a *app) paired(seq int, info driver.Info, err error, form bool) {
	var devs []gateway.DeviceView
	if err == nil {
		devs = a.api.Devices()
	}
	a.post(func() {
		st := &a.add.pair
		if form {
			st.formBusy = false
		}
		if err != nil {
			if form {
				st.formErr = pairError(err)
			} else if st.seq == seq {
				st.state, st.err = "failed", pairError(err)
			}
			return
		}
		a.devices = devs
		id := info.ID
		if a.device(id) == nil {
			if d := a.add.fresh(a, driver.Android); d != nil {
				id = d.Info.ID
			}
		}
		a.stopPairing()
		st.form, st.addr, st.pin = false, "", ""
		a.add.step = ""
		a.selectPhone(id)
		paired := locales.PairingPairedPhone()
		if info.Name != "" {
			paired = locales.PairingPaired(info.Name)
		}
		if d := a.device(id); d != nil {
			paired = locales.PairingPaired(d.Info.Name)
		}
		a.toasts = append(a.toasts, paired)
		a.afterRefresh(false)
	})
}

// checkPairAddr says what is wrong with an address typed for pairing, or
// "" when it is an IP address (or name) and port.
func checkPairAddr(addr string) string {
	want := locales.PairingAddrInvalid()
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return want
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return want
	}
	return ""
}

// pairError says in plain words why pairing failed.
func pairError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, gateway.ErrNoPairing):
		return locales.PairingErrNoAdb()
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
		return locales.PairingErrTimeout()
	case errors.Is(err, driver.ErrUnsupported):
		return locales.PairingErrUnsupported()
	case strings.Contains(msg, "password") || strings.Contains(msg, "wrong") || strings.Contains(msg, "incorrect"):
		return locales.PairingErrWrongCode()
	case strings.Contains(msg, "refused") || strings.Contains(msg, "unreachable") || strings.Contains(msg, "no route") || strings.Contains(msg, "connect"):
		return locales.PairingErrUnreachable()
	}
	return locales.PairingErrOther(err.Error())
}

// qrGeometry places a QR code of size modules in r, with 4 modules of
// quiet zone around it: where its first module starts and how wide each
// is, in whole pixels at scale s, so the code stays sharp.
func qrGeometry(r mg.Rect, s float32, size int) (ox, oy, cell float32) {
	if s <= 0 {
		s = 1
	}
	n := size + 8
	px := float32(int(min(r.W, r.H) * s / float32(n)))
	if px < 1 {
		px = 1
	}
	cell = px / s
	ox = float32(int((r.X+(r.W-cell*float32(n))/2)*s+0.5))/s + 4*cell
	oy = float32(int((r.Y+(r.H-cell*float32(n))/2)*s+0.5))/s + 4*cell
	return ox, oy, cell
}

// qrView draws a QR code with its quiet zone, its modules snapped to whole
// pixels so the code stays sharp at any scale.
func qrView(c *mg.Context, code *qr.Code, size float32) mg.Element {
	box := mg.Box(c).Size(size, size).Radius(12).Background(hex("#ffffff")).Shrink(0).Role(mg.RoleImage).
		Label(locales.PairingQRLabel())
	box.Draw(func(pt *mg.Painter, r mg.Rect) {
		if code == nil {
			return
		}
		ox, oy, cell := qrGeometry(r, pt.Scale(), code.Size)
		black := hex("#000000")
		for y := 0; y < code.Size; y++ {
			// One rectangle per run of dark modules, so no seams show.
			for x := 0; x < code.Size; {
				if !code.Black(x, y) {
					x++
					continue
				}
				x0 := x
				for x < code.Size && code.Black(x, y) {
					x++
				}
				pt.Fill(mg.Rect{X: ox + float32(x0)*cell, Y: oy + float32(y)*cell, W: float32(x-x0) * cell, H: cell}, black, 0)
			}
		}
	})
	return box
}

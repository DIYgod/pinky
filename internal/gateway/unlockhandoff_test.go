package gateway

import (
	"context"
	"testing"
	"time"
)

// Unlocking from the computer ends a handoff opened on a locked phone only
// when unlocking is all it asks for (field test #26): "unlock, then
// unblock three apps in Settings" stays open until the person hands back.
func TestUnlockKeepsAHandoffThatAsksForMore(t *testing.T) {
	ctx := context.Background()
	x := newExtraDev("pixel", "Pixel 9")
	x.pin = "4711"
	g := newExtraGateway(t, x, func(o *Options) {
		o.MaxBlock = 300 * time.Millisecond
		o.PIN = func(context.Context, string) (string, error) { return "4711", nil }
	})
	s, _ := connect(t, g, "Bookkeeping")
	x.SetLocked(true)
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusLocked)

	const reason = "Please unlock the phone, then unblock Wise, 招商银行 and 掌上生活 in Settings"
	res := s.Handoff(ctx, HandoffRequest{Reason: reason})
	mustStatus(t, res, StatusPending)
	n := waitNeedKind(t, g, NeedHandoff)
	if n.EndsOnUnlock {
		t.Fatalf("the card says unlocking ends it: %+v", n)
	}
	if err := g.Unlock("pixel"); err != nil {
		t.Fatal(err)
	}
	res = s.Handoff(ctx, HandoffRequest{Call: Call{ApprovalID: res.ApprovalID}, Reason: reason})
	mustStatus(t, res, StatusPending)
	if err := g.HandBack("pixel", "unblocked all three"); err != nil {
		t.Fatal(err)
	}
	res = s.Handoff(ctx, HandoffRequest{Call: Call{ApprovalID: res.ApprovalID}, Reason: reason})
	mustStatus(t, res, StatusSettled)
	if res.Note != "unblocked all three" {
		t.Fatalf("note: %+v", res)
	}

	// A handoff that asks only for the unlock still ends with it.
	x.SetLocked(true)
	mustStatus(t, s.Observe(ctx, ObserveRequest{}), StatusLocked)
	done := make(chan *Result, 1)
	go func() {
		done <- s.Handoff(ctx, HandoffRequest{Reason: "手机已锁屏，请解锁手机，以便我继续"})
	}()
	if n := waitNeedKind(t, g, NeedHandoff); !n.EndsOnUnlock {
		t.Fatalf("the card does not say unlocking ends it: %+v", n)
	}
	if err := g.Unlock("pixel"); err != nil {
		t.Fatal(err)
	}
	res = <-done
	mustStatus(t, res, StatusSettled)
	if res.Note != "The phone was unlocked." {
		t.Fatalf("unlock handoff: %+v", res)
	}
}

func TestUnlockOnly(t *testing.T) {
	for _, c := range []struct {
		reason string
		want   bool
	}{
		{"Please unlock the phone", true},
		{"Unlock the phone", true},
		{"The phone is locked. Please unlock it.", true},
		{"Please unlock the phone so I can continue.", true},
		{"Please unlock the phone, thanks!", true},
		{"Unlock the phone, then I'll continue with the task", true},
		{"The screen is locked; please unlock it so that I can open Settings", true},
		{"请解锁手机", true},
		{"手机已锁屏，请解锁", true},
		{"请解锁手机，以便我继续", true},
		{"请解锁手机，谢谢", true},
		{"請解鎖手機", true},
		{"ロックを解除してください", true},
		{"휴대폰 잠금을 해제해 주세요", true},
		{"Bitte entsperre das Telefon", true},
		{"Déverrouillez le téléphone, s'il vous plaît", false}, // "s'il vous plaît" is not in the list: ask for Done
		{"Unlock, then unblock three apps in Settings", false},
		{"Please unlock the phone and sign in to Wise with Face ID", false},
		{"Unlock the phone. Then open the bank app and pass Face ID.", false},
		{"请先解锁手机，然后在设置里解除屏蔽三个应用", false},
		{"解锁后在设置里把 Wise 解除屏蔽", false},
		{"请解锁手机并打开支付宝", false},
		{"Please pass Face ID in the bank app", false},
		{"Enter the Trust app's passcode", false},
		{"Unblock Wise in Settings", false},
		{"ブロックを解除してください", false},
		{"Please sign in to Dinely", false},
		{"", false},
	} {
		if got := unlockOnly(c.reason); got != c.want {
			t.Errorf("unlockOnly(%q) = %v, want %v (clauses %q)", c.reason, got, c.want, clauses(c.reason))
		}
	}
}

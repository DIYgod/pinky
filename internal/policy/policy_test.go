package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/screen"
)

var (
	dinely   = screen.App{Name: "Dinely", ID: "com.dinely.app"}
	whatsapp = screen.App{Name: "WhatsApp", ID: "com.whatsapp"}
	chase    = screen.App{Name: "Chase", ID: "com.chase.sig.android"}
	play     = screen.App{Name: "Play Store", ID: "com.android.vending"}
	settings = screen.App{Name: "Settings", ID: "com.android.settings"}
	launcher = screen.App{Name: "Pixel Launcher", ID: "com.google.android.apps.nexuslauncher"}
)

func button(label string) *screen.Element {
	return &screen.Element{Ref: 1, Role: screen.RoleButton, Label: label, Bounds: screen.Rect{X: 0, Y: 0, W: 100, H: 50}}
}

func tap(app screen.App, label string) Input {
	return Input{Op: OpTap, App: app, Target: button(label), Screen: &screen.Screen{App: app}}
}

func TestCommitDetection(t *testing.T) {
	commits := []string{
		"Send", "Pay $12.80", "Place order · $12.80", "Checkout", "Buy now", "Book a table", "Confirm",
		"Submit", "Delete", "Transfer", "Post", "Slide to pay", "Sign out",
		"发送", "立即支付 ¥12.80", "提交订单", "去结算(3)", "确认支付", "同意并支付", "删除",
		"傳送", "結帳", "注文を確定する", "購入する", "送信", "결제하기", "보내기", "주문하기",
		"Enviar", "Comprar ahora", "Passer la commande", "Payer", "Jetzt kaufen", "Zahlungspflichtig bestellen",
		"Finalizar compra", "Acquista ora", "Оформить заказ", "Отправить", "Yes, delete", "OK, send",
	}
	for _, l := range commits {
		if !isCommitLabel(l) {
			t.Errorf("%q should be a commit", l)
		}
	}
	notCommits := []string{
		"Orders", "Order history", "Order details", "Payment methods", "Sign in", "Search", "Back",
		"Settings", "Cart · 1", "Order by date", "订单详情", "我的订单", "支付方式", "注文履歴", "微信",
		"Send feedback about this long paragraph that goes on and on", "Share", "",
	}
	for _, l := range notCommits {
		if isCommitLabel(l) {
			t.Errorf("%q should not be a commit", l)
		}
	}
}

func TestDefaultRules(t *testing.T) {
	e := Default()
	cases := []struct {
		name string
		in   Input
		want Decision
		rule string
		kind Kind
	}{
		{"payment", tap(dinely, "Place order · $12.80"), Ask, "Payments", Payment},
		{"commit", tap(dinely, "Delete"), Ask, "Commits", Commit},
		{"navigation", tap(dinely, "Orders"), Allow, "", ""},
		{"finance app", tap(chase, "Accounts"), Deny, "Finance apps", ""},
		{"home in finance app", Input{Op: OpKey, Key: "home", App: chase}, Allow, "", ""},
		{"launch finance app", Input{Op: OpLaunch, Launch: chase}, Deny, "Finance apps", ""},
		{"install", tap(play, "Install"), Ask, "Installs and links", InstallApp},
		{"paid install", tap(play, "$4.99"), Ask, "Payments", Payment},
		{"call", tap(dinely, "Call restaurant"), Ask, "Calls", Call},
		{"settings toggle", Input{Op: OpTap, App: settings, Target: &screen.Element{Role: screen.RoleSwitch, Label: "Wi-Fi"}}, Ask, "Settings changes", ChangeSetting},
		{"settings navigation", tap(settings, "Network & internet"), Allow, "", ""},
		{"link", Input{Op: OpTap, App: dinely, Target: &screen.Element{Role: screen.RoleLink, Label: "https://example.com/offer"}}, Ask, "Installs and links", OpenURL},
		{"send message", Input{Op: OpType, App: whatsapp, Text: "hi", Submit: true, Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}}, Ask, "Messages", SendMessage},
		{"enter in chat", Input{Op: OpKey, Key: "enter", App: whatsapp, Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}}, Ask, "Messages", SendMessage},
		{"type in chat", Input{Op: OpType, App: whatsapp, Text: "hi", Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}}, Allow, "", ""},
		{"newline in chat", Input{Op: OpType, App: whatsapp, Text: "hi\n", Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}}, Ask, "Messages", SendMessage},
		{"search submit in chat", Input{Op: OpType, App: whatsapp, Text: "sam", Submit: true, Target: &screen.Element{Role: screen.RoleInput, Label: "Search"}}, Allow, "", ""},
		{"password", Input{Op: OpType, App: dinely, Text: "x", Target: &screen.Element{Role: screen.RoleInput, Label: "", States: []string{screen.StatePassword}}}, Deny, RuleCredentials, ""},
		{"otp", Input{Op: OpType, App: dinely, Text: "1", Target: &screen.Element{Role: screen.RoleInput, Label: "Verification code"}}, Deny, RuleCredentials, ""},
		{"otp zh", Input{Op: OpType, App: dinely, Text: "1", Target: &screen.Element{Role: screen.RoleInput, Label: "请输入验证码"}}, Deny, RuleCredentials, ""},
		{"promo code", Input{Op: OpType, App: dinely, Text: "SAVE10", Target: &screen.Element{Role: screen.RoleInput, Label: "Promo code"}}, Allow, "", ""},
		{"dialog ok", Input{Op: OpTap, App: dinely, Target: button("OK"), Screen: &screen.Screen{Elements: []screen.Element{{Role: screen.RoleText, Label: "Delete this message?"}, *button("OK")}}}, Ask, "Commits", Commit},
		{"plain ok", Input{Op: OpTap, App: dinely, Target: button("OK"), Screen: &screen.Screen{Elements: []screen.Element{{Role: screen.RoleText, Label: "Saved"}}}}, Allow, "", ""},
	}
	for _, c := range cases {
		v := e.Evaluate(c.in)
		if v.Decision != c.want || v.Rule != c.rule {
			t.Errorf("%s: got %v %q (kinds %v), want %v %q", c.name, v.Decision, v.Rule, v.Kinds, c.want, c.rule)
		}
		if c.kind != "" && !v.Has(c.kind) {
			t.Errorf("%s: kinds %v miss %s", c.name, v.Kinds, c.kind)
		}
	}
}

func TestBlockedApps(t *testing.T) {
	e := Default()
	if rule, ok := e.Blocked(chase); !ok || rule != "Finance apps" {
		t.Fatalf("Chase not blocked: %q %v", rule, ok)
	}
	if _, ok := e.Blocked(screen.App{Name: "1Password"}); !ok {
		t.Fatal("password manager by name not blocked")
	}
	if _, ok := e.Blocked(screen.App{ID: "com.hsbc.hsbcnet"}); !ok {
		t.Fatal("prefix entry com.hsbc.* not matched")
	}
	if _, ok := e.Blocked(dinely); ok {
		t.Fatal("Dinely blocked")
	}
}

func TestReadOnly(t *testing.T) {
	e := Default()
	e.SetReadOnly("dev1", true)
	v := e.Evaluate(Input{Op: OpTap, Device: "dev1", App: dinely, Target: button("Pay $3")})
	if v.Decision != Deny || v.Rule != RuleReadOnly {
		t.Fatalf("read-only commit: %v %q", v.Decision, v.Rule)
	}
	v = e.Evaluate(Input{Op: OpType, Device: "dev1", App: dinely, Text: "x", Target: &screen.Element{Role: screen.RoleInput, Label: "Note"}})
	if v.Decision != Deny || v.Rule != RuleReadOnly {
		t.Fatalf("read-only typing: %v %q", v.Decision, v.Rule)
	}
	v = e.Evaluate(Input{Op: OpType, Device: "dev1", App: dinely, Text: "rice", Target: &screen.Element{Role: screen.RoleInput, Label: "Search dishes"}})
	if v.Decision != Allow {
		t.Fatalf("read-only search typing: %v %q", v.Decision, v.Rule)
	}
	v = e.Evaluate(Input{Op: OpTap, Device: "dev2", App: dinely, Target: button("Pay $3")})
	if v.Decision != Ask {
		t.Fatalf("other device should not be read-only: %v", v.Decision)
	}
	v = e.Evaluate(Input{Op: OpTap, Device: "dev2", App: dinely, Target: button("Pay $3"), ReadOnly: true})
	if v.Decision != Deny {
		t.Fatalf("read-only scope: %v", v.Decision)
	}
}

func TestTaskAppsAndScope(t *testing.T) {
	e := Default()
	apps := []string{"Dinely"}
	v := e.Evaluate(Input{Op: OpLaunch, Launch: whatsapp, TaskApps: apps})
	if v.Decision != Deny || !v.Scope || v.Rule != RuleTaskApps {
		t.Fatalf("launch outside task apps: %+v", v)
	}
	v = e.Evaluate(Input{Op: OpLaunch, Launch: dinely, TaskApps: apps})
	if v.Decision != Allow {
		t.Fatalf("launch inside task apps: %+v", v)
	}
	v = e.Evaluate(Input{Op: OpType, App: whatsapp, Text: "x", TaskApps: apps, Target: &screen.Element{Role: screen.RoleInput, Label: "Message"}})
	if v.Decision != Ask || v.Rule != RuleTaskApps {
		t.Fatalf("typing outside task apps: %+v", v)
	}
	v = e.Evaluate(Input{Op: OpTap, App: launcher, TaskApps: apps, Target: button("Dinely")})
	if v.Decision != Allow {
		t.Fatalf("launcher is always in the list: %+v", v)
	}
	v = e.Evaluate(Input{Op: OpTap, App: whatsapp, ScopeApps: apps, Target: button("Chats")})
	if v.Decision != Deny || !v.Scope || v.Rule != RuleAgentScope {
		t.Fatalf("agent scope: %+v", v)
	}
}

func TestRulesFile(t *testing.T) {
	f, err := ParseFile([]byte(`
rules:
  - name: Chatter sends
    match: { app: com.chatter.app, action: [commit, send_message] }
    decision: ask
  - name: No deleting
    match: { label: delete }
    decision: deny
`))
	if err != nil {
		t.Fatal(err)
	}
	e := New(f, nil)
	chatter := screen.App{Name: "Chatter", ID: "com.chatter.app"}
	if v := e.Evaluate(tap(chatter, "Send")); v.Decision != Ask || v.Rule != "Chatter sends" {
		t.Fatalf("app rule: %+v", v)
	}
	if v := e.Evaluate(tap(chatter, "Delete chat")); v.Decision != Deny || v.Rule != "No deleting" {
		t.Fatalf("strictest wins: %+v", v)
	}
	if v := e.Evaluate(tap(dinely, "Send")); v.Decision != Allow {
		t.Fatalf("no rule for Dinely in this file: %+v", v)
	}
	if _, err := ParseFile([]byte("rules:\n  - name: x\n    match: { action: fly }\n    decision: ask\n")); err == nil {
		t.Fatal("unknown action accepted")
	}
	if _, err := ParseFile([]byte("rules:\n  - name: x\n    match: {}\n    decision: deny\n")); err == nil {
		t.Fatal("match-everything rule accepted")
	}
}

func TestLoadCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	rules, apps := filepath.Join(dir, "rules.yaml"), filepath.Join(dir, "apps.yaml")
	e, err := Load(rules, apps)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Rules()) == 0 {
		t.Fatal("no default rules")
	}
	for _, p := range []string{rules, apps} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", p, st.Mode().Perm())
		}
	}
	if err := os.WriteFile(apps, []byte("add:\n  finance:\n    ids: [com.dinely.app]\nexclude: [com.chase.sig.android]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Blocked(dinely); !ok {
		t.Fatal("app added in apps.yaml is not blocked")
	}
	if _, ok := e.Blocked(screen.App{ID: "com.chase.sig.android"}); ok {
		t.Fatal("excluded app still blocked")
	}
}

func TestMaskCodes(t *testing.T) {
	cases := map[string]string{
		"Your verification code is 482913": "Your verification code is ••••••",
		"G-123456 is your Google code":     "G-•••••• is your Google code",
		"【银行】您的验证码为 839201，5分钟内有效":         "【银行】您的验证码为 ••••••，5分钟内有效",
		"Code: 123 456":                      "Code: ••• •••",
		"Lunch at 12:30, see you":            "Lunch at 12:30, see you",
		"Order 1234567 shipped":              "Order 1234567 shipped",
		"Your code costs $1234":              "Your code costs $1234",
		"Your shopping order 123456 shipped": "Your shopping order 123456 shipped",
		"PIN: 4821":                          "PIN: ••••",
	}
	for in, want := range cases {
		if got := MaskCodes(in); got != want {
			t.Errorf("MaskCodes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilterEvents(t *testing.T) {
	e := Default()
	got := e.FilterEvents([]screen.Event{
		{Kind: screen.EventNotification, App: "com.chase.sig.android", Text: "Payment received"},
		{Kind: screen.EventNotification, App: "com.google.android.apps.messaging", Text: "Your code is 556677"},
		{Kind: screen.EventToast, Text: "Added to cart"},
	})
	if len(got) != 2 {
		t.Fatalf("blocked notification kept: %+v", got)
	}
	if !strings.Contains(got[0].Text, "••••••") {
		t.Fatalf("code not masked: %q", got[0].Text)
	}
}

package ui

import (
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/gateway"
)

// gatewaySteps are steps as internal/gateway words them.
var gatewaySteps = []string{
	"Tapped “Place order · $12.80”",
	"Long-pressed “Photo”",
	"Tapped at 540,1200",
	"Tapped",
	"Typed “no cilantro”",
	"Typed “no cilantro” into “Note”",
	"Typed “chicken rice” and submitted",
	"Typed “chicken rice” into “Search” and submitted",
	"Swiped up",
	"Swiped down in “Messages”",
	"Swiped from 540,1800 to 540,600",
	"Pressed home",
	"Pressed forward delete",
	"Opened Messages",
	"Waiting for you: Tapped “Send SMS”",
	"Blocked by “Payments”: Tapped “Pay”",
	"Outside the scope: Opened Northbank",
	"Failed: Tapped “Pay”",
	"Connected to Pixel 9 for “Reorder lunch”",
	"Finished: done",
	"Waited for “Order placed”",
	"Asked you: “Is 3pm fine?”",
	"Asked you to take over: Sign in to Dinely",
	"Unlocked from the computer",
	"Listed the phone's Pinky folder (3 files)",
	"Copied 2 files from the phone: a.txt, b.png",
	"Copied 7 files to the phone: a, b, c, d, e, and 2 more",
	"Blocked by “Read-only”: copy to the phone",
	"Failed: list the phone's Pinky folder",
	"Added 1 chicken rice bowl", // the fake's own words
}

func TestGatewayWordsInEnglish(t *testing.T) {
	for _, s := range gatewaySteps {
		if got := stepText(s); got != s {
			t.Errorf("stepText(%q) = %q, want it unchanged", s, got)
		}
	}
	for _, s := range []string{"Offline", "Claude Code · Reorder lunch · in the background", "Waiting for you · Pay: “X”?"} {
		if got := statusLine(gateway.DeviceView{StatusLine: s, Status: gateway.DeviceWorking}); got != s {
			t.Errorf("statusLine(%q) = %q", s, got)
		}
	}
	n := gateway.NeedView{Kind: gateway.NeedApproval, Title: "Pay: “Place order”?", Detail: "Claude Code · lunch"}
	if needTitle(n) != n.Title || needDetail(n) != n.Detail {
		t.Errorf("need changed in English: %q, %q", needTitle(n), needDetail(n))
	}
}

func TestGatewayWordsInChinese(t *testing.T) {
	inLanguage(t, "zh-Hans")
	want := map[string]string{
		"Tapped “Place order · $12.80”":                    "点按了“Place order · $12.80”",
		"Long-pressed “Photo”":                             "长按了“Photo”",
		"Tapped at 540,1200":                               "点按了 540,1200",
		"Tapped":                                           "点按了屏幕",
		"Typed “no cilantro”":                              "输入了“no cilantro”",
		"Typed “no cilantro” into “Note”":                  "在“Note”中输入了“no cilantro”",
		"Typed “chicken rice” and submitted":               "输入了“chicken rice”并提交",
		"Typed “chicken rice” into “Search” and submitted": "在“Search”中输入了“chicken rice”并提交",
		"Swiped up":                                     "向上滑动了",
		"Swiped down in “Messages”":                     "在“Messages”中向下滑动了",
		"Swiped from 540,1800 to 540,600":               "从 540,1800 滑动到了 540,600",
		"Pressed home":                                  "按了主屏幕键",
		"Pressed forward delete":                        "按了向后删除键",
		"Opened Messages":                               "打开了 Messages",
		"Waiting for you: Tapped “Send SMS”":            "等你确认：点按“Send SMS”",
		"Blocked by “Payments”: Tapped “Pay”":           "被“付款”规则拦下：点按“Pay”",
		"Outside the scope: Opened Northbank":           "超出允许范围：打开 Northbank",
		"Failed: Tapped “Pay”":                          "失败：点按“Pay”",
		"Connected to Pixel 9 for “Reorder lunch”":      "为“Reorder lunch”连接了 Pixel 9",
		"Finished: done":                                "结束：完成",
		"Waited for “Order placed”":                     "等待了“Order placed”",
		"Asked you: “Is 3pm fine?”":                     "询问你：“Is 3pm fine?”",
		"Asked you to take over: Sign in to Dinely":     "请你接管：Sign in to Dinely",
		"Unlocked from the computer":                    "已从电脑解锁",
		"Listed the phone's Pinky folder (3 files)":     "列出了手机的 Pinky 文件夹（3 个文件）",
		"Copied 2 files from the phone: a.txt, b.png":   "从手机拷贝了 2 个文件：a.txt、b.png",
		"Blocked by “Read-only”: copy to the phone":     "被“只读”规则拦下：向手机拷贝文件",
		"Failed: list the phone's Pinky folder":         "失败：列出手机的 Pinky 文件夹",
		"Added 1 chicken rice bowl":                     "Added 1 chicken rice bowl",
		"Copied 7 files to the phone: a, b, and 2 more": "向手机拷贝了 7 个文件：a、b、另外 2 个",
	}
	for in, out := range want {
		if got := stepText(in); got != out {
			t.Errorf("stepText(%q) = %q, want %q", in, got, out)
		}
	}
	titles := map[string]string{
		"Pay: “Place order · $12.80”?":     "付款：“Place order · $12.80”？",
		"Send “See you at 3”?":             "发送“See you at 3”？",
		"Send the message?":                "发送这条消息？",
		"Open Northbank?":                  "打开 Northbank？",
		"Change “Wi-Fi”?":                  "更改“Wi-Fi”？",
		"Tap “Delete”?":                    "点按“Delete”？",
		"Type into “Message”?":             "在“Message”中输入？",
		"Press enter in “Message”?":        "在“Message”中按回车键？",
		"Tapped at 540,1200?":              "点按 540,1200？",
		"Let Codex use your phones?":       "允许 Codex 使用你的手机？",
		"Copy “ticket.pdf” to Pixel 9?":    "将“ticket.pdf”拷贝到 Pixel 9？",
		"Copy 3 files to Pixel 9?":         "将 3 个文件拷贝到 Pixel 9？",
		"Something the gateway never says": "Something the gateway never says",
	}
	for in, out := range titles {
		n := gateway.NeedView{Kind: gateway.NeedApproval, Title: in, Rule: "Payments"}
		if in == "Let Codex use your phones?" {
			n.Rule = "New agent"
		}
		if got := needTitle(n); got != out {
			t.Errorf("needTitle(%q) = %q, want %q", in, got, out)
		}
	}
	handoff := gateway.NeedView{Kind: gateway.NeedHandoff, Title: "Sign in to Dinely?"}
	if got := needTitle(handoff); got != handoff.Title {
		t.Errorf("a handoff's title is the agent's words, got %q", got)
	}
	unlock := gateway.NeedView{Kind: gateway.NeedUnlock, Title: "Unlock Pixel 9", Detail: "Claude Code is waiting for the phone to unlock · Reorder lunch"}
	if got := needTitle(unlock) + " / " + needDetail(unlock); got != "解锁 Pixel 9 / Claude Code 正在等手机解锁 · Reorder lunch" {
		t.Errorf("unlock need: %q", got)
	}
	agent := gateway.NeedView{Kind: gateway.NeedApproval, Rule: "New agent", Detail: "Codex wants to connect for “Book a table”. Allowing gives it everything access.",
		Created: time.Date(2026, 11, 7, 3, 51, 0, 0, time.Local)}
	if got := needDetail(agent); got != "Codex 想连接你的手机，用于“Book a table”。允许后，它可以使用除已屏蔽应用外的所有应用。" {
		t.Errorf("new agent detail: %q", got)
	}
	if got := agentRequestDetail(agent, true); got != "Codex 想连接你的手机，用于“Book a table”。请求于 03:51。" {
		t.Errorf("new agent detail with the picker: %q", got)
	}
	status := map[string]string{
		"Offline":                                         "离线",
		"Offline · USB debugging is off":                  "离线 · USB debugging is off",
		"Offline · reconnecting for Reorder lunch":        "离线 · 正在为“Reorder lunch”重新连接",
		"Waiting for you · Pay: “Place order”?":           "等你处理 · 付款：“Place order”？",
		"Locked · unlock to continue Reorder lunch":       "已锁定 · 解锁后继续“Reorder lunch”",
		"You're in control · Claude Code is paused":       "由你操作 · Claude Code 已暂停",
		"Claude Code · Reorder lunch · in the background": "Claude Code · Reorder lunch · 在后台",
		"Claude Code · Reorder lunch":                     "Claude Code · Reorder lunch",
		"Idle":                                            "空闲",
	}
	for in, out := range status {
		if got := statusLine(gateway.DeviceView{StatusLine: in, Status: gateway.DeviceWorking}); got != out {
			t.Errorf("statusLine(%q) = %q, want %q", in, got, out)
		}
	}
}

// TestStepDisplayInChinese checks the client's words about steps.
func TestStepDisplayInChinese(t *testing.T) {
	inLanguage(t, "zh-Hans")
	step := func(text, status string, state gateway.TaskState) string {
		task := gateway.TaskView{State: state, Steps: []gateway.StepView{{Text: text, Status: status, Tool: "phone_tap"}}}
		text, sub, _ := stepDisplay(task, 0)
		if sub != "" {
			return text + " | " + sub
		}
		return text
	}
	for _, c := range []struct {
		text, status string
		state        gateway.TaskState
		want         string
	}{
		{"Waiting for you: Tapped “Pay”", "approval_pending", gateway.TaskWaiting, "等你确认：点按“Pay”"},
		{"Waiting for you: Tapped “Pay”", "approval_pending", gateway.TaskWorking, "你已回应：点按“Pay”"},
		{"Waiting for you: Tapped “Pay”", "approval_denied", gateway.TaskDone, "你拒绝了：点按“Pay”"},
		{"Waiting for you: Tapped “Pay”", "user_took_over", gateway.TaskPaused, "点按“Pay” | 未执行：你接管了手机"},
		{"Waiting for you: Tapped “Pay”", "stale", gateway.TaskDone, "点按“Pay” | 未执行：等待期间屏幕变了"},
		{"Asked you to take over: Sign in", "settled", gateway.TaskWorking, "你交还了手机 | 请求：“Sign in”"},
		{"Asked you to take over: Sign in", "approval_denied", gateway.TaskWorking, "你拒绝了 | 请求：“Sign in”"},
		{"Asked you: “Is 3pm fine?”", "settled", gateway.TaskWorking, "你同意了：“Is 3pm fine?”"},
		{"Tapped “Reorder”", "settled", gateway.TaskDone, "点按了“Reorder”"},
		{"Tapped “Reorder”", "not_found", gateway.TaskDone, "点按了“Reorder” | 失败：没有找到"},
		{"Opened Dinely\nYour note: “No”", "settled", gateway.TaskDone, "打开了 Dinely | 你的备注：“No”"},
	} {
		if got := step(c.text, c.status, c.state); got != c.want {
			t.Errorf("%q (%s, %s) = %q, want %q", c.text, c.status, c.state, got, c.want)
		}
	}
}

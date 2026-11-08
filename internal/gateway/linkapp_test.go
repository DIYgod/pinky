package gateway

import (
	"context"
	"sync"
	"testing"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

// browserPhone is a demo iPhone that opens links in Safari, as the iOS
// driver does.
type browserPhone struct {
	driver.Device
	mu     sync.Mutex
	opened []string
}

func (b *browserPhone) LinkApp() driver.App {
	return driver.App{ID: "com.apple.mobilesafari", Name: "Safari"}
}

func (b *browserPhone) Apps(ctx context.Context) ([]driver.App, error) {
	apps, err := b.Device.Apps(ctx)
	return append(apps, driver.App{ID: "com.apple.mobilesafari", Name: "Safari"}), err
}

func (b *browserPhone) Launch(ctx context.Context, app string) error {
	b.mu.Lock()
	b.opened = append(b.opened, app)
	b.mu.Unlock()
	return nil
}

func (b *browserPhone) Opened() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.opened...)
}

type browserProvider struct {
	*demo.Provider
	phone *browserPhone
}

func (p browserProvider) Open(ctx context.Context, id string) (driver.Device, error) {
	d, err := p.Provider.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	p.phone.Device = d
	return p.phone, nil
}

// A link is judged as an app of the app that opens it: blocking Safari
// blocks its links, allowing Safari for the task allows them again, and a
// link still asks as a link.
func TestLinkIsJudgedByTheAppThatOpensIt(t *testing.T) {
	ctx := context.Background()
	phone := &browserPhone{}
	dev := demo.NewDevice("iphone", "iPhone", driver.IOS)
	g, _ := newTestGateway(t, func(o *Options) {
		o.Providers = []driver.Provider{browserProvider{demo.NewProvider(driver.IOS, dev), phone}}
	})
	s, _ := connect(t, g, "Links")

	answer(t, g, AllowOnce, func(n NeedView) {
		if n.Rule != "Installs and links" {
			t.Errorf("need %+v", n)
		}
	})
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "https://example.com/"}), StatusSettled)
	if got := phone.Opened(); len(got) != 1 || got[0] != "https://example.com/" {
		t.Fatalf("opened %v", got)
	}

	if err := g.SetAppBlocked(screen.App{ID: "com.apple.mobilesafari", Name: "Safari"}, true); err != nil {
		t.Fatal(err)
	}
	res := s.Act(ctx, Call{}, Action{Tool: "launch", App: "https://example.com/other"})
	if res.Status != StatusBlocked && res.Status != StatusDenied || res.Rule != policy.RuleBlocked {
		t.Fatalf("a link in a blocked Safari: %+v", res)
	}
	if got := phone.Opened(); len(got) != 1 {
		t.Fatalf("the blocked link opened: %v", got)
	}
	if len(g.Needs()) != 0 {
		t.Fatalf("a blocked link asked: %+v", g.Needs())
	}

	// The person allows the blocked Safari for this task: its links open
	// again, and still ask as links.
	go func() {
		n := waitNeed(t, g)
		if !n.Unblock || n.Rule != policy.RuleBlocked {
			t.Errorf("unblock need: %+v", n)
		}
		g.Decide(n.ID, AllowTask, "")
	}()
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "Safari"}), StatusSettled)
	answer(t, g, AllowOnce, func(n NeedView) {
		if n.Rule != "Installs and links" || n.Unblock {
			t.Errorf("link need %+v", n)
		}
	})
	mustStatus(t, s.Act(ctx, Call{}, Action{Tool: "launch", App: "https://example.com/third"}), StatusSettled)
	if got := phone.Opened(); len(got) != 3 || got[2] != "https://example.com/third" {
		t.Fatalf("opened %v", got)
	}
}

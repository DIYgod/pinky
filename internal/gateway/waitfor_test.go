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

// ocrDev is a demo phone with text that only OCR reads find, as on a web
// page without accessibility elements.
type ocrDev struct {
	*demo.Device
	mu   sync.Mutex
	text string
}

func (o *ocrDev) Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error) {
	s, err := o.Device.Observe(ctx, opt)
	if err != nil {
		return s, err
	}
	o.mu.Lock()
	text := o.text
	o.mu.Unlock()
	if opt.OCR && text != "" {
		s.Elements = append(s.Elements, screen.Element{Role: screen.RoleText, Label: text, Source: screen.SourceOCR,
			Bounds: screen.Rect{X: 100, Y: 900, W: 400, H: 60}})
	}
	return s, nil
}

func (o *ocrDev) show(text string) {
	o.mu.Lock()
	o.text = text
	o.mu.Unlock()
}

func newOCRGateway(t *testing.T, dev *ocrDev) *Gateway {
	t.Helper()
	pol := policy.Default()
	for cat, e := range demo.PolicyApps() {
		pol.AddApps(cat, e)
	}
	g, err := New(Options{
		Providers: []driver.Provider{videoProvider{demo.NewProvider(driver.Android, dev.Device), dev}}, Policy: pol,
		TrustNewAgents: true, DataDir: t.TempDir(), SettleMax: 50e6, PollInterval: 5e6, DiscoverEvery: 3600e9,
		NoScreenshots: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

// phone_wait_for finds text that only OCR sees, and does not call it gone
// while OCR still sees it.
func TestWaitForReadsOCRText(t *testing.T) {
	ctx := context.Background()
	dev := &ocrDev{Device: demo.NewDevice("pixel", "Pixel 9", driver.Android)}
	g := newOCRGateway(t, dev)
	s, _ := connect(t, g, "Check the bill")
	dev.show("预估未出账单 ¥5,527.07")

	res := s.WaitFor(ctx, WaitForRequest{Text: "预估未出账单", TimeoutMS: 3000})
	mustStatus(t, res, StatusSettled)
	if res.Matched == nil || !*res.Matched {
		t.Fatalf("OCR text not found: %s", res.Message)
	}
	res = s.WaitFor(ctx, WaitForRequest{Text: "预估未出账单", Gone: true, TimeoutMS: 1500})
	if res.Matched == nil || *res.Matched {
		t.Fatalf("called gone while OCR still shows it: %s", res.Message)
	}
	dev.show("")
	res = s.WaitFor(ctx, WaitForRequest{Text: "预估未出账单", Gone: true, TimeoutMS: 3000})
	if res.Matched == nil || !*res.Matched {
		t.Fatalf("not gone after it went: %s", res.Message)
	}
}

// phone_wait_for matches whole words in Latin text (field test #24):
// waiting for the Stop button is not answered at once by the Stopwatch tab.
func TestWaitForMatchesWholeWords(t *testing.T) {
	ctx := context.Background()
	dev := &ocrDev{Device: demo.NewDevice("pixel", "Pixel 9", driver.Android)}
	g := newOCRGateway(t, dev)
	s, _ := connect(t, g, "Time the run")
	dev.show("Stopwatch")

	res := s.WaitFor(ctx, WaitForRequest{Text: "Stop", TimeoutMS: 300})
	mustStatus(t, res, StatusSettled)
	if res.Matched == nil || *res.Matched {
		t.Fatalf("“Stop” matched the Stopwatch tab: %s", res.Message)
	}
	res = s.WaitFor(ctx, WaitForRequest{Text: "Stop", Gone: true, TimeoutMS: 300})
	if res.Matched == nil || !*res.Matched {
		t.Fatalf("“Stop” not gone although only Stopwatch shows: %s", res.Message)
	}
	dev.show("Stop")
	res = s.WaitFor(ctx, WaitForRequest{Text: "stop", TimeoutMS: 3000})
	if res.Matched == nil || !*res.Matched {
		t.Fatalf("“stop” not found: %s", res.Message)
	}
}

func TestContainsText(t *testing.T) {
	for _, c := range []struct {
		label, text string
		want        bool
	}{
		{"Stopwatch", "Stop", false},
		{"Stop", "Stop", true},
		{"STOP", "stop", true},
		{"Stop timer", "Stop", true},
		{"Nonstop", "stop", false},
		{"Done.", "Done", true},
		{"Order placed", "placed", true},
		{"Your order was placed", "order was", true},
		{"Stopwatch, Stop", "Stop", true}, // the second occurrence is a word
		{"Wi-Fi", "Wi-Fi", true},
		{"Wi-Fi Calling", "Wi-Fi", true},
		{"PayPal", "Pay", false},
		{"Apple Pay", "Pay", true},
		{"Pay $5", "Pay", true},
		{"$50", "$5", false},
		{"Step 1 of 3", "1", true},
		{"15 min", "5", false},
		{"Order #12: ready", "#12", true},
		{"立即还款", "还款", true},
		{"预估未出账单 ¥5,527.07", "预估未出账单", true},
		{"お支払いを確定", "支払い", true},
		{"결제하기", "결제", true},
		{"Café ouvert", "café", true},
		{"Cafés", "café", false},
		{"", "Stop", false},
		{"Stop", "", false},
	} {
		if got := containsText(c.label, c.text); got != c.want {
			t.Errorf("containsText(%q, %q) = %v, want %v", c.label, c.text, got, c.want)
		}
	}
}

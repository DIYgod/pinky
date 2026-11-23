package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/DIYgod/pinky/internal/demo"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// A web page's screen can show more lines than a read keeps for the next
// screen. Swiped at its end, the page does not move: the read must see
// that screen as the one before, not take the lines it did not keep as new
// ones that share nothing with it (a gap).
func TestReadListEndsOnAScreenWithMoreLinesThanItKeeps(t *testing.T) {
	base := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	page := func(first int) []string {
		var lines []string
		for n := first; n < first+readListKeep+4; n++ {
			lines = append(lines, fmt.Sprintf("Line %03d", n))
		}
		return lines
	}
	dev := &readListDevice{Device: base, lineStep: 20, pages: [][]string{page(1), page(43)}}
	g := newDevGateway(t, base, dev)
	s, first := connect(t, g, "Read a list")
	listRef := 0
	for _, e := range first.Screen.Elements {
		if e.Role == screen.RoleList {
			listRef = e.Ref
		}
	}
	res := s.ReadList(context.Background(), ReadListRequest{
		Ref: listRef, MaxScreens: 6, MaxMS: 5000, OCR: "off", ScrollPercent: 60,
	})
	if res.IsError() || res.Scan == nil || res.Scan.Stop != "no_new_content" {
		t.Fatalf("read: %+v", res.Scan)
	}
	text := scanText(res.Scan)
	for n := 1; n < 43+readListKeep+4; n++ {
		if c := strings.Count(text, fmt.Sprintf("Line %03d", n)); c != 1 {
			t.Fatalf("Line %03d read %d times:\n%s", n, c, text)
		}
	}
}

// Read back up the page, the next screen overlaps this one at its top: the
// lines a read keeps of a screen that shows more must be those, or the
// overlap is lost and rows above it come twice.
func TestReadListBackwardKeepsTheTopOfATallScreen(t *testing.T) {
	base := demo.NewDevice("pixel", "Pixel 9", driver.Android)
	page := func(first int) []string {
		var lines []string
		for n := first; n < first+readListKeep+4; n++ {
			lines = append(lines, fmt.Sprintf("Line %03d", n))
		}
		return lines
	}
	dev := &readListDevice{Device: base, reverse: true, lineStep: 20, pages: [][]string{page(101), page(51), page(1)}}
	g := newDevGateway(t, base, dev)
	s, first := connect(t, g, "Read backwards")
	listRef := 0
	for _, e := range first.Screen.Elements {
		if e.Role == screen.RoleList {
			listRef = e.Ref
		}
	}
	res := s.ReadList(context.Background(), ReadListRequest{
		Ref: listRef, Direction: "down", MaxScreens: 3, MaxMS: 5000, OCR: "off",
	})
	if res.IsError() || res.Scan == nil || res.Scan.Stop == "gap" || res.Scan.Pages != 3 {
		t.Fatalf("read: %+v", res.Scan)
	}
	text := scanText(res.Scan)
	for n := 1; n < 101+readListKeep+4; n++ {
		if c := strings.Count(text, fmt.Sprintf("Line %03d", n)); c != 1 {
			t.Fatalf("Line %03d read %d times:\n%s", n, c, text)
		}
	}
}

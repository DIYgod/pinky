package screen

import (
	"fmt"
	"reflect"
	"testing"
)

func TestCollectViewportOrdersAndFilters(t *testing.T) {
	s := &Screen{
		Width: 1000, Height: 2000,
		Elements: []Element{
			{Role: RoleText, Label: "lower", Bounds: Rect{10, 700, 200, 50}},
			{Role: RoleText, Label: "outside", Bounds: Rect{10, 1900, 200, 50}},
			{Role: RoleText, Label: "  upper\n title ", Bounds: Rect{10, 300, 200, 50}},
			{Role: RoleText, Label: "safe\x00\x1b\u202e text", Bounds: Rect{10, 360, 200, 50}},
			{Role: RoleText, Label: "upper title", Bounds: Rect{12, 302, 196, 46}},
			{Role: RoleText, Label: "", Bounds: Rect{10, 100, 200, 50}},
			{Role: RoleText, Label: "hidden", Bounds: Rect{10, 500, 200, 50}, Hidden: true},
			{Role: RoleText, Label: "right", Bounds: Rect{500, 300, 200, 50}},
		},
	}
	got := ViewportTexts(CollectViewport(s, Rect{X: 0, Y: 200, W: 1000, H: 800}))
	want := []string{"upper title", "right", "safe text", "lower"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %#v, want %#v", got, want)
	}
}

func TestCollectListViewportOmitsContainerAndStaticLines(t *testing.T) {
	list := Element{Ref: 1, Role: RoleList, Label: "Feed", Bounds: Rect{0, 100, 1000, 1000}}
	first := &Screen{Elements: []Element{
		list,
		{Role: RoleText, Label: "Filters", Bounds: Rect{0, 100, 1000, 80}},
		{Role: RoleText, Label: "first item", Bounds: Rect{0, 200, 1000, 80}},
	}}
	second := &Screen{Elements: []Element{
		list,
		{Role: RoleText, Label: "Filters", Bounds: Rect{0, 100, 1000, 80}},
		{Role: RoleText, Label: "second item", Bounds: Rect{0, 200, 1000, 80}},
	}}
	a := CollectListViewport(first, list)
	b := FilterPinned(a, CollectListViewport(second, list))
	if got, want := ViewportTexts(a), []string{"Filters", "first item"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first = %#v, want %#v", got, want)
	}
	if got, want := ViewportTexts(b), []string{"second item"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second = %#v, want %#v", got, want)
	}
}

func TestFilterPinnedDoesNotModifyItsInput(t *testing.T) {
	previous := lines("Pinned controls", "first item")
	current := lines("Pinned controls", "second item")
	before := append([]ViewportLine(nil), current...)
	got := FilterPinned(previous, current)
	if want := []string{"second item"}; !reflect.DeepEqual(ViewportTexts(got), want) {
		t.Fatalf("filtered = %#v, want %#v", ViewportTexts(got), want)
	}
	if !reflect.DeepEqual(current, before) {
		t.Fatalf("FilterPinned modified its input: got %#v, want %#v", current, before)
	}
}

func TestPinnedLinesReturnsOnlyStationaryContent(t *testing.T) {
	previous := []ViewportLine{
		{Text: "Pinned controls", Bounds: Rect{X: 0, Y: 20, W: 100, H: 40}},
		{Text: "moving item", Bounds: Rect{X: 0, Y: 200, W: 100, H: 40}},
	}
	current := []ViewportLine{
		{Text: "Pinned controls", Bounds: Rect{X: 0, Y: 20, W: 100, H: 40}},
		{Text: "moving item", Bounds: Rect{X: 0, Y: 80, W: 100, H: 40}},
	}
	if got, want := ViewportTexts(PinnedLines(previous, current)), []string{"Pinned controls"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pinned = %#v, want %#v", got, want)
	}
}

func TestAppendViewportMergesContiguousOverlap(t *testing.T) {
	previous := lines("alpha", "beta", "gamma", "delta")
	current := lines("gamma", "delta", "epsilon", "zeta")
	fresh, overlap := AppendViewport(previous, current)
	if overlap != 2 {
		t.Fatalf("overlap = %d, want 2", overlap)
	}
	if got, want := ViewportTexts(fresh), []string{"epsilon", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh = %#v, want %#v", got, want)
	}
}

func TestAppendViewportKeepsLegitimateRepeatedText(t *testing.T) {
	previous := lines("same headphones", "red cover", "same headphones", "blue cover")
	current := lines("same headphones", "blue cover", "new controller", "same headphones")
	fresh, overlap := AppendViewport(previous, current)
	if overlap != 2 {
		t.Fatalf("overlap = %d, want 2", overlap)
	}
	if got, want := ViewportTexts(fresh), []string{"new controller", "same headphones"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fresh = %#v, want %#v", got, want)
	}
}

func TestAppendViewportFallsBackWithoutUsefulAnchor(t *testing.T) {
	fresh, overlap := AppendViewport(lines("old"), lines("new"))
	if overlap != 0 || !reflect.DeepEqual(ViewportTexts(fresh), []string{"new"}) {
		t.Fatalf("fresh=%#v overlap=%d", ViewportTexts(fresh), overlap)
	}
}

func TestAppendViewportDoesNotMergeOnOnlyOneShortBoundaryLine(t *testing.T) {
	fresh, overlap := AppendViewport(lines("same"), lines("same", "new"))
	if overlap != 0 || !reflect.DeepEqual(ViewportTexts(fresh), []string{"same", "new"}) {
		t.Fatalf("fresh=%#v overlap=%d", ViewportTexts(fresh), overlap)
	}
}

func TestAppendViewportMergesOneUniqueLongBoundaryLine(t *testing.T) {
	shared := "a unique article title long enough to identify one row"
	fresh, overlap := AppendViewport(lines(shared), lines(shared, "new article"))
	if overlap != 1 || !reflect.DeepEqual(ViewportTexts(fresh), []string{"new article"}) {
		t.Fatalf("fresh=%#v overlap=%d", ViewportTexts(fresh), overlap)
	}
}

func TestUnseenPrefixDetectsPrependedContent(t *testing.T) {
	previous := lines("b long title", "c long title", "d long title")
	if !UnseenPrefix(previous, lines("new long title", "b long title", "c long title", "d long title", "e long title")) {
		t.Fatal("new line before the overlap was not detected")
	}
	if UnseenPrefix(previous, lines("b long title", "c long title", "d long title", "e long title")) {
		t.Fatal("ordinary suffix-only growth was treated as prepended content")
	}
}

func TestUnseenPrefixAcceptsALongAccessibilityFragment(t *testing.T) {
	long := "a detailed article title that a platform may expose by itself when the rest of the row leaves the viewport"
	previous := lines(long+" author name time", "next long title", "third long title")
	current := lines(long, "next long title", "third long title", "new long title")
	if UnseenPrefix(previous, current) {
		t.Fatal("a long fragment of the previous row was treated as new content")
	}
}

func TestSameViewportStopsOnlyForSameOrder(t *testing.T) {
	a := lines("one", "two")
	if !SameViewport(a, lines("ONE", "two")) {
		t.Fatal("case-insensitive equal viewport not detected")
	}
	if SameViewport(a, lines("two", "one")) {
		t.Fatal("reordered content counted as the same viewport")
	}
}

func TestViewportEndsAtIsExplicit(t *testing.T) {
	for _, text := range []string{"没有更多了", "No more results", "You've reached the end"} {
		if !ViewportEndsAt(lines(text)) {
			t.Errorf("%q not recognised", text)
		}
	}
	if !ViewportEndsAt(lines("—— 已经到底了 ——")) {
		t.Fatal("a decorated end marker was not recognised")
	}
	if ViewportEndsAt(lines("Recommended for you")) {
		t.Fatal("an ordinary recommendation was treated as the end")
	}
	if ViewportEndsAt(lines("No more worries")) {
		t.Fatal("an ordinary item was treated as the end")
	}
}

func TestAppendViewportKeepsOneHundredItemsInOrder(t *testing.T) {
	all := make([]string, 100)
	for i := range all {
		all[i] = fmt.Sprintf("item %03d", i)
	}
	var got []string
	var previous []ViewportLine
	pages := 0
	for start := 0; start < len(all); start += 5 {
		end := min(start+7, len(all))
		if start > 0 && start+5 < len(all) {
			end = start + 7
		}
		current := lines(all[start:end]...)
		fresh, _ := AppendViewport(previous, current)
		got = append(got, ViewportTexts(fresh)...)
		previous = current
		pages++
	}
	if pages != 20 {
		t.Fatalf("pages = %d, want 20", pages)
	}
	if !reflect.DeepEqual(got, all) {
		t.Fatalf("assembled %d items, want 100 in order: %#v", len(got), got)
	}
}

func TestNewViewportContentPreservesMultiplicityAtTheEnd(t *testing.T) {
	previous := lines("same long product title", "first unique long title", "second unique long title")
	current := lines("same long product title", "second unique long title", "same long product title", "new long final title", "end of list")
	if got, want := ViewportTexts(NewViewportContent(previous, current)),
		[]string{"same long product title", "new long final title", "end of list"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("new content = %#v, want %#v", got, want)
	}
}

func lines(texts ...string) []ViewportLine {
	out := make([]ViewportLine, len(texts))
	for i, text := range texts {
		out[i] = ViewportLine{Text: text, Role: RoleText, Source: SourceTree}
	}
	return out
}

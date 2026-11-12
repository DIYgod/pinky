# Reading a list in one call

`phone_read_list` turns the common "read these screens and summarise them" task into one agent call. The agent names the list, how far to read, and its time budget. The gateway scrolls inside that list, collects the text exposed after each screen settles, removes the contiguous overlap with the previous screen, and returns compact page-labelled text. The outer agent summarises that text; Pinky does not embed a model.

## Pipeline

1. Read the current screen without OCR. If no `ref` was passed, choose the largest list or scrollable element that covers at least a quarter of the screen.
2. Collect labelled elements whose centres are inside the list, ordered by y then x.
3. If the element tree inside the list is sparse, read the screen again with local OCR. `ocr: "all"` always asks for pixel-only text; `ocr: "off"` keeps the tree-only path.
4. Before each swipe, test several vertical lanes inside the list through the normal policy. A lane that would start on a protected control is skipped. The scan stops rather than starting a swipe when no safe lane exists.
5. Drag a configurable 30–80% of the visible list height for one second and wait for the existing settle detector. The deliberate drag avoids the extra inertia of a short fling, which can otherwise skip more than a viewport.
6. Compare adjacent viewports by contiguous line runs, not a global text set. This removes ordinary scroll overlap while keeping legitimate duplicate titles at different positions. Fixed controls are tracked separately so a changing nested-list boundary does not make the same toolbar appear as new text.
7. If continuous video or decoration keeps the pixels moving, compare two element viewports and accept the result when their text and order are stable.
8. Stop at an explicit end marker, two unchanged screens, the per-call screen limit, the time limit, output limit, a gap with no overlap, lock, Stop, takeover, blocked app, app/list change, or an unstable screen. At an explicit end, an accessibility tree may regroup or reorder an overlapping row; Pinky subtracts equal text by multiplicity and preserves extra occurrences before declaring the list complete. Anything already read is returned.

Each call records one history step. A partial result carries a continuation bound to the task, phone, app, list, scroll area and direction; `max_screens` is a fresh budget on each resumed call. It is not a replay of the task recording: reads use only the element tree, local OCR from policy-checked live frames, and the existing screen-capture path. A task MP4 is never used as input.

## Platform support

- Android through scrcpy works on the phone's display. On Android 11+ background work, the list has no `screencap`; OCR uses the existing live-video frame path.
- iPhone and iPad on iOS 27+ through CoreDevice use the same gateway path.
- iOS 17.4–26 needs WebDriverAgent for swipes. Without that helper, the phone can be watched and apps opened, but this tool returns `unsupported` at the swipe.
- Linux without a local OCR engine can still use the tree-only path. Pixel-only content is not guessed; use `ocr: "all"` on macOS or Windows, where the local recognizer is available.
- A page that does not expose a list or scrollable element can be read with an explicit `scroll_area` rectangle in screen pixels. Without either an element or area, the tool reports `not_found`; it does not guess a region and risk scrolling the wrong control.

## Safety

- The list and every swipe still pass the gateway's lease, lock, takeover, app-scope and policy checks.
- A blocked app contributes no text unless the person allowed it for this task. When allowed, text may be read while screenshots and recordings remain withheld.
- A blocked app allowed only for the task is returned to the agent but its transcript is also withheld from stored task history.
- One-time codes are masked before collection.
- A scroll that could begin on a protected control stops the scan. If the chosen lane has no labelled element at all, Pinky asks OCR before swiping so a pixel-only control cannot be mistaken for blank space.
- `max_ms` is at most 45 seconds. A partial result is explicit and must not be described as a complete list unless `complete: true`.

## Simulator measurement

Measured on 2026-11-10, macOS, an iPhone 16 Pro Simulator running iOS 18.6 with WebDriverAgent 16.15.3. Each run reset the app and started at the top of the logged-in `tech.ycyx.dev.yinchao` Explore feed:

| Path | Work | Pinky time |
| --- | --- | --- |
| Existing `phone_swipe` loop | 20 requested screens; 19 full action pipelines | 1:47.306 |
| `phone_read_list` gateway path | 3 screens, 2 swipes, 46 new text lines; stopped at the explicit end | 16.222 s |

The existing loop averaged 5.648 s per action. `phone_read_list` used one tool call and its Pinky-side elapsed time was 6.61 times shorter. The list read also stopped as soon as the feed exposed its end instead of asking for 20 screens regardless. This comparison excludes the outer agent's thinking time: the old flow also has a model round trip between screens, while the new flow needs only the final summary.

The gateway comparison is in `internal/gateway/read_list_sim_test.go` behind the `wdasim` build tag. It is a manual smoke test: open Explore at the top and set `PINKY_TEST_EXPLORE_READY=1`; without that prerequisite it skips instead of treating stale app state as evidence. The baseline deliberately keeps reading 20 times even after the feed reaches its end, showing the work the dedicated scan avoids. No Android device was used for this measurement; Android support follows the shared gateway and driver path but still needs a device run.

## Fixed-content benchmark

`internal/gateway/read_list_fixture_sim_test.go` serves a fixed 100-item mobile page from a local test server and runs the same first seven screens through both paths, with a 30% and one-second swipe:

| Path | Work | Pinky time | Calls |
| --- | --- | ---: | ---: |
| Existing `phone_swipe` loop | 7 requested screens, 6 actions | 52.312 s | 6 |
| `phone_read_list` | 7 screens, 6 swipes | 38.745 s | 1 |

The fixed fixture was 1.35 times faster on Pinky's side and preserved the first five visible article titles once each. The same page through the public MCP tool (`internal/mcp/read_list_sim_test.go`) returned seven screens and 13 new lines in 41.098 s and preserved each of the first five titles once. This run is short but fully repeatable. The larger end-to-end gain on personal feeds comes from removing the model round trip after every screen, which these gateway timers deliberately exclude.

## Public mobile-web feeds

The same comparison was also run against two pages in Safari where the accessibility tree exposes the content but no platform list element. In those cases the test passes an explicit `scroll_area`; there is no site-specific code. Every old and new path used a fresh URL navigation, the same rectangle, the same viewport travel and the same one-second drag. This removes gesture speed as an explanation for the result:

| Site | Common coverage | Existing swipe loop | `phone_read_list` | Calls |
| --- | --- | ---: | ---: | ---: |
| Bilibili home feed | 4 screens, 3 swipes, 30% travel | 17.004 s | 12.226 s | 3 → 1 |
| 36Kr mobile article feed | 7 screens, 6 swipes, 60% travel | 44.252 s | 29.479 s | 6 → 1 |

The Pinky-side speedups were 1.39 times on the short Bilibili range and 1.50 times on the longer 36Kr range. The old times count the full per-action read, policy, swipe, settle, post-read and diff pipeline. A longer 36Kr run stopped safely at a content gap after seven screens; the seven-screen result above is the final contiguous range rather than a claim about every article below it. Times exclude the outer model's thinking time and the initial screen read common to both paths. The larger product benefit is the removed agent round trips: six actions become one call, while the transcript is already kept in order and deduplicated.

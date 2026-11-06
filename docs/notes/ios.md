# iOS driver notes

Status of `internal/driver/ios`, the CoreDevice backend, and what step 0 found on a real iPhone. Measured on 2026-11-07 with an iPhone 13 mini (iPhone14,4) on iOS 27.0.1 (24A446), connected by USB to a Mac with macOS 27.2 and Xcode 27.2; the developer disk image was mounted and Developer Mode on.

## Step 0 results

| Item | Result | Numbers |
| --- | --- | --- |
| Tunnel without root | **Works.** A userspace tunnel in Pinky's own process, next to macOS's CoreDevice tunnel | Tunnel up in 0.25–0.35 s, RSD handshake 0.13–0.15 s, 85 services, MTU 16000 |
| Screen stream | **Works.** HEVC over RTP through the userspace tunnel, depacketized to Annex B | 1136x2464 HEVC Main, up to 60 fps, 1–3 Mbit/s typical (cap 6), first packet 0.6 s after the start request, keyframe 70 ms after a request, 0 packets lost over USB |
| Touch | **Works.** Tap, swipe, long press and raw touches through the universal HID service, on one long-lived session | Touch-down to the first changed video frame: 100–131 ms, median 109 ms (still Settings page) |
| Keys | Home **works** (Indigo button event); Back is a left-edge swipe and **works**; Recents returns `unsupported` | |
| Text | **Works for printable ASCII with an English keyboard active**, through a virtual hardware keyboard. The keys go through the phone's current input method, as a real keyboard's would: with Pinyin active (as on the test phone) the letters become an uncommitted Pinyin composition. Other characters return `unsupported` | 25–40 ms per character |
| Element tree (pass/fail) | **Fail for frames, pass for labels and roles.** No element frames over CoreDevice on iOS 27 (same as go-ios found on 18.4.1). Labels and roles in 30–60 ms per element | `Observe` with OCR: 0.8 s (Settings page, 10 elements), 1.0 s (Clock, 13), 1.3 s (Settings, 38; Calculator, 24), 1.4–1.9 s (home screen, 26) |
| OCR placement | **Works as a stopgap.** macOS Vision through purego places elements whose label is on screen | Vision 0.1–0.4 s per screenshot; 9 of 10, 12 of 13, 18 of 26 elements placed on Settings, Clock, home screen |
| Screenshot | **Works**, two services with fallback | 1125x2436 PNG, 0.35 s (Settings) to 0.6–0.75 s (home screen, 5–7 MB) over USB |
| Locked phone | **Not tested.** Locking the phone is not allowed in these sessions | |
| Windows and Linux | **Not tested.** The package cross-compiles without cgo for both | |

What these mean for the design:

- The userspace tunnel removes the "tunnel may need administrator rights" prerequisite on every OS: no utun, no wintun, no sudo. It still needs usbmuxd (built into macOS; Apple Devices on Windows; the `usbmuxd` package on Linux).
- The element tree is the weak part, as feared. Labels and roles are good and fast enough; frames are not available from the inspector API on a real phone. OCR places most elements that show text. Two better sources exist and are not done yet: the `remoteAXService` that Xcode 27's Device Hub uses (see "Element frames: next step") and WebDriverAgent.
- USB matters: an iPhone that also syncs over Wi-Fi is listed twice by usbmuxd, and a tunnel over Wi-Fi was several times slower (screenshots 2–5 s instead of 0.6 s, sessions 1–7 s instead of 0.3 s, lost video packets). The driver now always picks the USB entry; Wi-Fi works but is not tuned.

## What is built

`internal/driver/ios` implements `driver.Provider`, `driver.Device`, `driver.Toucher`, `driver.LockReporter` and `driver.Settler`.

| File | What |
| --- | --- |
| `tunnel.go` | Userspace CoreDevice tunnel: the `CoreDeviceProxy` lockdown service carries bare IPv6 packets; a gVisor netstack (TCP and UDP) runs them in-process. go-ios dials services through a 127.0.0.1 forwarder that speaks go-ios's userspace-tunnel preamble and only reaches the device's tunnel address |
| `conn.go` | Tunnel plus RSD handshake. The first handshake announces the host (`HandshakeAsHost`), once per tunnel, as iOS 27.2 requires |
| `coredevice.go` | CoreDevice RemoteXPC requests, always with protocol 629.3 / DDI version 2 (some features refuse the old version), with dial and request timeouts |
| `offer.go`, `display.go` | The media stream offer (adapted from go-ios, MIT) and start/stop. Payload type 100's feature list is `FLS;SW:1;` instead of Xcode's `FLS;VRAE:0;SW:1;`: VRAE:0 makes the encoder drop frames under motion (pymobiledevice3's measurement) |
| `rtp.go`, `sps.go` | RTP parsing; RFC 7798 HEVC (single NAL, AP, FU; PACI skipped) and RFC 6184 H.264 (single, STAP-A, FU-A) depacketizers; the codec is sniffed from the NAL header because payload type 100 carries HEVC. The display service's 14-byte slice trailer is stripped. SPS parsers for the picture size |
| `media.go` | The long-lived stream: access-unit assembly, `driver.Packet`s (a config packet before each keyframe, PTS from the 24 kHz RTP clock), RTCP receiver reports every second (the device stops after 10 s without them), a per-frame receipt like Xcode's, picture loss indications when a subscriber needs a keyframe (repeated every 600 ms until one comes), loss handling, fan-out to `Stream` subscribers |
| `hid.go`, `keys.go` | Universal HID touch reports (surface 257), Indigo button events (Home), a virtual HID keyboard registered with `createService`, ASCII to US-layout usages |
| `device.go` | The session: one tunnel, one media stream, one HID connection; taps, swipes and raw touches in screenshot pixels mapped to the panel's 0..65535 range |
| `ax.go`, `caption.go`, `observe.go` | The accessibility walk, caption parsing into label, role and states, OCR placement, keyboard detection, the foreground app |
| `ocr.go`, `ocr_darwin.go`, `ocr_other.go` | Vision text recognition through purego (no cgo); other OSes return `unsupported` |
| `screenshot.go`, `apps.go`, `lock.go`, `settle.go`, `provider.go`, `display_info.go` | The rest of the interface |
| `diag.go`, `cmd/iosctl` | Diagnostics and the debugging command |

Coordinates are screenshot pixels: 1125x2436 on an iPhone 13 mini, which is the size UIKit renders (375x812 points at 3x); the panel is 1080x2340 and the video 1136x2464. The mirror should map its pixels to that space, not to the video's.

### Decisions

- **One media stream for the whole session.** HID input is dropped unless a stream from the same client runs, so `Open` starts the stream and keeps it; `Stream` returns a subscriber view that starts at a fresh keyframe, and closing it does not stop the device's stream. Restarting streams often can wedge the device's media daemon until a reboot (pymobiledevice3 reports it).
- **Own display, HID and CoreDevice clients on top of go-ios's transport** instead of go-ios's `display` and `hid` packages: go-ios discards the stream answer (SSRCs and the RTCP port, needed for receiver reports), refuses userspace tunnels for media, and its HID package has no buttons or keyboard.
- **go-ios from main** (`v1.3.3-0.20261103131045-6910f3b585ad`): the display and HID packages and the RSD host announcement are not in a release yet. Pin a release once one has them.
- **HID timestamps in 24 MHz ticks.** go-ios and pymobiledevice3 stamp reports in nanoseconds. With those, a 12 ms key press opened the press-and-hold accent menu (as if held for half a second) and swallowed the rest of the text; stamping in 24 MHz ticks, the phone's mach time unit, fixed it. So the phone reads mach ticks and nanoseconds make every interval look 41.7 times longer. Touch looked fine only because paging goes by distance; fling velocities were affected too.
- **Roles from captions.** The audit daemon answers attribute queries (label, value, traits) only for the element holding the inspector focus, which would cost a round trip per element on top of the move. The walk keeps its moves pipelined and parses VoiceOver's caption ("Alarms, Button, Selected, Tab") with English and Simplified Chinese trait words; other languages get the whole caption as the label and the role `other`.
- **Foreground app from the elements.** An element handle's first four bytes are the owning process id; the process list (`listprocesses`) and the installation proxy's bundle paths turn it into a bundle id and name. The daemon's app-state events came once and never again, so they are only a fallback.
- **Screenshots from the instruments service first**, CoreDevice's screen capture second. Both got stuck on the device at times (most likely after a test process was killed mid-request); the fallback and dial timeouts keep calls from hanging.
- **Lock state from lockdown's `PasswordProtected`**, the value `devicectl` reports as `passcodeRequired`. iOS 27's developer disk image does not implement CoreDevice's `getlockstate` (the feature is listed but `dtdeviceinfod` has no implementation).

## How to run

Hold the device lock for every command that touches the phone.

```sh
go build -o /tmp/pinky-iosctl ./internal/driver/ios/cmd/iosctl
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> rsd
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> observe -ocr
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> stream -seconds 10 -out /tmp/pinky-stream.hevc
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> latency -vertical      # on a still list such as Settings
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> tap 285 2283
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> swipe 900 1218 225 1218 -ms 300
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> key home
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> type -append hello
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> launch Calculator
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> screenshot -out /tmp/pinky-shot.png
```

`iosctl devices` lists every phone usbmuxd sees and opens a tunnel to each to check its services, so do not run it while other sessions use their phones. Keep captures under `/tmp/pinky-*` and delete them afterwards.

Tests: `go test ./internal/driver/ios/` runs the unit tests (RTP and SPS parsing, depacketizers, access-unit assembly, the offer against a captured one, caption parsing, roles, keyboard detection, OCR placement, HID reports) without a phone. The real-phone tests share one session and use only the home screen, Settings, Calculator and Clock:

```sh
PINKY_DEVICE=<udid> scripts/devlock.sh <udid> go test -tags device -count=1 -v ./internal/driver/ios/
```

All of them pass on the iPhone 13 mini (inspect, stream, home screen observe, swipe and raw touch, apps, launch Settings by name, Calculator tap, screenshot, lock state, unsupported keys, latency).

## Details and data

### Tunnel

- The device gives each tunnel its own /64 (macOS's own tunnel was `fd3e:…`, ours `fd4f:…`), so both coexist. macOS 27's own tunnel is not routable from other processes (no utun route), so Pinky cannot borrow it.
- The device grants the MTU asked for (16000), which keeps per-packet overhead low. Over USB a 6.9 MB screenshot arrives in about 0.75 s including the phone's PNG encoding, so the userspace stack moves at least 9 MB/s; gVisor's TCP receive autotuning is on.
- `HandshakeAsHost` once per tunnel matters (go-ios's finding, not re-tested here): without the host's RemoteXPC version flags, services that send large replies (screenshots) crash on the device.

### Video

- The answer's `streamConfig`: `RxPayloadType` 100 (HEVC inside), `Framerate` 60, `KeyFrameInterval` 0 (no periodic keyframes), `RXMaxBitrate` 6000000, `RTCPTimeoutEnabled` with 10 s, `CustomWidth`x`CustomHeight` 1136x2448 while the SPS says 1136x2464 (the SPS is right; ffprobe agrees).
- The phone also creates a virtual "Wireless" display (id 2, 1136x2448) for the stream.
- RTCP is multiplexed on the receiver port; the phone sends a sender report about every second. Its NTP time against our arrival gives 55–95 ms one way, if both clocks agree.
- After a change the phone sends 60 fps for about 5–7 s, then stops sending frames until the screen changes again (a status bar clock tick restarts them); its RTCP sender reports keep coming every second throughout. While frames do come, a still screen gives identical frame sizes and motion varying ones (5–75 KB). The size of a still frame depends on the encoder's current bitrate: about 1–2 KB when idle, but after motion the encoder raises its bitrate and pads still frames to a constant 10 KB for seconds. So "no frames, or frames of one size" marks a still screen, not small frames; the settle detector uses that (step 0 item 4). The stream's watchdog counts RTCP too: only silence on both for 5 s means the phone stopped the stream.
- A subscriber that joins while frames have stopped gets the parameter sets and a fresh keyframe within about 25 ms of its request.
- Settle data from Settings: tapping "General" changed the first frame 331 ms after touch-down (the tap is held 60 ms, so about 270 ms after touch-up, including the app's own response); the push animation's frames were 58–74 KB; frame sizes became constant about 2.3 s after the tap. A fling in a list keeps the picture moving for about 2.5 s (measured as frame-to-frame pixel difference in the decoded video). Home screens with widgets (Photos, Clock) never look still.
- The audit daemon's screen and layout change notifications arrived in one session and not in others, so they are only a hint for settling.
- Without loss there are no keyframes after the first. Subscribers that join later ask for one; the phone ignores a request that comes right after a keyframe, hence the retry.
- Packet loss happened only over Wi-Fi and while large screenshots shared the tunnel; the gVisor UDP buffer never overflowed.

### Input

- Touch positions are 0..65535 on each axis of the panel in its native (portrait) orientation. Landscape is not handled yet: the mapping assumes `rot0`.
- `Key(back)` swipes from the left edge at mid-height over 250 ms. It starts at x=0 so the system gesture wins; a stray drag cannot select anything, but avoid screens where a drag at mid-height matters.
- The virtual keyboard makes iOS treat the phone as having a hardware keyboard while the session lasts; the software keyboard still showed in Settings. It goes away when the HID connection closes. `Type` without `Append` sends Command-A and Delete first.
- Keys go through the active input method. On the test phone the Pinyin keyboard was active: "clock" became a composition (Command-A did not select it, one Delete left "c lo c" with U+2006 spaces between the syllables), and Escape appeared to cancel it. On Pinyin, Return commits the letters as typed and a space picks a Chinese candidate (how the keyboard behaves; not tried here). So `Type` is right only with an English (or other Latin) keyboard active, and `Submit`'s Return only commits a composition. Fixes to choose from: paste through CoreDevice's pasteboard service and Command-V (handles CJK too, but overwrites the person's clipboard unless it is saved and restored, which means reading it: a decision for the owner), or switching the input source with the keyboard's globe or Control-Space and back (changes the person's keyboard).

### Element tree

- The walk: `deviceInspectorFocusOnElement:` then `deviceInspectorMoveWithOptions:` (first, then next) in batches of 8, until an element repeats; it covers the visible app including off-screen cells of scroll views (Settings lists all 38 rows, only the visible ones get positions), but not the keyboard (another process) or the status bar. It does not scroll the app.
- Per element: caption (label, value, traits, hint as VoiceOver speaks them), accessibility identifier, an opaque handle. Captions are localized.
- Tried for frames on iOS 27, all empty: attributes `Frame`, `AXFrame`, `frame`, `accessibilityFrame`, `Bounds`, `Position`, `Size`, `ElementRect`, `AXAttribute-2003` and more; hit testing (`deviceFetchElementAtNormalizedDeviceCoordinate:` with an `NSValue` point) answers nothing; `deviceFetchSpecialElement:` 0 and 1 return the first and last element, not the app. The audit (`deviceBeginAuditTypes:`) reports rects only for flagged elements (8 "element detection" issues on the home screen, 3.9 s).
- Side effect found and fixed: the inspector focus draws a ring on the focused element (visible in the video) until `deviceInspectorPreviewOnElement:` is sent with null, which every walk now does.
- A null element handle in `deviceElement:valueForAttribute:` broke the daemon connection; never send one.

### Element frames: next step

Xcode 27's Device Hub makes the phone's screen accessible to VoiceOver on the Mac, so it needs frames. It uses `com.apple.accessibility.axAuditDaemon.remoteAXService` (in `DeviceKit.framework`, `DeviceAccessibilityView`; the client is `AXAuditRemoteDevice` in Xcode's `AccessibilityAudit.framework`). Over the tunnel it is a DTX service (`AXAuditDevicesAppRemoteServer`) that answers `deviceAPIVersion` (26) and accepts `processDataFromHost:` with an `NSData`. The data is AccessibilityPlatformTranslation's transport (`AXPTranslatorRequest` and responses, as in iPhone Mirroring), which asks for attributes, frames included, element by element. Reverse engineering that format is the most promising way to real frames without installing anything; WebDriverAgent (installed on this phone) is the fallback the design already plans.

To reach it, open the tunnel and RSD as `dialLink` does, then `dtx.NewTunnelConnection(l.entry, "com.apple.accessibility.axAuditDaemon.remoteAXService")` (go-ios `ios/dtx_codec`), send `_notifyOfPublishedCapabilities:` on the global channel and call methods there; `deviceAPIVersion` answers 26. The probe that found this was a throwaway test and is not on the branch.

What the Mac's own framework shows (read at run time through the Objective-C runtime; the classes live in the dyld shared cache, `/System/Library/PrivateFrameworks/AccessibilityPlatformTranslation.framework`), all `NSSecureCoding`:

| Class | Properties |
| --- | --- |
| `AXPTranslatorRequest` | `translation` (`AXPTranslationObject`), `requestType`, `attributeType`, `actionType` (unsigned), `parameters` (`NSDictionary`), `clientType` |
| `AXPTranslatorResponse` | `resultData`, `attribute`, `notification`, `associatedRequestType`, `associatedTranslationObject`, `associatedNotificationObject`, `error`; derived `translationResponse`, `translationsResponse`, `boolResponse`, `treeDumpType`, `treeDumpResponse` |
| `AXPTranslationObject` | `pid`, `objectID`, `isApplicationElement`, `bridgeDelegateToken`, `rawElementData`, `remoteDebugDescription` |

A suggested plan: on macOS, drive Apple's `AXPTranslator` through purego with a transport whose send path calls `processDataFromHost:` and whose receive path is fed by the device's callbacks (that is what `AXAuditRemoteDevice` does), then read the translated elements' frames. That avoids decoding the archive format by hand on the Mac; Windows and Linux would need the format reimplemented with go-ios's NSKeyedArchiver. The enum values (request and attribute types) and the transport envelope are still unknown; a capture of Device Hub's traffic over its own tunnel, or the `AXAuditRemoteDevice` code in Xcode's `AccessibilityAudit.framework`, would give them.

### OCR

- Vision's accurate recognizer with `zh-Hans`, `zh-Hant`, `en-US`: 0.1–0.4 s per 1125x2436 screenshot once loaded. The first use on a Mac took 56 s (model compile), and runs with automatic language detection sometimes took 15 s, so detection is off.
- Placement matches labels to text in rounds (equal, truncated with an ellipsis, label inside a line, line inside the label) in reading order. Text that appears twice can be matched to the wrong element (a search field whose value equals a result title); elements without visible text (icons, dock apps, images) get no position.
- The on-screen keyboard is detected from OCR (five or more single letters in the lower half) and its keys are dropped from the output.

## What does not work yet

- Element frames from the tree (above). OCR positions are text boxes, not element bounds.
- Landscape: touch mapping and the screen size assume portrait.
- Text outside printable ASCII, and ASCII while a non-Latin input method is active (above).
- Wi-Fi-only phones: work, untuned.
- Lock detection for phones without a passcode, and everything about locked phones (not testable here).
- iOS below 27 (needs WebDriverAgent), Windows, Linux (cross-compiles, untested).
- `WaitSettled` uses frame sizes (and accessibility notifications when they come); animated widgets keep it from ever settling on such home screens (it times out), and after motion the encoder's slowly falling bitrate can keep it waiting a little after the picture stopped.

## Security notes

- While a session runs, the tunnel's forwarder listens on 127.0.0.1 and lets any local process reach the phone's developer services through it (go-ios's own userspace tunnel does the same). It adds nothing an agent with shell access could not already do through usbmuxd, which the threat model covers, but the gateway's isolation advice applies to it too.
- Media streams make the phone show its screen-sharing indicator and block its camera and microphone; `Close` stops the stream, and the phone also stops it by itself 10 s after receiver reports stop (a crashed process).
- The virtual keyboard and any held finger are released on `Close`. A killed process cannot release them; the phone should drop them when the HID connection closes with the tunnel, but that is not tested.

## Notes for other sessions

- `go.mod` gains go-ios (main), gVisor and purego; expect a merge in `go.mod`/`go.sum`.
- No shared contract changed.
- For the gateway and the client: `Stream` packets are HEVC Annex B; a `Config` packet (VPS, SPS, PPS) comes before every keyframe a subscriber receives; PTS counts from the stream's start on a 24 kHz clock. A new subscriber waits up to about 100 ms for a requested keyframe. No frames come while the screen is still (from about 7 s after the last change), so the mirror keeps showing the last frame and recordings have gaps. Screen coordinates are screenshot pixels (1125x2436 here), not video pixels (1136x2464).
- `WaitSettled` returns no sooner than 500 ms after the call, once the last 250 ms brought frames of one size, or no frames while the stream is alive; a fling in a list takes 2–3 s to stop, and animated widgets never stop.
- During testing `iosctl devices` probed every phone usbmuxd listed, which included the owner's iPad (iPad13,4, over Wi-Fi): one tunnel and one RSD handshake, nothing else.
- The phone was left on its home screen; Settings was only browsed (back navigation, scrolling, searches for "wallpaper" and "clock" that were cancelled). Once a test's cleanup tap landed on a search result and opened Settings > Apps > Clock; nothing there was touched (its Mobile Data switch stayed on), and the test no longer taps after typing.
- The on-screen keyboard check from OCR needs single letters spread across the screen: a list's alphabetical section index (Settings > Apps) fooled the first version.

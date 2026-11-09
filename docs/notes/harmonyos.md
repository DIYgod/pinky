# Native HarmonyOS through HDC

Native HarmonyOS exposes HDC USB (0xff/0x50/0x01), not Android ADB
(0xff/0x42/0x01). The native provider discovers and opens these phones
independently. Android-based Huawei phones keep the Android provider.

## Setup

Install the official HarmonyOS/OpenHarmony SDK's host toolchains. Pinky
finds hdc through PINKY_HDC, PATH, an app's Contents/Helpers, HDC_SDK_PATH,
HDC_HOME, OHOS_SDK_HOME, or HOS_SDK_HOME. Keep the matching libusb_shared
library next to it. Pinky downloads nothing and never restarts a shared
HDC server. macOS also discovers the USB interface without an SDK.

Enable USB debugging and trust the computer if asked. The initial listing
may briefly show Unauthorized during the handshake; subsequent discovery
updates it. An already trusted computer needs no new prompt. Add phone
replaces the ADB switches and Companion steps with the native state.

An authorized phone with UITest is online. Missing SDK tools and
unavailable control tools are separate from authorization, using the
additive Info.SetupCheck field. Existing drivers keep their default.

## Operations and limits

- Observe the focused UITest component tree with bounds, labels, roles,
  states and stable resource paths. OCR fills in image buttons only when
  the gateway allows capture.
- Tap, long press, swipe, Home, Back and common navigation keys. Mirror
  gestures coalesce into a UITest action when the finger lifts. Power and
  automatic unlocking are absent.
- List bundle IDs and localized labels; six workers reduce a 300-label
  first read from about 55 s to 7 s, with later reads cached. Launch only
  public main abilities found in bundle metadata. System/clone
  classification is not yet provided by the bundle list.
- Return screenshot images in memory. Native JPEG capture avoids the
  large PNG transfer cost on busy home screens; base64 avoids HDC shell
  control-byte conversion. Temporary device files use unique
  names and are removed after reads, including failed reads.
- Show a screenshot-based H.264 live preview at up to 2 fps through the
  existing frame encoder. The host needs ffmpeg with libx264, found through
  PINKY_FFMPEG, PATH, or standard Homebrew locations. The shared video API
  adds an explicit-encoder-path FrameStream variant; old callers retain
  their behavior.

The mirror batches each pair of lock/window guards and captures/transports
JPEGs in one shell session with trap-based cleanup. It reuses its initial
capture and feeds a duplicate picture to release each H.264 access unit
without waiting for the next screenshot. A blocked-next-capture regression
test covers this latency. Normal FrameStream callers are unchanged.
Input cancels an in-flight optional preview capture, then performs its own
fresh lock check. Preview captures use at most 1600 pixels on the long side;
the host restores the native coordinate space before encoding. Full-resolution
agent screenshots and OCR are unchanged. The original display dimensions
are checked on every scaled capture, so rotation restarts the stream.

Automatic text entry is unsupported until UITest password/input metadata
can be verified reliably; the person must enter text on the phone.
Recents, background displays, file transfer and physical-input detection
are also absent. Rotation restarts a preview with its new dimensions.

## Policy and lifecycle

Agent actions retain gateway scopes, read-only mode, blocked apps and
approvals. No automatic grant or bypass is added. SceneBoard is a system
launcher so Home works within app-scoped tasks. Reads, captures and input
serialize per device. Locked phones and unknown lock states receive no
input or screenshots. OCR checks MayCapture and rejects foreground
changes. Only the focused window tree is returned. Unknown system panels
and visible notification surfaces prevent capture instead of exposing
another app. Preview frames become blank when capture is refused.
Screenshots and images stay in memory; nothing from a real screen is
committed. Native temporary outputs are removed after each read.

## Verification

Run make check and go test -race ./internal/driver/harmony.

For real-device checks, set PINKY_HDC to the SDK tool and PINKY_DEVICE to
the serial, then run through scripts/devlock.sh <serial>:

go test -tags device ./internal/driver/harmony -run TestDeviceNativeControl -v -count=1

On 2026-11-09 a Huawei MIA-AL00 reporting OpenHarmony 7.0.0.111 / API 26
was tested over USB on an Apple silicon Mac with hdc 3.2.0b. An initial
Unauthorized result settled to Connected and the log reported
' daemonauthstatus = SUCCESS '. No new phone prompt was necessary.

The real-device smoke test listed 300 bundles, launched Calculator,
observed its 1224x2776 interface, verified a tap by a changed displayed
value, checked screenshot/input dimensions, consumed H.264 preview frames,
and returned Home. It used no personal app, credentials, install, settings
change or power action, and persisted no real-screen artifact. Synthetic
UI tests cover setup and the ready checkup in English, Chinese and Japanese.
The native desktop app also passed its checkup and displayed the real Home
screen after switching screenshot transport to JPEG over base64.

The Calculator latency smoke test measured first preview packet arrival
from about 5.06 s to 1.49 s, and a tap during mirroring from 2.03 s to
0.69 s after batching, immediate encoding and input preemption. Scaled
preview captures took 1.18–1.27 s versus 1.48–1.63 s at full resolution
in the same run. These are USB measurements on this phone, not latency
guarantees. The screenshot transport still limits smoothness; it is not a
continuous on-device video stream. TestDevicePreviewLatency measures the
same safe Calculator/Home path without storing any screen artifact.

Windows/Linux provider builds compile; real operation there is unverified.
SDK-free USB discovery is macOS-only. HDC TCP listings are parsed but real
TCP operation is unverified. This is an initial native driver, not Android
feature parity.

References: [Huawei HDC](https://developer.huawei.com/consumer/en/doc/harmonyos-guides/hdc/),
[Huawei UITest](https://developer.huawei.com/consumer/en/doc/harmonyos-guides/uitest-guidelines/).

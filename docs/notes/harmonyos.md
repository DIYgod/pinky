# Native HarmonyOS connection diagnostics

Native HarmonyOS phones expose an HDC USB interface (class `0xff`,
subclass `0x50`, protocol `0x01`), rather than Android's ADB interface
(`0xff/0x42/0x01`). They therefore do not appear in `adb devices`, even
with a working USB data cable. Android-based HarmonyOS 2–4 phones keep
using the existing Android provider.

The `internal/driver/harmony` provider lists native phones independently
of Android and iOS. On macOS it discovers HDC interfaces from IOKit's USB
descriptors even without an installed SDK. If `hdc` is available, it reads
`hdc list targets -v` and distinguishes unauthorized, connected, and offline
devices. Its executable comes from `PINKY_HDC`, `PATH`, `HDC_SDK_PATH`,
`HDC_HOME`, `OHOS_SDK_HOME`, or `HOS_SDK_HOME`. Pinky downloads nothing and
does not restart existing SDK servers. An explicit tool path may be needed
when launching the desktop app outside a terminal.

The device list, phone page, and checkup show the missing tool,
authorization, or backend limitation; setup text is available in English,
Simplified Chinese, and Japanese. Installing an SDK gives more diagnostic
detail, not control support. The provider never advertises these phones as
online or tries to open them through scrcpy or install Android Companion.

## Current limits

This is discovery and diagnostics, not a native screen-control driver.
`Open` fails closed. No HDC shell command, input, screenshot, app install,
or authorization acceptance is performed. Before native control can be
implemented, an authorized real phone must verify its UITest component
tree, foreground app and lock identification, input, screenshots, app
enumeration and launch capabilities. Existing gateway policy and approval
checks must remain in the path. Missing foreground or lock information
must not permit an unchecked screen capture or input.

Windows and Linux discover only through an installed `hdc`; USB descriptor
discovery without an SDK is currently macOS-only. USB and TCP listings are
parsed, but actual TCP discovery has not been verified on a real phone.

## Verification

```sh
make check
go test -race ./internal/driver/harmony
PINKY_HDC=/path/to/sdk/toolchains/hdc PINKY_DEVICE=<serial> \
  scripts/devlock.sh <serial> go test -tags device \
  ./internal/driver/harmony -run TestDeviceDiscovery -v -count=1
```

To verify the SDK-free macOS path, repeat the device test with
`PINKY_HDC=/nonexistent/hdc`. The test reads connection metadata only.

On 2026-11-09, both paths were verified on one real Huawei phone attached
by USB to an Apple silicon Mac. With OpenHarmony SDK's hdc 3.2.0b the
phone was `Unauthorized`; without hdc the USB descriptors still produced
a HarmonyOS device waiting for the missing diagnostic tool. No device
shell or screen-control operation was tested, because the phone had not
authorized the computer. The SDK archive was obtained from the official
OpenHarmony release repository and checked against its published SHA-256.

References: [Huawei HDC documentation](https://developer.huawei.com/consumer/en/doc/harmonyos-guides/hdc/),
[Huawei UITest documentation](https://developer.huawei.com/consumer/en/doc/harmonyos-guides/uitest-guidelines/).

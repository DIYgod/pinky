# Phone vitals: battery, storage, system

Branch `claude/eager-bell-15c8f8` (2026-11-11). A phone's page showed its name and how it is connected, and nothing about the phone itself. This note says what Pinky now shows, where, how each phone is read, and how the reads stay out of the person's way.

## What shows where

- **A phone's page header**, at its right end: the battery's level and a battery glyph filled to it, green with a bolt while it charges, red at 20% and below off the charger. A phone with storage but no battery (an emulator) shows an info mark instead. A click opens **About**: the model, the system (`iOS 27.0.1`, `iPadOS 26.1`, `Android 17`), the battery line (`82% · Charging · 31 °C`; `Plugged in` when the phone is on power but holds its charge; `Fully charged`) and the storage (`87.7 GB free of 128 GB`) with a bar that turns amber under 10% free.
- **All phones' tiles**: the battery at the end of the name row. A long name now ends in an ellipsis instead of wrapping.
- **Nowhere else.** The sidebar and the phone's ⋯ menu are unchanged: a percentage on every sidebar row is noise, and the header is one click away.
- Only a connected, open phone shows them. An offline, paused, connecting or needs-setup phone shows nothing, not old values, so nothing on screen can be mistaken for current.
- **Agents**: `phone_device` without arguments starts with `phone:`, `battery:` and `storage:` lines (docs/mcp.md). An iPhone without the helper now answers that read with the phone and "no switches" instead of `unsupported`.

Left out on purpose: the Wi-Fi network's name (needs a location permission on both systems), battery health and cycle count (iOS hides them from lockdown, Android's are unreliable across makers), the screen's size (the mirror shows it), serial numbers and IMEI (private, and nothing to act on).

## How each phone is read

Driver contract (`internal/driver/driver.go`): `driver.VitalsReader`, optional; `Vitals(ctx, parts)` reads `VitalBattery`, `VitalStorage` or both and returns what the phone reports, nil for what it does not.

- **iPhone and iPad**: lockdown values, one key at a time in one session. Battery from `com.apple.mobile.battery` (`BatteryCurrentCapacity`, `BatteryIsCharging`, `ExternalConnected`, `FullyCharged`); storage from `com.apple.disk_usage` (`TotalDiskCapacity`, `TotalDataAvailable`). Reading the whole disk domain took 20 s over Wi-Fi (it holds a NAND blob), the two keys take about a second. `TotalDataAvailable` counts what iOS can purge, as Settings › General › iPhone Storage does; `AmountDataAvailable` read a few GB on a phone Settings showed with 87 GB free. iOS reports no battery temperature. Lockdown answers while the phone is locked and starts nothing the person sees: no display stream, no runner, no screen. A Simulator has no lockdown and reports nothing (`ErrUnsupported`, not asked again).
- **Android**: one `adb shell` running `dumpsys battery` and `df -k /data`. Level is scaled by `scale`; `status` 2 is charging, 5 full; any `* powered: true` is plugged; `temperature` is tenths of a degree. `df` gives the data partition (about 115 GB on a 128 GB phone), so the total is rounded up to the capacity the phone is sold with, as Android's own `FileUtils.roundStorageSize` does for Settings and free is `df`'s available. An emulator's battery is made up, so it reports none.

## Keeping out of the way

- **Only while visible.** The client asks `gateway.PhoneVitals` about once a minute while a page that shows the battery builds. MyGo builds nothing while the window is hidden or covered, so a hidden Pinky asks nothing.
- **Rarely.** The gateway reads the battery at most once a minute and the storage every ten minutes whoever asks (the page, the tiles, an agent), and returns what is fresh enough in between. A failed read waits two minutes before the next; a value older than three of its periods is dropped.
- **Never a paused phone** (`pause.go`), nor one that is offline or not open, and a phone that pauses forgets what was shown.
- **Never awake.** Neither read wakes the screen, keeps it on, stops auto-lock or starts the display stream (`TestPhoneVitalsLeavesScreenOff`).
- **Quiet failures.** A read that fails leaves the field out; there is no error toast.

## Tested

- Unit tests: `internal/driver/ios/vitals_test.go`, `internal/driver/android/vitals_test.go` (parsing, rounding), `internal/gateway/vitals_test.go` (cadence, failures, paused, screen off, phone_device), `ui/vitals_test.go` (header, panel, tiles, paused and offline phones, every language fits).
- Real phones, read-only, 2026-11-11: iPhone 13 mini over Wi-Fi, locked: 20%, charging, 87.7 GB free of 128 GB, about 1.3 s for both. Mi 10 Pro (LineageOS, Android 17): 54%, plugged in but not charging (LineageOS holds the charge), 28.7 °C, 512 GB. Pixel (2016, Android 10): 53%, charging, 36 °C, 32 GB.

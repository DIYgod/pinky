# Mirror smoothness

How smoothly a phone's live picture reaches Pinky's mirror, measured on 2026-11-09 against Device MCP's claims, what was fixed, and what is left as a proposal.

Machine: Apple M4 Pro (14 cores), macOS 27.2, Homebrew FFmpeg 9.0.2, a Dell S2721QS at 60 Hz as the main display. Eight emulators of other sessions ran at the same time (load average 30 to 115), so the long tails of the gaps below are on the pessimistic side.

## Summary

- **Every source sends as many pictures as the screen makes.** An Android emulator with the host's GPU gives 60 changed pictures a second through scrcpy, on the main display and on a background display. WebDriverAgent on a Simulator gives 14 of its 15. Doubling scrcpy's bit rate changes the bits, not the frames.
- **The client decodes everything in time.** A 1080×2400 H.264 picture takes 2.4 to 2.9 ms in VideoToolbox; no packet was left without a picture and none was rejected.
- **A 60 Hz window misses 4 to 10% of a 60 fps stream's pictures**, because they arrive unevenly: two within one refresh, and the first is replaced before it is shown. Painting at 120 Hz misses 0 to 1%. Left as a proposal: it raises a ProMotion display to 120 Hz.
- **Fixed: the mirror painted 60 times a second for as long as it was open**, even while the phone's screen was still. It now stops 250 ms after the last new picture, and the player's next picture wakes it. With a still screen the app went from 60 frames a second to 0.1 to 4, and from 3 to 3.8% of a core to 0.1 to 0.6%. While the screen moves nothing changed.
- **Device MCP's "23 fps with scrcpy, 60 fps with the emulator's own stream" is the emulator's renderer, not the stream.** On a software-rendered emulator both streams give about 30 fps; with `-gpu host` both give 60.
- **iOS 27 already streams at 60 fps** (55 to 66 fps while moving, earlier notes). What Device MCP's HEVC 4:4:4 at up to 60 Mbit/s adds is sharper text and colour edges, not frames. Left as a proposal.

## How to measure

### The stream: `pinky-mirrorbench`

`cmd/pinky-mirrorbench` opens a phone through Pinky's own driver, as the app does, and decodes its stream with `video.Decoder` into Y′CbCr planes, as the mirror does. Run it under `scripts/devlock.sh`, like everything that touches a phone:

```sh
scripts/devlock.sh emulator-5560 go run ./cmd/pinky-mirrorbench -android emulator-5560
scripts/devlock.sh emulator-5560 go run ./cmd/pinky-mirrorbench -android emulator-5560 -bg
scripts/devlock.sh <udid> go run ./cmd/pinky-mirrorbench -ios <udid>
go run ./cmd/pinky-mirrorbench -wda http://127.0.0.1:18200 -mjpeg http://127.0.0.1:19200
```

`-bg` measures the background display an agent works on, with Settings on it. `-v` prints every drag and `-json` the summary as JSON. For Android, `-max-fps`, `-bitrate`, `-max-size` and `-codec` try other scrcpy settings.

It measures:

1. **Opening and the first picture.** The time to open the phone, then the first packet and the first decoded picture after asking for the stream: once cold, then `-warm` times (3) on the open phone.
2. **A still screen.** Pictures and bits a second over 3 s.
3. **Six drags** in Settings, which it opens first, alternately up and down, each moving for 1 s with the driver's raw touches (`driver.Toucher`; WDA's `Swipe` on a Simulator). Every 8th luma sample of every 8th row of each picture is compared with the picture before; a picture counts as changed when over 0.2% of the samples moved by more than 16 levels, which encoder noise on a still screen does not reach.
   - **Touch to picture:** from the first move past the touch slop to the first changed picture's packet, and to its decoded planes.
   - **Moving:** changed pictures a second while the content moved, and the gaps between them (median, p95, max).
   - **Bit rate** over the same span.
   - **Painting drops:** the share of changed pictures a window painting at an even 60 or 120 Hz would never show, averaged over 32 phases of the paints against the pictures.
   - **The app drew:** on Android, the frames Settings rendered meanwhile (`dumpsys gfxinfo`), to tell a slow stream from a slow app.

Reported numbers are medians over the drags. The bench does not paint; the painting drops are worked out from when pictures were ready. The client's own painting is measured below.

### The client's painting

A second Pinky, built from the branch, run beside the owner's with its own home and only the emulator:

```sh
PINKY_HOME=/tmp/pinky-pb PINKY_DEVICES=emulator-5700 USBMUXD_SOCKET_ADDRESS=127.0.0.1:9 \
  DO_NOT_TRACK=1 MYGO_FRAME_STATS=all ./pinky
```

`MYGO_FRAME_STATS=all` logs each frame (`mygo: frame N took …`). With the mirror open and its window in front (MyGo holds the frames of a window out of sight), count the frames and the process's CPU time (`ps -o time=`) over 20 s of a still screen, 20 s of `scripts/devlock.sh emulator-5700 adb -s emulator-5700 shell input swipe …` up and down, and 15 s still again. Keep `PINKY_HOME` short: the unix socket's path must fit in 104 bytes.

## Results

### Android emulator, scrcpy

`pinky-api36` (Android 16, 1080×2400, emulator 37.2.13), USB defaults: H.264, 8 Mbit/s, no frame cap.

| | `-gpu host`, main display | `-gpu host`, background display | software (swangle), main display |
| --- | --- | --- | --- |
| Opened in | 2.9 s | | |
| First picture, cold / warm | 206 / 83 ms | 174 / 81 ms | 609 / 132 ms |
| Still screen | 1 to 2.3 pictures/s | 1 picture/s | 8 pictures/s |
| Touch to picture | 65 to 70 ms | 66 ms | 101 to 110 ms |
| Moving | 59.4 to 60.3 fps | 60.2 fps | 22.4 to 29.9 fps |
| Gaps median / p95 / max | 16.6 / 19 to 43 / 21 to 80 ms | 16.6 / 20 / 23 ms | 24 to 41 / 89 to 130 / 141 to 195 ms |
| Bit rate | 5.7 to 8.5 Mbit/s | 6 to 8.3 Mbit/s | 5.3 to 5.9 Mbit/s |
| Painting drops, 60 / 120 Hz | 4 to 10% / 0 to 1% | 4% / 0% | 5 to 12% / 0 to 2% |
| The app drew | 60 to 75 fps | 61 to 74 fps | 41 to 44 fps |
| Decode | 2.4 to 2.6 ms | | 2.9 ms |

Ranges are separate runs at different loads. The wide p95 and max in the first column come from a run while the machine's load average was over 40.

scrcpy at 16 Mbit/s against the default 8 Mbit/s, two runs each, alternated:

| Bit rate asked | Moving | Touch to picture | Gaps p95 | Bit rate used | Drops 60 / 120 Hz |
| --- | --- | --- | --- | --- | --- |
| 8 Mbit/s (default) | 60.1, 60.3 fps | 65, 67 ms | 19.3, 19.2 ms | 8.5, 8.4 Mbit/s | 4% / 0% |
| 16 Mbit/s | 60.3, 60.2 fps | 68, 65 ms | 19.2, 19.6 ms | 15.4, 14.4 Mbit/s | 4 to 5% / 0% |

The encoder fills what it is given: twice the bits, the same frames and latency. On a software-rendered emulator the app itself draws only about 44 frames a second and the guest's software encoder (`c2.android.avc.encoder`) keeps up with less, so no scrcpy setting gets more out of it.

### Android emulator, its own gRPC stream

A throwaway client (not committed) of the emulator's `streamScreenshot` in RGB888 and `sendTouch`, on the same emulator started with `-grpc`, measured the same way:

| | `-gpu host` | software |
| --- | --- | --- |
| Moving | 59.9 fps | 30.1 to 32.8 fps (13.5 at 540 wide) |
| Touch to picture | 62 ms | 101 to 130 ms |
| Gaps median / p95 / max | 16.7 / 19.0 / 27.5 ms | 19.7 / 98.0 / 240.5 ms |
| Made → arrived | 6.3 ms | 8.8 ms |
| Client CPU | 13% of a core | 6 to 7% |

Against scrcpy on the same emulator: the same frame rate, about 8 ms less latency and steadier gaps under load, since nothing is encoded. But each picture is 7.8 MB of raw RGB (467 MB/s at 60 fps over gRPC), the client's CPU is several times the decoder's, there is nothing to record without encoding it again, and it needs a driver path of its own (the emulator's gRPC port and token from its `pid_<pid>.ini`). It works only for emulators, not phones. Not worth adding.

### WebDriverAgent on an iOS Simulator

iPhone Simulator, iOS 26.5, WDA's MJPEG at half size (584×1266), 15 fps, re-encoded to H.264 by VideoToolbox:

| | |
| --- | --- |
| Opened in | 52 s (the first session starts WDA) |
| First picture, cold / warm | 476 / 58 ms |
| Still screen | 0 pictures/s |
| Touch to picture | 297 ms median |
| Moving | 14.1 fps |
| Bit rate | 4.4 Mbit/s |
| Painting drops | 0% |

The latency includes WDA's `Swipe`, which performs the whole gesture before the picture moves (`Toucher` plays a drag back when the finger lifts). The rate is MJPEG's 15 fps cap. This is the Simulators-as-phones area of another session; recorded only.

### iOS 27 over Device Hub

Not measured on a phone in this round (it needs the owner's OK). What the stream negotiates, from the code and earlier notes ([ios.md](ios.md)):

- Pinky's offer uses `avcMediaStreamNegotiatorMode` 5, as go-ios, pymobiledevice3 and idevice do, with the features `FLS;SW:1;` and pixel formats 63.
- The phone answers with HEVC Main 1136×2464, `Framerate` 60 and `RXMaxBitrate` 6 Mbit/s. While the screen moves it sends 55 to 66 fps at about 2.9 Mbit/s, well under the cap, and nothing while it is still.

So the frame rate is not limited by what Pinky asks for. Device MCP's "HEVC 4:4:4 at up to 60 Mbit/s" is mode 7 (Remote Desktop), as open-screen-sharing measured on iPadOS 27.2: mode 5 caps at 6 Mbit/s 4:2:0, mode 7 at 60 Mbit/s with HEVC RExt 4:4:4, mode 9 at 40 Mbit/s 4:2:0. Its blog shows 22 to 32 Mbit/s while scrolling, 50 to 100 KB a picture against mode 5's 5 to 13 KB. That buys sharper text and colour edges, not frames.

### The client

Decoding: 2.4 to 2.9 ms a 1080×2400 H.264 picture with VideoToolbox, 0 errors, 0 packets without a picture in every run above. MyGo's frame stats: a frame that only paints the mirror again takes 0.1 to 0.5 ms on the main thread; one that builds the view again 0.3 to 0.6 ms.

Painting, before and after the fix (emulator `-gpu host`, the window on the 60 Hz display, frames a second of the whole app and its CPU as a share of one core, two runs each):

| | Still 20 s | Scrolling 20 s | Still again 15 s |
| --- | --- | --- | --- |
| Before | 59.2, 60.0 frames/s; 3.8%, 3.0% | 59.8, 60.0 frames/s; 4.7%, 11.4% | 59.9, 59.9 frames/s; 3.5%, 3.1% |
| After | 4.0, 0.1 frames/s; 0.6%, 0.1% | 58.3, 59.5 frames/s; 10.6%, 12.2% | 5.9, 11.3 frames/s; 0.6%, 1.0% |

The still screen's remaining frames follow the emulator's pictures: it sends up to 2 a second while still, and each one paints for 250 ms. An iPhone on iOS 27 and WDA send nothing while still, so their mirrors stop painting altogether. "Still again" includes the end of the last fling.

## The fix

Until now the mirror's `Draw` asked for the next paint with `AnimationFrameRate(60)` every time, so a window with a mirror painted 60 times a second while the mirror was open, whether or not a picture came, and MyGo's `idle` never settled.

Now (`ui/mirror.go`):

- `Draw` keeps asking for paints while pictures come, and for `mirrorPaintFor` (250 ms) after the last one. That is longer than the gaps of a moving screen even at 15 fps, so it does not stop and wake between them.
- Then it stops and marks the mirror asleep. A picture that came meanwhile (`Player.Pending`) keeps it painting.
- The player calls `OnFrame` after each picture. The mirror's `wake` posts an empty update once when asleep, which builds the view again and paints the picture.

`video.Player` stopped converting pictures once `Take` had not been called for a second, to save work for a window out of sight. A sleeping mirror does not call `Take` either, so the second picture after a still second was dropped as idle. The player now stops converting when the picture waiting has waited for over a second (`TestPlayerAfterPause`). A window out of sight still costs no conversions.

`TestMirrorSleepsOnStillScreen` checks with MyGo's tester that a mirror keeps asking for frames right after a picture, asks for none a second later, and that the next picture wakes it and is shown. It fails against the old code.

## Proposals

In order of value:

1. **Paint at 120 Hz on a ProMotion display while a 60 fps stream moves.** Drops fall from 4 to 10% to 0 to 1% (table above). On a 60 Hz display it changes nothing. Cost: the display runs at 120 Hz while the picture moves, which is a battery cost on a MacBook. One line in `Draw` (`AnimationFrameRate(120)` while pictures come). Needs a check on the built-in display.
2. **iOS 27 mode 7, as an option.** Sharper text at 20 to 60 Mbit/s instead of 3 to 6. Needs: the offer with mode 7 (open-screen-sharing also sends long-term references and field 12), a decode path for 4:4:4 (VideoToolbox decodes it in hardware, by Device MCP's account; `Decoder.ConvertYUV` and MyGo's `Bitmap.UpdateYUV` take only 4:2:0, so either convert to 4:2:0, which loses what 4:4:4 adds, or a 4:4:4 upload in MyGo), recordings ten times the size, and a fallback to mode 5 when the phone refuses or Wi-Fi is slow. Needs the real phone.
3. **A cross-goroutine repaint in MyGo.** Waking a sleeping mirror goes through `Window.Update`, which builds the view again (0.3 to 0.6 ms, once per wake). A call that only repaints would make even that free. Small; an upstream ask.
4. **A one-picture jitter buffer.** Showing each picture one refresh late would spread pictures that arrive in pairs, so a 60 Hz window would miss almost none, at 17 ms more latency. Not worth it at 60 fps with 120 Hz available.
5. **Recordings paint at 60 Hz while they play**, even through still parts (`ui/recording.go`). The same sleep would cut that. Small.
6. **Start emulators with `-gpu host`.** A software-rendered emulator gives about 30 fps whatever the stream. Pinky does not start emulators; this belongs in the Simulators-as-phones work and the README's emulator advice.

Not proposed: the emulator's gRPC stream (above) and a higher scrcpy bit rate (no frames gained on the emulator; a real phone's hardware encoder may differ, see below).

## Still to check on real devices

- **iOS 27 iPhone** (iPhone 13 mini): first picture, touch to picture, fps and drops with `pinky-mirrorbench -ios`, and the mirror's painting with the fix (it should stop entirely on a still screen).
- **A real Android phone** (Mi 10 Pro or Pixel): the same, plus scrcpy at 8 against 16 Mbit/s, where a hardware encoder may trade frames for bits differently.
- **The 120 Hz built-in display**: the fix's numbers there, and proposal 1.

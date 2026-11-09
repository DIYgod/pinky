# Mirror smoothness

How smoothly a phone's live picture reaches Pinky's mirror, measured on 2026-11-09 against Device MCP's claims, what was fixed, and what is left as a proposal. Emulators and a Simulator first; the iPhone 13 mini and the Mi 10 Pro the same evening, with the owner's OK.

Machine: Apple M4 Pro (14 cores), macOS 27.2, Homebrew FFmpeg 9.0.2, a Dell S2721QS at 60 Hz as the main display. Eight emulators of other sessions ran at the same time (load average 30 to 115), so the long tails of the gaps below are on the pessimistic side.

## Summary

- **Every source sends as many pictures as the screen makes.** An Android emulator with the host's GPU gives 60 changed pictures a second through scrcpy, on the main display and on a background display. WebDriverAgent on a Simulator gives 14 of its 15. Doubling scrcpy's bit rate changes the bits, not the frames.
- **The client decodes everything in time.** A 1080×2400 H.264 picture takes 2.4 to 2.9 ms in VideoToolbox; no packet was left without a picture and none was rejected.
- **A 60 Hz window misses 4 to 10% of a 60 fps stream's pictures**, because they arrive unevenly: two within one refresh, and the first is replaced before it is shown. Painting at 120 Hz misses 0 to 1%. Left as a proposal: it raises a ProMotion display to 120 Hz.
- **Fixed: the mirror painted 60 times a second for as long as it was open**, even while the phone's screen was still. It now stops 250 ms after the last new picture, and the player's next picture wakes it. With a still screen the app went from 60 frames a second to 0.1 to 4, and from 3 to 3.8% of a core to 0.1 to 0.6%. While the screen moves nothing changed.
- **But real phones keep sending pictures for a while after their screen stops**, and each one woke the mirror for another 250 ms of painting. In the 3 s after the screen settled, the iPhone 13 mini over Wi-Fi sent 22 to 29 pictures a second at 1.5 to 1.8 Mbit/s, none of them changed (in the run that counted, not one sample of the bench's grid moved); it stops 5 to 7 s after the last change ([ios.md](ios.md)). The Mi 10 Pro over USB sent 64 a second at 1 Mbit/s; how long it goes on, and what changes in them, is not known yet. The emulator sends 1 to 2, WDA none.
- **Fixed: the mirror now sleeps through pictures that repeat the one before**, sample for sample, as an encoder sends a screen that stopped. The player compares each picture with the last it handed over (74 µs for an iPhone's screen) and wakes nothing for a repeat. On the emulator the mirror sleeps up to 1.2 s sooner after a drag. Whether the real phones' trailing pictures are such repeats is still to measure on them.
- **The real phones:** the Mi 10 Pro over USB streams 89 to 90 fps from its 90 Hz screen, 56 ms from touch to picture. The iPhone 13 mini over Wi-Fi gives 50 to 54 changed pictures a second of its 60, 278 to 283 ms from touch to picture with outliers past 1.4 s, and opening its stream again takes 1.2 to 1.5 s (once no picture in 20 s).
- **Device MCP's "23 fps with scrcpy, 60 fps with the emulator's own stream" is the emulator's renderer, not the stream.** On a software-rendered emulator both streams give about 30 fps; with `-gpu host` both give 60.
- **iOS 27 already asks for 60 fps** (55 to 66 fps while moving over USB, earlier notes; 50 to 54 over Wi-Fi, below). What Device MCP's HEVC 4:4:4 at up to 60 Mbit/s adds is sharper text and colour edges, not frames. Left as a proposal.

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
2. **A still screen.** Pictures and bits a second over 3 s, how many of those pictures changed, how many were repeats (the same as the picture before, sample for sample, which the mirror skips), and with `-v` where on the screen the ones that moved any sample did, as shares of the height.
3. **Six drags** in Settings, which it opens first, alternately up and down, each moving for 1 s with the driver's raw touches (`driver.Toucher`; WDA's `Swipe` on a Simulator). Every 4th luma sample of every 4th row of each picture is compared with the picture before; a picture counts as changed when over 0.2% of the samples moved by more than 16 levels, which encoder noise on a still screen does not reach.
   - **Touch to picture:** from the first move past the touch slop to the first changed picture's packet, and to its decoded planes.
   - **Moving:** changed pictures a second while the content moved, and the gaps between them (median, p95, max).
   - **Bit rate** over the same span.
   - **Painting drops:** the share of changed pictures a window painting at an even 60 or 120 Hz would never show, averaged over 32 phases of the paints against the pictures.
   - **The app drew:** on Android, the frames Settings rendered from just before the touch to the end of the move (`dumpsys gfxinfo`), to tell a slow stream from a slow app. Until the evening run the bench read them 1 s after the finger lifted but divided by the 1 s of moving, which counted the frames drawn after the move too (scroll bar fading, overscroll); the first numbers below were too high.
4. **After the last drag**, for `-settle` (8 s): how long pictures kept coming, and how long pictures that were not repeats did. The difference is how much sooner the mirror sleeps for skipping repeats. When pictures still came at the end, it says so; watch longer.

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
| The app drew (too high, see below) | 60 to 75 fps | 61 to 74 fps | 41 to 44 fps |
| Decode | 2.4 to 2.6 ms | | 2.9 ms |

Ranges are separate runs at different loads. "The app drew" was counted with the bench's first, too long window (How to measure), so it overstates what the app drew while it moved. It was not measured again: `pinky-api36` now has a PIN set (by another session), which the bench cannot get past. The wide p95 and max in the first column come from a run while the machine's load average was over 40.

scrcpy at 16 Mbit/s against the default 8 Mbit/s, two runs each, alternated:

| Bit rate asked | Moving | Touch to picture | Gaps p95 | Bit rate used | Drops 60 / 120 Hz |
| --- | --- | --- | --- | --- | --- |
| 8 Mbit/s (default) | 60.1, 60.3 fps | 65, 67 ms | 19.3, 19.2 ms | 8.5, 8.4 Mbit/s | 4% / 0% |
| 16 Mbit/s | 60.3, 60.2 fps | 68, 65 ms | 19.2, 19.6 ms | 15.4, 14.4 Mbit/s | 4 to 5% / 0% |

The encoder fills what it is given: twice the bits, the same frames and latency. On a software-rendered emulator the app itself draws at most about 44 frames a second and the guest's software encoder (`c2.android.avc.encoder`) keeps up with less, so no scrcpy setting gets more out of it.

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

### Mi 10 Pro, scrcpy over USB

LineageOS, Android 17, 1080×2340 at 90 Hz, on a cable; USB defaults (H.264, 8 Mbit/s, no frame cap). One run of six drags in Settings. The phone locked itself a few minutes later, so a second run, 16 Mbit/s and the fixed app count were not done.

| | |
| --- | --- |
| Opened in | 3.3 s |
| First picture, cold / warm | 245 / 138 ms (129 to 150) |
| Still screen | 64.3 pictures/s, 1.06 Mbit/s |
| Touch to picture | 56 ms median (38 to 68), 58 ms decoded |
| Moving | 89.1 changed pictures/s (87.1 to 89.8), 89.8 sent |
| Gaps median / p95 / max | 10.8 / 25.5 / 29.6 ms (42 ms once) |
| Bit rate | 5.8 Mbit/s (4.7 to 7.2) |
| Painting drops, 60 / 120 Hz | 38% / 12% |
| Decode | 2.5 ms median, 67.7 ms max, 0 errors |

The stream keeps up with the phone's 90 Hz screen, and a touch shows sooner than on the emulator, since a 90 Hz screen shows the move sooner. A 60 Hz window can show only 60 of the 90 pictures, so it misses a third of them whatever the timing; 38% is that plus the uneven arrival. At 120 Hz it misses 12%. The app's frames (104 to 197 a second) were counted with the old window and say nothing.

### iOS 27 over Device Hub, iPhone 13 mini over Wi-Fi

iOS 27.0.1, CoreDevice over Wi-Fi (the phone had no cable), HEVC 1136×2464. Three runs: two complete, and one that stopped when its third warm start gave no picture in 20 s.

| | Run 1 | Run 2 |
| --- | --- | --- |
| Opened in | 7.3 s | 6.5 s |
| First picture, cold | 434 ms | 373 ms (155 ms in the stopped run) |
| First picture, warm | 1.05 to 1.29 s (mean 1.18 s) | not measured (in the stopped run 1.30 and 1.52 s, then none in 20 s) |
| Still screen | 21.7 pictures/s, 1.46 Mbit/s | 28.7 pictures/s, 0 changed, 1.82 Mbit/s |
| Touch to picture | 283 ms median: 110 to 283 ms, and 1.45 and 1.90 s | 278 ms median: 116 to 462 ms |
| Moving | 50.6 changed pictures/s, 60.4 sent | 54.1 changed pictures/s, 56.0 sent |
| Gaps median / p95 / max | 16.0 / 64.6 / 132.5 ms | 17.5 / 39.9 / 63.4 ms |
| Bit rate | 4.2 Mbit/s | 4.7 Mbit/s |
| Painting drops, 60 / 120 Hz | 27% / 15% | 14% / 5% |
| Decode | 2.5 ms median, 0 errors | 3.1 ms median, 0 errors |

Over Wi-Fi the iPhone is the least smooth source measured. It delivers 50 to 54 of its 60 frames, in bursts: a 60 Hz window misses 14 to 27%, against 4 to 10% on the emulator. A touch takes about four times as long to show as on the emulator, and now and then a second or more; the bench cannot tell whether the touch or the picture was late then. Over USB the earlier notes ([ios.md](ios.md)) saw 55 to 66 fps while moving; a cable run of the bench is still to do.

Opening the stream again on the open phone took 1.05 to 1.52 s, against 0.15 to 0.43 s the first time, and once nothing came in 20 s. The mirror does this each time it opens on a phone Pinky already has open. This is the iOS driver's area and was not looked into here.

What the stream negotiates, from the code and earlier notes ([ios.md](ios.md)):

- Pinky's offer uses `avcMediaStreamNegotiatorMode` 5, as go-ios, pymobiledevice3 and idevice do, with the features `FLS;SW:1;` and pixel formats 63.
- The phone answers with HEVC Main 1136×2464, `Framerate` 60 and `RXMaxBitrate` 6 Mbit/s. Over USB it sent 55 to 66 fps at about 2.9 Mbit/s while the screen moved, well under the cap, and stopped 5 to 7 s after the screen stopped changing.

So the frame rate is not limited by what Pinky asks for. Device MCP's "HEVC 4:4:4 at up to 60 Mbit/s" is mode 7 (Remote Desktop), as open-screen-sharing measured on iPadOS 27.2: mode 5 caps at 6 Mbit/s 4:2:0, mode 7 at 60 Mbit/s with HEVC RExt 4:4:4, mode 9 at 40 Mbit/s 4:2:0. Its blog shows 22 to 32 Mbit/s while scrolling, 50 to 100 KB a picture against mode 5's 5 to 13 KB. That buys sharper text and colour edges, not frames. Over Wi-Fi, where mode 5's 4 to 5 Mbit/s already arrives in bursts, ten times the bits would not make it smoother.

### The client

Decoding: 2.4 to 2.9 ms a 1080×2400 H.264 picture with VideoToolbox, 0 errors, 0 packets without a picture in every run above. MyGo's frame stats: a frame that only paints the mirror again takes 0.1 to 0.5 ms on the main thread; one that builds the view again 0.3 to 0.6 ms.

Painting, before and after the fix (emulator `-gpu host`, the window on the 60 Hz display, frames a second of the whole app and its CPU as a share of one core, two runs each):

| | Still 20 s | Scrolling 20 s | Still again 15 s |
| --- | --- | --- | --- |
| Before | 59.2, 60.0 frames/s; 3.8%, 3.0% | 59.8, 60.0 frames/s; 4.7%, 11.4% | 59.9, 59.9 frames/s; 3.5%, 3.1% |
| After | 4.0, 0.1 frames/s; 0.6%, 0.1% | 58.3, 59.5 frames/s; 10.6%, 12.2% | 5.9, 11.3 frames/s; 0.6%, 1.0% |

The still screen's remaining frames follow the emulator's pictures: it sends up to 2 a second while still, and each one paints for 250 ms. WDA sends nothing while still, so its mirror stops painting altogether. Real phones keep sending for a while (above); the mirror now sleeps through those that repeat the picture before ([below](#repeats)), and whether theirs do is still to measure. "Still again" includes the end of the last fling; both rows were measured before repeats were skipped.

## The fix

Until now the mirror's `Draw` asked for the next paint with `AnimationFrameRate(60)` every time, so a window with a mirror painted 60 times a second while the mirror was open, whether or not a picture came, and MyGo's `idle` never settled.

Now (`ui/mirror.go`):

- `Draw` keeps asking for paints while pictures come, and for `mirrorPaintFor` (250 ms) after the last one. That is longer than the gaps of a moving screen even at 15 fps, so it does not stop and wake between them.
- Then it stops and marks the mirror asleep. A picture that came meanwhile (`Player.Pending`) keeps it painting.
- The player calls `OnFrame` after each new picture. The mirror's `wake` posts an empty update once when asleep, which builds the view again and paints the picture.

`video.Player` stopped converting pictures once `Take` had not been called for a second, to save work for a window out of sight. A sleeping mirror does not call `Take` either, so the second picture after a still second was dropped as idle. The player now stops converting when the picture waiting has waited for over a second (`TestPlayerAfterPause`). A window out of sight still costs no conversions.

`TestMirrorSleepsOnStillScreen` checks with MyGo's tester that a mirror keeps asking for frames right after a picture, asks for none a second later, and that the next picture wakes it and is shown. It fails against the old code.

### Repeats

A phone goes on sending pictures after its screen stops, and each one woke the mirror for another 250 ms of painting at 60 Hz. An encoder sends a screen that stopped as pictures whose blocks are all skipped, and those decode to the very same samples as the picture before: FFmpeg's test pattern at 30 fps gives a key frame, one picture that refines it, and 28 such repeats a second.

- `video.PlayerOptions.SkipRepeats` makes the player compare each picture it copied with the last one it handed over (`YUV.Equal`: size, layout, colours, then every plane with `bytes.Equal`), and hand over nothing and call no `OnFrame` for one that is the same. `Stats.Repeats` counts them. The last picture is the one waiting or the one shown, which the player does not write, so the comparison needs no copy.
- The mirror's player has it on. A recording's does not: there a repeat's PTS moves the playhead.
- Cost: 74 µs for an iPhone's 1136×2464 picture that is a repeat (`BenchmarkYUVEqual`, about 57 GB/s on the M4 Pro), and less for one that is not, as the comparison stops at the first difference. A second of 60 repeats costs 4.4 ms; the painting it saves cost 3 to 3.8% of a core.
- A picture that differs by one sample anywhere is handed over, so a blinking caret or a clock still shows.
- `TestPlayerSkipsRepeats` (30 pictures of the test pattern: 2 handed over, 28 skipped, one `OnFrame` each and one at the end) and `TestMirrorSleepsThroughRepeats` (a mirror asleep after 2 pictures stays asleep through 10 repeats, and asks for no frame; it fails without the option).

Measured with the bench's `-settle` on the Android 17 emulator (`-gpu host`): after the last drag, pictures came for 1.46 to 1.48 s in four runs. In two of them the last 1.0 and 1.2 s were repeats (pictures that were not repeats for 0.46 and 0.24 s), so the mirror sleeps that much sooner; in the other two there were none. A still screen right after opening sent 1 to 4.4 pictures a second, none of them repeats.

Whether the iPhone's 5 to 7 s and the Mi 10 Pro's 64 a second are repeats is still to measure on the phones (`-settle 10s -v`). Where they are not (an encoder that refines a still picture for seconds, or a status bar that changes), the mirror paints through them as before.

## Proposals

In order of value:

1. **Paint at 120 Hz on a ProMotion display while a 60 fps stream moves.** Drops fall from 4 to 10% to 0 to 1% (emulator table above); a 90 Hz phone's from 38% to 12%. On a 60 Hz display it changes nothing. Cost: the display runs at 120 Hz while the picture moves, which is a battery cost on a MacBook. One line in `Draw` (`AnimationFrameRate(120)` while pictures come). Needs a check on the built-in display.
2. **iOS 27 mode 7, as an option.** Sharper text at 20 to 60 Mbit/s instead of 3 to 6. Needs: the offer with mode 7 (open-screen-sharing also sends long-term references and field 12), a decode path for 4:4:4 (VideoToolbox decodes it in hardware, by Device MCP's account; `Decoder.ConvertYUV` and MyGo's `Bitmap.UpdateYUV` take only 4:2:0, so either convert to 4:2:0, which loses what 4:4:4 adds, or a 4:4:4 upload in MyGo), recordings ten times the size, and a fallback to mode 5 when the phone refuses or Wi-Fi is slow. Over Wi-Fi it would not be smoother (above). Needs the real phone.
3. **A cross-goroutine repaint in MyGo.** Waking a sleeping mirror goes through `Window.Update`, which builds the view again (0.3 to 0.6 ms, once per wake). A call that only repaints would make even that free, and would let the mirror paint once per picture instead of at 60 Hz for 250 ms. Small; an upstream ask.
4. **A one-picture jitter buffer.** Showing each picture one refresh late would spread pictures that arrive in pairs, so a 60 Hz window would miss almost none, at 17 ms more latency. Not worth it at 60 fps with 120 Hz available; over Wi-Fi the iPhone's bursts are longer than one picture.
5. **Cap a faster phone's stream at the window's refresh rate.** A 90 Hz phone sends 90 pictures a second to a 60 Hz window that shows 60. scrcpy's `max_fps` at 60 would save the phone a third of its encoding and the client a third of its decoding, with nothing lost on screen; not when the window is on a 120 Hz display. Small.
6. **Recordings paint at 60 Hz while they play**, even through still parts (`ui/recording.go`). The same sleep would cut that. Small.
7. **Start emulators with `-gpu host`.** A software-rendered emulator gives about 30 fps whatever the stream. Pinky does not start emulators; this belongs in the Simulators-as-phones work and the README's emulator advice.

Not proposed: the emulator's gRPC stream (above) and a higher scrcpy bit rate (no frames gained on the emulator; the Mi 10 Pro used 5.8 of its 8 Mbit/s while moving).

## Still to check on real devices

- **iPhone 13 mini on a cable**: the same bench over USB, to tell Wi-Fi from the phone, and why opening its stream again takes over a second over Wi-Fi.
- **Repeats on both phones**: whether the pictures the iPhone and the Mi 10 Pro go on sending after a drag are repeats the mirror sleeps through, and for how long they come (`-settle 10s -v`); if they are not, what changes in them (`-still 30s -v`).
- **Mi 10 Pro**: scrcpy at 16 against 8 Mbit/s, and what the app draws with the fixed count.
- **The mirror's painting with a real phone**, in a Pinky built from the branch: how soon it stops after the screen does.
- **The 120 Hz built-in display**: the fix's numbers there, and proposal 1.

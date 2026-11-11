# Standalone iOS prototype

A feasibility prototype of Pinky running on the iPhone itself: no computer at run time, acting on other apps. It is not a product and ships with nothing. The results and numbers are in [docs/notes/standalone-phone.md](../../docs/notes/standalone-phone.md), "Prototype results (iOS)".

## How it works

- **Loopback.** A packet tunnel routes 10.7.0.1 into a utun and sends each IPv4 packet back with source and destination swapped, so a connection to 10.7.0.1 reaches the phone itself. There `remotepairingd` answers RemotePairing at 10.7.0.1:49152 as it would a computer on the network. LocalDevVPN does the same reflection; the lite build relies on it.
- **Pairing.** `cmd/rppair pair` asks `remotepairingd`, once and from a Mac the phone already trusts, to pair a fresh host identity over lockdown (`com.apple.dt.remotepairingdeviced.lockdown`, fixed PIN). The phone shows nothing. The app carries that identity (`build/Pairing.json`). On the phone the Go core pair-verifies, opens a tunnel listener, speaks TLS-PSK to it and hands the CDTunnel to Pinky's own iOS driver (`internal/driver/ios`, `OpenOverTunnel`): display stream, HID, the accessibility tree and screenshots over RSD, as on the desktop.
- **Engine.** `engine` guards input (never while Pinky's own screen is in front, while an approval waits, or on a locked phone), runs the Q1 probe and the Q2 soak, and runs the agent (`agent`, a small loop on the Claude Messages API). `core` is the C library both the app and its packet-tunnel extension link; either can host the engine.
- **App.** SwiftUI: the task, the API key (memory only), Start and Stop, the approval card (Approve needs Face ID or the passcode), a Live Activity with Stop, local notifications for approvals and ended tasks, and the Q1/Q2 test buttons.

## Layout

| Path | What |
| --- | --- |
| `rppair/`, `cmd/rppair/` | RemotePairing pair-setup over lockdown, pair-verify, the tunnel listener; the Mac tool that makes and checks the pairing |
| `engine/` | guard, tools, approvals, probe, soak, stats |
| `agent/` | the agent loop and the Messages API client |
| `core/` | the C library (`-buildmode=c-archive`, `ios` build tag) |
| `app/` | the Xcode project (XcodeGen): `App`, `Tunnel` (packet tunnel), `Activity` (Live Activity) |

## Building

Xcode 27, XcodeGen and Go. On the Mac:

```sh
go test ./ios/standalone/...                    # engine, agent, rppair
ios/standalone/build.sh core                     # the Go core only
ios/standalone/build.sh full CODE_SIGNING_ALLOWED=NO   # compile check, unsigned
```

To run it on a phone:

1. Pair once, from a Mac the phone trusts (under `scripts/devlock.sh`). Keep the file outside the repo: whoever holds it can open a CoreDevice tunnel to the phone from its network.

   ```sh
   go run ./ios/standalone/cmd/rppair pair -udid <udid> -out ~/.pinky-standalone/rp_pairing.plist
   go run ./ios/standalone/cmd/rppair check -udid <udid> -id ~/.pinky-standalone/rp_pairing.plist -addr <phone's Wi-Fi IP>:49152
   ```

2. Build with the pairing inside.
   - **Full** (own packet tunnel; Xcode registers explicit App IDs with the Network Extension capability):

     ```sh
     PINKY_PAIRING=~/.pinky-standalone/rp_pairing.plist PINKY_UDID=<udid> ios/standalone/build.sh full -allowProvisioningUpdates
     ```

   - **Lite** (no tunnel of its own; signed with the team's wildcard profile, so no new App IDs; needs LocalDevVPN from the App Store):

     ```sh
     PINKY_PAIRING=~/.pinky-standalone/rp_pairing.plist PINKY_UDID=<udid> ios/standalone/build.sh lite
     ```

3. Install: `xcrun devicectl device install app --device <udid> <the .app the build printed>`.
4. On the phone: open Pinky Local, Turn on (the full build asks to add a VPN configuration), allow notifications, then Probe, Soak or a task. With the lite build, turn LocalDevVPN on first.

The engine logs to the unified log with the prefix `pinky` (a heartbeat every 10 s: state, operations, timings, memory), which a Mac reads with the device's syslog.

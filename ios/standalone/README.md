# Standalone iOS prototype

A feasibility prototype of Pinky running on the iPhone itself: no computer at run time, acting on other apps. It is not a product and ships with nothing. The results and numbers are in [docs/notes/standalone-phone.md](../../docs/notes/standalone-phone.md), "Prototype results (iOS)".

## How it works

- **Loopback.** A packet tunnel routes 10.7.0.1 into a utun and sends each IPv4 packet back with source and destination swapped, so a connection to 10.7.0.1 reaches the phone itself. There `remotepairingd` answers RemotePairing as it would a computer on the network. Its port changes from phone to phone and with each reboot; the app finds it in the `_remotepairing._tcp` service `remotepairingd` advertises on `lo0` (49152 is only the fallback). LocalDevVPN does the same reflection; the lite build relies on it.
- **Pairing.** `cmd/rppair pair` asks `remotepairingd`, once and from a Mac the phone already trusts, to pair a fresh host identity over lockdown (`com.apple.dt.remotepairingdeviced.lockdown`, fixed PIN). The phone shows nothing. The app carries that identity (`build/Pairing.json`). On the phone the Go core pair-verifies, opens a tunnel listener, speaks TLS-PSK to it and hands the CDTunnel to Pinky's own iOS driver (`internal/driver/ios`, `OpenOverTunnel`): display stream, HID, the accessibility tree and screenshots over RSD, as on the desktop.
- **Engine.** `engine` guards input (never while Pinky's own screen is in front, while an approval waits, or on a locked phone), runs the Q1 probe and the Q2 soak, and runs the agent (`agent`, a small loop on the Claude Messages API). When the person answers an approval in Pinky's own app, the engine goes home and opens the task's app again, since it never acts on Pinky's screen. `core` is the C library both the app and its packet-tunnel extension link; either can host the engine.
- **App.** SwiftUI: the task, the model, an optional base URL (any server that speaks the Messages API), the API key (memory only), Start and Stop, the approval card (Approve needs Face ID or the passcode), a Live Activity with Stop, local notifications for approvals and ended tasks, and the Q1/Q2 test buttons.

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
PINKY_SIGN=0 ios/standalone/build.sh full         # compile check, unsigned
```

To run it on a phone:

1. Pair once, from a Mac the phone trusts (under `scripts/devlock.sh`). Keep the file outside the repo: whoever holds it can open a CoreDevice tunnel to the phone from its network.

   ```sh
   go run ./ios/standalone/cmd/rppair pair -udid <udid> -out ~/.pinky-standalone/rp_pairing.plist
   go run ./ios/standalone/cmd/rppair check -udid <udid> -id ~/.pinky-standalone/rp_pairing.plist -addr <phone's Wi-Fi IP>:<port>
   ```

   The port is the one the phone advertises on Wi-Fi: `dns-sd -B _remotepairing._tcp`, then `dns-sd -L` on its name. It was 49152 on the test phone.

2. Build with the pairing inside. Xcode builds unsigned and `build.sh` signs each part with the development profile Xcode keeps for its App ID (`~/Library/Developer/Xcode/UserData/Provisioning Profiles`), or the team's wildcard profile when it has none, so Xcode needs no signed-in account.
   - **Full** (own packet tunnel). It needs three explicit App IDs and a development profile for each that lists the phone: `cc.diygod.pinky.standalone` and `.tunnel` with the Network Extension capability, and `.activity`. With the App Store Connect CLI, for example:

     ```sh
     asc bundle-ids create --identifier cc.diygod.pinky.standalone --name "Pinky Local"   # and .tunnel, .activity
     asc bundle-ids capabilities add --bundle <its id> --capability NETWORK_EXTENSIONS     # the app and .tunnel
     asc profiles create --name "Pinky Local Development" --profile-type IOS_APP_DEVELOPMENT \
       --bundle <its id> --certificate <your development certificate's id> --device <the phone's id>
     asc profiles local install --id <the profile's id>
     PINKY_PAIRING=~/.pinky-standalone/rp_pairing.plist PINKY_UDID=<udid> ios/standalone/build.sh full
     ```

   - **Lite** (no tunnel of its own; its ids fall under the team's wildcard profile, so no new App IDs; needs LocalDevVPN from the App Store):

     ```sh
     PINKY_PAIRING=~/.pinky-standalone/rp_pairing.plist PINKY_UDID=<udid> ios/standalone/build.sh lite
     ```

3. Install: `xcrun devicectl device install app --device <udid> <the .app the build printed>`.
4. On the phone: open Pinky Local, Turn on (the full build asks to add a VPN configuration), allow notifications, then Probe, Soak or a task. With the lite build, turn LocalDevVPN on first.

The engine logs to the unified log with the prefix `pinky` (a heartbeat every 10 s: state, operations, timings, memory; each tool call's name, time and outcome, never its arguments), which a Mac reads with the device's syslog.

A debug build also starts a run from launch arguments, so a measurement needs no taps once the VPN configuration is allowed. It restarts the loopback so the engine runs where `-host` says (`tunnel` or `app`):

```sh
xcrun devicectl device process launch --terminate-existing --device <udid> \
  cc.diygod.pinky.standalone -- -pinkyRun soak -host tunnel -soakMinutes 20
xcrun devicectl device process launch --terminate-existing --device <udid> \
  --environment-variables '{"PINKY_MODEL_KEY":"…"}' \
  cc.diygod.pinky.standalone -- -pinkyRun task -task "…" -model <model> -baseURL <url>
```

`-pinkyRun probe` runs the Q1 probe. A task's key comes in the launch environment and stays in memory; never put it in a file. The `--` keeps devicectl from reading the app's arguments as its own.

# Phones away from home

Pinky can use an Android phone that is not on the computer's network: the computer at home, the phone with you on mobile data or on another Wi-Fi. It reaches the phone through [Tailscale](https://tailscale.com), a free app that keeps your own devices connected to each other privately wherever each one is. Agents can be elsewhere too: see [Remote agents](mcp.md#over-http).

iPhones need a cable for now. An Android 10 phone, which has no Wireless debugging, can use the same way on the computer's own Wi-Fi: see [Wi-Fi without Wireless debugging](#wi-fi-without-wireless-debugging).

## What you need

- Tailscale on the computer that runs Pinky and on the phone, signed in to the same account (the same *tailnet*). Personal use is free.
- An Android phone (Android 10 or later) that Pinky already uses: you set it up once while it is near the computer, by cable or on the same Wi-Fi.
- The computer awake while you are out ([below](#keeping-the-computer-awake)).

## Setting it up

1. **On the computer**, install Tailscale ([download](https://tailscale.com/download)) and sign in. On a Mac, either app works (from the website or the App Store). On Linux, `sudo tailscale up`.
2. **On the phone**, install Tailscale from Google Play, open it and sign in to the same account. Allow the VPN when Android asks.
3. **In Pinky**, with the phone connected by cable or Wi-Fi, open **Settings › Phones**, and under the phone, next to **Use away from home**, click **Turn on…**. Pinky says what this opens (below) and asks first. It then has the phone's adb listen on the network (`adb tcpip 5555`) and connects to the phone at its Tailscale address.

That's all. Pinky now keeps a connection to the phone through Tailscale and uses it whenever the phone has no connection on the computer's own network: the phone stays in Pinky's list as you leave, with **Tailscale** under its name instead of Wi-Fi or USB.

**Add phone › Android › Phone not on this network? Use Tailscale** shows the same steps, says what is missing on the computer, and lists the Android phones on your tailnet with a **Connect** button each. A phone that is not set up yet can also be connected there at the port its Wireless debugging screen shows, while it is on a Wi-Fi network with Wireless debugging on.

## What it opens, and how to turn it off

- **adb on the network.** After `adb tcpip`, the phone's adb accepts connections on port 5555 on every network the phone joins, not only through Tailscale: home Wi-Fi, a café's Wi-Fi, mobile data, IPv4 and IPv6. It lets in only the computers the phone already trusts (the ones you tapped **Allow** for); any other computer makes the phone ask, and you tap **Cancel**. The connection itself is not encrypted, except through Tailscale, which encrypts everything between your devices.
- **How long.** Until the phone restarts. After a restart Pinky can no longer reach the phone from afar, and Settings › Phones says so once the phone is near the computer again, with **Turn on again**.
- **Turning it off.** **Turn off** in Settings › Phones (it runs `adb usb` on the phone, which stops the listening, and forgets the phone's address); or restart the phone; or turn off USB debugging. While the phone is away, turning it off cuts Pinky off until the phone is back.
- **Removing a phone** from Pinky turns this off too, as **Turn off** does when Pinky can reach the phone, and drops its connection through Tailscale.
- **Tailscale itself.** Pinky reads Tailscale's status (`tailscale status --json`) and nothing else: it has no Tailscale credentials of its own and never changes Tailscale's settings.

Why not Wireless debugging? Android turns Wireless debugging off when the phone leaves Wi-Fi (on mobile data there is none) and, on Android 11 to 16, when the phone joins another Wi-Fi network; it takes a new port each time it comes on, and Pinky finds that port with mDNS, which does not cross a VPN. Wireless debugging's own connection is encrypted and paired, so Pinky uses it whenever the phone is on the computer's network, and the connection through Tailscale only when that one is gone.

## Keeping the computer awake

A sleeping computer cannot reach the phone. **Settings › Phones › Keep this computer awake for phones away** (on by default) keeps it from sleeping when idle while a phone is connected through Tailscale. On a Mac it shows in `pmset -g assertions` as "Pinky: a phone is connected through Tailscale". The display may still sleep.

What it cannot stop:

- **A MacBook's lid.** Closing the lid puts a Mac laptop to sleep, unless it runs in clamshell mode (plugged in, with an external display). To keep a closed MacBook awake anyway, `sudo pmset -a disablesleep 1` turns sleep off altogether (`sudo pmset -a disablesleep 0` turns it back on); it is a system setting, so mind the battery.
- **Sleep when nothing runs.** On a Mac that should stay up while you are out, also turn on System Settings › Battery › Options › **Prevent automatic sleeping on power adapter when the display is off** (or Energy on a desktop Mac).
- **Without a window.** `pinkyctl daemon` holds no such assertion: on a Mac, run it as `caffeinate -i pinkyctl daemon`.
- **Windows and Linux.** Pinky holds nothing there: set the computer not to sleep while plugged in (Windows: Settings › System › Power › Sleep), or on Linux run Pinky under `systemd-inhibit --what=idle:sleep`.

## Speed and data

<!-- measurements: filled in from the field test -->

## When it does not work

- **"Doesn't take connections from Pinky yet"**: the phone's adb does not listen on the network. It stops when the phone restarts. Bring the phone near the computer (cable or Wi-Fi) and turn **Use away from home** on again.
- **"Couldn't reach the phone through Tailscale"**: Tailscale on the phone is off or has no internet. Open Tailscale on the phone. Android may stop it to save power: in Android's settings, let Tailscale run in the background, or make it the always-on VPN (Settings › Network › VPN).
- **Slow, "connected through a Tailscale relay"**: the two devices could not reach each other directly, so their traffic goes through one of Tailscale's relay servers. `tailscale ping <phone>` on the computer says which way it goes. A home router or proxy that rewrites the computer's UDP traffic (a VPN gateway, Surge or Clash in gateway mode) often causes it: let Tailscale's traffic go direct there (UDP port 41641 and Tailscale's addresses, 100.64.0.0/10).
- **The phone shows up twice for a moment, or reconnects as you leave**: normal. Pinky moves from the local connection to the one through Tailscale; a task on the phone carries on.

## Wi-Fi without Wireless debugging

Android 10 has no Wireless debugging, so the phone pairs with no computer over Wi-Fi. Pinky moves it to Wi-Fi the older way, with the same adb listening on the network as above:

1. Plug the phone in and choose **Use Wi-Fi instead…**: in the ⋯ menu under its picture, in **Settings › Phones**, or at the end of **Add phone › Android**. The phone must be on the same Wi-Fi as the computer.
2. Pinky asks first and says what it opens: while it is on, any computer on the same network can try to connect to the phone. If the phone asks **Allow USB debugging?** when you are not connecting it yourself, tap **Cancel**, never **Allow**. The connection is not encrypted; use it only on Wi-Fi you trust.
3. Pinky has the phone's adb listen on the network (`adb tcpip 5555`), connects at the phone's Wi-Fi address and says when you can unplug it. If the phone cannot be reached there (another network, or one that keeps devices apart), Pinky stops the listening again and says so.

Pinky keeps the address and connects again whenever the phone is back on the network: after it slept, after the Wi-Fi dropped, and at a new address, which it finds by asking the network for the phone's adb (mDNS, `_adb._tcp`). It keeps a connection over Wi-Fi while the cable is in too, so pulling the cable does not cut the phone off.

When the phone restarts, its adb stops listening: the phone's page says so, instead of only "offline". Plug it in once and choose **Use Wi-Fi again**. **Stop the Wi-Fi connection** (the same menu, or Settings › Phones) turns it off for good: the phone's adb stops listening (`adb usb`), and Pinky reaches the phone only by cable. On a phone also used away from home, it ends that too.

Measured on the Mi 10 Pro (Android 17, which also takes this switch, standing in for an Android 10 phone): moving it to Wi-Fi took 0.4 s, a dropped connection came back in 62 ms, and a phone at a new address was found and connected again in 4.9 s.

## How it works

For developers; the code is in `internal/tailnet` and `internal/driver/android/network.go`, `tailnet.go` and `wifi.go`.

- `internal/tailnet` runs `tailscale status --json` (on PATH, then `/Applications/Tailscale.app/Contents/MacOS/Tailscale`, Homebrew's, the Windows and Linux install paths), at most every 3 s, and reads the state, the tailnet's name and each device's addresses, OS, online state and path (direct or relayed).
- The Android driver keeps the phones whose adb Pinky had listen on the network in `network-phones.json` in Pinky's config folder: the phone's id, its port, its Wi-Fi address (`driver.WiFiSwitch`) and its Tailscale node and address (`driver.Tailnet`).
- `List` starts a reconnect pass now and then (every 20 s while an address has no connection, every minute otherwise). For each kept address without a connection, a 3 s TCP probe goes first (the adb server would wait 75 s for an address nothing answers); then `adb connect`. A Wi-Fi address that does not answer, or refuses, is looked up again with mDNS for 6 s (a dozing phone answers in up to 5 s); a Tailscale address is tried only while Tailscale says the phone is online, and a new address for the same node is picked up. Failed addresses wait 10 s, doubling to 2 min.
- A kept phone with no connection at all is listed offline, with `driver.Info.Setup` `network_off` once its adb refused (it restarted: plug it in once) or `network_away` while it does not answer; the client words them.
- The phone's addresses come from the phone (`ip -o addr show`): the first IPv4 address of a `wlan` interface, and the VPN interface's addresses in 100.64.0.0/10, which carriers use too, matched against the tailnet's devices.
- A phone with several connections is listed once, by the best (USB, then the local network, then Tailscale), so it moves to the next when one goes; `driver.Info.Via` is `tailscale` when Tailscale is the one.
- Turning either on runs `tcpip:5555` over a connection the phone has (adbd restarts; the connection comes back in about 2 s) and connects at the new address; off runs `usb:`. Removing the phone from Pinky turns both off.

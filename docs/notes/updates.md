# Updates

How Pinky keeps itself and its Android companion app current, and tells the person what changed. Code: `internal/update` (feed, download, checks, the swap on macOS, the changelog, state, idle rules), `ui/update.go` (cards, dialogs, Settings, the automatic installs), `cmd/pinky/update.go` (wiring, the daemon, `pinky update`), `internal/driver/android/companion_update.go` and `internal/gateway/companion_update.go` (the companion).

## The feed

`latest.json` in the newest release of `update.Repo` (`DIYgod/pinky-releases`, one constant): `https://github.com/DIYgod/pinky-releases/releases/latest/download/latest.json`. `PINKY_UPDATE_FEED` overrides the whole URL (tests, a staging feed); plain HTTP is accepted only from this computer.

```json
{
  "version": "2026.1107.2",
  "tag": "v2026.1107.2",
  "date": "2026-11-07T10:00:00Z",
  "notes": "- Markdown, shown on the update card",
  "assets": {
    "darwin-arm64": {"url": "https://…/Pinky-2026.1107.2-arm64.zip", "sha256": "<64 hex>", "size": 41234567},
    "darwin-amd64": {"url": "…", "sha256": "…", "size": 0},
    "windows-amd64": {"url": "…/Pinky-2026.1107.2-setup.exe", "sha256": "…", "size": 0},
    "linux-amd64": {"url": "…", "sha256": "…", "size": 0}
  }
}
```

- Asset keys are `GOOS-GOARCH`. An asset whose URL is not HTTPS, whose sha256 is not 64 hex digits or whose size is not between 1 byte and 2 GiB is dropped; the feed itself must parse and name a release version, and is at most 1 MiB.
- The macOS asset is a zip of the signed, notarized and stapled `Pinky.app`, made with `ditto -c -k --keepParent Pinky.app`. Its `CFBundleShortVersionString` must equal `version`.
- Versions are dot-separated integers (`2026.1107.1`, date-based `YYYY.MDD.N`), compared part by part as numbers, missing parts counting as 0; a leading `v` is ignored. `0.0.0-dev` and anything else that does not parse never update and never see an update.
- Requests carry `User-Agent: Pinky/<version> (<os>; <arch>)` and go through the system's proxy (MyGo's `netproxy`), as browsers do.

## When Pinky looks and installs

- The app checks 10 s after its window exists and every 4 hours (`update.CheckEvery`), quietly when that fails (logged). Settings › Advanced has **Check now**, which says what it found.
- A newer release with a build for this computer shows a card above the sidebar's footer: the version, **What's new** (the feed's notes in a dialog), **Update now** and **Later** (hides it for that version for a day). Update now asks first while an agent task, an unlock or another job runs, since the restart stops it.
- **Update automatically** (Settings › Advanced, on by default) installs by itself when `update.Idle` holds at a minute's tick: no agent task running, waiting or paused, no request waiting, no unlock, companion install, pairing or playback under way, nobody in control of a phone, and neither the person (window focus and the system's idle time while focused) nor an agent active for 10 minutes (`update.QuietFor`). Downloading can take a while, so the restart checks again; if someone came back, the card says the update is ready and Pinky restarts at the next quiet tick (or on **Restart now**).
- A version whose install or start failed is not installed again by itself (`State.Failed`); Settings says why, and Update now tries again.
- An automatic update brings Pinky back without taking the front, and with its window closed (`--background`) if it was closed. Pinky's own variables (`PINKY_*`, `MYGO_AUTOMATION`) are passed on with `open --env`, which Launch Services would otherwise drop.

## Installing on macOS

`update.Locate` decides from the running executable:

| Where Pinky runs | What the card offers |
| --- | --- |
| a `Pinky.app` signed by team 492J8Q67PF, in a folder the person can write to | Update now: replace and restart |
| a copy macOS translocated (run from Downloads with quarantine) | Download, with "move Pinky to Applications" |
| the disk image, or a folder the person cannot write to, or a bundle owned by another user | Download, with why |
| a build signed ad hoc (`make app` on this Mac) | Download: a developer's build is never overwritten |
| Homebrew (a `pinky` cask in `Caskroom`, or a formula in `Cellar`) | the `brew upgrade` command, with Copy |
| not in a bundle (`go run`, a bare binary) | Download |

The install (`BundleInstaller`):

1. Download into `<data>/updates`, checking the size and SHA-256 against the feed.
2. `ditto -x -k` into `.Pinky.app.update-XXXX/new` next to the installed bundle (same volume, so the swap is a rename), then check: exactly one app, `CFBundleIdentifier` `app.pinky.desktop`, the feed's version, an executable, and `codesign --verify --deep --strict -R='anchor apple generic and identifier "app.pinky.desktop" and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = "492J8Q67PF"'` (a Developer ID Application certificate of Pinky's team). The quarantine flag is removed so the unattended first start does not wait on Gatekeeper's question; the signature check above is the trust decision.
3. Write `pending.json` (from, to) into the work folder and start the helper (`/bin/sh`, in a session of its own), then quit.
4. The helper waits for Pinky's pid to exit, renames the installed bundle to `work/old.app` and the new one into its place (putting the old one back if that fails), and starts it with `open`. Renames keep the path, so the Dock tile and Launch Services stay with the app.
5. The new version, once it holds the gateway's lock, writes its pid to `work/launched` (`update.CheckSwap`), and `work/started` once its window is up (`Swapped.Confirm`). The helper then removes the work folder and the old bundle.
6. If the new version exits before `started`, or never launches within a minute, the helper moves it to `work/failed.app`, puts the old bundle back, marks `rolled-back` and starts the old one, which records the version as failed. A new version that runs but is slow to come up is waited for, never rolled back under it.

`update.log` in the data folder has the helper's lines. Leftover work folders are removed after a day.

## Windows and Linux

- **Windows:** the card's **Download and install** downloads and checks the installer, opens it and quits Pinky. MyGo's NSIS installer installs per user in `%LOCALAPPDATA%\Programs\Pinky`, closes a running Pinky and supports `/S`; a silent, automatic install (run `/S` after Pinky exits, then start it) is the next step and needs a Windows machine to test.
- **Linux:** **Download** saves the release in `~/Downloads` and shows it. A Pinky that dpkg or pacman installed says to update it through the package manager; a Linuxbrew one shows `brew upgrade pinky`.

## The daemon and the command line

- `pinky daemon` checks at start and every 4 hours and logs when a newer version is out, with what to do; it never replaces itself (it may run under launchd, systemd or a package). `--no-update-check` turns the check off.
- `pinky update [--check]` reports and, without `--check`, downloads, checks and installs the update while no Pinky runs (it says to `pinky quit` first otherwise): on macOS the same checks, then the swap at once (`SwapNow`), no helper.
- Agents connected through `pinky mcp` lose their connection when the app restarts for an update; the shim does not reconnect yet (see Open questions).

## Pinky Companion

- A phone whose installed companion is older than the bundled APK (`driver.CompanionOutdated`) shows **Update Pinky Companion** on its page, as before; with Update automatically on, the card adds that Pinky updates it when the phone is free.
- The client checks each Android phone's companion every 10 minutes and updates it (`Gateway.UpdateCompanion`, `driver.CompanionUpdater`) when the phone has no task running or ended in the last two minutes (the driver's own use of the app ends a minute after a task), no request, no unlock and nobody in control. The gateway refuses while an agent holds the phone.
- The update is `pm install -r` of the bundled APK, Pinky's own package only, and keeps the accessibility service as it was. **Android keeps `enabled_accessibility_services` across `pm install -r` of the same package**: checked on the Android 10 emulator (`emulator-5562`), the service was still listed and answered right after the install. Pinky still runs `TurnOn` afterwards when the service was on, which adds its entry only if missing and waits until the app answers. A service that was off stays off.
- It refuses (and leaves the card to the person) where the phone would show a dialog: Google Play services installed and Play Protect checking adb installs (`verifier_verify_adb_installs` or `package_verifier_enable` not 0; unset counts as on), or an installed build signed with another key (round one's), which has to be uninstalled and replaced. Nothing else on the phone is read or changed.
- Each automatic update shows a toast and goes into What's new ("Updated on Mi 10 Pro to 0.1.0 (130)").

## What's new

- `CHANGELOG.md` at the repository's root is embedded through the root package (`pinky.Changelog`): newest first, `## <version> (<YYYY-MM-DD>)` per release with bullets; headings without a version (`## Unreleased`) are skipped, and `## [2026.1107.1] - 2026-11-07` is read too.
- `update.json` (next to the settings) keeps `last_run`. A start of a newer version sets `notes_from` to the version that ran before; the card **Pinky was updated** opens the entries after it, up to the running version, and the companion updates since. Closing it clears both. A first install shows nothing; Settings › Advanced › What's new shows this version's entries at any time.

## Tests

- `go test ./internal/update/`: versions, the feed (bad assets, redirects to HTTP, size limits), downloads (checksum, size, nothing left behind), the changelog, the state, idle rules, and on macOS staging (wrong version, identifier, two apps, not a zip, an unsigned bundle), the swap with a fake running app and a fake `open` (success, rollback after a crash, rollback when nothing launches), leftovers, `Locate` (translocated, read-only, ad hoc, Homebrew). `PINKY_SIGNED_APP=<a release Pinky.app> go test -run TestSignatureOfARelease ./internal/update/` checks the requirement against a real release and a modified copy.
- `go test ./ui -run 'Update|WhatsNew|Companion(UpdatesWhen|Waits)|SettingsCheckNow|RelaunchArgs'`: the cards and dialogs in `ui.Tester` against a local feed and a fake installer, automatic installs waiting for an agent, the person and the setting, Homebrew and download-only installs, What's new, the companion's automatic update with the fake gateway.
- `scripts/devlock.sh emulator-5562 env PINKY_DEVICE=emulator-5562 PINKY_COMPANION_OLD=<older apk> PINKY_COMPANION_APK=<bundled apk> go test -tags device -run TestDeviceCompanionUpdate -v ./internal/driver/android/`: off stays off, on stays on, and what Android does by itself; it leaves the bundled build installed and the setting as found.
- `PINKY_E2E_SIGN="Developer ID Application: … (492J8Q67PF)" go test -tags e2e -run TestUpdateEndToEnd -v -timeout 10m ./cmd/pinky/`: two e2e builds signed with the Developer ID in a temporary Applications folder and a local feed; the older one, left alone in the menu bar, updates itself, the newer one starts, drops the old bundle and shows What's new over MyGo's automation socket.

## Open questions

- **For the release pipeline:** the feed's repository (`update.Repo`), the asset names and formats (zip for macOS; the Windows installer; what Linux gets), that the macOS zip is of the stapled app, and that `CHANGELOG.md` follows the heading format. Releases should keep `latest` pointing at the newest stable release, since the feed URL follows it.
- **Agents during a restart:** `pinky mcp` could keep the agent's session across an update by reconnecting and replaying `initialize`; today the agent sees its MCP server exit.
- **Homebrew:** if a cask is published with `auto_updates true`, Pinky should update itself there too and not defer to `brew`.
- **Windows:** the silent install above.

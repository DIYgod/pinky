# Telemetry: usage statistics, crash reports, feedback

What people get and how to turn it off is in [docs/TELEMETRY.md](../TELEMETRY.md); the backend is in [worker/README.md](../../worker/README.md). These notes are for working on it.

## Decisions (the owner, 2026-11-07)

- Usage statistics and crash reports go to a Cloudflare Worker with D1 in the RSS3 account, https://pinky-telemetry.rss3.workers.dev (`telemetry.Endpoint`). Stored in D1 only; `worker/report.sh` reads them.
- Usage statistics are on by default and the first run shows no note about them. Crash reports go out by themselves while they are on: Settings › Privacy has one switch, and `DO_NOT_TRACK` stops both.
- Feedback goes to GitHub: Send Feedback opens a new issue in DIYgod/pinky with the Feedback form (`.github/ISSUE_TEMPLATE/feedback.yml`) filled in, and the person submits it with their own account. No e-mail field and no backend for it. The `feedback` label exists for the form.

## How it fits together

- `internal/telemetry.Client` (`Open`) owns `<data>/telemetry.json`: counters (`Count`, `Add`), gauges (`Max`), phones counted once a period by salted hash (`SeePhone`). `Run` saves every five minutes, sends crash reports at once and a usage report once a period is 24 hours old, backing off from an hour to a day after failures. The process that holds the gateway opens it (cmd/pinky `startTelemetry`), so one process at a time writes the file.
- Counting comes from three places, none of them inside the gateway: `Client.Observe` reads the gateway's views (phones, runs, needs); `telemetry.Gateway` wraps `*gateway.Gateway` for the desktop client and counts what the person does (it embeds the gateway, so the client's type assertions still find every method; `TestGatewayKeepsMethods` fails when a signature drifts); `Client.MCPMiddleware`, through `mcp.Server.Use`, counts tool calls, result statuses and agents. Only fixed vocabularies become counter names; anything else counts as `other`.
- Crashes: `CaptureCrashes` runs first in `runApp` and `runDaemon`. Each process writes a JSON header to `<data>/crashes/<process>-<start>-<pid>.run`, holds a lock on it (flock; LockFileEx on Windows) and gives it to `runtime/debug.SetCrashOutput`; `Stop` removes it on a clean exit. The next process to start turns files whose lock is free into reports (`<id>.json`, redacted) or `<id>.unclean` markers, which the client counts.
- The runtime prints a fatal error's message before it starts copying to the crash file, so such a report's reason is the function that failed, or the `fatal error:` line of `daemon.log` for a daemon `pinky mcp` started (its standard error goes there). Signals (`SIGQUIT: quit`, `SIGSEGV: …`) and panics keep their own first line.
- Feedback: `Client.Diagnostics` reads the phones, settings lines and the ends of `pinky.log` and `daemon.log`, redacted (`Redactor`); `telemetry.IssueURL` builds the GitHub address, dropping the oldest log lines until it is under 7.5 KB.

## Running and testing

```sh
go test ./internal/telemetry ./ui                          # crash tests re-run the test binary as a crashing child
go test -tags e2e -run TestTelemetryInTheApp ./cmd/pinky    # the app over MyGo automation; screen unlocked
cd worker && node --test && wrangler dev                    # then ./smoke.sh, and the Go client:
PINKY_TELEMETRY_TEST_URL=http://127.0.0.1:8787 go test -run TestAgainstTheWorker ./internal/telemetry
```

Development builds (version `0.0.0-dev`) never send; set `PINKY_TELEMETRY_URL` to a local Worker to try sending, or `-` to send nothing from any build. `PINKY_E2E_URLS` (e2e builds only) logs the addresses the app would open in the browser.

What was checked where: everything above on macOS 27.2 (arm64), including a SIGQUIT crash of the real app sent at the next start and the deployed Worker answering `smoke.sh`. Windows and Linux build and vet; the Windows file lock and the Linux `os_version` are untested on those systems.

## New strings in the client

For the zh-Hans catalogs (`cmd/pinky/locales`); the issue's contents (the version line and diagnostics) stay English, for whoever reads the issue.

- Menus: `Help` (Windows and Linux; macOS's Help role names itself), `Send Feedback…` (Help menu and the menu bar icon's menu).
- Settings › Privacy: `Helping Pinky get better`; `Share anonymous usage statistics and crash reports`; `Counts such as tasks run, tools used and phones connected, about once a day, and where Pinky's code stopped after a crash. Never your screens, what's typed, apps, phone names or PINs.`; `Off: DO_NOT_TRACK is set on this computer.`; `What's shared`; `The usage report as it would go out now`; `Show…` (label `Show what's shared`); `This build of Pinky sends nothing: it has no address to send to.`; group label `Usage statistics and crash reports`.
- Under Settings' sections: `Send feedback…` (label `Send feedback`).
- What Pinky shares: `What Pinky shares`; `About once a day, Pinky sends counts like these, with a random install ID, its version and your computer's system, and after a crash, where in Pinky's code it stopped. They never include what is on your phones' screens, what is typed, app names, phone names or IDs, PINs or file names. Turn this off in Settings › Privacy, or set DO_NOT_TRACK=1.`; `Nothing has been counted since the last report.`; `Close`.
- Send feedback: `Send feedback`; `What happened, or what would make Pinky better? If something went wrong, say what you were doing.`; `Title` with placeholder `Title (optional): a few words`; `Message` with placeholder `Your message`; `Attach diagnostics: versions, settings, phone models and Pinky's recent logs`; `Phone names and IDs, your user name, e-mail and IP addresses, app and task names are taken out.`; `Review` / `Hide` (labels `Review diagnostics` / `Hide diagnostics`); `Pinky opens a new issue on GitHub with this filled in; read it there and submit it with your GitHub account. Issues are public.`; `Not all of the logs fit in the link: Copy puts everything on the clipboard to paste into the issue.`; `Copy` (label `Copy feedback`, tooltip `Copy the message and diagnostics`); `Cancel`; `Open on GitHub`; toasts `Copied` and `Finish your feedback on GitHub. Thanks!`; error `Couldn't open the browser: <error>. Copy the feedback and send it another way.`

## Open

- No first-run note, by decision: people learn about the statistics from Settings, the README and docs/TELEMETRY.md. In the EU a random install id may count as personal data.
- Feedback links work for people outside the repository once it is public and the form is on `main`.

# Pinky

Let AI agents use your phone, with you in the loop. Pinky lets an agent (Claude Code, Codex, Cursor or a script) operate your iPhone or Android phone over MCP while you watch on your computer, approve risky steps and take over.

## Download

[The latest release](https://github.com/DIYgod/pinky-releases/releases/latest) has Pinky for each system:

- **macOS 12 or later:** `brew install --cask diygod/tap/pinky`, or the `.dmg` for your Mac (`arm64` for Apple silicon, `amd64` for Intel). The cask also gives you the `pinky` command, so agents can start `pinky mcp`.
- **Windows:** `pinky-setup-<version>-amd64.exe`.
- **Linux:** `pinky_<version>_amd64.deb`, or `pinky-<version>-linux-amd64.tar.gz`.

Pinky keeps itself up to date. Each release's notes say what changed.

## Versions

A version is the date of the release in China's time zone and that day's count: `2026.1107.2` is the second release of 7 November 2026, `2027.105.1` the first of 5 January 2027. Releases come often, sometimes several a day.

Every release carries `latest.json`, which describes it: its version, notes, and the archive, SHA-256 and size for each system. `https://github.com/DIYgod/pinky-releases/releases/latest/download/latest.json` is always the newest. `SHA256SUMS` lists every file's SHA-256.

## This repository

It holds the releases and the workflow that builds, signs and publishes them from Pinky's source, which is private for now.

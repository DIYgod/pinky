# Releasing Pinky

Pinky releases often, several times a day when there is something to ship, and nobody builds a release by hand: the agent that pushes to `main` writes the notes and runs one command, and a workflow does the rest. The rule for when a push is a release is in [CLAUDE.md](../CLAUDE.md).

## How a release happens

1. The notes wait in [CHANGELOG.md](../CHANGELOG.md) under `## Unreleased`.
2. `scripts/release.sh tag -push` gives them the next version, commits `chore(release): <version>`, tags it `v<version>` and pushes `main` and the tag together (`git push --atomic`).
3. The tag starts [.github/workflows/release.yml](../.github/workflows/release.yml) here, which checks that the tag has its notes and starts the release workflow of the public releases repository, [DIYgod/pinky-releases](https://github.com/DIYgod/pinky-releases), with the tag (`repository_dispatch`). That repository is public, so its runners, macOS's included, cost nothing.
4. That workflow (its source is [scripts/release/pinky-releases/](../scripts/release/pinky-releases/.github/workflows/release.yml)) checks out this repository at the tag and runs its scripts:
   - **Companion APK** (Ubuntu): `scripts/dist.sh companion`.
   - **Linux and Windows** (Ubuntu): `scripts/dist.sh windows`, `scripts/dist.sh linux`.
   - **macOS** (a macOS runner): `scripts/dist.sh darwin` builds Pinky.app for Apple silicon and Intel, each with its own FFmpeg, signs them with the Developer ID, notarizes both disk images at once and staples the tickets to the disk images and the apps.
   - **Publish** (Ubuntu): `release manifest` writes `latest.json` and `SHA256SUMS`, and `scripts/publish.sh` creates the GitHub release as a draft, uploads everything (`latest.json` last), publishes it, and updates the cask in [DIYgod/homebrew-tap](https://github.com/DIYgod/homebrew-tap).

On this Mac the whole build, notarization included, takes about two minutes once FFmpeg is built; GitHub's runners are slower, so expect roughly 10 to 20 minutes from the push to the published release (not measured yet), the first one longer while FFmpeg is built and cached.

## Versions

A version is `YEAR.MDD.N`: the release's date in Asia/Shanghai (the month without a leading zero, then the two-digit day) and that day's count from 1. `2026.1107.1`, `2026.1107.2`, `2027.105.1`. Every part is an integer without leading zeros, so versions are valid semver, compare part by part, and fit macOS bundle versions and Windows version resources. Tags are `v<version>`. `scripts/release.sh next` prints the next one, counting the tags here and on `origin`.

The version is not kept in the source. A release writes it into its checkout's `cmd/pinky/mygo.json` (`go run ./scripts/release stamp <version>`) before building, which gives it to the app (`Info.plist`, the Windows version resource, the packages), to `pinky version` (`-X main.version`), and to the companion APK's versionName. Builds from the source (`make app`, `go build`) say `0.0.0` or `0.0.0-dev`, which are not release versions.

## What a release publishes

| File | For |
| --- | --- |
| `pinky-<version>-darwin-arm64.zip`, `pinky-<version>-darwin-amd64.zip` | The updater and the Homebrew cask: `Pinky.app`, signed, notarized and stapled, zipped with `ditto -c -k --keepParent` |
| `pinky-<version>-arm64.dmg`, `pinky-<version>-amd64.dmg` | People on macOS: the disk image, signed, notarized and stapled |
| `pinky-<version>-windows-amd64.zip` | The updater on Windows: the app's files (`Pinky.exe` and the companion APK), as installed in `%LOCALAPPDATA%\Programs\Pinky` |
| `pinky-setup-<version>-amd64.exe` | People on Windows: the installer (not signed, so SmartScreen warns) |
| `pinky-<version>-linux-amd64.tar.gz` | The updater on Linux and `install.sh` of `mygo build`: `pinky`, its desktop entry and icon |
| `pinky_<version>_amd64.deb` | People on Debian and Ubuntu |
| `latest.json` | The updater: the release's manifest, below |
| `SHA256SUMS` | Everyone: every other file's SHA-256, as `sha256sum` writes it |

The Windows and Linux packages do not carry FFmpeg yet, so their live mirror needs FFmpeg 9 installed (see the README).

### latest.json

```json
{
  "version": "2026.1107.1",
  "tag": "v2026.1107.1",
  "date": "2026-11-07T08:09:10Z",
  "notes": "- The version's notes from CHANGELOG.md, in Markdown.",
  "assets": {
    "darwin-amd64": { "url": "https://github.com/DIYgod/pinky-releases/releases/download/v2026.1107.1/pinky-2026.1107.1-darwin-amd64.zip", "sha256": "…", "size": 41234567 },
    "darwin-arm64": { "url": "…/pinky-2026.1107.1-darwin-arm64.zip", "sha256": "…", "size": 40123456 },
    "linux-amd64": { "url": "…/pinky-2026.1107.1-linux-amd64.tar.gz", "sha256": "…", "size": 13300000 },
    "windows-amd64": { "url": "…/pinky-2026.1107.1-windows-amd64.zip", "sha256": "…", "size": 14000000 }
  }
}
```

`https://github.com/DIYgod/pinky-releases/releases/latest/download/latest.json` is always the newest release's: a release becomes the latest only when no newer one is out, and `latest.json` is uploaded after the files it names, before the release is published. `date` is when the release was assembled, in UTC. `notes` is the version's section of CHANGELOG.md without its heading, which `go run ./scripts/release notes <version>` prints.

### CHANGELOG.md

Newest first. Each release is a `## <version> (<YYYY-MM-DD>)` heading followed by short bullets for people; notes not released yet wait under `## Unreleased` at the top, which a dev build of the app should leave out. A heading's first word is its version (as MyGo's `update.ReleaseNotes` reads it too), and headings inside fenced code blocks do not count.

## Setting it up

This is done once, and only the owner can do most of it.

1. **The public repositories.** `CREATE=1 scripts/release.sh setup` creates [DIYgod/pinky-releases](https://github.com/DIYgod/pinky-releases) and [DIYgod/homebrew-tap](https://github.com/DIYgod/homebrew-tap) (both public), copies in the release workflow and the READMEs from `scripts/release/`, and lists the secrets that are missing. Run `scripts/release.sh setup` again whenever `scripts/release/` changes, so that the workflow there stays this repository's.
2. **A token for the workflows, `RELEASE_TOKEN`.** Create a fine-grained personal access token at <https://github.com/settings/personal-access-tokens/new>: resource owner DIYgod; repository access "Only select repositories": `pinky`, `pinky-releases` and `homebrew-tap`; permissions: Contents "Read and write". The source's workflow uses it to start the release workflow, and the release workflow to check out the source and push the cask. Store it in both repositories (gh asks for the value, so it never lands in a file):

   ```sh
   gh secret set RELEASE_TOKEN --repo DIYgod/pinky
   gh secret set RELEASE_TOKEN --repo DIYgod/pinky-releases
   ```

3. **The Developer ID certificate, `MACOS_CERTIFICATE` and `MACOS_CERTIFICATE_PASSWORD`.** In Keychain Access, under My Certificates in the login keychain, right-click "Developer ID Application: NATURAL SELECTION LABS PTE. LTD. (492J8Q67PF)", choose Export, and save it as a `.p12` with a new password. Then:

   ```sh
   base64 -i ~/Desktop/developer-id.p12 | gh secret set MACOS_CERTIFICATE --repo DIYgod/pinky-releases
   gh secret set MACOS_CERTIFICATE_PASSWORD --repo DIYgod/pinky-releases
   rm ~/Desktop/developer-id.p12
   ```

4. **An App Store Connect API key for notarization, `NOTARY_API_KEY`, `NOTARY_API_KEY_ID` and `NOTARY_API_ISSUER`.** At <https://appstoreconnect.apple.com/access/integrations/api> (Users and Access › Integrations › App Store Connect API › Team Keys; it takes the Admin or Account Holder role), add a key with the Developer role, download its `AuthKey_<id>.p8` (Apple offers it once), and note its Key ID and the Issuer ID above the list. A key that other workflows of the team already use works too.

   ```sh
   gh secret set NOTARY_API_KEY --repo DIYgod/pinky-releases < ~/Downloads/AuthKey_<id>.p8
   gh secret set NOTARY_API_KEY_ID --repo DIYgod/pinky-releases --body <key id>
   gh secret set NOTARY_API_ISSUER --repo DIYgod/pinky-releases --body <issuer id>
   ```

   The Apple ID instead also works: `APPLE_ID`, `APPLE_TEAM_ID` (492J8Q67PF) and `APPLE_APP_PASSWORD`, an app-specific password from <https://account.apple.com>.

Repository variables change the defaults: `RELEASES_REPO` here; `SOURCE_REPO`, `TAP_REPO` and `MACOS_SIGN_IDENTITY` in the releases repository.

## By hand

```sh
scripts/release.sh next                   # the version the next release gets
scripts/release.sh tag -n                 # what tag would do
scripts/release.sh build                  # a dry run: build HEAD on this Mac into dist/<version>/
NOTARIZE=1 scripts/release.sh build       # the same, notarized
scripts/release.sh trigger v2026.1107.1   # run the release workflow for a tag again
scripts/release.sh publish v2026.1107.1   # build and publish a tag from this Mac
```

`build` and `publish` check the commit out in a worktree of their own, so nothing uncommitted gets in, and build what the workflow builds into `dist/<version>/`, with the cask as `dist/pinky-<version>.rb`; FFmpeg's builds are kept in `~/Library/Caches/pinky-release`. They sign with the Developer ID in the login keychain and notarize with the notarytool profile `pinky-notary`, so they need no secrets. A dry run leaves out what it cannot make, with a warning: without nasm (`brew install nasm`) the Intel FFmpeg lacks its assembly, and without NSIS (`brew install makensis`) there is no Windows installer. `publish` stops instead, and needs both (or `STRICT=0`, to publish without them).

`publish` is for when the workflow cannot run (the secrets are not set up, or GitHub Actions is down). It publishes to the same places as the workflow.

## When something fails

- **The release workflow failed:** open the run in [the releases repository's Actions](https://github.com/DIYgod/pinky-releases/actions) and choose "Re-run failed jobs". Running it again for the same tag is safe: a draft release gets its files replaced, a published one keeps its files and only the cask is brought up to date.
- **The tag was pushed but nothing started:** the source's release workflow tells why (often a missing `RELEASE_TOKEN`). `scripts/release.sh trigger <tag>` starts the release workflow by hand.
- **A release is broken:** release a fix. Do not delete or move a published tag; installed apps and the cask follow the newest version.
- **Two releases at once:** each tag runs on its own. The later version becomes the latest release whichever finishes first, and the cask never goes back to an older version.

## Changing the pipeline

The build and publish steps are the scripts here (`scripts/dist.sh`, `scripts/publish.sh`, `scripts/release/`), and each release runs them as they are at its tag. Only the workflow's jobs live in the releases repository: change them in `scripts/release/pinky-releases/` and run `scripts/release.sh setup`. `go test ./scripts/release` tests the versions, the changelog, the tag, the manifest and the cask.

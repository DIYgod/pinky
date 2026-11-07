#!/bin/sh
# Publishes a release that scripts/dist.sh built and `release manifest`
# finished (latest.json, SHA256SUMS), for scripts/release.sh and the
# release workflow (see docs/RELEASING.md):
#
#   scripts/publish.sh <dir>
#
# - creates the GitHub release v<version> in RELEASES_REPO as a draft,
#   uploads every file in <dir> to it, latest.json last, and publishes it:
#   as the latest release, unless a newer one is out already, so that
#   releases/latest/download/latest.json is always the newest version's;
# - then writes the version into the Homebrew cask, Casks/pinky.rb in
#   TAP_REPO, unless the cask has a newer one.
#
# A release that is published already keeps its files: running this again
# only brings the cask up to date.
#
# Environment:
#   RELEASES_REPO  the public repository of the releases (DIYgod/pinky-releases)
#   TAP_REPO       the Homebrew tap (DIYgod/homebrew-tap)
#   GH_TOKEN       for gh, which must be able to write RELEASES_REPO
#                  (default: gh's own login)
#   TAP_TOKEN      a token that may push to TAP_REPO (default: gh's login)
set -eu

dir=${1:?usage: scripts/publish.sh <dir>}
case $dir in /*) ;; *) dir=$(pwd)/$dir ;; esac
cd "$(dirname "$0")/.."
repo=${RELEASES_REPO:-DIYgod/pinky-releases}
tap=${TAP_REPO:-DIYgod/homebrew-tap}
# brew's name of the tap: DIYgod/homebrew-tap is diygod/tap.
tapname=$(printf '%s/%s' "${tap%%/*}" "${tap#*/homebrew-}" | tr '[:upper:]' '[:lower:]')

say() { printf '==> %s\n' "$*" >&2; }
die() {
	printf 'publish: %s\n' "$*" >&2
	exit 1
}
release() { go run ./scripts/release "$@"; }

[ -f "$dir/latest.json" ] || die "$dir has no latest.json (go run ./scripts/release manifest)"
version=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$dir/latest.json" | head -1)
release newer "$version" >/dev/null || die "latest.json has no release version"
tag=v$version
work=$(mktemp -d "${TMPDIR:-/tmp}/pinky-publish.XXXXXX")
trap 'rm -rf "$work"' EXIT

# The release page: the notes, then where to get Pinky.
{
	release notes "$version"
	cat <<EOF

---

**macOS:** \`brew install --cask $tapname/pinky\`, or the \`.dmg\` for your Mac below (\`arm64\` for Apple silicon, \`amd64\` for Intel). Pinky keeps itself up to date.
**Windows:** \`pinky-setup-$version-amd64.exe\`. **Linux:** \`pinky_${version}_amd64.deb\`, or \`pinky-$version-linux-amd64.tar.gz\`.
EOF
} >"$work/notes.md"

state=$(gh release view "$tag" --repo "$repo" --json isDraft --jq .isDraft 2>/dev/null || echo none)
if [ "$state" = false ]; then
	say "$repo has published $tag already: its files stay as they are"
	gh release download "$tag" --repo "$repo" --pattern latest.json --dir "$work/published"
	manifest=$work/published/latest.json
else
	if [ "$state" = none ]; then
		say "creating the draft release $tag in $repo"
		gh release create "$tag" --repo "$repo" --draft --title "Pinky $version" --notes-file "$work/notes.md"
	else
		gh release edit "$tag" --repo "$repo" --title "Pinky $version" --notes-file "$work/notes.md"
	fi
	say "uploading $(find "$dir" -maxdepth 1 -type f ! -name '.*' | wc -l | tr -d ' ') files"
	find "$dir" -maxdepth 1 -type f ! -name '.*' ! -name latest.json -exec gh release upload "$tag" --repo "$repo" --clobber {} +
	# The manifest last, so that it never names a file still on its way.
	gh release upload "$tag" --repo "$repo" --clobber "$dir/latest.json"
	latest=$(gh release view --repo "$repo" --json tagName --jq .tagName 2>/dev/null || true)
	if release newer "$version" "${latest#v}"; then
		gh release edit "$tag" --repo "$repo" --draft=false --latest
	else
		say "$latest is newer: $tag is published without becoming the latest release"
		gh release edit "$tag" --repo "$repo" --draft=false --latest=false
	fi
	say "published https://github.com/$repo/releases/tag/$tag"
	manifest=$dir/latest.json
fi

# The cask. git gets its credentials from gh: TAP_TOKEN's, else gh's own.
tapgit() {
	if [ -n "${TAP_TOKEN:-}" ]; then
		GH_TOKEN=$TAP_TOKEN git -c credential.helper= -c 'credential.helper=!gh auth git-credential' "$@"
	else
		git -c credential.helper= -c 'credential.helper=!gh auth git-credential' "$@"
	fi
}
tapcommit() {
	if [ "${CI:-}" = true ]; then
		set -- -c user.name="github-actions[bot]" -c user.email="41898282+github-actions[bot]@users.noreply.github.com" "$@"
	fi
	git -C "$work/tap" "$@"
}
release cask -repo "$repo" "$manifest" >"$work/pinky.rb"
tapgit clone --quiet --depth 1 "https://github.com/$tap.git" "$work/tap"
mkdir -p "$work/tap/Casks"
attempt=1
while :; do
	current=$(sed -n 's/^ *version "\([^"]*\)".*/\1/p' "$work/tap/Casks/pinky.rb" 2>/dev/null | head -1)
	if [ -n "$current" ] && ! release newer "$version" "$current"; then
		say "the cask has pinky $current already; leaving it"
		break
	fi
	cp "$work/pinky.rb" "$work/tap/Casks/pinky.rb"
	git -C "$work/tap" add Casks/pinky.rb
	tapcommit commit --quiet -m "pinky $version"
	if tapgit -C "$work/tap" push --quiet origin HEAD; then
		say "brew install --cask $tapname/pinky installs $version: https://github.com/$tap/blob/HEAD/Casks/pinky.rb"
		break
	fi
	[ $attempt -lt 3 ] || die "pushing the cask to $tap failed $attempt times"
	attempt=$((attempt + 1))
	say "the tap moved; trying again"
	git -C "$work/tap" reset --quiet --hard HEAD~1
	tapgit -C "$work/tap" pull --quiet --rebase origin HEAD
done

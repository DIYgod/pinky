#!/bin/sh
# Pinky's releases; docs/RELEASING.md explains them.
#
#   scripts/release.sh next          the version the next release gets
#   scripts/release.sh tag [-push] [-n] [-trailer "Co-Authored-By: …"]
#                                    release what CHANGELOG.md's Unreleased
#                                    notes describe: give them the next
#                                    version, commit, tag v<version>, and with
#                                    -push push main and the tag together,
#                                    which starts the release workflow
#   scripts/release.sh build [<ref>] build a release on this Mac into
#                                    dist/<version>/ and publish nothing: a
#                                    dry run (NOTARIZE=1 notarizes too)
#   scripts/release.sh publish <tag> build and publish a release from this
#                                    Mac, for when the workflow cannot
#   scripts/release.sh trigger <tag> run the release workflow for a tag again
#   scripts/release.sh setup         copy the release workflow and the READMEs
#                                    into the releases repository and the tap,
#                                    and list the secrets they lack (CREATE=1
#                                    creates the public repositories first)
#
# RELEASES_REPO and TAP_REPO name the repositories (DIYgod/pinky-releases,
# DIYgod/homebrew-tap).
set -eu

cd "$(dirname "$0")/.."
root=$(pwd)
repo=${RELEASES_REPO:-DIYgod/pinky-releases}
tap=${TAP_REPO:-DIYgod/homebrew-tap}
export RELEASES_REPO=$repo TAP_REPO=$tap

say() { printf '==> %s\n' "$*" >&2; }
die() {
	printf 'release: %s\n' "$*" >&2
	exit 1
}
# The release tool, built once (build/ is ignored by git).
go build -o build/bin/release ./scripts/release
release() { "$root/build/bin/release" "$@"; }

# build_release builds the release of a commit (a tag, or any ref for a
# dry run) into dist/<version>/, in a checkout of its own so that nothing
# uncommitted gets in, and publishes it when asked to.
build_release() {
	ref=$1
	publishing=$2
	sha=$(git rev-parse --verify --quiet "$ref^{commit}") || die "no commit $ref"
	version=
	for t in $(git tag --points-at "$sha" --list 'v*'); do
		if release newer "${t#v}" >/dev/null 2>&1; then
			version=${t#v}
		fi
	done
	dryrun=
	if [ -z "$version" ]; then
		[ "$publishing" = 0 ] || die "$ref is not a release tag"
		version=$(release next)
		dryrun=1
		say "$ref has no release tag: building it as $version, the next version, for a dry run"
	fi
	[ "$(uname -s)" = Darwin ] || die "releases are built on a Mac (Pinky.app is signed and notarized there)"

	out=$root/dist/$version
	work=$(mktemp -d "${TMPDIR:-/tmp}/pinky-release.XXXXXX")
	# The worktree goes when this ends, however it ends.
	trap 'git -C "$root" worktree remove --force "$work/src" 2>/dev/null || true; rm -rf "$work"' EXIT
	trap 'exit 130' INT TERM
	git worktree add --quiet --detach "$work/src" "$sha"
	rm -rf "$out"
	mkdir -p "$out"
	(cd "$work/src" && release stamp "$version")

	# FFmpeg's builds outlive the worktree.
	export FFMPEG_CACHE="${FFMPEG_CACHE:-$HOME/Library/Caches/pinky-release/ffmpeg}"
	export COMPANION_APK="$work/companion/pinky-companion.apk"
	"$work/src/scripts/dist.sh" companion "$work/companion"
	"$work/src/scripts/dist.sh" darwin "$out"
	"$work/src/scripts/dist.sh" windows "$out"
	"$work/src/scripts/dist.sh" linux "$out"
	if [ -n "$dryrun" ]; then
		(cd "$work/src" && release manifest -dir "$out" -version "$version" -repo "$repo" -unreleased) ||
			(cd "$work/src" && release manifest -dir "$out" -version "$version" -repo "$repo" -notes "A dry run of $ref, with no notes.")
	else
		(cd "$work/src" && release manifest -dir "$out" -version "$version" -repo "$repo")
	fi
	release cask -repo "$repo" "$out/latest.json" >"$root/dist/pinky-$version.rb"
	if [ "$publishing" = 1 ]; then
		"$work/src/scripts/publish.sh" "$out"
	fi
	ls -l "$out" >&2
	say "the release's files are in $out, its cask is dist/pinky-$version.rb"
}

cmd=${1:-}
[ $# -gt 0 ] && shift
case $cmd in
next | tag)
	release "$cmd" "$@"
	;;
build)
	export STRICT="${STRICT:-0}" NOTARIZE="${NOTARIZE:-0}"
	build_release "${1:-HEAD}" 0
	;;
publish)
	tag=${1:?usage: scripts/release.sh publish <tag>}
	git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null || die "origin has no tag $tag: push it first"
	export STRICT=1 NOTARIZE=1
	build_release "$tag" 1
	;;
trigger)
	tag=${1:?usage: scripts/release.sh trigger <tag>}
	gh workflow run release.yml --repo "$repo" -f tag="$tag"
	say "started; see https://github.com/$repo/actions/workflows/release.yml"
	;;
setup)
	for r in "$repo" "$tap"; do
		if gh repo view "$r" --json name >/dev/null 2>&1; then
			continue
		fi
		[ "${CREATE:-0}" = 1 ] || die "$r does not exist; CREATE=1 scripts/release.sh setup creates it, as a public repository"
		case $r in
		"$repo") description="Downloads of Pinky: let AI agents use your phone, with you in the loop" ;;
		*) description="Homebrew casks: brew install --cask $(printf '%s' "${tap%%/*}" | tr '[:upper:]' '[:lower:]')/tap/pinky" ;;
		esac
		say "creating the public repository $r"
		gh repo create "$r" --public --description "$description" --disable-wiki
	done
	work=$(mktemp -d "${TMPDIR:-/tmp}/pinky-setup.XXXXXX")
	trap 'rm -rf "$work"' EXIT
	# Each repository gets its files from scripts/release/<its name>/.
	for pair in "$repo:scripts/release/pinky-releases" "$tap:scripts/release/homebrew-tap"; do
		r=${pair%%:*}
		src=${pair#*:}
		dst=$work/${r#*/}
		gh repo clone "$r" "$dst" -- --quiet
		(cd "$src" && find . -type f) | while read -r f; do
			mkdir -p "$dst/$(dirname "$f")"
			cp "$src/$f" "$dst/$f"
		done
		git -C "$dst" add -A
		if git -C "$dst" diff --cached --quiet; then
			say "$r is up to date"
			continue
		fi
		git -C "$dst" commit --quiet -m "chore: update from DIYgod/pinky's scripts/release"
		git -C "$dst" push --quiet origin HEAD:refs/heads/main
		say "updated $r"
	done
	# The secrets the workflows need, by name; docs/RELEASING.md says what
	# each is.
	missing=
	have=$(gh secret list --repo "$repo" --json name --jq '.[].name' 2>/dev/null || true)
	for s in RELEASE_TOKEN MACOS_CERTIFICATE MACOS_CERTIFICATE_PASSWORD NOTARY_API_KEY NOTARY_API_KEY_ID NOTARY_API_ISSUER; do
		printf '%s\n' "$have" | grep -qx "$s" || missing="$missing $repo:$s"
	done
	source_repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner)
	have=$(gh secret list --repo "$source_repo" --json name --jq '.[].name' 2>/dev/null || true)
	printf '%s\n' "$have" | grep -qx RELEASE_TOKEN || missing="$missing $source_repo:RELEASE_TOKEN"
	if [ -n "$missing" ]; then
		say "secrets to add (see docs/RELEASING.md):$missing"
	else
		say "every secret is there"
	fi
	;;
*)
	sed -n '2,/^set -eu/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//' >&2
	exit 2
	;;
esac

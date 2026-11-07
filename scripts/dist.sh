#!/bin/sh
# Builds what a release of Pinky ships, for scripts/release.sh and the
# release workflow (see docs/RELEASING.md):
#
#   scripts/dist.sh companion <out>   the companion APK, <out>/pinky-companion.apk
#   scripts/dist.sh darwin <out>      Pinky.app for Apple silicon and Intel Macs,
#                                     signed with the Developer ID, notarized and
#                                     stapled: pinky-<version>-darwin-<arch>.zip
#                                     (the updater's and Homebrew's) and
#                                     pinky-<version>-<arch>.dmg
#   scripts/dist.sh windows <out>     pinky-setup-<version>-amd64.exe (the installer,
#                                     which the updater opens too) and
#                                     pinky-<version>-windows-amd64.zip (the app's
#                                     files, to run it without installing)
#   scripts/dist.sh linux <out>       pinky-<version>-linux-amd64.tar.gz and the .deb
#
# The version is cmd/pinky/mygo.json's, which a release writes into a
# checkout of its tag first (go run ./scripts/release stamp <version>).
# Every app carries <out>/pinky-companion.apk, so build that first.
#
# Environment:
#   SIGN             the identity that signs Pinky.app (default: the Developer ID
#                    of NATURAL SELECTION LABS); "-" signs ad hoc
#   NOTARIZE         0 skips notarization, for a dry run (default 1)
#   NOTARY_PROFILE   the notarytool keychain profile (default pinky-notary)
#   NOTARY_KEYCHAIN  the keychain that holds it (default: the login keychain)
#   FFMPEG_CACHE     where FFmpeg's builds are kept (default build/ffmpeg-dist)
#   STRICT           1 stops rather than leave out what a release must have:
#                    the companion APK, the Windows installer, notarization,
#                    FFmpeg's assembly for Intel Macs
set -eu

# Homebrew's makensis (NSIS 3.13, which mygo build runs for the Windows
# installer) aborts with std::bad_alloc unless the character type is UTF-8,
# and a shell may have no locale at all, or C. The build then runs in
# C.UTF-8, which macOS and Linux both have. (Ubuntu's NSIS works either way.)
case ${LC_ALL:-${LC_CTYPE:-${LANG:-}}} in
*[Uu][Tt][Ff]-8* | *[Uu][Tt][Ff]8*) ;;
*) export LC_ALL=C.UTF-8 ;;
esac

usage="usage: scripts/dist.sh companion|darwin|windows|linux <out>"
cmd=${1:?$usage}
out=${2:?$usage}
case $out in /*) ;; *) out=$(pwd)/$out ;; esac
cd "$(dirname "$0")/.."
root=$(pwd)
mkdir -p "$out"

DEVELOPER_ID="Developer ID Application: NATURAL SELECTION LABS PTE. LTD. (492J8Q67PF)"
strict=${STRICT:-0}
stage=$root/build/dist
resources=$root/cmd/pinky/resources
version=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' cmd/pinky/mygo.json | head -1)

say() { printf '==> %s\n' "$*" >&2; }
die() {
	printf 'dist: %s\n' "$*" >&2
	exit 1
}
# lacking says what a build leaves out, and stops a strict one.
lacking() {
	if [ "$strict" = 1 ]; then
		die "$*"
	fi
	printf 'dist: warning: %s\n' "$*" >&2
}
sha256() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		sha256sum "$1" | cut -d' ' -f1
	fi
}

[ -n "$version" ] || die "cmd/pinky/mygo.json has no version"

# The companion APK goes into every app through MyGo's resources, which
# are staged for the build only.
unstage() {
	rm -f "$resources/pinky-companion.apk"
	rmdir "$resources" 2>/dev/null || true
}
trap unstage EXIT
trap 'exit 130' INT TERM

# build runs mygo build for platforms into $stage, with more flags.
build() {
	platforms=$1
	shift
	rm -rf "$stage"
	mkdir -p "$stage"
	apk=${COMPANION_APK:-$out/pinky-companion.apk}
	if [ -f "$apk" ]; then
		mkdir -p "$resources"
		cp "$apk" "$resources/pinky-companion.apk"
	else
		lacking "no companion APK at $apk (scripts/dist.sh companion): the apps will not offer to install it"
	fi
	say "mygo build $platforms (Pinky $version)"
	MYGO_LDFLAGS="-X main.version=$version" go tool mygo build -platform "$platforms" "$@" -o "$stage" cmd/pinky
	unstage
}

companion() {
	say "the companion APK (Pinky $version)"
	make companion
	apk=$root/android/companion/app/build/outputs/apk/debug/app-debug.apk
	# The version Pinky reads from the APK it carries.
	info=$(unzip -p "$apk" assets/pinky-companion.json)
	case $info in
	*"\"version_name\":\"$version\""*) ;;
	*) die "the APK says $info, not version $version" ;;
	esac
	cp "$apk" "$out/pinky-companion.apk"
	say "$out/pinky-companion.apk: $info"
}

# ffmpeg_for prints the prefix of FFmpeg built for a Mac's processor
# (arm64, amd64), building it the first time. The builds are kept by the
# hash of scripts/ffmpeg.sh, which changes with what they hold.
ffmpeg_for() {
	arch=$1
	dir=${FFMPEG_CACHE:-$root/build/ffmpeg-dist}/$(sha256 scripts/ffmpeg.sh | cut -c1-12)-$arch
	noasm=
	if [ ! -f "$dir/BUILD.txt" ] && [ "$arch" = amd64 ] && ! command -v nasm >/dev/null 2>&1; then
		lacking "no nasm (brew install nasm): FFmpeg for Intel Macs is built without its assembly, which decodes slower"
		noasm=1
		dir=$dir-noasm
	fi
	if [ ! -f "$dir/BUILD.txt" ]; then
		mkdir -p "$(dirname "$dir")"
		say "FFmpeg for $arch, in $dir (a few minutes the first time)"
		if ! FFMPEG_NO_ASM=$noasm scripts/ffmpeg.sh "$dir" "$arch" >"$dir.log" 2>&1; then
			tail -30 "$dir.log" >&2
			die "building FFmpeg for $arch failed; its log is $dir.log"
		fi
	fi
	echo "$dir"
}

# machine is how lipo names a processor.
machine() {
	case $1 in
	amd64) echo x86_64 ;;
	*) echo "$1" ;;
	esac
}

# notarize submits both disk images at once, waits, and staples the
# tickets to the disk images and to the apps, which the zips carry.
notarize() {
	set -- --keychain-profile "${NOTARY_PROFILE:-pinky-notary}"
	if [ -n "${NOTARY_KEYCHAIN:-}" ]; then
		set -- "$@" --keychain "$NOTARY_KEYCHAIN"
	fi
	pids=
	for arch in arm64 amd64; do
		dmg=$stage/darwin-$arch/pinky-$version-$arch.dmg
		say "notarizing $(basename "$dmg")"
		xcrun notarytool submit "$dmg" "$@" --wait --output-format json >"$stage/notary-$arch.json" 2>"$stage/notary-$arch.log" &
		pids="$pids $!"
	done
	for pid in $pids; do
		wait "$pid" || true
	done
	for arch in arm64 amd64; do
		app=$stage/darwin-$arch/Pinky.app
		dmg=$stage/darwin-$arch/pinky-$version-$arch.dmg
		json=$stage/notary-$arch.json
		status=$(plutil -extract status raw -o - "$json" 2>/dev/null || echo "no answer")
		id=$(plutil -extract id raw -o - "$json" 2>/dev/null || true)
		if [ "$status" != Accepted ]; then
			cat "$stage/notary-$arch.log" "$json" >&2 2>/dev/null || true
			if [ -n "$id" ]; then
				xcrun notarytool log "$id" "$@" >&2 || true
			fi
			die "Apple's notary service answered \"$status\" for the $arch disk image"
		fi
		say "notarized the $arch disk image ($id)"
		xcrun stapler staple "$dmg" >/dev/null
		xcrun stapler staple "$app" >/dev/null
		spctl --assess --type execute "$app"
		spctl --assess --type open --context context:primary-signature "$dmg"
	done
}

darwin() {
	[ "$(uname -s)" = Darwin ] || die "Pinky.app is built on macOS"
	sign=${SIGN:-$DEVELOPER_ID}
	notarize=${NOTARIZE:-1}
	if [ "$sign" = - ] && [ "$notarize" = 1 ]; then
		die "an app signed ad hoc cannot be notarized: set SIGN to a Developer ID, or NOTARIZE=0"
	fi
	ff_arm64=$(ffmpeg_for arm64)
	ff_amd64=$(ffmpeg_for amd64)
	build darwin/arm64,darwin/amd64 -sign "$sign" -skip-notarize
	for arch in arm64 amd64; do
		app=$stage/darwin-$arch/Pinky.app
		dmg=$stage/darwin-$arch/pinky-$version-$arch.dmg
		if [ "$arch" = arm64 ]; then ff=$ff_arm64; else ff=$ff_amd64; fi
		SIGN="$sign" scripts/macapp.sh "$app" "$ff" "$dmg"
		want=$(machine "$arch")
		for bin in "$app/Contents/MacOS/Pinky" "$app"/Contents/Frameworks/*.dylib; do
			got=$(lipo -archs "$bin")
			[ "$got" = "$want" ] || die "$bin is for $got, not $want"
		done
		[ "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist")" = "$version" ] ||
			die "$app is not version $version"
	done
	if [ "$notarize" = 1 ]; then
		notarize
	else
		lacking "not notarized (NOTARIZE=0): Gatekeeper refuses these apps on other Macs"
	fi
	for arch in arm64 amd64; do
		app=$stage/darwin-$arch/Pinky.app
		zip=$out/pinky-$version-darwin-$arch.zip
		rm -f "$zip"
		ditto -c -k --keepParent "$app" "$zip"
		# What the updater and Homebrew unpack must still be signed (and
		# stapled).
		check=$stage/check-$arch
		rm -rf "$check"
		ditto -x -k "$zip" "$check"
		codesign --verify --deep --strict "$check/Pinky.app"
		if [ "$notarize" = 1 ]; then
			xcrun stapler validate "$check/Pinky.app" >/dev/null
		fi
		rm -rf "$check"
		cp "$stage/darwin-$arch/pinky-$version-$arch.dmg" "$out/"
		say "$zip and pinky-$version-$arch.dmg"
	done
}

windows() {
	skip=
	if ! command -v makensis >/dev/null 2>&1; then
		lacking "no makensis (NSIS: brew install makensis, apt install nsis): no Windows installer"
		skip=-skip-installer
	fi
	# shellcheck disable=SC2086 # skip is one flag or none
	build windows/amd64 $skip
	dir=$stage/windows-amd64
	files=$stage/windows-files
	rm -rf "$files"
	mkdir -p "$files"
	for f in "$dir"/*; do
		case ${f##*/} in
		pinky-setup-* | install.ps1 | SHA256SUMS) ;;
		*) cp -R "$f" "$files/" ;;
		esac
	done
	[ -f "$files/Pinky.exe" ] || die "mygo build made no Pinky.exe"
	zip=$out/pinky-$version-windows-amd64.zip
	rm -f "$zip"
	go run ./scripts/release zip "$zip" "$files"
	say "$zip"
	installer=$dir/pinky-setup-$version-amd64.exe
	if [ -f "$installer" ]; then
		cp "$installer" "$out/"
		say "$out/${installer##*/}"
	elif [ -z "$skip" ]; then
		lacking "mygo build made no Windows installer"
	fi
}

linux() {
	build linux/amd64
	dir=$stage/linux-amd64
	for f in "pinky-$version-linux-amd64.tar.gz" "pinky_${version}_amd64.deb"; do
		[ -f "$dir/$f" ] || die "mygo build made no $f"
		cp "$dir/$f" "$out/"
		say "$out/$f"
	done
}

case $cmd in
companion | darwin | windows | linux) "$cmd" ;;
*) die "$usage" ;;
esac

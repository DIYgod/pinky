#!/bin/sh
# Builds Pinky's iPhone app (see README.md).
#
#   ios/build.sh core                    the Go core only (libpinkycore.a)
#   ios/build.sh full  [xcodebuild args] app + packet tunnel + Live Activity
#   ios/build.sh lite  [xcodebuild args] app + Live Activity, no tunnel (use
#                                        LocalDevVPN; for a personal team)
#
# Xcode builds unsigned; this script signs each part with the development
# profile of its App ID, or the team's wildcard profile when it has none, so
# no Xcode account is needed. The full build needs explicit App IDs with the
# Network Extension capability and their profiles (README.md). PINKY_SIGN=0
# skips signing (a compile check).
#
# PINKY_BUNDLE_ID sets the app's bundle id (the extensions add .tunnel and
# .activity; lite adds .lite); it must be an App ID of PINKY_TEAM, which
# overrides the team. PINKY_PAIRING=<rppair pair's file> and
# PINKY_UDID=<udid> put the pairing into the app (build/Pairing.json, never
# committed), which moves it into the Keychain on its first start.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
app=$here/app
out=$app/build
team=${PINKY_TEAM:-492J8Q67PF}
what=${1:-full}
[ $# -gt 0 ] && shift

mkdir -p "$out/core"

sdk=$(xcrun --sdk iphoneos --show-sdk-path)
cc="$(xcrun --sdk iphoneos -f clang) -isysroot $sdk -arch arm64 -miphoneos-version-min=17.0"
echo "== Go core"
(cd "$root" && GOOS=ios GOARCH=arm64 CGO_ENABLED=1 CC="$cc" \
	go build -buildmode=c-archive -trimpath -ldflags=-s -o "$out/core/libpinkycore.a" ./ios/core)
[ "$what" = core ] && exit 0

if [ -n "${PINKY_PAIRING:-}" ]; then
	: "${PINKY_UDID:?PINKY_UDID is needed with PINKY_PAIRING}"
	umask 077
	printf '{"udid":"%s","pairing":"%s"}\n' "$PINKY_UDID" "$(base64 <"$PINKY_PAIRING" | tr -d '\n')" >"$out/Pairing.json"
elif [ ! -f "$out/Pairing.json" ]; then
	echo '{}' >"$out/Pairing.json" # a build without a pairing
fi

echo "== Xcode project"
(cd "$app" && xcodegen generate --quiet)

profiles="$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles"

# profile <application-identifier>: the newest profile Xcode keeps for it
# that has not expired.
profile() {
	now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	best= bestDate=
	for p in "$profiles"/*.mobileprovision; do
		security cms -D -i "$p" >"$out/profile.plist" 2>/dev/null || continue
		[ "$(plutil -extract Entitlements.application-identifier raw -o - "$out/profile.plist")" = "$1" ] || continue
		[ "$(plutil -extract ExpirationDate raw -o - "$out/profile.plist")" \> "$now" ] || continue
		d=$(plutil -extract CreationDate raw -o - "$out/profile.plist")
		if [ -z "$best" ] || [ "$d" \> "$bestDate" ]; then
			best=$p bestDate=$d
		fi
	done
	rm -f "$out/profile.plist"
	[ -n "$best" ] && printf '%s\n' "$best"
}

# identity <profile>: one of the profile's certificates we hold, by SHA-1.
identity() {
	security cms -D -i "$1" >"$out/profile.plist"
	have=$(security find-identity -v -p codesigning | awk '{ print $2 }')
	i=0
	while cert=$(plutil -extract "DeveloperCertificates.$i" raw -o - "$out/profile.plist" 2>/dev/null); do
		h=$(printf '%s' "$cert" | base64 -d | shasum -a 1 | awk '{ print toupper($1) }')
		if printf '%s\n' "$have" | grep -qx "$h"; then
			rm -f "$out/profile.plist"
			printf '%s\n' "$h"
			return 0
		fi
		i=$((i + 1))
	done
	rm -f "$out/profile.plist"
	return 1
}

# sign <app>: signs the extensions, then the app, each with its profile and
# the entitlements project.yml gives its target (build/gen/<target>.entitlements,
# the target being the bundle's executable).
sign() {
	for target in "$1"/PlugIns/*.appex "$1"; do
		id=$(plutil -extract CFBundleIdentifier raw -o - "$target/Info.plist")
		p=$(profile "$team.$id") || p=$(profile "$team.*") || {
			echo "no development profile for $team.$id" >&2
			exit 1
		}
		who=$(identity "$p") || {
			echo "no signing identity for $p" >&2
			exit 1
		}
		exe=$(plutil -extract CFBundleExecutable raw -o - "$target/Info.plist")
		ent=$out/sign.entitlements
		if [ -f "$out/gen/$exe.entitlements" ]; then
			cp "$out/gen/$exe.entitlements" "$ent"
		else
			plutil -create xml1 "$ent"
		fi
		plutil -replace application-identifier -string "$team.$id" "$ent"
		plutil -replace 'com\.apple\.developer\.team-identifier' -string "$team" "$ent"
		plutil -replace get-task-allow -bool YES "$ent"
		cp "$p" "$target/embedded.mobileprovision"
		codesign --force --sign "$who" --entitlements "$ent" --timestamp=none "$target"
		echo "signed $id with $(basename "$p")"
	done
	rm -f "$out/sign.entitlements"
}

case $what in
full) scheme=Pinky ;;
lite) scheme=PinkyLite ;;
*)
	echo "usage: $0 core|full|lite [xcodebuild args]" >&2
	exit 2
	;;
esac

set -- DEVELOPMENT_TEAM="$team" "$@"
[ -n "${PINKY_BUNDLE_ID:-}" ] && set -- PINKY_BUNDLE_ID="$PINKY_BUNDLE_ID" "$@"
xcodebuild -project "$app/Pinky.xcodeproj" -scheme "$scheme" \
	-configuration Debug -destination generic/platform=iOS -derivedDataPath "$out/dd" \
	CODE_SIGNING_ALLOWED=NO "$@" build
bundle=$out/dd/Build/Products/Debug-iphoneos/$scheme.app
[ "${PINKY_SIGN:-1}" = 1 ] && sign "$bundle"
echo "built $bundle"

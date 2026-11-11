#!/bin/sh
# Builds the standalone prototype (see README.md).
#
#   ios/standalone/build.sh core                 the Go core only (libpinkycore.a)
#   ios/standalone/build.sh full  [xcodebuild args]   app + packet tunnel + Live Activity,
#                                                  automatic signing (explicit App IDs
#                                                  with the Network Extension capability)
#   ios/standalone/build.sh lite  [xcodebuild args]   app + Live Activity, no tunnel (use
#                                                  LocalDevVPN), signed here with the
#                                                  team's wildcard profile
#
# PINKY_PAIRING=<rppair pair's file> and PINKY_UDID=<udid> put the pairing
# into the app (build/Pairing.json, never committed). PINKY_TEAM overrides
# the team.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
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
	go build -buildmode=c-archive -trimpath -ldflags=-s -o "$out/core/libpinkycore.a" ./ios/standalone/core)
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

case $what in
full)
	xcodebuild -project "$app/PinkyStandalone.xcodeproj" -scheme PinkyStandalone \
		-configuration Debug -destination generic/platform=iOS -derivedDataPath "$out/dd" \
		DEVELOPMENT_TEAM="$team" "$@" build
	echo "built $out/dd/Build/Products/Debug-iphoneos/PinkyStandalone.app"
	;;
lite)
	xcodebuild -project "$app/PinkyStandalone.xcodeproj" -scheme PinkyStandaloneLite \
		-configuration Debug -destination generic/platform=iOS -derivedDataPath "$out/dd" \
		CODE_SIGNING_ALLOWED=NO "$@" build
	bundle=$out/dd/Build/Products/Debug-iphoneos/PinkyStandaloneLite.app
	if [ "${PINKY_SIGN:-1}" = 1 ]; then
		# The team's wildcard profile, which Xcode keeps.
		profile=
		for p in "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles"/*.mobileprovision; do
			security cms -D -i "$p" >"$out/profile.plist" 2>/dev/null || continue
			if [ "$(plutil -extract Entitlements.application-identifier raw -o - "$out/profile.plist")" = "$team.*" ]; then
				profile=$p
				break
			fi
		done
		[ -n "$profile" ] || { echo "no wildcard profile of team $team" >&2; exit 1; }
		# The signing identity: one of the profile's certificates we hold.
		have=$(security find-identity -v -p codesigning | awk '{ print $2 }')
		identity=
		i=0
		while cert=$(plutil -extract "DeveloperCertificates.$i" raw -o - "$out/profile.plist" 2>/dev/null); do
			h=$(printf '%s' "$cert" | base64 -d | shasum -a 1 | awk '{ print toupper($1) }')
			if printf '%s\n' "$have" | grep -qx "$h"; then
				identity=$h
				break
			fi
			i=$((i + 1))
		done
		[ -n "$identity" ] || { echo "no signing identity for the wildcard profile" >&2; exit 1; }
		for target in "$bundle"/PlugIns/*.appex "$bundle"; do
			id=$(plutil -extract CFBundleIdentifier raw -o - "$target/Info.plist")
			cp "$profile" "$target/embedded.mobileprovision"
			cat >"$out/sign.entitlements" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>application-identifier</key><string>$team.$id</string>
<key>com.apple.developer.team-identifier</key><string>$team</string>
<key>get-task-allow</key><true/>
</dict></plist>
EOF
			codesign --force --sign "$identity" --entitlements "$out/sign.entitlements" --timestamp=none "$target"
		done
		rm -f "$out/profile.plist" "$out/sign.entitlements"
	fi
	echo "built $bundle"
	;;
*)
	echo "usage: $0 core|full|lite [xcodebuild args]" >&2
	exit 2
	;;
esac

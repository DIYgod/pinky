#!/bin/sh
# Package a development-signed WDA runner to use the iOS 16 XCTest runtime.
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: $0 WebDriverAgentRunner-Runner.app output.ipa" >&2
    exit 2
fi
source_app=$1
output=$2
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
executable=$(/usr/libexec/PlistBuddy -c 'Print CFBundleExecutable' "$source_app/Info.plist")
if [ "$executable" != WebDriverAgentRunner-Runner ] ||
    [ ! -d "$source_app/PlugIns/WebDriverAgentRunner.xctest" ] ||
    [ ! -f "$source_app/embedded.mobileprovision" ]; then
    echo "Expected a development-signed WebDriverAgent runner for a real iPhone." >&2
    exit 1
fi
codesign --verify --deep --strict "$source_app"

stage=$(mktemp -d "${TMPDIR:-/tmp}/pinky-legacy-wda.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
mkdir "$stage/Payload"
app="$stage/Payload/WebDriverAgentRunner-Runner.app"
ditto "$source_app" "$app"

# The new executable keeps the app's original certificate and entitlements.
codesign -d --extract-certificates="$stage/cert" "$app"
identity=$(shasum -a 1 "$stage/cert0" | awk '{ print toupper($1) }')
codesign -d --entitlements :- "$app" >"$stage/entitlements.plist"
plutil -lint "$stage/entitlements.plist"

# Modern Xcode's testing frameworks and runner entry point can depend on
# libraries and symbols missing from iOS 16. Keep the WDA test and its own
# framework under PlugIns, and link the host against the device's XCTest.
for framework in "$app/Frameworks"/XC*.framework "$app/Frameworks/Testing.framework"; do
    rm -rf -- "$framework"
done
rm -f -- "$app/Frameworks/libXCTestSwiftSupport.dylib"
sdk=$(xcrun --sdk iphoneos --show-sdk-path)
xcrun --sdk iphoneos clang -isysroot "$sdk" -arch arm64 -miphoneos-version-min=15.0 -fobjc-arc \
    -framework UIKit -framework Foundation \
    -Wl,-rpath,/Developer/Library/Frameworks -Wl,-rpath,/Developer/Library/PrivateFrameworks \
    -Wl,-rpath,/Developer/usr/lib -Wl,-rpath,@executable_path/Frameworks \
    "$script_dir/RunnerHost.m" -o "$app/$executable"
codesign --force --sign "$identity" --entitlements "$stage/entitlements.plist" \
    --generate-entitlement-der --timestamp=none "$app"
codesign --verify --deep --strict "$app"

ditto -c -k --keepParent "$stage/Payload" "$stage/runner.ipa"
mv -f -- "$stage/runner.ipa" "$output"

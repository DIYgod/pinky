package assets

import _ "embed"

// IOSIcon is the iOS app icon at 1024 pixels, opaque and full bleed (iOS
// rounds the corners), which Pinky gives the WebDriverAgent helper it
// builds with Xcode. Only the macOS build carries it: the helper is built
// with Xcode.
//
//go:embed icon/pinky-ios-1024.png
var IOSIcon []byte

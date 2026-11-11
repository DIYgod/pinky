//go:build !darwin

package assets

// IOSIcon is empty off macOS, where no Xcode builds the WebDriverAgent
// helper (ios_darwin.go).
var IOSIcon []byte

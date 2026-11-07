VERSION := $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' cmd/pinky/mygo.json)
UNAME := $(shell uname -s)
GOARCH := $(shell go env GOARCH)

# FFmpeg's libraries for Pinky.app (make ffmpeg).
FFMPEG := build/ffmpeg
# The companion APK that make app puts in the app (make companion).
COMPANION_APK ?= android/companion/app/build/outputs/apk/debug/app-debug.apk

.PHONY: build test vet check app e2e companion ffmpeg

build:
	CGO_ENABLED=0 go build ./...

test:
	go test ./...

vet:
	go vet ./...

check: build vet test

# A minimal LGPL build of FFmpeg 9 (shared libraries, H.264 and HEVC with
# VideoToolbox) in build/ffmpeg; see scripts/ffmpeg.sh.
ffmpeg:
	scripts/ffmpeg.sh $(FFMPEG)

$(FFMPEG)/BUILD.txt: scripts/ffmpeg.sh
	scripts/ffmpeg.sh $(FFMPEG)

# Pinky.app and its .dmg in build/pinky/<os>-<arch>/. The companion APK
# (when built) goes in through MyGo's resources, so every platform's build
# and the .dmg carry it. On macOS the app also gets FFmpeg's libraries in
# Contents/Frameworks, building them the first time, and the .dmg is made
# again from the finished app.
ifeq ($(UNAME),Darwin)
app: $(FFMPEG)/BUILD.txt
endif
app:
	@rm -f cmd/pinky/resources/pinky-companion.apk
	@if [ -f "$(COMPANION_APK)" ]; then \
		mkdir -p cmd/pinky/resources && \
		cp "$(COMPANION_APK)" cmd/pinky/resources/pinky-companion.apk && \
		echo "adding the companion APK ($(COMPANION_APK))"; \
	else \
		echo "no companion APK at $(COMPANION_APK) (make companion); the app will not offer to install it"; \
	fi
	MYGO_LDFLAGS="-X main.version=$(VERSION)" go tool mygo build cmd/pinky
	@rm -f cmd/pinky/resources/pinky-companion.apk
	@rmdir cmd/pinky/resources 2>/dev/null || true
ifeq ($(UNAME),Darwin)
	scripts/macapp.sh build/pinky/darwin-$(GOARCH)/Pinky.app $(FFMPEG) build/pinky/darwin-$(GOARCH)/pinky-$(VERSION)-$(GOARCH).dmg
endif

# A pinky binary with MyGo's automation socket, for end-to-end checks.
e2e:
	go build -tags e2e -o build/pinky-e2e ./cmd/pinky

# The Android companion APK (needs the Android SDK and a JDK, as Android
# Studio installs them).
ANDROID_HOME ?= $(HOME)/Library/Android/sdk
JAVA_HOME ?= /Applications/Android Studio.app/Contents/jbr/Contents/Home
export ANDROID_HOME JAVA_HOME

companion:
	cd android/companion && ./gradlew --quiet assembleDebug

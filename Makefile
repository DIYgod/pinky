VERSION := $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' cmd/pinky/mygo.json)

.PHONY: build test vet check app e2e companion

build:
	CGO_ENABLED=0 go build ./...

test:
	go test ./...

vet:
	go vet ./...

check: build vet test

# Pinky.app and a .dmg in build/pinky/<os>-<arch>/. The Android companion
# APK goes into the bundle when it has been built (make companion).
app:
	MYGO_LDFLAGS="-X main.version=$(VERSION)" go tool mygo build cmd/pinky
	@apk=android/companion/app/build/outputs/apk/debug/app-debug.apk; \
	for app in build/pinky/*/Pinky.app; do \
		if [ -f "$$apk" ] && [ -d "$$app/Contents/Resources" ]; then \
			cp "$$apk" "$$app/Contents/Resources/pinky-companion.apk"; \
			codesign --force --sign - "$$app" >/dev/null 2>&1 || true; \
			echo "added the companion APK to $$app"; \
		fi; \
	done

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

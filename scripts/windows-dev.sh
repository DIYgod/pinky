#!/bin/sh
# Build a local Windows Pinky.exe with its video and developer-image dependencies.
# Run from the repository root with: sh scripts/windows-dev.sh
set -eu
cd "$(dirname "$0")/.."
go run ./scripts/windows-dev-assets ddi
go tool mygo build -platform windows/amd64 cmd/pinky
go run ./scripts/windows-dev-assets ffmpeg
printf 'Ready: build/pinky/windows-amd64/Pinky.exe\n'

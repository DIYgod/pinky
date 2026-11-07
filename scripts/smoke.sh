#!/usr/bin/env bash
# A smoke run of the gateway with fake phones, as CI does on Linux, Windows
# (Git Bash) and macOS:
#
#   scripts/smoke.sh [outdir]
#
# It builds pinky and scripts/mcpcall into outdir (build/smoke), starts
# `pinky daemon --fake` with its own PINKY_HOME there, lists the phones
# with `pinky devices`, and drives a short task through `pinky mcp` as an
# agent would. The daemon's log and the outputs stay in outdir.
set -euo pipefail

out=${1:-build/smoke}
exe=
case "${OS:-}" in Windows_NT) exe=.exe ;; esac
mkdir -p "$out"
pinky=$out/pinky$exe
mcpcall=$out/mcpcall$exe

go build -o "$pinky" ./cmd/pinky
go build -o "$mcpcall" ./scripts/mcpcall

# A relative PINKY_HOME, which pinky makes absolute: no path translation
# on Windows, and a socket path short enough for macOS's 104 bytes.
export PINKY_HOME=$out/h
rm -rf "$PINKY_HOME"

"$pinky" daemon --fake >"$out/daemon.log" 2>&1 &
daemon=$!
stop() {
	kill "$daemon" 2>/dev/null || true
	wait "$daemon" 2>/dev/null || true
}
trap stop EXIT

for _ in $(seq 1 60); do
	grep -q "gateway ready" "$out/daemon.log" && break
	kill -0 "$daemon" 2>/dev/null || break
	sleep 0.5
done
if ! grep -q "gateway ready" "$out/daemon.log"; then
	echo "smoke: the gateway did not start" >&2
	cat "$out/daemon.log" >&2
	exit 1
fi

echo "== pinky devices"
"$pinky" devices | tee "$out/devices.txt"
grep -q "fake-pixel-9" "$out/devices.txt"
grep -q "fake-iphone-17" "$out/devices.txt"

echo "== pinky mcp"
"$mcpcall" -pinky "$pinky" -delay 100ms \
	phone_connect '{"task":"Smoke run","device":"fake-pixel-9"}' \
	phone_launch '{"app":"Calculator"}' \
	phone_observe '{}' \
	phone_release '{"summary":"Smoke run done"}' | tee "$out/mcp.txt"
if grep -q "error=true" "$out/mcp.txt"; then
	echo "smoke: a tool call failed" >&2
	exit 1
fi
grep -q "Released Pixel 9 (fake)" "$out/mcp.txt"

stop
trap - EXIT
echo "smoke: passed"

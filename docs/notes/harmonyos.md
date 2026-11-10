# HarmonyOS USB and display mirroring

This branch was developed against Pinky `v2026.1109.10`. The HarmonyOS provider is opt-in. The native H.264 stream and the mouse controls belong to the desktop owner. Agent observations and navigation use a separate, checked path limited to supported system surfaces. Readable settings controls and scrolling were exercised on a Huawei MatePad Air with HarmonyOS 7 over USB.

## Run on Windows

Enable USB debugging on the tablet, connect it, and authorize the computer. Install Huawei's HDC and the HOScrcpy SDK from their distributors, and make Java 17 available. Set `PINKY_HARMONYOS_HDC` to the absolute path of `hdc.exe` before starting Pinky.

For native video and rotation, set `PINKY_HARMONYOS_H264` to the absolute path of a writable directory with `hosScrcpy-1.0.18-beta.jar`. Compile the bridge source from the Pinky checkout into that directory:

```powershell
$bridge = $env:PINKY_HARMONYOS_H264
javac -cp (Join-Path $bridge 'hosScrcpy-1.0.18-beta.jar') -d (Join-Path $bridge 'rotation') internal/driver/harmonyos/bridge/PinkyH264.java
```

`PINKY_HARMONYOS_JAVA` can point to a specific Java executable. The SDK and Java runtime are external dependencies. When native video is unavailable, the desktop can still use the screenshot preview.

## Scope

- MatePad Air over USB: screen preview, native H.264, mouse input, Back, Home, and requested rotation were exercised. The HDC connection intermittently needed a USB reconnect. Computer unlock is unavailable for HarmonyOS.
- iPhone 13 Pro: display padding, the physical cutouts, and color handling were checked on Windows. Remote iPhone rotation uses a separate signed WebDriverAgent with orientation endpoints. That rotation path was exercised by tests.
- The driver contract adds optional stream and mirror-key capabilities. An owner-only stream is excluded from agent screenshot and task video paths.

# Pinky icon set

A phone character with a watermelon-pink strap ring on its top-right corner, on a lemon-yellow field. The face on the screen stands for the agent working on the phone; the ring is a "pinky ring" and the loop you keep it on.

Copied from the brand work of 2026-11-07 (palette: lemon + watermelon). The desktop client embeds some of these files through `assets.go`.

## Source SVGs (`svg/`)

| File | Use |
| --- | --- |
| `pinky-icon-1024.svg` | Full-bleed master, opaque, no corner mask. Source for store art, iOS-style exports and Icon Composer layers. |
| `pinky-macos-1024.svg` | macOS app icon: 824 px rounded square (continuous corners) on a 1024 canvas, transparent margin, soft shadow. |
| `pinky-windows-256.svg` | Windows app icon, 256 canvas. |
| `pinky-linux-128.svg` | Linux app icon (hicolor `scalable`), with a darker bottom edge for depth. |
| `pinky-android-foreground.svg`, `pinky-android-background.svg` | Android adaptive icon layers (108 dp canvas; the character stays inside the 66 dp safe circle). |
| `pinky-32.svg`, `pinky-16.svg` | Redrawn on the pixel grid for favicons, tray icons and small sizes. The client's sidebar shows `pinky-32.svg`. |
| `pinky-menubar-template.svg` | macOS menu bar template image, 18 × 18 pt, black and alpha only (the system tints it). |

## Exports

| File | Contents |
| --- | --- |
| `icon/Pinky.icns` | macOS app icon, 16 to 1024 px (@1x and @2x), built with `iconutil`; `mygo build` uses it (`macos.icon`). |
| `icon/pinky-macos-1024.png` | The macOS icon at 1024 px, the PNG `mygo build` turns into the Windows and Linux icons. |
| `icon/pinky-ios-1024.png` | The iPhone helper's icon: `svg/pinky-icon-1024.svg` at 1024 px, full bleed and opaque (iOS masks the corners). Pinky embeds it on macOS (`assets.IOSIcon`) and compiles it with `actool` into the WebDriverAgent helper it builds, which then shows as Pinky Helper with this icon. |
| `icon/pinky.ico` | Windows icon: 16, 24, 32, 48, 64, 128, 256 px. The 16 and 32 px frames are the redrawn versions. |
| `icon/hicolor/` | `<size>/apps/pinky.png` for 16 to 512 px, plus `scalable/apps/pinky.svg`, for Linux icon themes. The 32 px one is the tray icon on Windows and Linux. |
| `tray/pinkyTemplate.png`, `pinkyTemplate@2x.png`, `pinkyTemplate.svg` | macOS status item image. The `Template` suffix makes AppKit treat it as a template image. |

All PNGs were rendered from the SVGs with headless Chrome on a transparent background, except `icon/pinky-ios-1024.png`, which has no transparency: iOS fills an icon's transparent pixels with black.

## Palette

| Role | Value |
| --- | --- |
| Field (dominant) | `#FFE680` to `#FFBE1A`, with a soft `#FFF7C9` lift behind the character |
| Ring, blush, mouth (contrast) | ring `#FF8A9B` to `#EE4D6C`; blush and mouth `#FF7F95` |
| Body (neutral) | `#FFFFFF` to `#EFE4D3` (warm white) |
| Screen (ink) | `#2B3144` to `#151923`; eyes `#F8F6F5` |
| Depth | shadow `#9E6B00`; Linux bottom edge `#D99600` |

How the client uses these colors, and how it keeps them apart from the amber of "Needs you" and the red of "Stop", is in `docs/notes/client.md`.

## README images (`readme/`)

| File | Contents |
| --- | --- |
| `readme/hero.png` | Pinky's window (1760 × 1100) during a real Claude Code run on an Android 16 emulator: the Send tap waits for approval. On the lemon field. |
| `readme/demo.gif` | The same run, about 12 s: the reply is typed, the Send waits, it is allowed and sent. |
| `readme/social-preview.png` | The repository's social preview (1280 × 640). GitHub reads it only from Settings › General › Social preview, so upload it there. |

They were made from captures of the e2e build taken through MyGo's automation socket, with the launch materials' scripts (kept outside the repository).

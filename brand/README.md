# Pinky icon set

A phone character with a watermelon-pink strap ring on its top-right corner, on a lemon-yellow field. The face on the screen stands for the agent working on the phone; the ring is a "pinky ring" and the loop you keep it on.

Status: palette chosen (lemon + watermelon). The name "Pinky" is tentative.

## Source SVGs

| File | Use |
| --- | --- |
| `pinky-icon-1024.svg` | Full-bleed master, opaque, no corner mask. Source for store art, iOS-style exports and Icon Composer layers. |
| `pinky-macos-1024.svg` | macOS app icon: 824 px rounded square (continuous corners) on a 1024 canvas, transparent margin, soft shadow. |
| `pinky-windows-256.svg` | Windows app icon, 256 canvas. |
| `pinky-linux-128.svg` | Linux app icon (hicolor `scalable`), with a darker bottom edge for depth. |
| `pinky-android-foreground.svg`, `pinky-android-background.svg` | Android adaptive icon layers (108 dp canvas; the character stays inside the 66 dp safe circle). Import with Android Studio's Image Asset tool. |
| `pinky-32.svg`, `pinky-16.svg` | Redrawn on the pixel grid for favicons, tray icons and small Windows and Linux sizes. |
| `pinky-menubar-template.svg` | macOS menu bar template image, 18 x 18 pt, black and alpha only (the system tints it). |
| `pinky-preview.png` | Everything above at real sizes on light and dark backgrounds. |

## Exports (`export/`)

| File | Contents |
| --- | --- |
| `Pinky.icns`, `Pinky.iconset/` | macOS app icon, 16 to 1024 px (@1x and @2x), built with `iconutil`. |
| `pinky.ico` | Windows icon: 16, 24, 32, 48, 64, 128, 256 px. The 16 and 32 px frames are the redrawn versions. |
| `favicon.ico`, `apple-touch-icon.png` | Web: 16 + 32 px favicon; 180 px opaque touch icon. |
| `linux/hicolor/` | `<size>/apps/pinky.png` for 16 to 512 px, plus `scalable/apps/pinky.svg`. Copy into `/usr/share/icons/hicolor` (or the app's own icon theme dir). |
| `menubar/pinkyTemplate.png`, `pinkyTemplate@2x.png`, `pinkyTemplate.svg` | macOS status item image. The `Template` suffix makes AppKit treat it as a template image. |

All PNGs were rendered from the SVGs with headless Chrome on a transparent background.

## Palette

| Role | Value |
| --- | --- |
| Field (dominant) | `#FFE680` to `#FFBE1A`, with a soft `#FFF7C9` lift behind the character |
| Ring, blush, mouth (contrast) | ring `#FF8A9B` to `#EE4D6C`; blush and mouth `#FF7F95` |
| Body (neutral) | `#FFFFFF` to `#EFE4D3` (warm white) |
| Screen (ink) | `#2B3144` to `#151923`; eyes `#F8F6F5` |
| Depth | shadow `#9E6B00`; Linux bottom edge `#D99600` |

## Construction notes

- Made with the design-playful-app-icons skill (soft-3D object-character lane): one subject (the phone), one signature detail (the ring), three colour roles plus ink.
- Light comes from the upper left. The body uses one soft-plastic gradient; the face stays flat.
- The character is tilted 6 degrees at large sizes. The 32 and 16 px versions drop the tilt and the shadow so edges stay crisp.
- The menu bar template is a filled body with the eyes cut out, plus the ring as a 1.3 pt stroke.
- No text, no trademark-like monogram, no platform logos.

## Explorations

`explorations/` keeps the palette rounds that led here (8 fields, 6 pinks on lemon, 5 oranges with a yellow ring), as comparison PNGs and per-option macOS SVGs.

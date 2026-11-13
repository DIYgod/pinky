# Bundled MingCute icons

The SVG files here are unmodified icons of the free [MingCute](https://github.com/Richard9394/MingCute) icon set by Richard, licensed under the Apache License 2.0 (see [LICENSE](LICENSE)). Pinky embeds them (`ui/icons.go`) and draws them in the color of the text around them, so they follow the light and dark themes.

| | |
| --- | --- |
| Package | npm [`mingcute_icon`](https://www.npmjs.com/package/mingcute_icon) 2.9.72 |
| Source | https://github.com/Richard9394/MingCute |

Only the icons the app shows are here. To add one:

1. Find it in the free set (mingcute.com, or `svg/<category>/<name>.svg` in the npm package), never in the paid Pro set, whose license does not allow publishing its files.
2. Copy it here under its own name, unchanged, and add a variable for it in `ui/icons.go`.

Icons are `_line` at rest; a selected row or tab shows the `_fill` of the same icon.

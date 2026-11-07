#!/bin/sh
# Finishes a Pinky.app that `mygo build` made (make app), then makes its
# disk image again so the .dmg carries the finished app:
#
#   scripts/macapp.sh <Pinky.app> <ffmpeg prefix> [<disk image>]
#
# - copies FFmpeg's libraries from <ffmpeg prefix>/lib (make ffmpeg) into
#   Contents/Frameworks, named by their major versions as internal/video
#   loads them; each one's install name is @rpath/<name> and its rpath is
#   @loader_path, so they find one another there whoever loads them;
# - puts FFmpeg's license and build notes in Contents/Resources;
# - signs the libraries and the app again, ad hoc;
# - rebuilds the disk image from the finished app, keeping the Finder
#   window (.DS_Store) and volume icon that mygo build gave it.
set -eu

app=${1:?usage: scripts/macapp.sh <Pinky.app> <ffmpeg prefix> [<disk image>]}
ffmpeg=${2:?usage: scripts/macapp.sh <Pinky.app> <ffmpeg prefix> [<disk image>]}
dmg=${3:-}
contents=$app/Contents
frameworks=$contents/Frameworks
libs="libavutil.61 libswscale.10 libavcodec.63 libavformat.63"

# hdiutil fails with "Resource busy" while Spotlight or an antivirus holds
# an image; try again, as mygo build does.
hdiutil_retry() {
	n=0
	until out=$(hdiutil "$@" 2>&1); do
		n=$((n + 1))
		case $out in
		*"Resource busy"*) [ $n -lt 5 ] && sleep $((2 << n)) && continue ;;
		esac
		echo "hdiutil $*: $out" >&2
		return 1
	done
}

rm -rf "$frameworks"
mkdir -p "$frameworks"
for lib in $libs; do
	src=$ffmpeg/lib/$lib.dylib
	if [ ! -f "$src" ]; then
		echo "macapp: no $src; run make ffmpeg" >&2
		exit 1
	fi
	dst=$frameworks/$lib.dylib
	cp -L "$src" "$dst"
	chmod 755 "$dst"
	install_name_tool -id "@rpath/$lib.dylib" "$dst" 2>/dev/null
	for dep in $(otool -L "$dst" | awk 'NR > 2 { print $1 }'); do
		case $dep in
		@rpath/*) ;;
		*/libav*.dylib | */libsw*.dylib) install_name_tool -change "$dep" "@rpath/$(basename "$dep")" "$dst" 2>/dev/null ;;
		esac
	done
	if ! otool -l "$dst" | grep -q "path @loader_path "; then
		install_name_tool -add_rpath @loader_path "$dst" 2>/dev/null
	fi
done

# Every library must come from the bundle or the system.
bad=$(otool -L "$frameworks"/*.dylib | awk '/^\t/ { print $1 }' | grep -v -e '^@rpath/' -e '^/usr/lib/' -e '^/System/Library/' || true)
if [ -n "$bad" ]; then
	echo "macapp: the FFmpeg libraries need libraries outside the system:" >&2
	echo "$bad" >&2
	exit 1
fi
for f in COPYING.LGPLv2.1 BUILD.txt; do
	cp "$ffmpeg/$f" "$contents/Resources/FFmpeg-$f"
done

codesign --force --sign - "$frameworks"/*.dylib
codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"
echo "added FFmpeg's libraries to $app"

[ -n "$dmg" ] || exit 0
if [ ! -f "$dmg" ]; then
	echo "macapp: no disk image $dmg" >&2
	exit 1
fi
work=$(mktemp -d "$(dirname "$dmg")/.dmg-XXXXXX")
cleanup() {
	hdiutil detach -force "$work/mnt" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT
mkdir "$work/src" "$work/mnt"

hdiutil_retry attach -readonly -noverify -noautoopen -nobrowse -mountpoint "$work/mnt" "$dmg"
volume=$(diskutil info "$work/mnt" | sed -n 's/^ *Volume Name: *//p')
for f in .DS_Store .VolumeIcon.icns; do
	if [ -f "$work/mnt/$f" ]; then
		cp "$work/mnt/$f" "$work/src/$f"
	fi
done
hdiutil_retry detach "$work/mnt"

ditto --noclone "$app" "$work/src/$(basename "$app")"
ln -s /Applications "$work/src/Applications"
hdiutil_retry create -ov -volname "${volume:-Pinky}" -srcfolder "$work/src" \
	-fs HFS+ -fsargs "-c c=64,a=16,e=16" -format UDRW "$work/rw.dmg"
hdiutil_retry attach -readwrite -noverify -noautoopen -nobrowse -mountpoint "$work/mnt" "$work/rw.dmg"
if [ -f "$work/src/.VolumeIcon.icns" ]; then
	# The Finder's "has a custom icon" flag on the volume (FinderInfo
	# offset 8).
	xattr -wx com.apple.FinderInfo "0000000000000000040000000000000000000000000000000000000000000000" "$work/mnt"
fi
rm -rf "$work/mnt/.fseventsd" "$work/mnt/.Trashes"
hdiutil_retry detach "$work/mnt"

rm -f "$dmg"
# macOS 27's hdiutil convert fails with "Resource temporarily unavailable"
# and points to diskutil, which older systems lack.
if diskutil image create from -h >/dev/null 2>&1; then
	if ! out=$(diskutil image create from --format ULMO "$work/rw.dmg" "$dmg" 2>&1); then
		echo "diskutil image create from: $out" >&2
		exit 1
	fi
else
	hdiutil_retry convert "$work/rw.dmg" -format ULMO -ov -o "$dmg"
fi
echo "made $dmg again from the finished app"

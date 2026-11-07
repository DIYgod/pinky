#!/bin/sh
# Builds the FFmpeg libraries Pinky ships (make ffmpeg): a minimal LGPL
# build of FFmpeg 9 with shared libraries, holding only what Pinky uses.
#
#   scripts/ffmpeg.sh <prefix>
#
# - libavcodec: the H.264 and HEVC decoders and parsers, with VideoToolbox
#   on macOS (internal/video decodes the phones' streams with them);
# - libavutil and libswscale (hardware frames, pixel conversion);
# - libavformat with the MP4/MOV muxer and demuxer, the raw H.264/HEVC
#   demuxers, the file protocol and the Annex B bitstream filters, for
#   recordings that are remuxed without re-encoding.
#
# Nothing GPL or nonfree is enabled, and every library FFmpeg would find on
# the machine by itself (zlib, iconv, SDL, X11...) is turned off, so the
# libraries depend on the system's frameworks alone. The source is checked
# against its SHA-256 and kept in <prefix>-src for the next build.
set -eu

VERSION=9.0.2
SHA256=8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e
URL=https://ffmpeg.org/releases/ffmpeg-$VERSION.tar.xz

prefix=${1:?usage: scripts/ffmpeg.sh <prefix>}
case $prefix in /*) ;; *) prefix=$(pwd)/$prefix ;; esac
src=$prefix-src
tarball=$src/ffmpeg-$VERSION.tar.xz

mkdir -p "$src"
if [ ! -f "$tarball" ] || [ "$(shasum -a 256 "$tarball" | cut -d' ' -f1)" != "$SHA256" ]; then
	echo "Downloading $URL"
	curl -fL --retry 3 -o "$tarball.part" "$URL"
	got=$(shasum -a 256 "$tarball.part" | cut -d' ' -f1)
	if [ "$got" != "$SHA256" ]; then
		echo "ffmpeg-$VERSION.tar.xz has SHA-256 $got, want $SHA256" >&2
		rm -f "$tarball.part"
		exit 1
	fi
	mv "$tarball.part" "$tarball"
fi

tree=$src/ffmpeg-$VERSION
rm -rf "$tree"
tar -xJf "$tarball" -C "$src"

flags="--prefix=$prefix
	--enable-shared --disable-static --enable-pic
	--disable-programs --disable-doc --disable-debug
	--disable-autodetect --disable-network
	--disable-everything
	--disable-avdevice --disable-avfilter --disable-swresample
	--enable-decoder=h264,hevc
	--enable-parser=h264,hevc
	--enable-demuxer=mov,h264,hevc
	--enable-muxer=mp4,mov,h264,hevc
	--enable-protocol=file
	--enable-bsf=h264_mp4toannexb,hevc_mp4toannexb,extract_extradata,h264_metadata,hevc_metadata"

case $(uname -s) in
Darwin)
	# Pinky.app runs on macOS 12 and later (minimumSystemVersion in
	# cmd/pinky/mygo.json). The libraries are found through @rpath, which
	# make app points at Contents/Frameworks; the header padding lets
	# install_name_tool rewrite their load commands.
	min=12.0
	flags="$flags
	--install-name-dir=@rpath
	--enable-videotoolbox
	--enable-hwaccel=h264_videotoolbox,hevc_videotoolbox
	--extra-cflags=-mmacosx-version-min=$min
	--extra-ldflags=-mmacosx-version-min=$min
	--extra-ldflags=-Wl,-headerpad_max_install_names"
	jobs=$(sysctl -n hw.ncpu)
	;;
*)
	jobs=$(nproc 2>/dev/null || echo 4)
	;;
esac

rm -rf "$prefix"
cd "$tree"
# shellcheck disable=SC2086 # the flags are split on purpose
./configure $flags
make -j"$jobs"
make install

cp COPYING.LGPLv2.1 "$prefix/COPYING.LGPLv2.1"
{
	echo "FFmpeg $VERSION, built for Pinky under the LGPL 2.1 (see COPYING.LGPLv2.1)."
	echo "Source: $URL"
	echo "SHA-256: $SHA256"
	echo "Configured with:"
	echo "$flags" | sed 's/^[[:space:]]*/  /'
} >"$prefix/BUILD.txt"
rm -rf "$tree"
echo "FFmpeg $VERSION is in $prefix"

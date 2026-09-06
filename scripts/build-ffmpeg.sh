#!/bin/sh

set -eu

: "${FFMPEG_VERSION:?FFMPEG_VERSION is required}"
: "${FFMPEG_DSC_SHA256:?FFMPEG_DSC_SHA256 is required}"
: "${DEBIAN_SNAPSHOT:?DEBIAN_SNAPSHOT is required}"
jobs=${FFMPEG_BUILD_JOBS:-4}
case "$jobs" in
  [1-9]|1[0-6]) ;;
  *) echo "ERROR: FFMPEG_BUILD_JOBS must be between 1 and 16" >&2; exit 1 ;;
esac

source_dir=/build/ffmpeg-source
licenses=/opt/ffmpeg/share/licenses/ffmpeg
descriptor="ffmpeg_${FFMPEG_VERSION#*:}.dsc"
mkdir -p "$source_dir" "$licenses"
cd "$source_dir"

# APT authenticates the snapshot index. Pin the complete source descriptor too;
# dpkg-source verifies its component checksums and applies any Debian patches.
apt-get source --download-only "ffmpeg=${FFMPEG_VERSION}"
printf '%s  %s\n' "$FFMPEG_DSC_SHA256" "$descriptor" | sha256sum --check --strict
dpkg-source --extract "$descriptor" source
sha256sum ffmpeg_* > "$licenses/SHA256SUMS"
install --mode=0644 "$descriptor" "$licenses/$descriptor"
cd source

# Keep native decoders, demuxers and filters. Only external libraries needed by
# the household profiles are enabled; teletext and optical-disc input are not.
set -- \
  --prefix=/opt/ffmpeg \
  --libdir=/opt/ffmpeg/lib \
  --disable-autodetect \
  --disable-debug \
  --disable-doc \
  --disable-ffplay \
  --disable-static \
  --disable-libcdio \
  --disable-libzvbi \
  --disable-xlib \
  --enable-shared \
  --enable-gpl \
  --enable-ffmpeg \
  --enable-ffprobe \
  --enable-pthreads \
  --enable-libdav1d \
  --enable-libdrm \
  --enable-libmp3lame \
  --enable-libx264 \
  --enable-libzimg \
  --enable-vaapi \
  --enable-bzlib \
  --enable-lzma \
  --enable-zlib
printf '%s\n' "$@" > "$licenses/configure-flags.txt"
./configure "$@"
make -j"$jobs"
make install

install --mode=0644 LICENSE.md COPYING.* "$licenses/"
cp -a debian "$licenses/debian"
install --mode=0644 debian/copyright "$licenses/copyright"
install --mode=0644 /usr/local/bin/build-ffmpeg "$licenses/build-ffmpeg.sh"
printf '%s\n' \
  'Source: ffmpeg' \
  "Version: ${FFMPEG_VERSION}" \
  "Snapshot: ${DEBIAN_SNAPSHOT}" \
  "Descriptor: ${descriptor}" \
  "SHA256: ${FFMPEG_DSC_SHA256}" \
  "Source-URL: https://snapshot.debian.org/archive/debian/${DEBIAN_SNAPSHOT}/pool/main/f/ffmpeg/${descriptor}" \
  'Extracted with dpkg-source, including Debian patches when present; no additional source modifications.' \
  'See SHA256SUMS, the signed descriptor, debian/, configure-flags.txt and build-ffmpeg.sh.' \
  > "$licenses/SOURCE"

# Headers and pkg-config files are not needed in the runtime, but all notices
# and the exact corresponding-source identities remain with the libraries.
rm -rf /opt/ffmpeg/include /opt/ffmpeg/lib/pkgconfig

#!/bin/sh

set -eu

if test "$#" -eq 0; then
  set -- filetwist:prototype
fi

MAX_COMPRESSED_MIB=${MAX_COMPRESSED_MIB:-450}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "ERROR: required command not found: $1" >&2
    exit 1
  }
}

mib() {
  awk -v bytes="$1" 'BEGIN { printf "%.1f", bytes / 1048576 }'
}

require_command docker
require_command gzip
docker info >/dev/null

for image in "$@"; do
  echo "=== $image ==="
  docker image inspect "$image" >/dev/null

  measurement_dir=$(mktemp -d "${TMPDIR:-/tmp}/filetwist-measure.XXXXXX")
  container_id=
  cleanup() {
    if test -n "$container_id"; then
      docker rm --force "$container_id" >/dev/null 2>&1 || true
    fi
    rm -rf "$measurement_dir"
  }
  trap cleanup EXIT HUP INT TERM

  docker image save --output "$measurement_dir/image.tar" "$image"
  gzip -n -1 -c "$measurement_dir/image.tar" >"$measurement_dir/image.tar.gz"
  compression_level=1
  compressed_bytes=$(wc -c <"$measurement_dir/image.tar.gz" | tr -d ' ')
  max_compressed_bytes=$((MAX_COMPRESSED_MIB * 1024 * 1024))
  # A fast-compressed archive below the ceiling needs no expensive recompression.
  if test "$compressed_bytes" -gt "$max_compressed_bytes"; then
    gzip -n -9 -c "$measurement_dir/image.tar" >"$measurement_dir/image.tar.gz"
    compression_level=9
    compressed_bytes=$(wc -c <"$measurement_dir/image.tar.gz" | tr -d ' ')
  fi
  container_id=$(docker create --platform linux/amd64 --entrypoint /bin/true "$image")
  docker export --output "$measurement_dir/rootfs.tar" "$container_id"
  unpacked_bytes=$(wc -c <"$measurement_dir/rootfs.tar" | tr -d ' ')
  docker rm "$container_id" >/dev/null
  container_id=
  rm -rf "$measurement_dir"
  trap - EXIT HUP INT TERM

  echo "compressed OCI archive: ${compressed_bytes} bytes ($(mib "$compressed_bytes") MiB)"
  echo "measurement compression: gzip -${compression_level}"
  echo "unpacked root filesystem: ${unpacked_bytes} bytes ($(mib "$unpacked_bytes") MiB)"
  if test "$compressed_bytes" -gt "$max_compressed_bytes"; then
    echo "ERROR: compressed image exceeds ${MAX_COMPRESSED_MIB} MiB ceiling" >&2
    exit 1
  fi
  echo "PASS: compressed image is within the ${MAX_COMPRESSED_MIB} MiB ceiling"
  echo
  echo "largest installed Debian packages (KiB):"
  docker run --rm --platform linux/amd64 --entrypoint /usr/bin/dpkg-query "$image" \
    -W '-f=${Installed-Size}\t${Package}\n' | sort -nr | sed -n '1,25p'
  echo
  echo "layer contributions:"
  docker history --no-trunc --format '{{.Size}}\t{{.CreatedBy}}' "$image"
  echo
done

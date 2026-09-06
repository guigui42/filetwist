#!/bin/sh
# Run only in a disposable candidate container. APT verifies the snapshot indexes.
set -eu

if test "$#" -ne 1 || ! test -s "$1"; then
  echo "ERROR: Supply the host-validated exact source request list." >&2
  exit 1
fi
sed -i 's/^Types: deb$/Types: deb deb-src/' /etc/apt/sources.list.d/debian.sources
apt-get -o APT::Sandbox::User=root -o Acquire::Retries=3 update >&2
while IFS="$(printf '\t')" read -r package version; do
  printf 'FILETWIST-SOURCE\t%s\t%s\n' "$package" "$version"
  apt-get --print-uris --download-only --only-source \
    -o APT::Sandbox::User=root -o Acquire::ForceHash=sha256 source "$package=$version"
done < "$1"

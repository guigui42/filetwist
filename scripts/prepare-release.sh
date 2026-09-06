#!/bin/sh

set -eu

goroot=$(go env GOROOT)
license=${GO_LICENSE_FILE:-"$goroot/LICENSE"}
# Homebrew keeps the package licence above its libexec Go root.
if test -z "${GO_LICENSE_FILE:-}" && ! test -r "$license" && test "${goroot##*/}" = libexec; then
  license="${goroot%/libexec}/LICENSE"
fi
if ! test -s "$license"; then
  echo "ERROR: Go licence not found at $license; set GO_LICENSE_FILE to the toolchain licence." >&2
  exit 1
fi
mkdir -p .release
cp "$license" .release/go.LICENSE
git rev-parse HEAD > .release/filetwist-source-commit.txt

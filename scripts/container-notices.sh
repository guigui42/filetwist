#!/bin/sh
set -eu

cd /
{
  printf '%s\n' usr/local/share/licenses/filetwist opt/vips/share/licenses/libvips \
    usr/share/common-licenses var/lib/dpkg/status
  if test -d opt/ffmpeg/share/licenses/ffmpeg; then
    printf '%s\n' opt/ffmpeg/share/licenses/ffmpeg
  fi
  awk -F '\t' 'NR > 1 { split($1, name, ":"); print "usr/share/doc/" name[1] "/copyright" }' \
    usr/local/share/licenses/filetwist/debian-packages.tsv
} | tar --dereference --hard-dereference -cf - --files-from -

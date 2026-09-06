#!/bin/sh

set -eu

command=${1:-serve}

case "$command" in
  serve|healthcheck|version|help|-h|--help)
    exec /usr/local/bin/filetwist-server "$@"
    ;;
  convert)
    shift
    exec /usr/local/bin/filetwist "$@"
    ;;
  probe)
    shift
    exec /usr/local/bin/probe-media "$@"
    ;;
  *)
    echo "ERROR: unknown container command: $command" >&2
    echo "Usage: filetwist-container {serve|healthcheck|version|convert|probe}" >&2
    exit 2
    ;;
esac

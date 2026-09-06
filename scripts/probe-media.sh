#!/bin/sh

set -eu

DEFAULT_DEVICE=/dev/dri/renderD128

usage() {
  cat <<'EOF'
Usage:
  probe-media capabilities
  probe-media image-codecs INPUT_IMAGE [OUTPUT_DIR]
  probe-media heic INPUT_HEIC [OUTPUT_JPEG]
  probe-media cpu [OUTPUT_MP4]
  probe-media vaapi [OUTPUT_MP4] [DEVICE]
  probe-media inspect INPUT_MP4
  probe-media all [OUTPUT_DIR] [INPUT_HEIC] [DEVICE]

"all" always runs the CPU encode. It attempts VA-API only when the render
device is accessible, and reports an explicit CPU fallback if that encode fails.
EOF
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "ERROR: required command not found: $1" >&2
    exit 1
  }
}

require_pattern() {
  description=$1
  pattern=$2
  shift 2

  if ! "$@" 2>&1 | grep -Eq "$pattern"; then
    echo "ERROR: missing capability: $description" >&2
    exit 1
  fi
}

capabilities() {
  require_command ffmpeg
  require_command ffprobe
  require_command vips
  require_command vipsheader

  echo "libvips: $(vips --version)"
  echo "ffmpeg: $(ffmpeg -version | sed -n '1p')"
  require_pattern "HEIF/HEIC and AVIF loader" 'heifload' vips -l foreign
  require_pattern "HEIF/HEIC and AVIF saver" 'heifsave' vips -l foreign
  require_pattern "JPEG loader" 'jpegload' vips -l foreign
  require_pattern "PNG loader" 'pngload' vips -l foreign
  require_pattern "WebP loader" 'webpload' vips -l foreign
  require_pattern "TIFF loader" 'tiffload' vips -l foreign
  require_pattern "libx264 encoder" '[[:space:]]libx264[[:space:]]' ffmpeg -hide_banner -encoders
  require_pattern "AAC encoder" '[[:space:]]aac[[:space:]]' ffmpeg -hide_banner -encoders
  require_pattern "h264_vaapi encoder" '[[:space:]]h264_vaapi[[:space:]]' ffmpeg -hide_banner -encoders
  require_pattern "zscale filter" '[[:space:]]zscale[[:space:]]' ffmpeg -hide_banner -filters
  require_pattern "tonemap filter" '[[:space:]]tonemap[[:space:]]' ffmpeg -hide_banner -filters
  echo "PASS: required image loaders, video encoders, and HDR filters are registered"
}

probe_image_codecs() {
  input=$1
  output_dir=${2:-/tmp/filetwist-image-codecs}

  test -r "$input" || {
    echo "ERROR: image fixture is not readable: $input" >&2
    return 1
  }
  mkdir -p "$output_dir"
  rm -f "$output_dir/probe.heic" "$output_dir/probe.avif"
  rm -f "$output_dir/heic.jpg" "$output_dir/avif.jpg"

  vips copy "$input" "$output_dir/probe.heic[compression=hevc,Q=80]"
  vips copy "$input" "$output_dir/probe.avif[compression=av1,Q=80]"
  vips copy "$output_dir/probe.heic" "$output_dir/heic.jpg[Q=90,strip]"
  vips copy "$output_dir/probe.avif" "$output_dir/avif.jpg[Q=90,strip]"

  for output in \
    "$output_dir/probe.heic" \
    "$output_dir/probe.avif" \
    "$output_dir/heic.jpg" \
    "$output_dir/avif.jpg"; do
    test -s "$output" || {
      echo "ERROR: image codec probe produced no output: $output" >&2
      return 1
    }
    vipsheader "$output"
  done
  echo "PASS: HEIC and AVIF encode/decode round trips completed"
}

inspect_mp4() {
  input=$1
  test -s "$input" || {
    echo "ERROR: MP4 is missing or empty: $input" >&2
    return 1
  }

  video_codec=$(ffprobe -v error -select_streams v:0 \
    -show_entries stream=codec_name -of csv=p=0 "$input")
  audio_codec=$(ffprobe -v error -select_streams a:0 \
    -show_entries stream=codec_name -of csv=p=0 "$input")
  format_name=$(ffprobe -v error -show_entries format=format_name \
    -of csv=p=0 "$input")
  duration=$(ffprobe -v error -show_entries format=duration \
    -of csv=p=0 "$input")

  test "$video_codec" = h264 || {
    echo "ERROR: expected H.264 video, got: $video_codec" >&2
    return 1
  }
  test "$audio_codec" = aac || {
    echo "ERROR: expected AAC audio, got: $audio_codec" >&2
    return 1
  }
  case "$format_name" in
    *mp4*) ;;
    *)
      echo "ERROR: expected an MP4-family container, got: $format_name" >&2
      return 1
      ;;
  esac
  awk -v duration="$duration" 'BEGIN { exit !(duration > 0) }' || {
    echo "ERROR: expected positive duration, got: $duration" >&2
    return 1
  }

  ffprobe -v error -show_entries \
    format=format_name,duration,size:stream=index,codec_type,codec_name,pix_fmt,width,height,sample_rate,channels \
    -of default=noprint_wrappers=1 "$input"
  echo "PASS: MP4 re-probe found H.264 video, AAC audio, and positive duration"
}

probe_heic() {
  input=$1
  output=${2:-/tmp/filetwist-heic-probe.jpg}

  test -r "$input" || {
    echo "ERROR: HEIC fixture is not readable: $input" >&2
    return 1
  }
  mkdir -p "$(dirname "$output")"
  rm -f "$output"
  vips copy "$input" "$output[Q=90,strip]"
  test -s "$output" || {
    echo "ERROR: libvips produced no JPEG output" >&2
    return 1
  }
  vipsheader "$output"
  echo "PASS: libvips decoded HEIC and wrote a non-empty JPEG"
}

probe_cpu() {
  output=${1:-/tmp/filetwist-cpu-probe.mp4}

  mkdir -p "$(dirname "$output")"
  rm -f "$output"
  ffmpeg -hide_banner -loglevel error -y \
    -f lavfi -i "testsrc2=size=1280x720:rate=30" \
    -f lavfi -i "sine=frequency=1000:sample_rate=48000" \
    -t 3 -shortest \
    -map 0:v:0 -map 1:a:0 \
    -c:v libx264 -preset veryfast -pix_fmt yuv420p \
    -c:a aac -b:a 128k \
    -movflags +faststart \
    "$output"
  inspect_mp4 "$output"
}

probe_vaapi() {
  output=${1:-/tmp/filetwist-vaapi-probe.mp4}
  device=${2:-$DEFAULT_DEVICE}

  test -c "$device" || {
    echo "ERROR: VA-API device is not a character device: $device" >&2
    return 1
  }
  test -r "$device" && test -w "$device" || {
    echo "ERROR: VA-API device is not readable and writable: $device" >&2
    return 1
  }

  mkdir -p "$(dirname "$output")"
  rm -f "$output"
  ffmpeg -hide_banner -loglevel error -y \
    -init_hw_device "vaapi=filetwist:${device}" \
    -filter_hw_device filetwist \
    -f lavfi -i "testsrc2=size=1280x720:rate=30" \
    -f lavfi -i "sine=frequency=1000:sample_rate=48000" \
    -t 3 -shortest \
    -map 0:v:0 -map 1:a:0 \
    -vf "format=nv12,hwupload" \
    -c:v h264_vaapi -qp 24 \
    -c:a aac -b:a 128k \
    -movflags +faststart \
    "$output"
  inspect_mp4 "$output"
}

run_all() {
  output_dir=${1:-/tmp/filetwist-probe}
  heic_fixture=${2:-}
  device=${3:-$DEFAULT_DEVICE}

  mkdir -p "$output_dir"
  capabilities
  probe_cpu "$output_dir/cpu.mp4"

  if test -n "$heic_fixture"; then
    probe_heic "$heic_fixture" "$output_dir/heic.jpg"
  else
    echo "SKIP: HEIC decode requires a fixture path"
  fi

  if test -c "$device" && test -r "$device" && test -w "$device"; then
    if probe_vaapi "$output_dir/vaapi.mp4" "$device"; then
      echo "PASS: functional VA-API encode completed"
    else
      echo "FALLBACK: VA-API encode failed; CPU output remains valid" >&2
    fi
  else
    echo "FALLBACK: $device is unavailable; CPU output remains valid"
  fi
}

command=${1:-capabilities}
case "$command" in
  capabilities)
    capabilities
    ;;
  image-codecs)
    test "$#" -ge 2 || {
      usage >&2
      exit 2
    }
    probe_image_codecs "$2" "${3:-/tmp/filetwist-image-codecs}"
    ;;
  heic)
    test "$#" -ge 2 || {
      usage >&2
      exit 2
    }
    probe_heic "$2" "${3:-/tmp/filetwist-heic-probe.jpg}"
    ;;
  cpu)
    probe_cpu "${2:-/tmp/filetwist-cpu-probe.mp4}"
    ;;
  vaapi)
    probe_vaapi "${2:-/tmp/filetwist-vaapi-probe.mp4}" "${3:-$DEFAULT_DEVICE}"
    ;;
  inspect)
    test "$#" -eq 2 || {
      usage >&2
      exit 2
    }
    inspect_mp4 "$2"
    ;;
  all)
    run_all "${2:-/tmp/filetwist-probe}" "${3:-}" "${4:-$DEFAULT_DEVICE}"
    ;;
  help|-h|--help)
    usage
    ;;
  *)
    echo "ERROR: unknown command: $command" >&2
    usage >&2
    exit 2
    ;;
esac

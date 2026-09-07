#!/bin/sh

set -eu

IMAGE=${1:-filetwist:prototype}
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/filetwist-container.XXXXXX")
OUTPUT="$WORK/output"
SERVER_ID=

cleanup() {
  if test -n "$SERVER_ID"; then
    docker rm --force "$SERVER_ID" >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT HUP INT TERM

# Keep bind-mounted directories host-owned so cleanup works across runtime UIDs.
mkdir -p "$OUTPUT/codecs"
chmod 0777 "$OUTPUT" "$OUTPUT/codecs"

run_runtime() {
  docker run --rm --platform linux/amd64 \
    --read-only \
    --tmpfs /tmp:size=512m,mode=1777 \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --volume "$ROOT/fixtures/generated:/fixtures:ro" \
    --volume "$OUTPUT:/data" \
    "$@"
}

run_container() {
  run_runtime "$IMAGE" "$@"
}

docker image inspect "$IMAGE" >/dev/null
run_container probe capabilities
run_container probe image-codecs /fixtures/rgba-2x2.png /data/codecs

run_container convert \
  --output /data/photo.jpg \
  --operation compatible_photo \
  /fixtures/opaque-3x2.jpg >"$WORK/photo.json"
run_container convert \
  --output /data/audio.mp3 \
  --operation compatible_audio \
  /fixtures/tone-8khz-mono.wav >"$WORK/audio.json"
run_container convert \
  --output /data/video.mp4 \
  --operation compatible_video \
  /fixtures/h264-aac-32x24.mp4 >"$WORK/video.json"

run_runtime --entrypoint vips "$IMAGE" \
  icc_transform /fixtures/opaque-3x2.jpg /data/cmyk16.v cmyk \
  --input-profile srgb --depth 16
run_runtime --entrypoint vips "$IMAGE" \
  tiffsave /data/cmyk16.v /data/cmyk16.tif --keep icc
run_container convert \
  --output /data/precision.png \
  --operation lossless_image \
  /data/cmyk16.tif >"$WORK/precision.json"
input_precision=$(run_runtime --entrypoint vipsheader "$IMAGE" -f format /data/cmyk16.tif)
output_precision=$(run_runtime --entrypoint vipsheader "$IMAGE" -f format /data/precision.png)
test "$input_precision" = '((VipsBandFormat) VIPS_FORMAT_USHORT)' &&
  test "$output_precision" = "$input_precision" || {
  echo "ERROR: lossless image did not retain 16-bit ICC sample precision" >&2
  exit 1
}

python3 - "$WORK/photo.json" "$WORK/audio.json" "$WORK/video.json" "$WORK/precision.json" <<'PY'
import json
import sys

for path in sys.argv[1:]:
    with open(path, encoding="utf-8") as result_file:
        result = json.load(result_file)
    if result.get("status") != "success":
        raise SystemExit(f"conversion did not succeed: {path}")
    if result.get("validation", {}).get("status") != "passed":
        raise SystemExit(f"conversion did not validate: {path}")

with open(sys.argv[3], encoding="utf-8") as result_file:
    video = json.load(result_file)
execution = video.get("execution", {})
if execution.get("requested") != "auto" or execution.get("final") != "cpu":
    raise SystemExit(f"CPU fallback was not explicit: {execution}")
if execution.get("fallback_reason") != "vaapi_device":
    raise SystemExit(f"unexpected CPU fallback reason: {execution}")
PY

docker run --rm --platform linux/amd64 \
  --read-only --tmpfs /tmp:size=64m,mode=1777 \
  --volume "$OUTPUT:/data:ro" \
  --entrypoint vipsheader "$IMAGE" /data/photo.jpg >/dev/null
docker run --rm --platform linux/amd64 \
  --read-only --tmpfs /tmp:size=64m,mode=1777 \
  --volume "$OUTPUT:/data:ro" \
  --entrypoint ffprobe "$IMAGE" \
  -v error -select_streams a:0 \
  -show_entries stream=codec_name,channels,sample_rate \
  -of default=noprint_wrappers=1 /data/audio.mp3 |
  grep -Eq 'codec_name=mp3'
run_container probe inspect /data/video.mp4 >/dev/null

started_ms=$(python3 -c 'import time; print(round(time.time() * 1000))')
SERVER_ID=$(docker run --detach --rm --init --platform linux/amd64 \
  --read-only \
  --tmpfs /tmp:size=128m,mode=1777 \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --publish 127.0.0.1::8080 \
  "$IMAGE")

attempt=0
while test "$attempt" -lt 30; do
  health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$SERVER_ID")
  if test "$health" = healthy; then
    break
  fi
  if test "$health" = unhealthy; then
    docker inspect --format '{{json .State.Health.Log}}' "$SERVER_ID" >&2
    exit 1
  fi
  attempt=$((attempt + 1))
  sleep 1
done
test "$health" = healthy || {
  echo "ERROR: container did not become healthy" >&2
  exit 1
}
ready_ms=$(python3 -c 'import time; print(round(time.time() * 1000))')

server_port=$(docker port "$SERVER_ID" 8080/tcp | sed -n '1s/.*://p')
test -n "$server_port" || {
  echo "ERROR: container web port was not published" >&2
  exit 1
}
server_url="http://127.0.0.1:$server_port"
curl --fail --silent --show-error "$server_url/" >"$WORK/index.html"
grep -q '/static/htmx.min.js' "$WORK/index.html"

curl --fail --silent --show-error \
  --header 'HX-Request: true' \
  --form "files=@$ROOT/fixtures/generated/opaque-3x2.jpg;filename=opaque-3x2.jpg" \
  "$server_url/jobs" >"$WORK/job.html"
job_id=$(python3 - "$WORK/job.html" <<'PY'
import re
import sys

with open(sys.argv[1], encoding="utf-8") as job_file:
    markup = job_file.read()
match = re.search(r"/jobs/([A-Za-z0-9_-]{22})/(?:start|status)", markup)
if match is None:
    raise SystemExit("job id was not present in the upload fragment")
print(match.group(1))
PY
)

curl --fail --silent --show-error \
  --header 'HX-Request: true' \
  --data 'operation.f001=compatible_photo' \
  "$server_url/jobs/$job_id/start" >"$WORK/started.html"

attempt=0
while test "$attempt" -lt 60; do
  curl --fail --silent --show-error \
    "$server_url/jobs/$job_id/status" >"$WORK/status.html"
  if grep -q 'data-job-state="completed"' "$WORK/status.html"; then
    break
  fi
  if grep -Eq 'data-job-state="(failed|canceled|interrupted)"' "$WORK/status.html"; then
    cat "$WORK/status.html" >&2
    exit 1
  fi
  attempt=$((attempt + 1))
  sleep 1
done
grep -q 'data-job-state="completed"' "$WORK/status.html" || {
  cat "$WORK/status.html" >&2
  echo "ERROR: web conversion did not complete" >&2
  exit 1
}

curl --fail --silent --show-error \
  --dump-header "$WORK/download.headers" \
  --output "$WORK/download.jpg" \
  "$server_url/jobs/$job_id/files/f001"
grep -Eiq '^content-disposition: attachment;' "$WORK/download.headers"
grep -Eiq '^x-content-type-options: nosniff' "$WORK/download.headers"
test -s "$WORK/download.jpg"

curl --fail --silent --show-error \
  --output "$WORK/download.zip" \
  "$server_url/jobs/$job_id/download"
python3 - "$WORK/download.zip" <<'PY'
import sys
import zipfile

with zipfile.ZipFile(sys.argv[1]) as archive:
    entries = archive.infolist()
    if len(entries) != 1:
        raise SystemExit(f"expected one archive entry, found {len(entries)}")
    if entries[0].compress_type != zipfile.ZIP_STORED:
        raise SystemExit("web archive entry was recompressed")
PY

runtime_uid=$(docker exec "$SERVER_ID" id -u)
docker exec "$SERVER_ID" test -r /usr/local/share/licenses/filetwist/LICENSE
docker exec "$SERVER_ID" test -r /usr/local/share/licenses/filetwist/htmx.LICENSE
docker exec "$SERVER_ID" test -s /usr/local/share/licenses/filetwist/go.LICENSE
docker exec "$SERVER_ID" test -s /usr/local/share/licenses/filetwist/THIRD_PARTY_NOTICES.md
docker exec "$SERVER_ID" test -s /usr/local/share/licenses/filetwist/Dockerfile
docker exec "$SERVER_ID" test -s /usr/local/share/licenses/filetwist/debian-packages.tsv
docker exec "$SERVER_ID" test -s /usr/local/share/licenses/filetwist/debian-snapshot.txt
docker exec "$SERVER_ID" test -s /usr/local/share/licenses/filetwist/ffmpeg-buildconf.txt
docker exec "$SERVER_ID" test -s /opt/vips/share/licenses/libvips/LICENSE
docker exec "$SERVER_ID" test -s /opt/vips/share/licenses/libvips/SOURCE
docker exec "$SERVER_ID" test -s /usr/share/doc/ffmpeg/copyright
docker exec "$SERVER_ID" test -s /usr/share/doc/intel-media-va-driver-non-free/copyright
pid_one=$(docker exec "$SERVER_ID" cat /proc/1/comm)
test "$runtime_uid" = 10001 || {
  echo "ERROR: runtime UID is $runtime_uid, expected 10001" >&2
  exit 1
}
test "$pid_one" = docker-init || {
  echo "ERROR: --init did not install docker-init as PID 1" >&2
  exit 1
}

docker rm --force "$SERVER_ID" >/dev/null
SERVER_ID=

# The healthcheck derives its URL from LISTEN_ADDRESS, so a non-default bind
# port has to reach healthy without any healthcheck argument.
ALT_PORT=9443
SERVER_ID=$(docker run --detach --rm --init --platform linux/amd64 \
  --read-only \
  --tmpfs /tmp:size=128m,mode=1777 \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --env "LISTEN_ADDRESS=:$ALT_PORT" \
  --publish "127.0.0.1::$ALT_PORT" \
  "$IMAGE")

attempt=0
alt_health=
while test "$attempt" -lt 30; do
  alt_health=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$SERVER_ID")
  if test "$alt_health" = healthy; then
    break
  fi
  if test "$alt_health" = unhealthy; then
    docker inspect --format '{{json .State.Health.Log}}' "$SERVER_ID" >&2
    echo "ERROR: the healthcheck did not follow LISTEN_ADDRESS=:$ALT_PORT" >&2
    exit 1
  fi
  attempt=$((attempt + 1))
  sleep 1
done
test "$alt_health" = healthy || {
  echo "ERROR: container on port $ALT_PORT did not become healthy" >&2
  exit 1
}

alt_port=$(docker port "$SERVER_ID" "$ALT_PORT/tcp" | sed -n '1s/.*://p')
test -n "$alt_port" || {
  echo "ERROR: container port $ALT_PORT was not published" >&2
  exit 1
}
curl --fail --silent --show-error "http://127.0.0.1:$alt_port/healthz" |
  grep -q '"status":"ok"'
docker exec "$SERVER_ID" filetwist-container healthcheck | grep -q healthy
docker rm --force "$SERVER_ID" >/dev/null
SERVER_ID=

echo "PASS: image, audio, and video conversions validated"
echo "PASS: lossless PNG retained 16-bit ICC sample precision"
echo "PASS: automatic CPU fallback reported vaapi_device"
echo "PASS: web upload, conversion, attachment, and streamed ZIP validated"
echo "PASS: runtime UID $runtime_uid, health $health, init $pid_one"
echo "PASS: healthcheck followed LISTEN_ADDRESS=:$ALT_PORT"
echo "startup_to_healthy_ms=$((ready_ms - started_ms))"

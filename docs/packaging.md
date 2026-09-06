# Deployment and configuration

Use [deploy/Dockerfile](../deploy/Dockerfile) with
[deploy/compose.yaml](../deploy/compose.yaml). The
[source-build quick start](../README.md#quick-start-from-source) builds the image
locally over a digest-pinned media runtime and binds the web interface to loopback.
The first build needs registry access to fetch that immutable seed.
The [release workflow](releasing.md) can publish versioned images to GHCR
once the maintainer configures the publishing prerequisites.

The supported image target is Linux/amd64. Docker on macOS can run that image
in CPU mode, with emulation on Apple Silicon. Native macOS acceleration and
arm64 images are not supported.

Normal builds compile only Go. Native FFmpeg/libvips and Debian installation live
in [deploy/Dockerfile.runtime](../deploy/Dockerfile.runtime), built deliberately
through the separate [media-runtime release lane](releasing.md#updating-the-media-runtime).
Application releases always use the fixed reviewed seed, not the previous
application image, so they do not accumulate a chain of application layers.

## Using a published image

After an image is published and made accessible, copy its reference from the
repository's GitHub Packages page. Replace `owner/repository` and the example
version below with that image's actual lowercase repository path and tag:

```sh
FILETWIST_IMAGE=ghcr.io/owner/repository:v0.1.0 \
docker compose -f deploy/compose.yaml -f deploy/compose.cpu.yaml \
  up --no-build --pull always --detach --wait
```

Use the Compose files from the same release tag. A digest reference such as
`ghcr.io/owner/repository@sha256:<digest>` pins exact contents. No `latest` tag
is published. Images can be private until the maintainer changes package
visibility; private pulls need registry authentication.

## Configure Compose

Optionally copy [configs/compose.env.example](../configs/compose.env.example)
to `.env` at the repository root, then edit it:

```sh
cp configs/compose.env.example .env
```

Use `FILETWIST_IMAGE=filetwist:local` for a local image and
`FILETWIST_PORT=127.0.0.1:8080` for a loopback-only published port. Then:

```sh
docker compose --env-file .env \
  -f deploy/compose.yaml -f deploy/compose.cpu.yaml \
  up --build --pull never --detach --wait
```

`FILETWIST_PORT` defaults to `127.0.0.1:8080`, so the service is reachable only
from the Docker host. For deliberate trusted-LAN access, set a specific host
address such as `192.168.1.10:8080`; `0.0.0.0:8080` exposes all IPv4 interfaces.
A port-only value such as `8080` also publishes on all host interfaces.
Filetwist has no built-in authentication. `FILETWIST_CONTAINER_PORT` defaults to
`8080` and controls the server bind port and container healthcheck together.
`FILETWIST_VERSION` supplies the build version. `FILETWIST_REVISION` optionally
records the full source commit in the image revision label; it defaults to
`unknown` rather than inventing provenance for an unversioned source archive.

**Compose interpolation is not runtime configuration.** Settings such as
`FILETWIST_JOB_TTL` are translated by Compose into `JOB_TTL` inside the
container. A host-side `JOB_TTL=1h` does not override the supplied Compose
mapping, and passing `FILETWIST_JOB_TTL` directly to the Go server does nothing.
The runtime defaults and Compose mappings are listed below.

## Intel VA-API

On Linux, use a suitable Intel GPU, a working host driver, and an accessible
render node. The override maps only the device and its numeric group:

```sh
FILETWIST_IMAGE=filetwist:local FILETWIST_PORT=127.0.0.1:8080 \
FILETWIST_RENDER_GID="$(stat -c '%g' /dev/dri/renderD128)" \
docker compose \
  -f deploy/compose.yaml \
  -f deploy/compose.vaapi.yaml \
  up --build --pull never --detach --wait
```

For another render node, set `VAAPI_DEVICE` and derive
`FILETWIST_RENDER_GID` from that same device. Do not combine the CPU and
VA-API overrides.

The [VA-API override](../deploy/compose.vaapi.yaml) sets `ACCELERATION=auto`.
Filetwist tests a real hardware encode, checks its output, and decodes it before
selecting hardware. Automatic mode uses CPU if probing fails. Recognized
hardware execution errors retry once on CPU and record the reason; unrelated
errors do not silently retry. Explicit `ACCELERATION=vaapi` requires a
successful probe but can still fall back after a recognized execution error.

Device access alone does not establish support for every codec, resolution, or
driver combination. HDR video uses software decoding and tone mapping even
when its final encode uses VA-API. No NVIDIA, AMD, or VideoToolbox acceleration
path is provided.

## Runtime settings

These names apply to `filetwist-server` and the container environment.
Unset settings use defaults; invalid values are rejected at startup.
Sizes accept bytes, decimal units such as `512MB`, or binary units such as
`2GiB`. Durations use Go syntax such as `24h`, `90m`, or `45s`.

| Runtime variable | Default | Purpose |
| --- | --- | --- |
| `DATA_DIR` | `/data` | Absolute path for inputs, outputs, and job manifests |
| `JOB_TTL` | `24h` | Job retention window |
| `CLEANUP_INTERVAL` | `5m` | Expired-job sweep interval |
| `MAX_CONCURRENT_PROCESSES` | CPU count, capped at 4 | Concurrent probe/conversion work; Compose sets 2 |
| `MAX_FILES_PER_JOB` | `25` | File count per upload |
| `MAX_UPLOAD_SIZE` | `4GiB` | Total file bytes per upload |
| `MIN_FREE_SPACE` | `2GiB` | Free-space floor during upload intake |
| `COMMAND_TIMEOUT` | `10m` | Converter command deadline |
| `LISTEN_ADDRESS` | `:8080` | HTTP listen address |
| `WEBROOT` | unset | URL prefix, for example `/convert` |
| `DIAGNOSTICS` | `local` | `local`, `disabled`, or `enabled` |
| `READ_STALL_TIMEOUT` | `60s` | Request-body read inactivity window; `off` disables |
| `WRITE_STALL_TIMEOUT` | `60s` | Response-write inactivity window; `off` disables |
| `MIN_UPLOAD_RATE` | `4KiB` | Average upload bytes/second floor; `off` disables |
| `TRUSTED_PROXIES` | `none` | Trusted proxy peer addresses/CIDRs, or `loopback` |
| `HEALTHCHECK_URL` | derived | Optional healthcheck URL override |
| `ACCELERATION` | `auto` | Video: `auto`, `cpu`, or `vaapi` |
| `VAAPI_DEVICE` | `/dev/dri/renderD128` | Linux render device |

Compose maps the `FILETWIST_` counterparts of `JOB_TTL`,
`MAX_CONCURRENT_PROCESSES`, `MAX_FILES_PER_JOB`, `MAX_UPLOAD_SIZE`,
`MIN_FREE_SPACE`, `COMMAND_TIMEOUT`, `DIAGNOSTICS`, `READ_STALL_TIMEOUT`,
`WRITE_STALL_TIMEOUT`, `MIN_UPLOAD_RATE`, and `TRUSTED_PROXIES`.
Other runtime settings need an `environment` entry in a local Compose override,
for example:

```yaml
services:
  filetwist:
    environment:
      WEBROOT: /convert
      CLEANUP_INTERVAL: 1m
```

Append that override with `-f` after the base and CPU/VA-API files. Changing
`DATA_DIR` also requires mounting writable storage at the new path. Prefer
`FILETWIST_CONTAINER_PORT` over overriding `LISTEN_ADDRESS` in Compose.

Transfers have inactivity limits, not short total-duration limits.
`MIN_UPLOAD_RATE` applies after the read stall window; disabling
`READ_STALL_TIMEOUT` also disables that rate check. Active conversion and
downloads can delay expiry.
`MIN_FREE_SPACE` is an intake guard, not a storage quota or a reservation for
large decoded intermediates. Leave headroom for concurrent jobs and `/tmp`.
Configure proxy body limits and timeouts as described in the
[reverse-proxy guide](reverse-proxy.md).

## Storage and upgrades

Compose runs as UID/GID 10001, with a read-only root filesystem, temporary
`/tmp`, dropped Linux capabilities, and `no-new-privileges`. Its named
`filetwist-data` volume mounts at `/data`. Bind-mounted replacements must be
writable by UID/GID 10001.

Uploads, outputs, and job manifests persist in that volume until deletion or
expiry. Canceling a job is not deletion. Download anything you want to keep;
this workspace is not a backup. A restart marks unfinished jobs interrupted
rather than resuming them automatically.

Before replacing a deployment, stop active work and back up its data volume
and configuration. Keep the Compose project and volume names stable: changing
the project name creates a different volume rather than migrating jobs.
Check storage-format changes before upgrading or rolling back, and retain the
backup until jobs and downloads are confirmed. `docker compose down` keeps the
volume; **`down --volumes` deletes it**. Do not use it for routine upgrades.

## Local server and container commands

To run without Docker, install Go 1.27 and the converter tools on `PATH`, then
build both binaries from the repository root:

```sh
go build -o ./bin/filetwist ./cmd/filetwist
go build -o ./bin/filetwist-server ./cmd/filetwist-server
mkdir -p ./data
PATH="$PWD/bin:$PATH" DATA_DIR="$PWD/data" ACCELERATION=cpu \
  LISTEN_ADDRESS=127.0.0.1:8080 ./bin/filetwist-server serve
```

The container entrypoint is `filetwist-container`; its default command is
`serve`. Other commands on the locally built image include:

```sh
docker run --rm --platform linux/amd64 filetwist:local version
docker run --rm --platform linux/amd64 filetwist:local probe capabilities
docker run --rm --platform linux/amd64 filetwist:local probe cpu
```

`convert` invokes the [CLI](cli.md). The Compose healthcheck uses
`filetwist-container healthcheck`, which probes `/healthz` at the configured
listen port. `/healthz` remains at the root even with `WEBROOT` set. If using
`serve --listen` instead of `LISTEN_ADDRESS`, also set `HEALTHCHECK_URL` so a
separate healthcheck process uses the same address.

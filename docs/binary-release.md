# Filetwist binary archive

This archive contains `filetwist` and `filetwist-server` for Linux/amd64.
No Go compiler or frontend build is needed. These binaries do not run natively
on macOS, Windows, or ARM64.

## Install

Compare the archive's SHA-256 with the checksums file attached to the same
GitHub release before extracting it. Keep both binaries together on `PATH`.
The server requires the CLI to be discoverable at startup.

Install `ffmpeg`, `ffprobe`, `vips`, and `vipsheader` separately. Supported
formats depend on their build options. The project's Dockerfile defines the
canonical runtime; the binary archive does not bundle codecs or drivers.

From the extracted directory:

```sh
export PATH="$PWD:$PATH"
filetwist --version
filetwist-server version
mkdir -p ./data
DATA_DIR="$PWD/data" LISTEN_ADDRESS=127.0.0.1:8080 ACCELERATION=cpu \
  filetwist-server serve
```

Open <http://127.0.0.1:8080/>. Everyone with access shares all jobs, including
downloads and deletion. Use authenticated HTTPS proxying before allowing access
beyond a trusted network. There is no built-in authentication or TLS.

For one local conversion:

```sh
filetwist --output ./converted --operation compatible_photo ./photo.heic
```

Jobs expire after 24 hours by default. Conversion can re-encode media, normalize
colors, strip metadata, or drop tracks. Keep your original files. Refer to the
documentation at the same release tag for profile limitations and settings.

## Licences

Filetwist is Apache-2.0 (`LICENSE`). `licenses/go.LICENSE` covers the Go runtime
and standard library; `licenses/htmx.LICENSE` covers the embedded HTMX asset.
`THIRD_PARTY_NOTICES.md` also describes the separate container dependency stack.
Those FFmpeg/libvips/driver components are not included in this binary archive.

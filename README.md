# Filetwist

Self-hosted image, audio, and video conversion for a household. Upload files
through the web interface, choose a named operation, and download individual
results or a ZIP. A local CLI provides the same conversion profiles.

Filetwist uses Go 1.27, embedded HTMX, FFmpeg/ffprobe, and libvips. Conversion
outputs are checked against the selected profile before they become available.
These checks cover declared, probe-visible properties, not every visual defect
or opaque metadata payload. Keep your originals.

**One shared workspace, not private user accounts.** Anyone who can access the
service can access and manage its jobs. There is no built-in authentication,
TLS, or multi-tenancy. Use a trusted network or an
[authenticated reverse proxy](docs/reverse-proxy.md).

## Quick start from source

You need Docker with Compose. Obtain the source using the repository's
**Code > Clone** URL, or **Code > Download ZIP** and extract the archive.
Open a terminal in the resulting directory containing this README.
No published Filetwist image is required; this command builds a local image,
including the runtime dependencies:

```sh
FILETWIST_IMAGE=filetwist:local FILETWIST_PORT=127.0.0.1:8080 \
docker compose \
  -f deploy/compose.yaml \
  -f deploy/compose.cpu.yaml \
  up --build --pull never --detach --wait
```

Open <http://127.0.0.1:8080/>. Upload files, review the detected media and selected
operations, then convert. Jobs expire after 24 hours by default, and you can
cancel or delete them.

To stop the service while keeping its data volume:

```sh
docker compose -f deploy/compose.yaml -f deploy/compose.cpu.yaml down
```

The supported container target is **Linux/amd64**. On macOS, use the CPU mode
above; Apple Silicon runs this image under Docker emulation. There is no
supported arm64 image or native macOS acceleration. Linux hosts with a suitable
Intel render device can use the [VA-API override](docs/packaging.md#intel-va-api).

## Release artifacts

Version tags automate Linux/amd64 binary releases and, after redistribution
approval, GHCR images. Once published, use the repository's **Releases** page
for archives/checksums and **Packages** for image tags and digests.
The binary archive contains both executables but requires converter tools
installed separately; see [binary installation](docs/binary-release.md).
For images, see [pull-based deployment](docs/packaging.md#using-a-published-image).
Maintainer setup and publishing gates are in the [release guide](docs/releasing.md).

## Operations

| Operation | Output |
| --- | --- |
| `compatible_photo` | JPEG, orientation applied, transparency flattened onto white |
| `smaller_photo` | WebP, quality 80 |
| `lossless_image` | PNG with supported transparency preserved |
| `compatible_video` | H.264 MP4 with AAC when usable audio is present |
| `smaller_video` | H.264 MP4 fitted within 1280 x 720 |
| `extract_audio` | AAC in M4A |
| `compatible_audio` | Stereo 48 kHz MP3 |
| `lossless_audio` | FLAC |

These are conversion profiles, not archival copies. They can re-encode, strip
metadata, drop tracks, downmix audio, or tone-map supported HDR video to SDR.
Smaller modes do not guarantee a smaller file. Animated/multipage images and
HDR still-image tone mapping are unsupported. See
[behavior and limitations](docs/web.md) before converting important files.

## Documentation

- [Deployment and configuration](docs/packaging.md)
- [CLI usage and exit codes](docs/cli.md)
- [Web workflow, conversion behavior, and limitations](docs/web.md)
- [Reverse proxy, TLS, and authentication](docs/reverse-proxy.md)
- [Contributing and release checks](CONTRIBUTING.md)
- [Security policy and private reports](SECURITY.md)
- [Third-party notices and redistribution](THIRD_PARTY_NOTICES.md)

## License

Filetwist is licensed under [Apache-2.0](LICENSE). Generated fixtures carry
their license information in [the fixture manifest](fixtures/manifest.json).
Vendored HTMX retains its [upstream license](internal/web/static/htmx.LICENSE).
Container dependencies have their own licences; see
[third-party notices](THIRD_PARTY_NOTICES.md).

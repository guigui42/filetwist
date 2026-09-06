# Third-party software

Filetwist's own source is licensed under Apache-2.0. That licence does not
replace the licences of software bundled with the server or container.

## Components and notices

| Component | Licence information | Notice location in the image |
| --- | --- | --- |
| Filetwist | Apache-2.0 | `/usr/local/share/licenses/filetwist/LICENSE` |
| Go runtime and standard library | BSD-3-Clause; licence from the build toolchain | `/usr/local/share/licenses/filetwist/go.LICENSE` |
| Vendored HTMX | 0BSD | `/usr/local/share/licenses/filetwist/htmx.LICENSE` |
| Custom-built libvips | LGPL; retain the exact upstream licence | `/opt/vips/share/licenses/libvips/LICENSE` |
| Debian FFmpeg and codec libraries | LGPL/GPL and other licences depending on the package and build configuration | `/usr/share/doc/<package>/copyright` |
| Intel media driver, including its non-free components | Package-specific terms, including bundled components; do not assume an OSI licence for the whole package | `/usr/share/doc/intel-media-va-driver-non-free/copyright` |
| Other Debian runtime packages | Each package's own terms | `/usr/share/doc/<package>/copyright` |

Debian copyright files can be symlinks to another package's notice. Preserve
their targets and referenced texts under `/usr/share/common-licenses`.
The table is an overview, not an exhaustive legal classification.

Generated public fixtures are designated CC0-1.0 in `fixtures/manifest.json`.
Private media is not part of the distribution. Browser test dependencies are
development-only and are not copied into the runtime image.

## Exact build inventory

The image contains the following under `/usr/local/share/licenses/filetwist/`:

- `debian-packages.tsv`: installed binary package/version and source
  package/version, including transitive runtime dependencies;
- `debian-snapshot.txt`: the Debian snapshot used for installation;
- `ffmpeg-buildconf.txt`: the installed FFmpeg build configuration;
- `Dockerfile`: the build recipe, including libvips configuration.

`/opt/vips/share/licenses/libvips/SOURCE` records the exact upstream archive URL
and SHA-256 used for the unmodified libvips source. The image revision label
identifies the application commit when the builder supplies `REVISION`.
These records help assemble corresponding source; they are **not themselves
a source-code distribution or an offer of source**.

## Before distributing binaries or images

Review the licences for the exact candidate, including the Intel non-free
package and all bundled codecs. Preserve required copyright and licence
notices. For components with corresponding-source obligations, arrange a
compliant source distribution matching the shipped binaries, including
applicable patches and build materials. An upstream URL alone is not a
substitute for fulfilling those obligations.

Do not describe the entire container as Apache-2.0 or fully open-source solely
because Filetwist is. The OCI licence label describes the Filetwist application.
Codec patent questions are separate from copyright licensing.

References:
[FFmpeg licensing](https://ffmpeg.org/legal.html),
[libvips licence](https://github.com/libvips/libvips/blob/master/LICENSE),
[Go licence](https://go.dev/LICENSE),
[Debian redistribution guidance](https://www.debian.org/doc/manuals/debian-faq/redistributing.en.html),
and [CC0-1.0](https://creativecommons.org/publicdomain/zero/1.0/legalcode).

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
| Custom-built FFmpeg | GPL-2.0-or-later, built from the pinned Debian source without libcdio or libzvbi | `/opt/ffmpeg/share/licenses/ffmpeg/` |
| Debian codec libraries | Package-specific LGPL/GPL and other licences | `/usr/share/doc/<package>/copyright` |
| Intel media driver, including its non-free components | Package-specific terms, including bundled components; do not assume an OSI licence for the whole package | `/usr/share/doc/intel-media-va-driver-non-free/copyright` |
| Other Debian runtime packages | Each package's own terms | `/usr/share/doc/<package>/copyright` |

Debian copyright files can be symlinks to another package's notice. Preserve
their targets and referenced texts under `/usr/share/common-licenses`.
The table is an overview, not an exhaustive legal classification.

This software is based in part on the work of the Independent JPEG Group.
Release source materials also retain the Intel driver's additional licence
and third-party notices and the notices for statically incorporated code.

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

`/opt/ffmpeg/share/licenses/ffmpeg/` preserves its exact Debian source descriptor,
source checksums, Debian packaging, build helper, configure flags and licence
texts. It is a custom build, not the stock Debian FFmpeg binary.

`/opt/vips/share/licenses/libvips/SOURCE` records the exact upstream archive URL
and SHA-256 used for the unmodified libvips source. The image revision label
identifies the application commit when the builder supplies `REVISION`.
These records help assemble corresponding source; they are **not themselves
a source-code distribution or an offer of source**.

The installed source-package inventory alone does not cover statically embedded
components. Release tooling also reads `Built-Using` and `Static-Built-Using` from
`/var/lib/dpkg/status`, preserves their exact source versions and consuming
binary notice hashes, and requires those contributions in the source review.
Source routing is not publication clearance: unresolved licence-combination,
source-completeness or notice-delivery blockers remain publication blockers
even when all selected source archives have been downloaded.

Release automation consumes the exact inventory and the
[reviewed source policy](.github/container-source-policy.json). It preserves
official Debian/upstream links only for specifically reviewed source routes and
mirrors complete source sets where required. The versioned release carries the
source index, mirrored archives, build materials, actual notices and checksums.
See the [publishing and source-retention procedure](docs/releasing.md).
Changed dependency identities, notice hashes, libvips source or Dockerfile
require renewed review; the workflow never approves new terms automatically.

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

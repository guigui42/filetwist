# Publishing a release

The [Release workflow](../.github/workflows/release.yml) uses GoReleaser 2.18.1
to publish GitHub releases. It then calls the
[Publish container workflow](../.github/workflows/container-publish.yml)
directly to publish to GitHub Container Registry (GHCR).
Adding these workflows does not publish a release or image by itself.

## One-time repository setup

1. Confirm the public repository URL. Keep `go.mod`, the Docker image source
   label, and release links consistent with it. GoReleaser infers the GitHub
   destination from the checkout; the image workflow uses
   `ghcr.io/<owner>/<repository>` in lowercase.
2. Review source and Git history for private media, credentials, and material
   without redistribution permission. Confirm provenance and copyright
   ownership before making the repository public.
3. Enable GitHub private vulnerability reporting and confirm that the
   **Security > Report a vulnerability** route works. Follow `SECURITY.md`.
4. Allow Actions to create releases and write packages. The workflows request
   `contents: write` only for binary publication and `packages: write` for GHCR.
   They use the repository's `GITHUB_TOKEN`; no personal access token or Docker
   Hub credentials are needed.
5. Create a GitHub environment named `ghcr` with required reviewers and restrict
   who can publish release tags. Creating the workflow does not configure
   environment protection rules.

## GitHub Actions dry run

Use **Actions > Release > Run workflow** and select the branch to test, or run:

```sh
gh workflow run release.yml --ref YOUR_BRANCH
```

Manual runs are always non-publishing, even when selecting a tag. They run
the same Go, container, browser, and GoReleaser snapshot checks as CI. The
binary and container publication jobs only run for tag pushes, not manual
dispatches, and dry-run jobs retain read-only repository permissions.

Download `release-dry-run-<attempt>` from the workflow run's artifacts section
within seven days. It contains the Linux/amd64 snapshot archive, checksums,
and the exact source commit marker. These are workflow artifacts, not a GitHub
release or GHCR image. A successful dry run does not exercise registry writes
or replace the redistribution approval and corresponding-source requirements.

## Container redistribution gate

Complete the [third-party redistribution review](../THIRD_PARTY_NOTICES.md)
first. Include required notices and provide corresponding source for the exact
GPL/LGPL components using a compliant distribution mechanism. Resolve non-free
component terms before distribution. The current inventory is not a substitute
for that review or for hosting the required sources.

Configure these repository or `ghcr` environment variables:

| Variable | Required value |
| --- | --- |
| `GHCR_PUBLISH_ENABLED` | `true`, only after the redistribution review is complete |
| `CONTAINER_SOURCE_BASE_URL` | HTTPS base directory containing a source directory for each version tag |

For example, a base URL of `https://downloads.example.org/filetwist-sources`
must serve the matching source materials at
`https://downloads.example.org/filetwist-sources/v0.1.0/`.
Do not include a query string or fragment in the base URL.
The workflow requires that versioned URL to respond successfully over HTTPS
and records it in the image label `io.github.filetwist.corresponding-source`.
Reachability is not a legal compliance check: the maintainer must ensure the
source materials actually match the shipped image and fulfil applicable terms.

Without this configuration, image publication fails explicitly before building
or logging in. The binary release can still succeed because its archive does
not include the FFmpeg/libvips/driver stack.

## Publish a version

Push a new tag on the intended commit after merging the release configuration:

```sh
git tag -a v0.1.0 -m "Filetwist v0.1.0"
git push origin v0.1.0
```

`v0.1.0` is an example, not a claim that it exists. Use `vMAJOR.MINOR.PATCH`,
optionally with a prerelease suffix such as `v0.1.0-rc.1`. Build metadata
containing `+` is not supported by the image tag format.

The release workflow runs the same Go, container, and browser CI as pull
requests. Only after CI succeeds does GoReleaser create the GitHub release:

- `filetwist_<version>_linux_amd64.tar.gz`, containing both executables,
  installation notes, and Filetwist/Go/HTMX notices;
- `filetwist_<version>_checksums.txt`, containing SHA-256 archive and
  source-commit asset checksums;
- `filetwist-source-commit.txt`, recording the exact source commit, also
  included in the binary archive;
- generated change notes and automatic prerelease marking for prerelease tags.

The container workflow checks out the exact commit recorded in the release's
`filetwist-source-commit.txt` asset and rejects a tag that has since moved.
It builds the canonical
Linux/amd64 Dockerfile, exercises conversions and browser workflows, enforces
the image size budget, and then pushes the cached build with SBOM and build
provenance attestations. The job summary records its digest.

Images use the exact version tag, for example
`ghcr.io/<owner>/<repository>:v0.1.0`. No `latest`, major, or minor aliases are
updated, including for prereleases. Prefer the recorded digest for deployment.
Never move a Git tag or republish a version with different contents.

The container workflow is called directly, rather than relying on a
`release: published` event: releases created by `GITHUB_TOKEN` do not start
another workflow through that event.

If GHCR publication fails after the binary release succeeds, fix the error and
use **Actions > Publish container > Run workflow** with the same existing tag.
Do not recreate the binary release or move its tag. A manually dispatched image
run requires an existing non-draft GitHub release with that source-commit asset,
still performs the image and browser checks, and requires the same gate.

## First public image

GHCR packages can initially be private. After the first successful push, make
the package public if intended, and confirm anonymous pulling works. For an
existing package, grant this repository Actions access to it. The image source
label links the package to the publishing repository.

Update release notes with the image digest, supported-platform and storage
compatibility details, and any rollback limitations. Claim VA-API support only
with evidence from a suitable real Linux Intel device. The CPU workflow cannot
establish hardware support.

## Local dry run

Install GoReleaser 2.18.1, use a Git checkout with its origin configured, then:

```sh
goreleaser check
goreleaser release --snapshot --clean
```

This writes archives and checksums to ignored `dist/` without publishing or
requiring a tag. The hook stages the installed Go licence under ignored
`.release/`. If your Go packaging stores its licence elsewhere, set
`GO_LICENSE_FILE` to that file. Homebrew's `libexec` layout is recognized.

For the container path, use the existing local commands:

```sh
make packaging-build IMAGE=filetwist:candidate VERSION=dev REVISION=unknown
scripts/validate-container.sh filetwist:candidate
scripts/measure-container.sh filetwist:candidate
make test-browser IMAGE=filetwist:candidate
```

GitHub references:
[managing releases](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository),
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/working-with-repository-security-advisories/configuring-private-vulnerability-reporting-for-a-repository),
[container registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry),
[workflow token event restrictions](https://docs.github.com/en/actions/how-tos/writing-workflows/choosing-when-your-workflow-runs/triggering-a-workflow),
and [GoReleaser configuration](https://goreleaser.com/customization/).

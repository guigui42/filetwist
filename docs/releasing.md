# Publishing a release

The [Release workflow](../.github/workflows/release.yml) uses GoReleaser 2.18.1
to publish GitHub releases. It then calls the
[Publish container workflow](../.github/workflows/container-publish.yml)
directly to publish to GitHub Container Registry (GHCR).
Adding these workflows does not publish a release or image by itself.
The source/OCI helpers use Python 3.11 or newer and its standard library; the
Ubuntu 24.04 workflow runners already provide it.

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
   `contents: write` only for binary publication and appending corresponding-source
   assets. Only the protected image job requests `packages: write`.
   They use the repository's `GITHUB_TOKEN`; no personal access token or Docker
   Hub credentials are needed.
5. Create a GitHub environment named `ghcr` with required reviewers and restrict
   who can publish release tags. Creating the workflow does not configure
   environment protection rules. Allow version tags (`v*`) and `main` in its
   deployment policy: image-only retries run the current tooling from `main`.
   Protect release tags against updates and deletion.
6. Configure the redistribution gate below. After the first image push, explicitly
   make the package **Public** in its package settings. Public repository visibility
   does not make a GHCR package public. Grant this repository Actions access to an
   existing package. No workflow changes package visibility or auto-approves licences.

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
first. The reviewed [source policy](../.github/container-source-policy.json) pins
each Debian source and binary version, the SHA-256 of every resolved copyright
notice, shared licence texts, Go/HTMX notices, exact libvips source and licence,
and the Dockerfile. Package-specific review selects official upstream/Debian
source links where permitted and release mirrors where needed.
Source-built FFmpeg is reviewed separately from dpkg-installed packages. Its
policy pins the exact Debian source version and every bundled source/build
record, including descriptor, source-file checksums, configuration flags, build
script, Debian packaging and licences. Its full Debian source set is still
resolved from authenticated APT metadata and checked against those records.
Missing custom-source review or a mixed custom/packaged FFmpeg stack fails closed.

To draft a renewed policy from an actual candidate, use the same notice collector
and source helper used by the workflow:

```sh
docker run --rm --network none --read-only \
  --mount "type=bind,src=$PWD/scripts/container-notices.sh,dst=/notices.sh,readonly" \
  --entrypoint sh filetwist:candidate /notices.sh > .release/inventory.tar
python3 scripts/container-sources.py review-template \
  --inventory .release/inventory.tar --checkout . --revision "$(git rev-parse HEAD)" \
  --first-party-asset internal/web/static/app.js \
  --first-party-asset internal/web/static/app.css > .release/source-policy.pending.json
```

The template is always pending, conservatively selects mirrors, and cannot
authorize publication. Review the exact grants, select source routes, record
any required supplemental notices and resolve all blockers before replacing
`.github/container-source-policy.json`. Supplemental notices are bound to exact
authenticated source archives, archive members and content hashes. Mirrored
embedded-source contributions also retain their licence/NOTICE files in the
accompanying materials, rather than relying only on the consuming binary's notice.

The collector also reads the image's complete `/var/lib/dpkg/status`. It includes
every exact source pair declared by `Built-Using` and `Static-Built-Using`,
including folded fields, epochs and multiple versions of one source. Each
embedded contribution records its consuming binary, declaration field and the
consumer's notice hash. Missing source records or an incomplete review fail
closed. Counting only the installed source-package names is not a source-closure
check. These declarations also do not replace reviewing notice delivery or
undeclared embedded contributions.

There is no automatic approval based on a licence keyword. A new dependency,
changed version, notice, source archive, or Dockerfile fails closed and requires
a renewed policy review. Application-only releases reuse the same policy.
The policy is evidence of the recorded review, not legal advice or a replacement
for reviewing the actual terms, including Intel non-free components.
The `container-source-evidence-<tag>-<attempt>` workflow artifact retains actual
notices/inventory and APT source metadata for seven days, including when the
policy rejects a changed dependency. Review that evidence, update the policy in
a reviewed repository change, then retry using the updated tooling.

Source routing and publication clearance are separate. The policy must explicitly
set `status: "approved"` and include `blockers: []`, or document every blocker as
`status: "resolved"` with a nonempty `resolution`. Mirroring a component does not
resolve an incompatible licence combination, missing source, or incomplete
notices. The workflow may prepare review artifacts for a pending policy, but
will not append immutable release assets or publish the image until clearance
is recorded. `GHCR_PUBLISH_ENABLED=true` cannot override this check.

Configure these repository or `ghcr` environment variables:

| Variable | Required value |
| --- | --- |
| `GHCR_PUBLISH_ENABLED` | `true`, only after the redistribution review is complete |
| `CONTAINER_SOURCE_BASE_URL` | `https://github.com/<owner>/<repository>/releases/tag` |

For this repository, set the source base to
`https://github.com/guigui42/filetwist/releases/tag`. The pipeline hosts versioned
source assets directly on the matching GitHub release. Arbitrary external base
URLs are not accepted: a reachable landing page is not proof that the prepared
source assets were published there. The image's
`io.github.filetwist.corresponding-source` label points at that release.

Source preparation and publication happen **before** the protected `ghcr` job,
so reviewers can inspect the actual source assets before approving the image.
Neither source job can write packages. Without the enabled gate and matching
source location, the protected job fails before image publication. A binary
release can still succeed because it does not include the media/driver stack.

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

CI and image-only retries share the
[container preparation action](../.github/actions/prepare-container/action.yml),
which reuses the existing browser setup action. There is one implementation of
the build, conversion, size-budget and browser checks.

For a new tag, CI builds and validates the release-version OCI candidate once,
before binary publication. The existing container workflow then runs automatically:

1. Resolve `filetwist-source-commit.txt` and reject a moved tag. Keep the current
   workflow's tooling separate from the exact release source checkout.
2. Download the candidate already validated by CI, including its SBOM and build
   provenance. Verify its validation receipt against the entire archive SHA-256,
   OCI index/runtime hashes, release tag, exact binary source commit, current run,
   and successful conversion, size and browser checks. Load that same runtime to
   collect source evidence. Do not rebuild it or repeat those test passes.
3. Extract the actual notices, inventory and build recipe. In a disposable
   candidate container, enable `deb-src` for its pinned snapshot, authenticate
   APT indexes, and resolve each **exact source version**, including declared
   embedded contributions and epochs. Version-scoped requests and archive paths
   keep different versions of one source distinct.
   Check descriptor SHA-256 hashes and complete source file sets. Download and
   hash-check the reviewed mirrors; check official external links over HTTPS.
4. Prepare deterministic source archives and append them to the existing release.
   Compare all existing asset hashes before adding any missing asset. Changed
   existing assets stop the run, rather than being overwritten.
5. Pause for the `ghcr` reviewer. Approve after checking source policy and assets.
   The protected job uploads the original OCI blobs, manifests, SBOM and provenance
   using `GITHUB_TOKEN`. It never assigns `latest` or replaces a different version.
6. Pull the published digest with an empty Docker credential configuration.
   The summary claims public availability only after this anonymous pull succeeds.

Companion source assets are separate from the binary archive and its checksums:

| Asset | Contents |
| --- | --- |
| `filetwist_<version>_sources.json` | Exact source identities, authenticated APT file hashes, source routes, immutable OCI index/runtime digests and archive hashes |
| `filetwist_<version>_sources.md` | Human-readable source index and links |
| `filetwist_<version>_source-mirrors.tar.gz` | Complete selected Debian source sets and libvips where required |
| `filetwist_<version>_source-materials.tar.gz` | Exact application source, Dockerfile, inventories, notices, licence texts and reviewed policy |
| `filetwist_<version>_source-checksums.txt` | SHA-256 for the four companion source assets |

Debian mirror sets retain the `.dsc`, upstream archive(s), Debian patches and
signatures named by authenticated source metadata. Nothing is silently converted
to a source homepage link or substituted with a newer source package.
Build materials retain the raw dpkg status used to derive embedded-source
declarations. The source-evidence artifact also includes the complete exact
request list, so an unavailable old source version is visible rather than
silently omitted.

Images use the exact version tag, for example
`ghcr.io/<owner>/<repository>:v0.1.0`. No `latest`, major, or minor aliases are
updated, including for prereleases. Prefer the recorded digest for deployment.
Never move a Git tag or republish a version with different contents.

The container workflow is called directly, rather than relying on a
`release: published` event: releases created by `GITHUB_TOKEN` do not start
another workflow through that event.

## Retry an existing image release

After fixing a failed step, prefer **Re-run failed jobs** in the existing run.
Source and candidate artifacts are passed by immutable artifact IDs, so a failed
publication job can reuse the original prepared artifacts across run attempts.
Approve within their seven-day retention window.
The protected publisher rechecks the validation receipt and public source assets
after approval. A missing, stale or mismatched receipt fails rather than silently
skipping validation or rebuilding a different candidate.

To use newer release tooling without retagging old application code:

```sh
gh workflow run container-publish.yml --ref main -f tag=v0.1.0
```

This runs tooling from `main`, but builds/tests the exact existing binary release
commit. It does **not** recreate the binary release or alter any Git tag.
The same command works for any supported existing version, not just the example.
Manual dispatch of **Release** remains a read-only dry run and cannot retry a push.

An image-only run without this run's CI artifact uses the same shared preparation
action. It builds once, or loads the already published exact image, then runs the
full conversion, size and browser checks and records fresh evidence. Source
preparation and publication use the same helpers as the automated new-tag path.

When the image already exists, preparation fetches its OCI archive by exact
hashes, verifies the runtime digest against the immutable source manifest, then
reruns its checks. The full OCI index hash is checked too, including attestations.
Mirrored source is recovered from the existing release assets
and verified against exact source metadata, rather than depending on its original
upstream host still being available. The publish job compares the exact runtime
manifest digest (config and compressed layer hashes) and retains the existing
image index and attestations unchanged. A discrepancy in any of these hashes
fails explicitly.
An existing release reuses its hash-verified historical review and original
archive bytes. Updating the policy for newer dependencies does not implicitly
approve a changed old image, and a different gzip version cannot rewrite its
historical archives. The actual old inventory, notices and build recipe must
still match that review exactly.

If a run stopped after source publication but before any image push, rerun its
failed job while the candidate artifact remains available. A rebuilt candidate
with different runtime or attestation hashes cannot replace that recorded candidate.
If the original artifact is unavailable and no identical candidate survives, use a new version instead
of weakening the checks or moving the tag.

## First public image

The first push can succeed while the anonymous verification fails because GHCR
created a private package. Open the package's **Settings > Change visibility >
Public**, then rerun the failed job. There is no supported visibility change
in this workflow and no assumption that a public repository implies a public
package. The failure message provides these instructions; it never reports a
private package as public.

Update release notes with the image digest, supported-platform and storage
compatibility details, and any rollback limitations. Claim VA-API support only
with evidence from a suitable real Linux Intel device. The CPU workflow cannot
establish hardware support.

## Keep corresponding source available

Retain the source assets for every distributed image. GPLv3 section 6(d) can
permit equivalent third-party source access, but responsibility for continued
access remains with the publisher. A successful HTTP check is not a legal
compliance determination.

Check the recorded public locations periodically and after a hosting change:

```sh
python3 scripts/container-sources.py check-links \
  --repository guigui42/filetwist --tag v0.1.0
```

If an external source disappears, restore compliant equivalent access promptly.
Do not silently rewrite a historical manifest, overwrite source assets, or delete
mirrors while their image remains distributed. Review the remedy and publish
additional clearly identified source materials when needed.

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

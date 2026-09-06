# Contributing

Keep changes focused on household image, audio, and video conversion. Preserve
the [declared conversion behavior](docs/web.md), explicit errors, and output
validation before making a result available.

## Development

Use Go 1.27 as specified in [go.mod](go.mod). Runtime integration tests also need
`ffmpeg`, `ffprobe`, `vips`, and `vipsheader` on `PATH`. The
[application Dockerfile](deploy/Dockerfile) pins the media runtime;
the [native recipe](deploy/Dockerfile.runtime) records its toolchain and codec
dependencies. The web assets are embedded; no frontend build is needed.

Run the smallest relevant tests while developing. Before submitting:

```sh
go test -race ./...
go vet ./...
git ls-files -z '*.go' | xargs -0 gofmt -l
make packaging-compose-config
```

The formatting command should print nothing. Format changed Go files with
`gofmt -w` before submitting. [CI](.github/workflows/ci.yml) runs these checks
without publishing anything. Converter-dependent Go tests can skip when a tool
or codec is missing; a green Go run is not a container or hardware check.
The separate Linux container job builds Go over the pinned runtime and executes the smoke
script with mandatory codec/conversion assertions, then the browser workflow.
CI also creates a GoReleaser snapshot archive without publishing it.

CI and release jobs share cached Go modules/build outputs, npm downloads,
GoReleaser and Chromium binaries, and Docker build layers. Go caches track
`go.mod` and `go.sum` when present; npm and Chromium headless-shell caches track
the browser lockfile. Binary caches use exact OS/architecture and version keys,
with the Ubuntu release included for Chromium. Browser setup still runs `npm ci` and
installs required OS libraries on cache hits; it does not cache `node_modules`.
CI installs only the headless shell used by the tests, not full Chromium.
The shared setup actions live under [`.github/actions`](.github/actions).
The [shared container preparation action](.github/actions/prepare-container/action.yml)
owns the CI and image-only retry build/test sequence. New-tag releases pass its
validated OCI artifact through the existing CI → binary → container workflow
chain instead of rebuilding or repeating container tests. A receipt binds the
archive hash, OCI digests, exact commit, tag, run and completed checks; publication
verifies it again after environment approval.

Docker builds use the GitHub Actions v2 cache with `mode=max`: `filetwist-app`
for Go-only builds and `filetwist-runtime` for deliberate media-runtime builds.
Native compilation occurs only in the [media-runtime lane](docs/releasing.md#updating-the-media-runtime),
not when application caches miss. Caches follow GitHub's branch access rules
and never bypass conversion checks or publication gates.

Describe the user-visible change, affected operations, reproduction steps, and
checks performed. Add regression coverage for changed behavior. Do not put
private media, metadata, paths, tokens, or original filenames in an issue or
pull request.

## Browser workflow

Browser tests use a disposable CPU-mode container, not an existing deployment.
Node.js 22 or newer is needed only for these tests, not to build or run
Filetwist. Install the locked development dependency and Chromium once:

```sh
npm --prefix tests/browser ci
cd tests/browser && npx playwright install chromium && cd ../..
make packaging-build IMAGE=filetwist:local
make test-browser IMAGE=filetwist:local
```

On Linux, use `npx playwright install --with-deps chromium` when browser system
libraries are missing. The tests cover upload, operation selection, polling,
individual and ZIP downloads, failed-action retry, cancellation, and deletion.
Conversion and storage use the real image; the queue-full response alone is
injected to exercise deterministic error handling.

The test server uses loopback port 18765 and refuses to reuse an existing
service. Set `FILETWIST_TEST_PORT` to another unused port if needed. Job data is
ephemeral and the container is stopped after the run. Failure traces and
screenshots are under `tests/browser/test-results/`; use synthetic fixtures only.

## Fixtures

Prefer a small generated case that isolates one behavior. Public fixtures must
be generated, public domain, or explicitly licensed for redistribution, with
source and license recorded in [fixtures/manifest.json](fixtures/manifest.json).
Keep public fixtures at or below 64 KiB and remove personal metadata.

Use [fixtures/generate.go](fixtures/generate.go) to update the corpus, then run:

```sh
go run ./fixtures/generate.go -check
go test ./...
```

The [fixture guide](fixtures/README.md) lists generator prerequisites and the
manifest workflow. Synthetic `ffprobe_json` fixtures exercise probing or
planning only; they are not executable media or evidence of an actual
conversion. Never commit private phone captures or bypass fixture ignores.

## Release checks

For a release candidate, run the development and fixture checks above, then:

```sh
goreleaser check
goreleaser release --snapshot --clean
make packaging-build IMAGE=filetwist:local VERSION="$(git rev-parse --short=12 HEAD)"
scripts/validate-container.sh filetwist:local
scripts/measure-container.sh filetwist:local
make test-browser IMAGE=filetwist:local
```

These checks require GoReleaser 2.18.1, Docker, Compose, Python 3, curl, gzip,
and the browser dependencies described above. The smoke script uses generated
fixtures and a temporary container on a random loopback port;
it does not need the deployed household service. The size tool checks the
built image rather than relying on a previously reported measurement.
Size measurement first uses `gzip -1`; if that archive exceeds the unchanged
450 MiB ceiling, it retries with `gzip -9` before rejecting the image. The
reported compression level identifies which measurement was used.

On suitable Linux Intel hardware, run `make test-vaapi` after changes to the
hardware path, FFmpeg, libva, or the driver stack. Confirm that it executes
rather than skips. CPU checks and macOS emulation do not establish VA-API
readiness. Review both CPU fallback and a real device conversion.

Before distributing a candidate, review the source-build instructions,
relative links, declared limitations, and dependency license notices. Preserve
[LICENSE](LICENSE), the [HTMX notice](internal/web/static/htmx.LICENSE), and
fixture licensing. Building or checking a candidate does not publish an image,
tag, or release. There is no promised release cadence, support SLA, or universal
phone compatibility.

Follow the [publishing procedure](docs/releasing.md) before creating a public
release. Image redistribution additionally requires the
[third-party licence and corresponding-source review](THIRD_PARTY_NOTICES.md).
For changes to release tooling, run `go test ./scripts ./deploy`. The Go runner
also executes Python-stdlib regression cases for source identities, notice-policy
gates, complete descriptors, deterministic archives, immutable release assets and
OCI image retry behavior. These cases do not publish anything. A live release
still requires the protected environment and successful anonymous image pull.

Unless explicitly marked otherwise, contributions intentionally submitted for
inclusion are accepted under [Apache-2.0](LICENSE), consistent with Section 5.

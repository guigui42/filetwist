# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

The browser interface is server-rendered, including on mobile devices. A local
CLI shares its conversion layer and profiles; there is no native mobile app in
the current product. See [README.md](README.md).

## Users

Two audiences have equal priority, confirmed during product initialization:

- Non-technical household members who need to convert photos, audio, or video
  through the browser using named operations rather than codec configuration.
- Self-hosting enthusiasts who run the service and use both the browser and CLI.

Do not assume every person converting a file also administers the deployment,
or treat either audience as secondary.

## Product Purpose

Filetwist converts household images, audio, and video into formats suited to
compatibility, smaller-file profiles, or supported lossless outputs. Success
means choosing an appropriate operation, understanding its changes and warnings,
and retrieving a result that passes the selected profile's output validation.

Conversion is not archival preservation. Users should keep their originals and
inspect important results. See [conversion behavior](docs/web.md).

## Positioning

A self-hosted household converter with named profiles, shared CLI/web conversion
logic, and output validation before publication. Media is processed on the
deployment host, not submitted to a third-party conversion service.

This is one shared workspace, not a private account-based service. Self-hosting
does not imply per-user isolation, complete anonymization, or safe handling of
hostile media. See [the product scope](README.md) and
[shared jobs](docs/web.md#shared-jobs).

## Operating Context

- Browser workflow: upload files, review detected media and eligible operations,
  confirm conversion, follow per-file progress and warnings, then download
  individual results or a ZIP.
- CLI workflow: convert one local input using the same named profiles, with
  structured JSON results by default and optional human-readable output.
- Anyone with access can view, download, cancel, or delete shared jobs. Cancel
  stops work without deleting stored files; delete removes the job and its files.
- Originals and results are temporary. Retention is configurable and defaults
  to 24 hours. Interrupted work does not automatically resume after a restart.
- Administrators manage runtime dependencies, storage, resource limits, and
  access. Authentication and TLS belong at the reverse proxy when required;
  there is no built-in authentication or TLS.

Sources: [web workflow](docs/web.md), [CLI usage](docs/cli.md),
[deployment](docs/packaging.md), and [reverse proxy](docs/reverse-proxy.md).

## Capabilities and Constraints

- Scope is images, audio, and video. Documents, PDF, OCR, arbitrary converter
  plugins, and multi-tenant hosting are outside the documented product scope.
- Preserve the eight named operations and their guarantees:
  `compatible_photo`, `smaller_photo`, `lossless_image`, `compatible_video`,
  `smaller_video`, `extract_audio`, `compatible_audio`, and `lossless_audio`.
  The [profile definitions](docs/web.md#conversion-profiles) remain authoritative.
- Recommendations are content-aware. The web form can recommend an eligible
  alternative for review before conversion. The CLI rejects an unavailable
  default rather than silently switching operations or media kind.
- "Smaller" does not guarantee fewer bytes. Lossless outputs do not restore
  information already lost or preserve the original file unchanged. Audio
  extraction re-encodes the selected track rather than copying it bit for bit.
- Unsupported inputs must fail explicitly. Animated/multipage images and HDR
  still-image tone mapping are unsupported; format support depends on actual
  runtime capabilities.
- Output validation covers declared, probe-visible properties. It does not prove
  perfect fidelity, universal device playback, or removal of all identifying
  information. Metadata policies are not an anonymization guarantee.
- Keep the web layer lightweight: Go templates, embedded HTMX, CSS, and
  JavaScript, with no frontend build or runtime CDN dependency. Conversion
  belongs in the shared layer, not duplicated in handlers or browser code.
- Runtime support and acceleration are deployment-dependent. The documented
  container target is Linux/amd64; native macOS acceleration and an arm64 image
  are not supported. Do not present skipped codec or hardware checks as support.

Sources: [behavior and limitations](docs/web.md), [CLI selection](docs/cli.md),
[runtime support](README.md), and [contributor constraints](CONTRIBUTING.md).

## Brand Commitments

The product name is **Filetwist**. Existing interface copy describes
"Compatibility-first conversion for phone photos, audio, and video."
Keep labels grounded in the actual operation and its consequences.

The existing [application shell](internal/web/templates/layout.html) and
[interface styles](internal/web/static/app.css) are implementation evidence, not
permission to replace the visual identity. No new aesthetic or voice direction
was selected during initialization.

## Evidence on Hand

- [README.md](README.md) and [behavior documentation](docs/web.md) establish
  scope, capabilities, limitations, and deployment expectations.
- [Web templates](internal/web/templates) contain current product copy and
  workflow states.
- [Synthetic fixtures](fixtures/README.md) and the
  [fixture manifest](fixtures/manifest.json) provide small, redistributable
  examples. Simulated probe data is not proof of executable media support.
- [LICENSE](LICENSE) and [third-party notices](THIRD_PARTY_NOTICES.md) establish
  licensing obligations. Preserve vendored dependency notices.

Use synthetic or explicitly redistributable media in demonstrations. Do not
fabricate testimonials, benchmarks, compatibility claims, or support promises.

## Product Principles

1. Serve household members and self-hosting enthusiasts equally without making
   codec or deployment expertise a prerequisite for browser conversion.
2. Make the selected operation and its consequences explicit. Never silently
   weaken a profile or substitute a different media kind.
3. Publish only validated outputs, and communicate the limits of that validation.
4. Make shared access and temporary storage clear. Protect uploaded media and
   preserve safe deployment defaults without overstating privacy guarantees.
5. Keep CLI and web conversion behavior consistent through shared logic, while
   preserving their documented differences in selection and interaction.

## Accessibility & Inclusion

Preserve existing keyboard-operable upload controls, labels, the skip link, and
announced status/error messages in the
[upload page](internal/web/templates/index.html) and
[application shell](internal/web/templates/layout.html).

Open decisions: a formal accessibility conformance target, additional language
requirements, and audience-specific assistive-technology needs have not been
established. Existing accessibility features are not a conformance claim.

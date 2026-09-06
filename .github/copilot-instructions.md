# Filetwist

Filetwist is a self-hosted household image, audio, and video converter.
It uses Go, FFmpeg/ffprobe, libvips, and a server-rendered web interface with
embedded HTMX. The CLI and web interface share the conversion layer.

- Keep changes focused and simple. Follow existing package boundaries and
  patterns; avoid unnecessary dependencies or frameworks.
- Preserve documented conversion profiles and consistent CLI/web behavior.
  Reject unsupported inputs explicitly and validate outputs before publishing
  them. Do not weaken a profile's guarantees or silently substitute a different
  media kind.
- Write idiomatic Go with explicit error handling, cancellation, and resource
  cleanup. Keep subprocess execution and concurrency bounded.
- Keep the web layer lightweight and server-rendered. Reuse shared conversion
  logic rather than duplicating it in handlers or browser code.
- Protect uploaded media and user privacy. Preserve safe deployment defaults
  and use small, synthetic or explicitly redistributable fixtures.
- Add regression coverage for behavior changes and run the relevant existing
  checks. Do not treat skipped converter or hardware checks as proof of support.
- Update documentation when behavior, configuration, or deployment changes.
  Preserve dependency licence notices and release publication safeguards.

Use [README.md](../README.md) for scope,
[CONTRIBUTING.md](../CONTRIBUTING.md) for development guidance, and
[docs/web.md](../docs/web.md) for conversion behavior and limitations.

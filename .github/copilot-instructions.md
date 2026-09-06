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


<!-- github-knowledge-base-start -->
## Knowledge Base

### Purpose

This repository uses the Knowledge Base at [https://github.com/guigui42/filetwist](https://github.com/guigui42/filetwist) on branch `main`.

### Required behavior

1. Before changing code, read `docs/index.md` from that branch.
2. Use the index to open only the knowledge files relevant to the task.
3. If the index is unavailable, stop and report that the Knowledge Base could not be loaded.

### Source of truth

Generated knowledge tracks the code. When the knowledge and code disagree, trust the code.
<!-- github-knowledge-base-end -->

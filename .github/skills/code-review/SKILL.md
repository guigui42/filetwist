---
name: code-review
description: Review Filetwist pull requests for high-confidence defects, regressions, security or privacy risks, broken conversion guarantees, and release-safety failures. Use this skill for every GitHub Copilot pull request code review in this repository, including Go, web, conversion, deployment, workflow, fixture, and documentation changes.
license: Apache-2.0
---

# Filetwist code review

Review the pull request as a defect-finding exercise. Focus on behavior introduced
or changed by the pull request, not on restyling existing code. Apply the broad
repository constraints in `.github/copilot-instructions.md`; the checks below
focus on review-specific failure modes rather than repeating those instructions.

## Establish the intended behavior

1. Read the pull request description, linked issue context, and repository
   instructions.
2. Inspect the complete diff and enough surrounding code to understand the
   changed control flow, callers, cleanup paths, tests, and user-visible behavior.
3. Use the relevant repository documentation as the contract:
   - `README.md` for product scope and supported deployment.
   - `docs/web.md` for conversion profiles, job behavior, privacy, and limitations.
   - `docs/cli.md` for CLI behavior, result schema, and exit codes.
   - `docs/packaging.md` for container, platform, acceleration, and deployment
     guarantees.
   - `docs/reverse-proxy.md` for authentication, TLS, diagnostics, and trusted
     proxy behavior.
   - `CONTRIBUTING.md` for tests, fixtures, packaging, and release expectations.
   - `docs/releasing.md` for publication and redistribution safeguards.
4. When documentation and code disagree, determine whether the pull request is
   intentionally changing the contract. Require matching tests and documentation
   for intentional behavior changes.
5. Use configured MCP context when a pull request references an issue, incident,
   or external requirement. Treat that context as supporting evidence and verify
   the implementation against the repository.

## Review priorities

Look for concrete failures in these areas:

### Conversion correctness

- Preserve each named profile's documented format, codec, stream, metadata,
  color, precision, dimension, and fast-start guarantees.
- Reject unsupported inputs explicitly. Do not silently select a different
  operation or media kind.
- Ensure every generated output is probed and validated before it is published,
  returned, downloaded, or reported as successful.
- Keep CLI and web behavior consistent by using the shared conversion layer.
- Check stream selection, silent media, HDR handling, orientation, alpha,
  high-bit-depth content, and partial converter failures when affected.

### Jobs, files, and resources

- Check upload, probe, queue, conversion, cancellation, download, expiry, and
  deletion transitions for races or stranded files.
- Ensure temporary files and failed outputs are removed while committed inputs
  and valid outputs remain available for their intended lifetime.
- Preserve cancellation and timeout propagation. Do not let a client disconnect
  corrupt committed job state.
- Keep goroutines, queues, subprocesses, file descriptors, and concurrent
  conversions bounded and cleaned up on every return path.
- Prevent path traversal, unsafe archive names, accidental overwrite, and
  publication of partially written files.

### Web and privacy behavior

- Keep the interface server-rendered and progressively enhanced with embedded
  HTMX. Avoid adding a frontend runtime or duplicating server validation in
  browser code.
- Remember that Filetwist is one shared workspace without built-in accounts.
  Random job IDs are not authorization, and proxy authentication does not create
  per-user isolation.
- Flag exposure of uploaded media, original filenames, filesystem paths, probe
  output, converter logs, credentials, or identifying metadata.
- Preserve localhost bindings, proxy assumptions, diagnostics restrictions,
  retention controls, and other documented safe defaults.

### Packaging and release safety

- Preserve digest pins, Linux/amd64 support boundaries, non-root execution, and
  the separation between Go application builds and media-runtime builds.
- Treat direct Release workflow dispatches as dry runs. Publication must remain
  gated by the documented tag or Create release paths.
- Do not allow cache hits, retries, manual inputs, or skipped checks to bypass
  validation, environment approval, immutable artifact checks, licence review,
  corresponding-source preparation, or package publication gates.
- Require renewed review when dependencies, native recipes, notices, source
  identities, runtime digests, or redistribution evidence change.
- Require third-party actions to remain pinned to full commit SHAs and workflow
  permissions to remain at the minimum needed scope.
- Do not interpolate pull request titles, branch names, issue text, or other
  attacker-controlled GitHub context directly into shell scripts. Pass values
  through environment variables and quote their use.

### Tests, fixtures, and documentation

- Require focused regression coverage for changed behavior and failure paths.
- A skipped converter, codec, browser, container, or hardware test is not proof
  that the affected path works. A passing test against Ubuntu's packaged FFmpeg
  or libvips also does not establish behavior against Filetwist's pinned media
  runtime when a change depends on codec, feature, or tool version.
- Require the container smoke checks for media-runtime-sensitive behavior and a
  real supported device run for VA-API behavior.
- Use only small synthetic, generated, public-domain, or explicitly
  redistributable fixtures. Preserve `fixtures/manifest.json` provenance and
  licence data.
- Require user-facing documentation updates when behavior, configuration,
  limitations, deployment, or release procedures change.

## Comment threshold

Leave a review comment only when all of these are true:

- The pull request introduces or exposes a specific defect, regression, security
  or privacy risk, data-loss path, contract violation, or release-safety failure.
- The issue is supported by the diff and repository behavior, not speculation
  about an unobserved environment.
- The author can act on the comment.
- The comment identifies the triggering condition and its concrete impact.

Do not report formatting preferences, naming opinions, broad refactoring ideas,
or pre-existing problems unrelated to the pull request. Do not demand guarantees
the project explicitly disclaims, such as perfect visual fidelity, removal of
all opaque metadata, smaller output from every smaller profile, universal device
playback, arm64 images, or VA-API support without a real device check.

## Writing review comments

- Comment on the narrowest changed line that demonstrates the problem.
- Start with the consequence, then explain the condition that reaches it.
- Cite the relevant profile, lifecycle rule, test expectation, or release gate.
- Use GitHub's High, Medium, or Low severity based on impact and likelihood.
- Provide a concise fix direction. Add a suggested change only when it is locally
  correct and does not hide required error handling or validation.
- Report separate root causes separately. Do not split one root cause into
  multiple comments.

If no issue meets this threshold, submit the review without inventing a comment.

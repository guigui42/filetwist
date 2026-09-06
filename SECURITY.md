# Security policy

## Reporting a vulnerability

Do not report vulnerabilities in public issues or attach private media, raw
metadata, access tokens, or original filenames.

On the project's published GitHub repository, use **Security > Report a
vulnerability** to send a private report. Include the affected version or
commit, deployment mode, impact, and minimal reproduction using synthetic
files. Share only what is needed to reproduce the problem.

Private reporting must be enabled by the repository maintainer. If the button
is unavailable, open an issue asking for a private reporting channel **without
technical details or sensitive attachments**. Do not assume that a public
compatibility report is confidential.

## Supported versions

During the source preview, security fixes target the current `main` branch.
There are no supported older release branches or guaranteed response times.
Versioned releases must state their support status in their release notes.

## Deployment boundary

Filetwist is a shared household workspace, not a public conversion service.
Anyone with access can view, download, cancel, and delete its jobs. There is
no built-in authentication or TLS. Use an authenticated HTTPS reverse proxy
for access beyond a trusted local network, and block direct access to the
backend. Compose binds to loopback by default.

File size limits, command deadlines, and non-root containers are not a sandbox
for hostile media. Keep the application and its FFmpeg, libvips, codec, and
driver dependencies updated. Metadata removal is not a guarantee of anonymity.

See [deployment guidance](docs/packaging.md) and
[the reverse-proxy example](docs/reverse-proxy.md).

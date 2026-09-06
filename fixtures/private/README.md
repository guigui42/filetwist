# Private compatibility fixtures

Place private phone-media fixtures and their private manifest in this
directory. Everything here except this README is gitignored.

1. Copy the capture into this directory with a neutral filename such as
   `iphone-heic-orientation.heic` or `iphone-spatial-audio.mov`.
2. Remove unrelated photos, contacts, account names, location names, and other
   personal context from notes and filenames. Do not edit the source media
   before the privacy probe because metadata behavior is part of the test.
3. Inspect metadata locally with `vipsheader -a` for images and
   `ffprobe -show_streams -show_format -print_format json` for audio or video.
   Store probe output only in this gitignored directory.
4. Add the case to a private manifest using schema version `1.1`,
   `representation: media`, `source.category: private_capture`,
   `source.privacy: private`, and `source.redistribution: prohibited`.
5. Use neutral fixture IDs and descriptions. Never record a person's name,
   exact location, device owner, account identifier, or capture timestamp in
   the manifest or report.
6. Keep private reports under `reports/private/`, which is also gitignored.
7. Before any Git operation, run `git status --short` and confirm no private
   media, manifests, probes, or reports are staged. Never use `git add -f` for
   these paths.

Real-device gaps that belong here include recent iPhone HEIC with gain maps or
HDR metadata, iPhone MOV with an actual APAC spatial-audio stream, Live Photos,
slow-motion captures, phone-originated HDR video, and Android vendor-specific
camera output.

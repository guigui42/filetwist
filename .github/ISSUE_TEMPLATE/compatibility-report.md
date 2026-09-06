---
name: Compatibility report
about: Report a media conversion rejection, failure, or invalid output
title: "[compatibility] "
assignees: ""
---

## Operation

<!-- Example: compatible_video -->

## Expected result

<!-- Describe the output profile you expected. -->

## Actual result

<!-- Include the error code, warning, or privacy-safe observed output details. -->

## Environment

- Filetwist version or commit:
- Run mode: local CLI / Docker CPU / Docker VA-API / web
- Host architecture:
- FFmpeg version:
- libvips version:
- For VA-API only, GPU and driver:

## Reproduction

<!-- Give commands with local paths, hostnames, tokens, and IDs removed. -->

## Media safety checklist

- [ ] I have not attached private or copyrighted media without redistribution permission.
- [ ] I removed names, locations, account identifiers, capture timestamps, local paths, and private URLs.
- [ ] I did not include raw metadata or probe output that contains personal information.
- [ ] If a public fixture is attached, its source and redistribution license are documented.

Do not upload private phone media to the issue. If the file cannot be shared,
provide only neutral aggregate properties such as media kind, codec, stream
count, dimensions, rotation, HDR transfer, and the error code.

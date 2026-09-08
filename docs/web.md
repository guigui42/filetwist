# Web workflow and conversion behavior

Use **Media intake** to upload files, review their detected type and recommended
operation, then select **Convert**. The job path shows **Inspect**, **Configure**,
**Convert**, **Validate**, and **Download** as the work progresses. Each file is
presented as a work order that separates the original, selected profile, and
output.

Processing warnings appear before the output-validation result so consequences
that need attention are visible before a successful validation seal. Probe and
execution information stays optional under **Conversion details**.
Download successful results individually, as a streamed ZIP, or start every
successful output as a separate browser download.

The upload form identifies this as a shared workspace before you send any files.
It checks file-count and total-size limits before sending, while the server
remains authoritative. **Cancel upload** is available while bytes are being sent
and keeps the selected files available to retry. Once the upload is sent, the
control disappears while the server finishes inspecting the files. Inspection
continues even if the browser disconnects after the uploaded bytes are stored.
The built-in **Help & file handling** page (`/help`, under `WEBROOT` when set)
explains access, retention, conversion tradeoffs, and recovery without requiring
an external documentation service.

After upload, review takes focus and the uploader collapses to **Upload more
files**. The browser URL points to the job so it can be revisited. Each preset
shows its output format and a short explanation before conversion. Changing an
individual or batch preset updates the output-format preview immediately.
Warnings and errors stay visible. Successful jobs put **Download all as ZIP**
before the per-file results. For every successful job,
**Download all files** starts each output as a separate browser download. The
same action is available for a single output, without requiring a ZIP. The
browser may ask you to allow multiple downloads for the site; this permission
cannot be granted by Filetwist. Individual links and ZIP downloads remain
available if the browser blocks the batch.

For batches, open **Apply a preset to several files** and explicitly apply a
preset to eligible files. **Keep individual choices** is enabled by default and
preserves presets changed separately. Ineligible files are unchanged, and the
interface reports how many choices were applied or kept. Each file can still
be adjusted afterward; the server validates every operation again at start.

You can remove a selected file before upload or an uploaded file while its job
is still pending. Removing an uploaded file deletes its stored input; removing
the last file deletes the empty job. Once conversion starts, removal is
unavailable: use Cancel or Delete job instead. A failed remove request preserves
the current review controls, and remaining preset choices survive successful
removal in the browser.

Operation choices reflect the detected streams and image properties. For
example, silent video does not offer audio extraction, and unsupported
high-precision audio does not offer lossless FLAC. If the usual recommendation
is unavailable, the web form recommends an eligible alternative, which can be
audio extraction when a video's picture cannot be converted. Review that
selection before choosing **Convert**. Inputs with no eligible operation are
rejected during inspection.

Eligibility is a content check, not a guarantee that runtime codecs or hardware
will work. Functional converter and acceleration checks still run at conversion
time.

## Shared jobs

There are no user accounts or per-user permissions. Everyone with access to the
service shares the same jobs, including download, cancel, and delete controls.
Random job IDs are not an authentication mechanism. Proxy authentication
restricts who can enter this shared workspace; it does not isolate households
or users from each other.

Jobs retain uploaded originals, outputs, and a manifest under `DATA_DIR`.
The default retention is 24 hours with cleanup every five minutes. Active work
and downloads postpone removal. Cancel stops queued/running work but does not
delete stored files. Delete cancels work and removes the job; it can refuse
while a download still holds the job open. Retry deletion after that download
finishes.

A restart marks unfinished jobs interrupted; they do not resume automatically.
Upload the original files again to start a new job, or delete the interrupted
job. Keep separate original copies and download wanted results before expiry.

## Conversion profiles

| Operation | Profile and important changes |
| --- | --- |
| `compatible_photo` | JPEG quality 90, progressive; applies orientation and flattens alpha onto white |
| `smaller_photo` | WebP quality 80; preserves supported transparency |
| `lossless_image` | PNG; preserves supported alpha and decoded 8-bit/16-bit sample precision, but orientation/color normalization and metadata removal still apply |
| `compatible_video` | Fast-start MP4, H.264 `yuv420p`, normalized rotation and even dimensions; selected audio becomes stereo 48 kHz AAC |
| `smaller_video` | Same container/codecs, fitted within 1280 x 720 with stronger compression |
| `extract_audio` | Re-encodes the selected audio track to stereo 48 kHz AAC in fast-start M4A; not a bit-for-bit extraction |
| `compatible_audio` | Re-encodes to stereo 48 kHz MP3 at 192 kb/s |
| `lossless_audio` | FLAC preserving the selected track's channel count and sample rate; rejects floating-point or greater-than-24-bit PCM |

"Smaller" is a profile choice, not a guaranteed size reduction. PNG and FLAC do
not restore information already lost in the source, and these modes are not
original-file preservation.

Video keeps the first usable video stream and first supported audio stream.
Unsupported/additional audio tracks produce warnings. Subtitles, attachments,
data streams, and chapters are dropped. Unsupported audio is not replaced with
silence: video can succeed without audio, while an audio-only operation rejects
an input with no supported audio track.

Supported PQ/HLG HDR video is tone-mapped to BT.709 SDR. Unsupported HDR color
signaling is rejected; Dolby Vision metadata and the original HDR presentation
are not preserved. Listen to and inspect important conversions.

## Images and metadata

Image support depends on the installed libvips loaders. HEIF/HEIC and AVIF
decoding are checked independently with real decode probes. Animated and
multipage images are rejected. HDR still-image tone mapping is unsupported;
PQ/HLG images, unsupported CICP color signaling, and ambiguous high-bit-depth
HEIF/AVIF inputs are rejected rather than assumed to be SDR.

Some color/alpha representations are unsupported, including unprofiled CMYK,
floating-point images, and high-precision alpha through certain color
transforms. The default decoded-image limits are 32,768 pixels per axis and
100 million pixels total; WebP output also requires each axis at most 16,383.

Lossless PNG normalizes CMYK and ICC-profiled grayscale to sRGB at the input
sample depth. Unexpected output precision is rejected before publication.
This is not bit-for-bit archival preservation: color normalization still
changes samples, and preserving 16-bit alpha through color normalization
remains unsupported.

Image profiles strip EXIF (including capture dates), GPS, XMP, and gain-map
metadata while retaining supported ICC color profiles. Media profiles strip
container/stream metadata and chapters. These are profile policies and checks
for known probe-visible fields, **not a guarantee that all identifying data is
removed**. Content, filenames, retained profiles, or opaque payloads can still
contain information you should not share.

Output validation checks declared properties such as format, codec, streams,
dimensions, orientation, audio layout, duration when known, and applicable
color/metadata/fast-start requirements. It does not prove visual fidelity,
absence of every artifact, or playback on every phone.

## Operational limits

Filetwist handles images, audio, and video, not documents, PDF, OCR, arbitrary
converter plugins, or multi-tenant hosting. Available codecs and hardware
depend on the deployed runtime. Resource limits reduce risk but are not a
sandbox for hostile media or untrusted users. Put authentication and TLS at the
[reverse proxy](reverse-proxy.md), and keep the backend inaccessible directly.

The [deployment guide](packaging.md) covers upload limits, timeouts, storage,
CPU/VA-API selection, and platform support.

## Health, diagnostics, and assets

`GET /healthz` returns health JSON at the server root. `/diagnostics` follows
`WEBROOT` and reports tool availability, queue/storage information, and
acceleration status. `DIAGNOSTICS=local` limits it to loopback clients,
`disabled` removes it, and `enabled` exposes it to everyone with access.
The [proxy guide](reverse-proxy.md#diagnostics-and-forwarded-addresses) explains
trusted client-address handling.

Templates, CSS, JavaScript, and [HTMX](../internal/web/static/htmx.min.js) are
embedded in the Go server. There is no frontend build step or runtime CDN
dependency. The [HTMX license](../internal/web/static/htmx.LICENSE) must remain
with the vendored asset.

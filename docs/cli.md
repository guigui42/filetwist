# CLI

The CLI converts one local file at a time. It detects content rather than
trusting its extension, selects or checks a named operation, and makes the
output available only after profile validation.

## Build and use

From the repository root, with Go 1.27 installed:

```sh
go build -o ./bin/filetwist ./cmd/filetwist
./bin/filetwist --output ./converted ./photo.heic
./bin/filetwist --output ./clip.mp4 --operation compatible_video ./source.mov
./bin/filetwist --human -o ./audio --operation extract_audio ./source.mov
./bin/filetwist --version
```

The runtime needs `ffmpeg`, `ffprobe`, `vips`, and `vipsheader` on `PATH`.
Available formats depend on their codec support; the
[container build](packaging.md) supplies the supported runtime stack.

```text
filetwist --output PATH [--operation NAME] [--human | --json] INPUT
```

Put flags before `INPUT`. Without `--operation`, the CLI recommends
`compatible_photo`, `compatible_video`, or `compatible_audio` for the detected
kind. If that default cannot accept the content, the CLI rejects the input
rather than silently substituting another operation, such as audio extraction
for a video. Use `--operation` to select an eligible alternative explicitly.
See [all operations and their limitations](web.md#conversion-profiles).

`--output` (or `-o`) accepts an existing directory, a new extensionless directory
path, or a file path with the selected operation's extension. It never
overwrites an existing file. Directory output names use the input stem with
these suffixes:

| Operation | Suffix |
| --- | --- |
| `compatible_photo` | `-compatible.jpg` |
| `smaller_photo` | `-smaller.webp` |
| `lossless_image` | `-lossless.png` |
| `compatible_video` | `-compatible.mp4` |
| `smaller_video` | `-smaller.mp4` |
| `extract_audio` | `-audio.m4a` |
| `compatible_audio` | `-compatible.mp3` |
| `lossless_audio` | `-lossless.flac` |

To use the locally built container image instead:

```sh
docker run --rm --platform linux/amd64 \
  -v "$PWD/input:/input:ro" \
  -v "$PWD/output:/data" \
  filetwist:local convert --output /data /input/photo.heic
```

Create `input` and `output` first, place the source in `input`, and make `output`
writable by container UID/GID 10001. Do not mount a private media library when
a single input directory is enough.

## Acceleration

```sh
ACCELERATION=cpu ./bin/filetwist -o ./converted ./clip.mov
ACCELERATION=auto ./bin/filetwist -o ./converted ./clip.mov
ACCELERATION=vaapi VAAPI_DEVICE=/dev/dri/renderD128 \
  ./bin/filetwist -o ./converted ./clip.mov
```

`auto` is the default. Video uses Intel VA-API only after a functional probe;
otherwise CPU selection is reported. `vaapi` rejects video conversion if that
probe fails. Recognized hardware execution failures can retry once on CPU;
the result records the path and fallback reason. Images and audio do not use
VA-API. See [hardware requirements](packaging.md#intel-va-api).

The standalone CLI uses a 10-minute command timeout and 30-second input probe
timeout. Server settings such as `JOB_TTL`, `DATA_DIR`, and `COMMAND_TIMEOUT`
do not configure the standalone CLI.

## Results and exit codes

JSON on standard output is the default; `--human` selects terminal text.
The [result type](../internal/conversion/types.go) defines schema version `1.0`.
Important fields are `status`, `detected_media`, `selected_operation`,
`output_path`, `validation`, `warnings`, `execution`, `timing`, and, when
available, output properties in `observed`. Failures include
`error.kind`, `error.code`, and `error.message`.

Treat `warnings` as an array, ignore unknown fields, and do not depend on
measured timings. Results omit raw probe metadata and converter output but
include `output_path`; redact paths before sharing them.

| Exit code | Meaning |
| --- | --- |
| `0` | Conversion succeeded and passed profile validation |
| `2` | Input, operation, or existing output rejected |
| `3` | Invalid arguments, environment, path, or converter configuration |
| `4` | Input or output probing failed |
| `5` | Conversion, timeout, or output publication failed |
| `6` | Output failed profile validation |
| `7` | Canceled by `SIGINT` or `SIGTERM` |

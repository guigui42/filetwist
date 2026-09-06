# Compatibility fixtures

`manifest.json` uses schema version `1.1` from `internal/corpus`. Paths are
relative to this directory.

## Public corpus

The generator creates 17 small, synthetic, CC0-1.0 fixtures:

| Area | Cases |
| --- | --- |
| Images | transparent PNG, opaque odd-width JPEG, EXIF-rotated JPEG with synthetic GPS tags, animated GIF rejection |
| Audio | PCM WAV, FLAC, MP3, Opus, and AAC in M4A |
| Video | H.264/AAC MP4, silent H.264 MP4, rotated MP4, odd-dimension H.264 MKV, MKV with an extra subtitle stream, VP9/Opus WebM, HEVC/AAC MP4 |
| Simulated probe | iPhone-like HEVC + AAC + unsupported APAC, attached picture, and data stream |

Every generated file is capped at 64 KiB. The H.264 and HEVC yuv420p cases are
suitable inputs for comparing CPU conversion with a functionally available
VA-API path. The APAC case is explicitly marked `ffprobe_json`; it is not a
claim that FFmpeg generated APAC media.

## Generation

Requirements:

- Go matching `go.mod`;
- `ffmpeg` and `ffprobe` on `PATH`;
- FFmpeg encoders `libx264`, `libx265`, `libvpx-vp9`, `libmp3lame`,
  `libopus`, `aac`, and `flac`.

Regenerate the public corpus and manifest from the repository root:

```sh
go run ./fixtures/generate.go
```

Verify the committed corpus:

```sh
go run ./fixtures/generate.go -check
```

The standard-library images, WAV, ffprobe JSON, and manifest are byte-stable.
FFmpeg is run with fixed synthetic sources, metadata stripping, bitexact muxer
flags, and single-threaded video encoders. Repeated generation with one
toolchain must be byte-identical. Encoded bytes can still change across FFmpeg
or external codec-library versions, so the check compares committed FFmpeg
files by normalized codec, stream, dimensions, pixel format, channel, sample
rate, duration, rotation, and container properties.

## Adding a public fixture

1. Generate it locally or verify its redistribution license and remove all
   personal metadata.
2. Keep it small and place it under `generated/` (or a clearly licensed public
   directory).
3. Add its source, representation, input probe characteristics, named
   operation, expected result, tolerances, and compatibility traits to the
   generated manifest definition in `generate.go`.
4. Run `go run ./fixtures/generate.go`, then
   `go run ./fixtures/generate.go -check`.
5. Run `go test -race ./...`. Corpus tests validate manifest paths,
   redistribution metadata, the 64 KiB ceiling, actual ffprobe properties, and
   generator reproducibility when FFmpeg is available.

Private phone captures follow the separate workflow in
[`private/README.md`](private/README.md) and must never be added to the public
manifest.

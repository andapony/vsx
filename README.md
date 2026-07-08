# vsx

`vsx` extracts audio from Roland VS-series storage media — VS-1880 (VR5) and
VS-880EX (VR9) live-disk HDD images and CD "Song Copy Archive" dumps — into
faithful mono WAV files. Point it at a single **Source** and it identifies the
format, walks the structure, and writes one WAV per populated v-track at the
song's native sample rate and bit depth.

See [`CONTEXT.md`](./CONTEXT.md) for the project's coined vocabulary and
[`ROLAND-VS-FORMAT-SPEC.md`](./ROLAND-VS-FORMAT-SPEC.md) for the authoritative
format specification. Design decisions are recorded in [`docs/adr/`](./docs/adr).

> **Status:** extraction works end-to-end for both machines (VS-1880/VR5 and
> VS-880EX/VR9) across all supported Sources — HDD live-disk images, single-disc
> CD archives, and multi-disc CD backup sets (`§5.6` spanning) — with best-effort
> and `--strict` modes, in-context deviation reporting, and optional `--stereo`
> pairing (`§8.4`). Deferred: the HDD↔CD byte-identical cross-check (`§5.7`, a
> ready skipped test slot pending matching media) and the JSON `--report` output
> (v1.1).

## Build

Pure Go, no cgo — one static binary per platform:

```sh
CGO_ENABLED=0 go build -o vsx ./cmd/vsx
```

## Test

```sh
go test ./...
```

Media-dependent tests read real images from the directory named by
`VSX_TEST_MEDIA` and skip when it is unset (see
[ADR-0005](./docs/adr/0005-verification-strategy.md)); the corpus lives outside
the repository. CI runs the media-independent suite.

## Layout

| Path | What |
|---|---|
| `cmd/vsx` | CLI: argument parsing, the stdout-manifest / stderr-diagnostics split, exit codes |
| `internal/core` | the `Extract` façade, detection, per-machine extractors, timeline building, `Result`/`Deviation` types, and the `Decoder` seam |
| `internal/cd` | CD "Song Copy Archive" reader: frame/user-data geometry, header-block walk, multi-disc spanning |
| `internal/hdd` | HDD live-disk reader: Roland MBR, byte-swapped FAT16, directory/take resolution |
| `internal/wav` | `wav.Encode`/`wav.EncodeStereo` — PCM to RIFF/WAVE bytes |
| `internal/rdac` | the vendored golden RDAC codec (see below) |
| `internal/testutil` | media-skip and golden-master PCM-hash test helpers |

## Licensing

`vsx` is licensed **LGPL-3.0-or-later** ([ADR-0006](./docs/adr/0006-distribution-and-licensing.md)),
uniformly across its own code and the vendored `rdac` codec. The RDAC decoder
in `internal/rdac/` is a vendored derivative of Randy Gordon's
[`rdac`](https://github.com/randygordon/rdac)
([ADR-0004](./docs/adr/0004-codec-vendored-golden-rdac.md)); its provenance and
license notices are preserved in that directory alongside `COPYING` and
`COPYING.LESSER`.

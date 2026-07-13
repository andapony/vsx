# vsx

`vsx` extracts audio from Roland VS-series storage media — VS-1880 (VR5) and
VS-880EX (VR9) live-disk HDD images and CD "Song Copy Archive" dumps — into
faithful mono WAV files. Point it at a single **Source** and it identifies the
format, walks the structure, and writes one WAV per populated v-track at the
song's native sample rate and bit depth.

See [`CONTEXT.md`](./CONTEXT.md) for the project's coined vocabulary and
[`ROLAND-VS-FORMAT-SPEC.md`](./ROLAND-VS-FORMAT-SPEC.md) for the authoritative
format specification. Design decisions are recorded in [`docs/adr/`](./docs/adr).

Need to make the images first? See
[`docs/obtaining-images.md`](./docs/obtaining-images.md) for how to dump VS HDDs
and CD archives raw — and how to tell whether a rip is trustworthy (the ripping
chain won't tell you on its own).

> **Status:** extraction works end-to-end for both machines (VS-1880/VR5 and
> VS-880EX/VR9) across all supported Sources — HDD live-disk images, single-disc
> CD archives, and multi-disc CD backup sets (`§5.6` spanning) — with best-effort
> and `--strict` modes, in-context deviation reporting, per-frame MODE1 EDC
> corruption detection (`§10`), and optional `--stereo` pairing (`§8.4`).
> Deferred: the HDD↔CD byte-identical cross-check (`§5.7`, a
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

## Listing and extracting one song

`--list` prints the source's song catalog and exits without extracting
anything: a tab-separated data row per song on stdout, with a header line
(`KEY  SONG#  MACHINE  VTRK  LENGTH  CREATED  SAVED  NAME`) and any enumeration
deviations on stderr.

```sh
vsx --list image.hdd
```

`--song KEY` extracts just the given song instead of the whole source; repeat
the flag or comma-separate keys to pull several:

```sh
vsx --song 2.7 image.hdd
vsx --song 2.7 --song 3.1 image.hdd
```

A source is an HDD image, a single CD dump, a directory of one backup set's
disc dumps, **or several disc files named together** — the last is handy for a
multi-disc set you haven't put in a folder. The same `--list`/`--song` work on
all of them; multi-disc discs are grouped by set ID and ordered by disc index,
so filename and argument order don't matter:

```sh
vsx --list vs-cd-4a.bin vs-cd-4b.bin
vsx --song 12 -o out vs-cd-4a.bin vs-cd-4b.bin
```

On HDD sources the output folder is named `PP.OOO - name` (partition number,
enumeration index within that partition — e.g. `07.017 - LBL 4_19`), because
the VS device's own song number is not unique across a multi-partition disk.
CD sources keep the existing `NN - name` folders, since the catalog number is
already unique there.

## Flags

| Flag | Effect |
|---|---|
| `--list` | print the source's song catalog to stdout and exit; no extraction |
| `--song KEY` | extract only the given song(s) by list key (repeatable, or comma-separated) |
| `-o DIR` | directory to write song folders into (default `.`) |
| `--stereo` | pair adjacent matched mono tracks into one interleaved stereo WAV (`§8.4` heuristic) |
| `--strict` | conformance gate: abort on the first deviation with no output |
| `--as TYPE` | force the source type when detection is ambiguous (`hdd`\|`cd`\|`vr9`\|`vr5`) |
| `-v` | verbose: log each extracted v-track to stderr |
| `-q` | quiet: suppress deviations and the run summary |

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

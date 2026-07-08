# Vendored RDAC decoder (Roland VS-series)

Go package decoding Roland's RDAC compressed audio — formats **MTP** (24-bit),
**MT1**, and **MT2** (16-bit) — as used by VS-series digital studio
workstations (VS-880/880EX, VS-1680, VS-1880, …).

This directory is a self-contained vendor drop: copy `rdac/` into your
project (e.g. `internal/rdac/`) and keep `COPYING.LESSER`, `COPYING`, and
this file with it.

## Provenance

- **Reference decoder:** Randy Gordon's **rdac**
  (<https://github.com/randygordon/rdac>), LGPL-3.0-or-later.
  Copyright (c) Randy Gordon (randy@integrand.com) — `rdac2wav` (C) 2006,
  `vs2reaper` (Go) 2017.
- `rdac/decoder.go` is a Go-to-Go port of `vs2reaper/decode/decode.go`
  (pattern LUT, bit-layout strings, unpackers, reconstruction primitives).
- `rdac/mt1mt2.go` adds the corrected MT1/MT2 block decoders and the
  MT2 cluster-page streaming layer (`DecodeMT2Cluster`), structure from the
  Go reference, constants from the C reference (`rdac2wav/src/decode.c`).
- **Validation:** output verified sample-exact against the compiled C
  reference on real VS-880EX/VS-1880 media (2026-07-05); the algorithm is
  documented independently in `ROLAND-VS-FORMAT-SPEC.md` Appendix A (the
  vsx project), which was machine-verified against this package.
- Port and modifications: Rob Duncan, 2025–2026.

## License

**LGPL-3.0-or-later**, inherited from the reference decoder. This package is
a derivative work of Randy Gordon's decoder; if you modify these files, keep
the headers and note your changes. `COPYING.LESSER` (LGPLv3) supplements
`COPYING` (GPLv3) — ship both with source distributions. Using this package
from a differently-licensed application is expressly permitted by the LGPL;
if you distribute static binaries (any normal Go build), the LGPL's
relinking requirement is satisfied by making the application source
available.

## API

| Function | Input | Output |
|---|---|---|
| `DecodeMTP(data, sampleRate)` | 16-byte blocks | `[]int32` (24-bit range) |
| `DecodeMT1(data, sampleRate)` | 16-byte blocks | `[]int16` |
| `DecodeMT2(data, sampleRate)` | 12-byte blocks, assumes 32 KB pages | `[]int16` |
| `DecodeMT2Cluster(data, clusterSize)` | 12-byte blocks + per-page padding | `[]int16` |

Each block decodes to 16 mono samples. The `sampleRate` parameters are
vestigial (unused — kept for API compatibility with the original vsx
callers; feel free to drop them when adapting).

## Integration notes (hard-won; see the format spec for details)

- **Byte order of input:** HDD FAT reads must be 16-bit byte-pair swapped
  *before* decoding (Roland subdirectory quirk); CD-R archive content is
  native and decodes directly. Spec §4.2 / §5.7.
- **MT2 page padding:** MT2's 12-byte block doesn't divide cluster pages;
  each 32 KB page holds 2730 blocks + 8 pad bytes. `DecodeMT2Cluster`
  handles this — pass the BPB cluster size on HDD, `32768` on CD. The pad
  bytes are **not zeros** on real media (write-buffer garbage); never
  validate them. Spec §2.
- **64 KB clusters (open question):** the C reference hardcodes 5460 blocks
  + 16 pad; the author's own Go implies `floor(65536/12)` = 5461 + 4, which
  is what `DecodeMT2Cluster` computes. No 64 KB MT2 media has been available
  to adjudicate. Spec §12.
- **Decoder state:** the only inter-block state is the previous block's
  final sample (`d0`), carried inside the streaming functions. Concatenate a
  take's clusters in chain order and decode as one stream.
- **Take streams have no header** and commonly begin with one silent block —
  decode it, don't skip it. Spec §2.
- **M16/M24** (uncompressed) are deliberately not here: they're plain
  little-endian 16/24-bit PCM, no decoder needed. Spec §A.8.
- Sample-rate byte (SONG file byte 18, low nibble): 0 = 48 kHz,
  1 = 44.1 kHz, 2 = 32 kHz. Spec §3.

## Smoke test

`rdac/smoke_test.go` checks stream-shape invariants (block/sample counts,
page-pad consumption, determinism). It is not a golden-output test; for
byte-exact validation, compare against compiled `rdac2wav` output on a real
take, as the vsx project did.

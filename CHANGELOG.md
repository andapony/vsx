# Changelog

All notable changes to `vsx` are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-07-11

First stable release. `vsx` extracts audio from Roland VS-series storage media —
VS-1880 (VR5) and VS-880EX (VR9) HDD live-disk images and CD "Song Copy Archive"
dumps — into per-v-track WAV files. It reads the media structurally against
`ROLAND-VS-FORMAT-SPEC.md` and reports every deviation rather than failing:
single-disc and multi-disc CD backup sets (§5.6 spanning) and HDD live disks, for
both machines, in best-effort and `--strict` modes, with `--list`, `--song`
selection, and optional `--stereo` pairing (§8.4).

### Added

- Per-frame MODE1 EDC corruption detection (§10): a raw sector whose stored EDC
  disagrees with its bytes is reported by frame (or contiguous frame run), naming
  the physically corrupt sector the ripping chain otherwise passes through
  silently.
- `--version`, printing the version stamped into the binary from Go build info.
- Top-level `LICENSE` with an SPDX `LGPL-3.0-or-later` marker, alongside the
  existing `COPYING` / `COPYING.LESSER`.

### Fixed

- Multi-disc sets: a non-terminal disc that is a truncated rip with no trailing
  TDI filler run (§10) no longer hides every following disc's songs (#31). The
  missing-filler fallback is rounded down to a block boundary so later discs stay
  on the chain-walk grid, and the exact junction is reconstructed from the
  continuation disc (§5.6) — recovering the spanning file too, not just the
  independent songs on later discs.

### Changed

- Substantial internal restructuring toward one machine-neutral pipeline: a single
  shared CD chain walk behind a per-machine layout, the machine format sealed
  behind a timeline reduction, core driven from bytes behind an injectable
  `Decoder`, and one shared song prologue for `--list` and extract so the two
  agree by construction (#24, #27).
- Documentation and code comments audited against the code for correctness and
  consistency.

### Deferred

- HDD↔CD byte-identical cross-check (§5.7): a ready, skipped test slot pending
  paired media.
- JSON `--report` output: planned for v1.1.

## [0.1.0] - 2026-07-09

Initial tagged pre-release: the extraction foundation for both machines,
including accepting multiple disc files as one CD backup set.

[1.0.0]: https://github.com/andapony/vsx/releases/tag/v1.0.0
[0.1.0]: https://github.com/andapony/vsx/releases/tag/v0.1.0

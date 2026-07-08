# vsx

`vsx` is a from-scratch Go tool that extracts audio from Roland VS-series storage
media (VS-1880 / VS-880EX HDD images and CD "Song Copy Archive" dumps) into WAV
files. See [ADR-0001](./docs/adr/0001-from-scratch-go-spec-authoritative.md).

The **Roland VS storage format** vocabulary — *song*, *take*, *v-track*, *event
record*, *timeline*, *RDAC*, *header block*, *backup set*, *spanning*, and the
codec names (*MTP*, *MT2*, *MT1*, *M16*, *M24*) — is defined authoritatively in
[`ROLAND-VS-FORMAT-SPEC.md`](./ROLAND-VS-FORMAT-SPEC.md). `vsx` uses those terms
exactly as the spec defines them; this glossary covers only the vocabulary the
project itself coins on top of the spec.

## Language

**Source**:
An input to `vsx`, identified by its bytes rather than declared: either an HDD
live-disk image or a CD Song Copy Archive dump (one or more disc files forming a
backup set).
_Avoid_: disk, drive, file (all ambiguous — a "source" may be many files).

**Deviation**:
Any respect in which input media departs from `ROLAND-VS-FORMAT-SPEC.md` —
missing takes, an unknown field value, a cooked or truncated rip, a corrupt FAT
chain, a degenerate record. `vsx` reports deviations rather than failing on them.
_Avoid_: error, corruption (a deviation is not necessarily either).

**Best-effort mode**:
The default posture: report every deviation, continue with a documented
guess/default, write all recoverable audio, and exit non-zero if any deviation
occurred. See [ADR-0002](./docs/adr/0002-best-effort-and-strict-modes.md).

**Strict mode**:
The `--strict` posture: a pass/fail conformance gate that aborts the whole run
with no output on the first deviation. Answers "is this a spec-clean image?"
rather than "what audio can I recover?"
_Avoid_: validate mode, check mode.

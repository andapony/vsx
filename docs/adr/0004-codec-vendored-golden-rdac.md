# The RDAC codec is the vendored golden implementation, not reimplemented

The RDAC audio codec — the compressed VS formats MTP, MT2, and MT1 — is **not**
written from scratch. `vsx` **vendors** Randy Gordon's `rdac` decoder as the
golden implementation, carried as a pure-Go, I/O-free port of his reference
decoder in `internal/rdac` and called behind a narrow `Decoder` interface in our
core. (The uncompressed formats M16 and M24 are not RDAC at all: core unpacks
them directly as little-endian PCM.)

This is the deliberate exception to ADR-0001. For the codec, the **golden
implementation is authoritative**, not the spec: `ROLAND-VS-FORMAT-SPEC.md`
Appendix A is *derived from* this same code, so spec and codec are one source,
not two independent ones — the spec cannot serve as an independent check on the
codec. Rather than re-derive the codec from the spec we carry the golden code as
a Go-to-Go port, validated sample-exact against the reference implementation on
real VS media (ADR-0005); the transcription is guarded by that validation and by
characterization tests, not assumed error-free.

## Consequences

- **Pure Go → single static binary.** No cgo, so `vsx` stays trivially
  cross-compilable and distributable as one file.
- **Codec seam is a narrow Go `Decoder` interface**, so the codec's provenance
  is invisible to the rest of the pipeline and the vendored port can be updated
  in isolation.
- **Verification of the codec is deferred to provenance** (ADR-0005): trusted
  because it *is* the golden code, checked only by light characterization tests
  that guard the repackaging/wiring, not by comprehensive per-pattern fixtures.
- **Licensing:** the vendored `rdac` is LGPL-3.0-or-later; `vsx`'s distribution
  must honour that for the vendored component. Final licensing posture is
  settled in the distribution ADR ([ADR-0006](./0006-distribution-and-licensing.md)).

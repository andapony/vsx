# The RDAC codec is the vendored golden implementation, not reimplemented

The RDAC audio codec (MTP/MT2/MT1/M16/M24 decoding) is **not** written from
scratch. `vsx` **vendors** a pure-Go module — Randy Gordon's `rdac` decoder,
repackaged by us from his application into a standalone importable module — and
calls it behind a narrow `Decoder` interface in our core.

This is the deliberate exception to ADR-0001. For the codec, the **golden
implementation is authoritative**, not the spec: `ROLAND-VS-FORMAT-SPEC.md`
Appendix A is *derived from* this same code, so spec and codec are one source,
not two independent ones — the spec cannot serve as an independent check on the
codec. We reuse the golden code directly instead of transcribing it, which
eliminates the transcription-error risk entirely.

## Consequences

- **Pure Go → single static binary.** No cgo, so `vsx` stays trivially
  cross-compilable and distributable as one file.
- **Codec seam is a narrow Go `Decoder` interface**, so the codec's provenance
  is invisible to the rest of the pipeline and the vendored module can be
  updated in isolation.
- **Verification of the codec is deferred to provenance** (ADR-0005): trusted
  because it *is* the golden code, checked only by light characterization tests
  that guard the repackaging/wiring, not by comprehensive per-pattern fixtures.
- **Licensing:** the vendored `rdac` is LGPL-3.0-or-later; `vsx`'s distribution
  must honour that for the vendored component. Final licensing posture is
  settled in the distribution ADR (forthcoming).

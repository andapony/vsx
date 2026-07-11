# From-scratch Go implementation; the spec is authoritative

`vsx` is built from scratch in Go as a full-pipeline extractor implementing
`ROLAND-VS-FORMAT-SPEC.md`, which is the **sole authority** for the format.

The reference implementations the spec cites in §11 (Randy Gordon's `rdac` Go
port, Kyle J. Smith's VS Wave Export, and the `analysis/tools/*.py` scripts)
are treated as **untrusted prior art** — possibly incomplete or wrong — and are
**not consulted, even for color**. Ground truth for correctness is real media
(the ripped disk images and CD dumps) and the spec's own stated invariants,
never the prior code.

**Scope note:** this from-scratch, spec-authoritative rule governs the
*format/structure* layers (MBR, FAT+swap, CD header-block walk, event records,
spanning, enumeration). The RDAC audio **codec is the deliberate exception** — it
is the vendored golden implementation, for which the golden code, not the spec,
is authoritative (Appendix A is merely derived from it). See
[ADR-0004](./0004-codec-vendored-golden-rdac.md).

## Considered Options

- **Port / consolidate the existing §11 code** — faster to something running,
  but inherits a two-language split and code whose correctness is unestablished.
- **From-scratch Go, prior art as untrusted reference** (chosen) — the spec was
  explicitly written to be sufficient for a third party to implement from
  scratch; a single-language build re-derived from the spec yields one coherent,
  verifiable codebase and avoids importing unverified behaviour.
- **Scope to a single slice** (codec-only, or CD-only) — rejected; the goal is
  the whole HDD+CD → WAV pipeline (see [ADR-0003](./0003-scope-sources-machines-one-source-per-run.md) scope).

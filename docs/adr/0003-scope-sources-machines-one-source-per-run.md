# Scope: both sources, both verified machines, one Source per execution

`vsx` targets exactly the spec's **verified surface**: HDD live-disk images and
CD "Song Copy Archive" dumps, for the **VS-1880 (VR5)** and **VS-880EX (VR9)**.
VR6/VS-1680 ("assumed VR5-like," unverified) and VR1/VS-2480 (out of scope in
the spec) are **excluded** — but machine-format handling sits behind a seam so
VR6 can be slotted in if media ever appears.

Each execution extracts exactly **one Source** — one HDD image, or one CD backup
set (its disc dumps auto-grouped by set ID and ordered by disc index) — and
extracts **all** of that Source's songs by default. Input type and machine are
auto-detected from the bytes, with an override escape hatch.

## Why

- The verified surface is the only surface we can hold to the spec (ADR-0001);
  the unverified machines have no ground truth to check against.
- Both sources converge on one shared core (event model → RDAC codec → WAV), so
  the second front-end is mostly just its reader, not a second pipeline.
- HDD↔CD cross-validation — the same song extracted from both sources being
  byte-identical — is a primary correctness oracle (see the verification ADR),
  and it only exists if both sources are in scope.

# Verification strategy: real-media invariants now, cross-check deferred

`vsx` is built test-first (red/green TDD). Because we deliberately refuse the
usual oracle — we do not consult the untrusted prior pipeline code (ADR-0001) —
correctness rests on a layered strategy rather than a diff against a reference:

1. **Structural invariants against real media.** The corpus has real HDD *and*
   real CD media (with **disjoint content** — no song exists on both). The
   spec's stated facts become assertions over each source independently: MBR
   offsets, FAT chain + Roland swap, the 288-entry V-track table, take-resolution
   counts, spanning byte-exactness, the TDI filler signature, timestamp
   day-of-week. This covers readers, enumeration, event parsing, and spanning
   now.
2. **Internal consistency checks.** The event record is redundant
   (`0x18` count vs `(0x16−0x14)+1`; take size vs `count × clusterBytes`;
   directory size vs `0x18 × clusterBytes`, §8.3). Asserting agreement catches
   take-resolution bugs with no external oracle.
3. **Codec: characterization only.** The codec is the vendored golden module
   (ADR-0004), trusted by provenance, so we write only light characterization
   tests (silent block → silence; a known take → stable golden-master hash) to
   guard the repackaging/wiring — not comprehensive per-pattern fixtures.
4. **Ear verification** — the interim end-to-end audio oracle, a manual
   checklist, exactly as the spec authors relied on for e.g. the 32 kHz songs.
5. **Golden-master snapshots** — once a human ear-blesses an extraction, its
   output hash (small, committable, referencing out-of-repo media) guards
   against regression. Locks in verified-correct output; does not by itself prove
   correctness.

## Deferred keystone: HDD↔CD cross-check

The strongest possible oracle — the same song extracted from HDD and from CD
being byte-identical (§5.7) — is **not available yet**, because no song in the
corpus exists on both sources. When matching archive sets are manufactured from
the HDD songs, this check drops in as the keystone. The harness is built with a
ready, skipped slot for it.

## Test-media logistics

Real images live **outside the repo**, located via an env var (e.g.
`VSX_TEST_MEDIA`); media-dependent tests **skip when it is absent**. The repo
never carries copyrighted audio or multi-GB blobs; only small synthetic fixtures
and golden-master hashes are committed. CI without media still runs the
media-independent tests.

## Residual risk

Until the cross-check exists, a *subtle* error in how we *wire* the codec or
resolve takes could survive both ears and golden-master (which would merely lock
it in). The consistency checks and, eventually, the cross-check are the defence.

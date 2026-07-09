# Recording sessions to close spec open questions

A field checklist for producing new VS-1880 / VS-880EX test media that targets
the open questions in [`ROLAND-VS-FORMAT-SPEC.md`](../ROLAND-VS-FORMAT-SPEC.md)
§12. Each session lists exactly what to set, record, edit, archive, and log —
take this to the machines.

Imaging and ripping procedure (dd for HDDs, `cdrdao --read-raw` for CDs, EDC
verification) is in [`obtaining-images.md`](./obtaining-images.md) and is not
repeated here; every "image the drive" / "rip the disc" step below means "per
that document".

## Before any session

**Kit:**

- [ ] Tone source with known, settable frequency (a phone/laptop signal-
      generator app into a recorder input is fine). You will use **250, 350,
      450, 550 Hz and a slow 100 Hz–5 kHz sweep**. The set 250/350/450/550 is
      chosen so no tone is an integer multiple of another — misrouted or
      pitch-shifted tracks are unambiguous.
- [ ] A microphone (or the tone source) for spoken slates.
- [ ] Blank CD-R media (CD-RW if the burner accepts it — several sessions burn
      2+ discs). Label pen.
- [ ] IDE-USB adapter + host with disk space, for HDD images.
- [ ] A **scratch HDD** for Session 4 — partition initialization erases it.
      Never initialize a drive that carries corpus material.
- [ ] A paper or phone log. Sessions 6 and 7 are useless without an ordered
      operation log — the whole point is correlating log records with a known
      edit history.

**Conventions used by every session:**

- **Slate every song**: before the test signal, record a few seconds of speech
  — song name, machine, sample rate, record mode ("Probe one, eight-eighty,
  forty-four one, M T two"). The slate survives into the WAV and makes any
  later mix-up self-identifying.
- **Song names must be unique across everything you record.** VR5 CD archives
  associate files to songs *by the 12-byte name alone* (spec §5.4) — two songs
  with the same first 12 characters in one set are ambiguous. The tables below
  assign each song an exact name; keep them.
- **Start the first take at timeline zero**: press ZERO, then record. On the
  VS-880EX this feeds the origin=12 question (§12) with every song you make.
- **Rate verification is by measurement, not by ear**: a 440 Hz slate tone
  extracted at the wrong rate shows up at 440 × (wrong/right) Hz — e.g. a
  32 kHz song misread as 44.1 kHz plays 440 Hz as ~606 Hz. Any spectrum view
  (Audacity → Analyze → Plot Spectrum, or `sox`) settles it.
- **Log the media**: which song went to which disc/set, in what order. Rip and
  image promptly; verify rips (EDC) before reusing or filing the discs.

**Fixed facts to work from** (spec §2/§3): sample-rate byte low nibble
0 = 48 kHz, 1 = 44.1 kHz, 2 = 32 kHz; format codes 0 = MT1, 1 = MT2, 3 = M16,
5 = MTP, 8 = M24; codes 2/4/6/7 unobserved. Rate and record mode are fixed at
New Song time and cannot be changed afterward.

---

## Session 0 — record-mode matrix probe (both machines)

**Question(s):** which panel record mode produces which format code (byte 19)
— the spec table maps codes to codec names but not to front-panel names; the
unobserved codes 2/4/6/7 (presumably the LIV family); whether any VS-1880 mode
produces M24 at all (gates Session 3); the roles of `SONG.VR5` +0x24 and the
4-byte version/flags pair, which need many SONG headers with varied parameters
to correlate against (§12).

**Why first:** ten minutes of recording per machine, one disc per machine, and
its results tell you which mode to use for the M24 session and whether LIV
media is worth pursuing.

**Procedure — VS-880EX:**

- [ ] Create one song **per record mode** the panel offers — the manual lists
      MAS, MT1, MT2, LIV, LV2 (confirm on the panel; create a song for
      anything else it offers). All at 44.1 kHz. Names and expectations:

      | Song name    | Mode | Expected code (unconfirmed) |
      |--------------|------|------------------------------|
      | `P8 MAS 441` | MAS  | 3 (M16)?                     |
      | `P8 MT1 441` | MT1  | 0                            |
      | `P8 MT2 441` | MT2  | 1                            |
      | `P8 LIV 441` | LIV  | 2/4/6/7 — unknown            |
      | `P8 LV2 441` | LV2  | 2/4/6/7 — unknown            |

- [ ] In each song: slate, then ~15 s of 440 Hz tone on track 1, starting at
      timeline zero.
- [ ] One extra song, `P8 VP 441`, mode MT2: engage **Vari-Pitch** before and
      during recording, record the same 15 s. This is the best available guess
      at what sets the mystery 0x40 bit in the rate byte (the single `0x41`
      observation, §12). A long shot — log the exact Vari-Pitch setting.
- [ ] Archive **all probe songs to one CD set** (same-set headers let the
      +0x164C tag and version/flags pair be compared across songs with
      everything else held equal).
- [ ] Rip.

**Procedure — VS-1880:** same shape. Modes per the manual: MTP, CDR, MAS, MT1,
MT2, LIV, LV1, LV2 (confirm on the panel).

| Song name    | Mode | Expected code (unconfirmed)         |
|--------------|------|--------------------------------------|
| `P1 MTP 441` | MTP  | 5                                    |
| `P1 CDR 441` | CDR  | 3 (M16)?                             |
| `P1 MAS 441` | MAS  | 8 (M24)? — this is the Session 3 gate |
| `P1 MT1 441` | MT1  | 0                                    |
| `P1 MT2 441` | MT2  | 1                                    |
| `P1 LIV 441` | LIV  | 2/4/6/7 — unknown                    |
| `P1 LV1 441` | LV1  | 2/4/6/7 — unknown                    |
| `P1 LV2 441` | LV2  | 2/4/6/7 — unknown                    |

- [ ] Slate + 15 s of 440 Hz per song, from zero; archive all to one set; rip.

**Analysis afterward:** read byte pair 18–19 from each song's catalog entry
and header block (`analysis/tools/vs_archive.py`); tabulate panel mode →
format code. Diff `SONG.VRx` headers across the probe songs for +0x24 and the
version/flags pair. For LIV-family songs, inspect the take streams: block
size, whether the 440 Hz tone's structure is visible, silent-block shape.

---

## Session 1 — sample-rate verification, VS-880EX

**Question(s):** `0x00` (48 kHz) media is completely unobserved; `0x02`
(32 kHz) extraction has never been rate-verified (the only corpus media is a
truncated rip); whether VR9 origin=12 holds across rates (§3, §12).

**Procedure:**

- [ ] Three songs, all mode MT2 (the verified 880EX workhorse), one per rate:

      | Song name    | Rate     | Expected rate byte |
      |--------------|----------|--------------------|
      | `R8 480 MT2` | 48 kHz   | `0x00` — unobserved |
      | `R8 441 MT2` | 44.1 kHz | `0x01` (control)    |
      | `R8 320 MT2` | 32 kHz   | `0x02`              |

- [ ] In each: slate, then **60 s of 440 Hz tone** on track 1, recorded from
      timeline zero. Then ~5 s of silence, then 10 s of the sweep (the silence
      and sweep give the decoder silent blocks and broadband content at each
      rate).
- [ ] In `R8 441 MT2` only, additionally record a second take on track 2
      starting at ~10 s (not zero) — a same-song contrast case for the origin
      question: does the first event of a not-from-zero take still relate to
      origin 12?
- [ ] Archive all three to one CD set; rip. If the machine's drive is imaged
      anyway, an HDD image of the same songs is a bonus (CD/HDD pair, §5.7).

**Analysis afterward:** confirm rate bytes 0x00/0x01/0x02 in catalog + header;
extract; measure the tone — 440 Hz on the nose at each declared rate closes
the two §12 rate bullets. Check StartFrame of every origin take: 12 at all
three rates ⇒ fixed preroll, not an artifact.

---

## Session 2 — sample-rate verification, VS-1880

Same three-song shape as Session 1, on the VS-1880 in MTP:

| Song name    | Rate     | Expected rate byte |
|--------------|----------|--------------------|
| `R1 480 MTP` | 48 kHz   | `0x00` — unobserved |
| `R1 441 MTP` | 44.1 kHz | `0x01` (control)    |
| `R1 320 MTP` | 32 kHz   | `0x02`              |

- [ ] Slate + 60 s of 440 Hz from zero + 5 s silence + 10 s sweep, per song.
- [ ] Archive to one set; rip.

This gives the `0x00`/`0x02` observations on the *other* machine's header
layout, and VR5 timeline-zero events at each rate (VS-1880 origin is frame 0,
§3 — confirm it stays 0).

---

## Session 3 — M24 deep-dive (VS-1880 only; gated on Session 0)

**Question(s):** no M24 media exists anywhere in the verification set; M24's
48-byte block does not divide the 32 KB cluster (32768 mod 48 = 32), so its
page-padding behavior — the spec's guess: 682 blocks + 32 pad bytes per page —
is entirely unverified (§2, §12).

**Prerequisite:** Session 0 identified which VS-1880 panel mode produces
format code 8. If none does, M24 is unreachable on this hardware — record that
finding in the spec and skip this session.

**Procedure:**

- [ ] One song, `M24 441 PAD`, 44.1 kHz, in the code-8 mode.
- [ ] Slate, then record on track 1 from zero: **3 minutes of continuous
      content** — 60 s steady 440 Hz sine, 60 s slow sweep, 30 s silence, 30 s
      of 440 Hz again. Rationale: at 44.1 kHz M24 is 132.3 kB/s, so a 32 KB
      page passes every ~0.25 s; a steady sine makes any pad-byte
      mishandling audible/visible as a phase or sample discontinuity every
      32768 bytes, the sweep catches errors a single frequency could alias
      past, and the silent stretch shows what an M24 silent block is.
- [ ] Record a second, short take on track 2 (15 s tone) — a second
      independent M24 stream.
- [ ] Archive to CD; rip. **Also image the HDD** — M24 on both paths at once,
      and the HDD copy is the one whose FAT cluster boundaries are directly
      inspectable.

**Analysis afterward:** decode under the guessed 682+32 rule and check the
sine is continuous across every page boundary; if not, diff the raw take
around 32 KB boundaries to find the actual whole-block/pad split. Verify
CD-vs-HDD take byte-identity after the swap (§5.7). Update spec §2's M24
caveat from "expect" to verified fact either way.

---

## Session 4 — MT2 on 64 KB clusters (scratch drive required)

**Question(s):** the reference decoder hardcodes 5460 blocks + 16 pad bytes
per 64 KB cluster, but 5461 whole blocks would fit; no 64 KB-cluster MT2 media
exists (§2, §12). Also: does the CD path of such a song decode with 0x8000
paging regardless (§5.4 says CD paging is always 32 KB)?

**Step 0 — establish that 64 KB clusters are even reachable.** Cluster size
comes from the FAT BPB, and every verified partition so far is 32 KB. It is
*not confirmed* that any VS-880EX/VS-1880 partition setting yields 64 KB:

- [ ] Initialize the **scratch drive** (⚠️ erases it) at the **largest
      partition size the machine offers** (2 GB where available).
- [ ] Image the drive (or just its first few MB) and read sectors-per-cluster:
      find the BPB via `grep -abo 'Roland  ' scratch.img | head` (the OEM ID
      sits at byte 3 of the boot sector, so sector start = offset − 3), then
      read byte 0x0D of that sector. `0x40` (64) = 32 KB clusters, `0x80`
      (128) = 64 KB clusters.
- [ ] If it reads 64 (32 KB): try the other machine / other partition sizes;
      if nothing yields 128, the 64 KB question is **not testable on this
      hardware** — record that in the spec (it bounds the question: 64 KB MT2
      media would have to come from another model) and stop here.

**If 64 KB clusters were achieved:**

- [ ] One song on the scratch drive, `MT2 64K CLU`, 44.1 kHz, mode MT2.
- [ ] Slate, then 2 minutes of continuous steady sine + sweep on track 1 from
      zero (MT2 at 44.1 kHz is ~33 kB/s, so a 64 KB cluster passes every ~2 s;
      2 minutes ≈ 60 clusters).
- [ ] **Image the HDD** — this is the primary capture; the FAT chain plus raw
      cluster bytes answer 5460-vs-5461 directly.
- [ ] Archive to CD and rip — this tests what the recorder writes to CD for a
      64 KB-cluster MT2 take (re-paged to 32 KB? carried as-is?) and exercises
      the §5.4 rule that CD-side MT2 paging is always 0x8000.

**Analysis afterward:** in the HDD image, walk one take's clusters and locate
the pad bytes: after byte 65520 (5460 × 12) or after byte 65532 (5461 × 12)?
Decode under both rules; only the correct one keeps the sine continuous.
Compare the CD take stream's paging against the HDD one.

---

## Session 5 — simultaneous-track allocation stride

**Question(s):** cluster-allocation stride for >2 simultaneously-recorded
takes (§12) — how does the recorder interleave FAT clusters when 3+ takes
grow at once?

Fold this into any session where the HDD will be imaged (3 or 4), or run it
standalone:

- [ ] VS-880EX: one song, `SIM4 441MT2`, 44.1 kHz MT2. Arm the maximum
      simultaneous inputs the machine allows (4 on the 880EX; confirm on the
      panel) with a **distinct tone per input**: 250, 350, 450, 550 Hz.
- [ ] Slate on one channel, then record **all tracks in one pass**, ~60 s,
      from zero.
- [ ] VS-1880: same, `SIM8 441MTP`, MTP, as many simultaneous inputs as it
      allows (up to 8 with the digital pair; use 250/350/450/550 Hz doubled
      across pairs if inputs run out of distinct tones).
- [ ] **Image the HDD** (the stride is a FAT-level fact; this is the capture
      that matters). Archive to CD as well.

**Analysis afterward:** map each take's FAT chain (`cmd/hdd-ls` /
`cmd/hdd-extract-file`); the per-take tone identifies which physical take is
which track. Look at the interleaving pattern of cluster numbers across the
simultaneously-grown takes.

---

## Session 6 — VR9 edit forensics (VS-880EX)

**Question(s):** VR9 flag byte (0x21) full semantics; counter = 0xFFFF on a
song's first log record; the 0x2E field's 2–23 values on live records; §9
optimize-remnant shape under controlled conditions (§12).

**This session is only as good as its log.** Number every operation and write
it down as you do it, with the approximate song-position of each edit. The
analysis step is "replay the VR9 log next to the operation log and attribute
every record".

- [ ] One song, `EDIT8 441`, 44.1 kHz MT2.
- [ ] Scripted operations, in order (tick and timestamp each):
      1. [ ] Record base take, track 1, 0:00–0:60 (440 Hz + slate).
      2. [ ] Record base take, track 2, 0:00–0:60 (550 Hz).
      3. [ ] **Punch-in** on track 1, roughly 0:20–0:30 (350 Hz so the
             overwrite is audible).
      4. [ ] **Track Erase** on track 2, roughly 0:15–0:25.
      5. [ ] **Track Copy** track 1 (0:40–0:50) → track 3 at 0:00.
      6. [ ] **Track Move** (or Cut) a region of track 3.
      7. [ ] Record a take on **V-track 2 of track 1** (250 Hz, ~15 s) and
             leave V-track 2 selected... then switch back to V-track 1
             (log both).
      8. [ ] **Undo** the last operation (note exactly which one it undid).
- [ ] **Archive to CD → set A** (pre-optimize state, undo history intact).
- [ ] Run **Song Optimize**.
- [ ] **Archive to CD → set B** (post-optimize: same audio, compacted log,
      remnants after the live count).
- [ ] Rip both sets; image the HDD if convenient (post-optimize state).

**Analysis afterward:** diff the EVENTLST logs of sets A and B; attribute
every log record to a numbered operation; tabulate flag-byte and 0x2E values
against operation type; check the first record's counter; characterize the
remnant region of set B against §9.

---

## Session 7 — VR5 erase / copy / optimize (VS-1880)

**Question(s):** whether a VR5 timeline record can carry take cluster 0 (an
erase/silence event) — none observed; the Valid-like 0x1E field's 0-on-copied-
songs behavior, via a deliberate Song Copy; VR5 optimize remnants past the
288-entry table (§12, §9).

- [ ] One song, `EDIT1 441`, 44.1 kHz MTP.
- [ ] Operations, in order, logged as in Session 6:
      1. [ ] Record base takes on tracks 1 and 2, 0:00–0:60 (440 / 550 Hz).
      2. [ ] **Track Erase** the middle of track 1, roughly 0:20–0:40 — the
             direct probe for a cluster-0 timeline record.
      3. [ ] Punch-in on track 2 (350 Hz, ~0:30–0:40).
      4. [ ] Record ~15 s on V-track 2 of track 1.
- [ ] **Song Copy** (playable copy) the song on the machine → name the copy
      `EDIT1 CPY` — the 0x1E probe. (If the machine offers both "playable"
      and "archive" copy flavors, playable-to-disk is the one that produced
      the 0x1E = 0 corpus songs; log which you used.)
- [ ] Archive **both** songs to one CD set (pre-optimize).
- [ ] **Song Optimize** the original, archive again → second set.
- [ ] Rip both sets; HDD image if convenient.

**Analysis afterward:** scan the original's 288-entry V-track table for any
record with take cluster 0 over the erased region (present ⇒ new spec fact;
absent ⇒ erase is represented purely by event splitting — also a fact). Diff
0x1E between `EDIT1 441` and `EDIT1 CPY` records. Characterize post-optimize
remnants.

---

## Session 8 (stretch) — a take spanning three discs (VS-1880)

**Question(s):** §5.6's per-disc spanning procedure is verified only across a
single junction; a file spanning ≥3 discs is unobserved (§12). (The related
"non-0x8000-aligned remainder" question is *not* addressable by recording —
take sizes are always 0x8000-aligned, so no session can produce one.)

**Run this only after Session 3 verifies M24** (it uses M24 for data rate; a
still-unverified codec would conflate two unknowns).

Arithmetic: mono M24 at 44.1 kHz ≈ 132.3 kB/s; a 650 MB disc holds ≈ 86 min of
it. To cross **two** junctions the take must start late on disc 1 and outlast
all of disc 2:

- [ ] Use a partition with room for ~1 GB of song (the Session 4 scratch
      drive's large partition is ideal).
- [ ] First create a **filler song** `FILL M24 A` and record ~60–70 min of M24
      (tone/sweep on loop; it needs no musical content) — its job is to occupy
      most of disc 1 ahead of the real test song. Confirm on the panel that
      the archive will write it first (song order); if archive order proves
      not controllable, size the filler so the set has 2 discs mostly full
      before the big take regardless.
- [ ] Then create `SPAN3 M24` and record **one continuous take ≥ 100 min**
      on a single track (~800 MB). Slate at the start; let the sweep/tone run;
      put a distinctive tone change every 10 min (log the times) so any
      spanning-reassembly error is locatable to a junction.
- [ ] Archive both songs to **one CD set** — expect 3 discs. Label them with
      set order immediately.
- [ ] Rip all discs; verify each disc's trailing TDI filler and EDC before
      calling the session done (a truncated mid-set rip destroys the test).

**Analysis afterward:** confirm the big take's data crosses two junctions;
verify each continuation disc's block 0 repeats the header with only the
+0x27 disc-index byte differing; extract and check the 10-minute tone marks
land at the right sample positions across both junctions.

---

## Priority and coverage map

| Session | Effort | §12 bullets addressed |
|---|---|---|
| 0 — mode matrix | ~30 min + 2 discs | mode→code map (incl. codes 2/4/6/7 / LIV), SONG +0x24, version/flags pair, 0x40-bit probe |
| 1 — rates, 880EX | ~30 min + 1 disc | `0x00` unobserved, `0x02` rate-verification, origin=12 across rates |
| 2 — rates, 1880 | ~30 min + 1 disc | `0x00`/`0x02` on VR5 |
| 3 — M24 | ~30 min + 1 disc + HDD image | M24 page padding (entirely unobserved codec) |
| 4 — 64 KB MT2 | scratch drive + init | MT2 64 KB padding (5460+16 vs 5461+4), or bounds the question as untestable |
| 5 — simultaneous tracks | folds into 3/4 | >2-take cluster stride |
| 6 — VR9 edits | ~1 h + 2 discs | flag byte 0x21, counter 0xFFFF, 0x2E, §9 remnants |
| 7 — VR5 edits | ~1 h + 2 discs | cluster-0 timeline records, 0x1E on copies, §9 remnants |
| 8 — 3-disc span | ~3 h + 3 discs | multi-junction spanning |

Not addressable by any session on this hardware: the non-0x8000-aligned
spanning remainder (take sizes are always aligned), the MBR 286–334 partition
group (firmware never populates it on these machines), and everything
VR6/VS-1680 — that one needs a VS-1680.

## Per-song capture log (copy per session)

```
Song name:            Machine:            Date:
Rate:        Mode:        Partition/cluster size (if known):
Tracks/V-tracks recorded:
Signal plan (tone per track, durations):
Operations performed, in order (Sessions 6/7):
  1.
  2.
Archived to set:         Disc labels/order:
Ripped files:            EDC check result:
HDD imaged? file:
Anomalies noticed on the machine:
```

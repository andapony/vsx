# Roland VS-Series Storage Format Specification

**Authoritative specification for the Roland VS-1880 (VR5) and VS-880EX (VR9) live-disk
(HDD) and CD-R "Song Copy Archive" formats.** Written to be sufficient for a third party to
implement extraction tooling from scratch, including a complete specification of the RDAC
audio codecs (Appendix A).

Status: **verified** against real media (2026-07-05; §4.4/§5.3 catalog and timestamp layouts and
the §5.5 validation rules re-verified and corrected against the full disc corpus 2026-07-07; a
third media-verified review the same day corrected §4.1 Roland detection, the §5.4/§5.5
song-boundary-block walking rules, and the §10 filler-frame signature; a fourth media-verified
review the same day corrected take resolution on copied songs (§4.3/§7/§8.3), the Valid-field
claim (§7), the disc-end/spanning arithmetic (§5.6/§10), the MT2 pad-byte content (§2), and the
VR9 header-tag scope (§5.4)).
Every structural claim here was checked byte-for-byte against `vs-1880.img`, `vs-880ex.img`, and
21 CD-R dumps, and cross-validated between HDD and CD where the same song exists on both.

---

## 1. Scope and machine matrix

| Model | Gen tag | File ext | Tracks × V-tracks | Event record | CD archive |
|---|---|---|---|---|---|
| VS-1880 | `R06` | `VR5` | 18 × 16 | 64 bytes | `VS1880EXR06 Song Copy Archives  ` |
| VS-880EX | `R02` | `VR9` | 16 × 8 | 48 bytes | `VS-8EXECR02 Song Copy Archives  ` |
| VS-1680 | — | `VR6` | 16 × 16 | 64 bytes | believed same family as VR5 |
| VS-2480 | — | `VR1` | 24 × 16 | 128 bytes | (out of scope) |

Both event records share an identical field layout for their first 0x20 bytes (they diverge at
0x20); the VR5 record also has more trailing fields. See §7.

**Verification scope:** everything in this document was validated on VS-1880 and VS-880EX media
only. VR6 (VS-1680) is included in the matrix for orientation but no claim here has been checked
against VS-1680 media; treat VR6 as "expected VR5-like" until verified.

---

## 2. RDAC audio codecs

All PCM is mono per file. One RDAC *block* always decodes to **16 samples**. Format code lives in
`SONG.VRx` byte 19 and is copied into CD headers.

| Code | Name | Block | Bits | Notes |
|---|---|---|---|---|
| 0 | MT1 | 16 B | 16 | own pattern dispatch (layouts A/B/B3/C/D/E/F) |
| 1 | MT2 | **12 B** | 16 | own dispatch (layouts 12A–12G); **cluster padding, see below** |
| 3 | M16 | 32 B | 16 | uncompressed LE PCM |
| 5 | MTP | 16 B | 24 | **default**; the common VS-1880 format |
| 8 | M24 | 48 B | 24 | uncompressed LE PCM |

**Decoding.** MT1/MT2/MTP are predictive differential codecs: each block selects one of up to 37
bit-packing patterns, unpacks per-sample differentials, scales them, and adds them to linear
interpolations between anchor samples, carrying the previous block's last sample across block
boundaries. The complete algorithm — pattern lookup table, bit layouts, per-pattern dispatch
tables, and reconstruction rules — is specified in **Appendix A**. MTP output is clamped to
signed 24-bit `[−8388608, +8388607]`; MT1/MT2 to signed 16-bit `[−32768, +32767]`.

**MT2 cluster padding (critical).** MT2's 12-byte block does not divide the storage cluster
evenly, so the recorder writes whole blocks per cluster page and pads the remainder:

- 32 KB cluster (VS-880EX and VS-1880 HDD default): **2730 blocks, then 8 pad bytes**. Verified
  against real media.

  The pad bytes are **not reliably zero**. On verified CD media they are non-audio garbage from
  the write buffer: on three of every four page boundaries an exact copy of the *next* page's
  first 8 bytes, and on every fourth boundary (a 128 KB firmware write-chunk seam) a stale
  8-byte constant; they are zeros only inside silent regions. A decoder must skip them
  unconditionally by position, and a validator must not expect any particular content there.
- 64 KB cluster: **5460 blocks, then 16 pad bytes** — the figure asserted (hardcoded) by the
  reference decoder. Note this is **not** `floor(65536/12)` = 5461 whole blocks + 4 pad; it
  corresponds to keeping the 32 KB page unit (2 × 2730 blocks). **Unverified** — no
  64 KB-cluster MT2 media exists in the verification set (§12), and a naive
  whole-blocks-per-cluster formula gives a different (probably wrong) answer.

A decoder must consume those pad bytes at each page boundary. MT1/MTP (16-byte blocks) and M16
(32-byte blocks) divide both cluster sizes evenly and need no padding handling.

**M24 caveat.** M24's 48-byte block does **not** divide either cluster size evenly
(32768 mod 48 = 32; 65536 mod 48 = 16). No M24 media exists in the verification set, so its
page-boundary behavior is unverified; implementers should expect whole-block-per-page padding
analogous to MT2 (682 blocks + 32 pad bytes per 32 KB page) and verify against real M24 media.

**Silent blocks.** A silent MT2 block is 12 zero bytes; a silent MTP block is 16 zero bytes.
Takes commonly begin with one silent block; it is ordinary audio data and must be decoded, not
skipped. Take audio streams have **no header** — decoding starts at byte 0 of the take.

---

## 3. Timing model

- 1 frame = 16 samples. `samples = frames × 16`; `seconds = frames × 16 / sampleRate`.
- 44.1 kHz → 2756.25 frames/s; 48 kHz → 3000 frames/s; 32 kHz → 2000 frames/s.
- Sample rate = `SONG.VRx` byte 18 low nibble: **0 = 48 kHz, 1 = 44.1 kHz, 2 = 32 kHz** — the
  three rates the New Song dialog offers on both machines (44.1 is the machine default yet
  codes as 1 — counter-intuitive). The reference decoder additionally maps 3 = 96 kHz and
  4 = 88.2 kHz (later VS models; not creatable on the VS-880EX/VS-1880). Verification status:
  1 = 44.1 kHz is media-verified end-to-end; `0x02` is observed on four catalogued songs but
  their audio has not been rate-verified by listening; `0x00` is unobserved; the `0x40` bit
  seen once (`0x41`) is unknown (§12). (Older project docs labeled codes 2/3 "varispeed" — a
  fabrication with no source in Roland documentation or the reference decoders; see the
  HISTORY document.)
- **Timeline origin:** VS-1880 places timeline zero at frame 0. **VS-880EX places timeline zero
  at frame 12** — origin *audio* events have StartFrame = 12, not 0. Subtract 12 when computing
  sample positions on VR9. The rule holds for normal records only: erase/tombstone records
  (FileID = 0 and/or flag byte = 1, §7/§8.2) legitimately carry StartFrame = 0. The origin is
  machine-verified as a real 12-frame preroll on a shared absolute timeline, not an artifact
  of the observed songs: when a VS-1880 imports a VR9 song it keeps StartFrame values
  unchanged on its frame-0 timeline, so every imported v-track begins exactly 192 samples
  later than the origin-subtracted VR9 extraction and is sample-identical thereafter
  (verified on a corpus song present in both a VR9 archive and two later VR5 archives).
  Subtracting 12 therefore aligns VR9 output to first-possible-audio; prepend 12 frames of
  silence to reproduce Roland's own conversion alignment.

---

## 4. HDD (live-disk) format

### 4.1 Roland MBR (12 partitions)

Standard MBR is extended to 12 partition entries at non-standard offsets:

```
446, 462, 478, 494,   382, 398, 414, 430,   286, 302, 318, 334
```

Each entry: type@+4, start-LBA@+8 (LE u32), sector-count@+12 (LE u32). Boot signature `55 AA` at
0x1FE. Non-Roland disks use the standard 4 entries. The partition type byte is informational
only; treat any entry with type ≠ 0 as a candidate FAT16 partition and probe its BPB.

**Detecting a Roland disk.** The `Roland  ` signature is **not** in the MBR: sector 0 is x86
boot code, and its bytes at 0x03 are zero on all verified media. The signature is the OEM-ID
field at offset 0x03 of each **partition's boot sector (BPB, §4.2)**. Detection is therefore
two-step: parse the four standard entries (446/462/478/494), read the first valid partition's
BPB, and if its OEM ID begins `Roland  `, re-parse the MBR using all 12 offsets.
(Equivalently: probe all 12 offsets unconditionally and keep entries whose BPB validates —
validate as: OEM ID `Roland  `, 512 bytes/sector, sectors-per-cluster a nonzero power of two.) This
matters in practice — on the verified VS-1880 image the extended offsets 382–430 hold four live
partitions that a standard 4-entry parser silently misses. The 286–334 group is unpopulated on
all verified media (§12).

### 4.2 FAT16 with Roland byte-swap quirk

Filesystem is **FAT16** (512 B/sector; typically 64 sectors/cluster = 32 KB clusters — always
read `SectorsPerCluster` from the BPB rather than assuming it, and pass the resulting cluster
size to the MT2 decoder). The structures are standard; everything a VS extractor actually needs
is reproduced below, so the Microsoft FAT specification (*fatgen103*) is required only for edge
cases beyond this summary.

**BPB** (the partition's first sector; all sector numbers below are relative to the partition's
start LBA from §4.1):

| off | size | field |
|---|---|---|
| 0x03 | 8 | OEM ID (`Roland  ` on VS media, §4.1) |
| 0x0B | u16 LE | bytes per sector (512 on all verified media) |
| 0x0D | u8 | sectors per cluster |
| 0x0E | u16 LE | reserved sectors (= sector of the first FAT) |
| 0x10 | u8 | number of FATs |
| 0x11 | u16 LE | root-directory entry count |
| 0x16 | u16 LE | sectors per FAT |

Derived layout (in sectors): `fatStart = reservedSectors`;
`rootDirStart = reservedSectors + numFATs × sectorsPerFAT`;
`dataStart = rootDirStart + ceil(rootEntryCount × 32 / 512)`. Cluster→byte:
`(dataStart + (cluster−2) × sectorsPerCluster) × 512`.

**FAT chain:** the FAT entry for cluster N is the u16 LE at byte `N × 2` of the FAT. Values:
`0x0000` = free, `0xFFF7` = bad cluster, `≥ 0xFFF8` = end of chain, anything else = next
cluster in the chain.

**Directory entries** (32 bytes each; the root directory is a fixed array of `rootEntryCount`
entries, a subdirectory is a cluster chain of them): 8.3 name @+0x00 (8-byte name + 3-byte
extension, space-padded, no dot; first byte `0x00` = end of directory, `0xE5` = deleted entry),
attribute @+0x0B (`0x10` = subdirectory, `0x08` = volume label, `0x0F` = long-file-name entry —
skip these; they do occur on real VS disks that have been touched by a PC), first cluster
u16 LE @+0x1A, file size u32 LE @+0x1C. Subdirectories begin with the usual `.` and `..`
entries.

**Directory timestamps are firmware constants, not history.** The standard FAT16 time/date
fields (creation @+0x0E, access date @+0x12, write @+0x16) carry no per-file chronology: VS
firmware zeroes creation/access and writes one **fixed constant** into the write stamp —
identical to the second across every entry that machine writes. Observed constants:
`2000-03-29 09:19:52` on all 669 entries of the verified VS-880EX image (both partitions),
`2000-02-17 23:59:00` on all 1,860 VR5 entries of the verified VS-1880 image, and
`1998-07-31 11:27:52` on one of that image's VR9 partitions (an older machine or firmware,
§12). The constant per machine makes the stamp useless as a date but useful as provenance: on
a multi-machine disk it records which machine last wrote each entry — the VS-1880 image's VR9
partitions keep their originating 880-family constants except for the handful of entries the
VS-1880 itself rewrote, which carry its constant instead. Entries bearing any other (often
impossible) values, or populated creation/access fields, are foreign-OS litter from the same
PC/Mac mounts that leave LFN entries.

The Roland quirk — **16-bit byte-pair swapping**:
- **Root directory**: normal (no swap).
- **Subdirectory entries**: every 32-byte directory entry is byte-pair swapped before parsing.
- **Files inside subdirectories** (all TAKE/SONG/EVENTLST data): byte-pair swapped on read.

So `TAKExxxx.VRx` content read through the FAT layer must be pair-swapped to yield the real RDAC
stream. (This is the same transform the CD format stores pre-corrected — see §5.7.) Swap at an
even granularity (per cluster is simplest) and then truncate to the directory-entry file size —
odd-length files exist (e.g. a 911-byte `SYSTEM.VR5`), and the swap is defined on the underlying
even-sized clusters, not on the logical byte length.

### 4.3 Directory layout

The directory hierarchy is exactly one level deep and is required knowledge for enumeration:

```
/                       FAT16 root (unswapped)
  SONG0000.VR5/         one subdirectory per song; name = SONGxxxx + machine extension
  SONG0001.VR5/
    SONG.VR5            song header (§4.4) — literal fixed name
    EVENTLST.VR5        event list (§4.5/§4.6) — literal fixed name
    TAKE193C.VR5        take audio; xxxx = uppercase hex of the take's FAT start cluster
    TAKE....VR5         (may also contain MIXER, AUTOMIX, SYNCTRK, etc. — not needed for audio)
```

Enumerate songs by listing root-directory subdirectories whose names end in `.VR5`/`.VR9`/…, then
read `SONG.VRx` and `EVENTLST.VRx` inside each. One disk can host both machines' songs (the
verified VS-1880 image carries whole partitions of `SONGxxxx.VR9` directories) — the machine
format is per song directory, keyed by its extension, not per disk.

Take files are named `TAKExxxx.VRx` where `xxxx` is the uppercase hex of the FAT cluster the
take was **recorded** at — which is the number the event records carry (§7), but **not
necessarily the file's current start cluster**. Songs processed by Song Copy keep every take's
original filename while the file lands at a new FAT location; on the verified VS-1880 image
494 of 1814 takes (always whole songs at a time) have filename ≠ actual start cluster, and the
event fields always match the *filename* (996/996), never the relocated cluster. **Resolve
takes by directory lookup** — format the event's cluster field as `TAKE%04X.VRx`, find that
entry in the song directory, and follow the FAT chain from the entry's first-cluster field.
Following the FAT directly from the event's cluster value silently reads wrong or free clusters
on every copied song (88 events on the verified image would dangle).

The verified VS-880EX image is the opposite extreme of that census: **0 of 500 takes** are
relocated — every take file sits at the cluster its name encodes, so no song on that disk was
ever Song-Copied onto it. Together the two censuses make take naming a provenance tool: an
all-in-place song is original to its disk, and because a **VR9** CD archive stores each take
under its on-disk name (§5.7 — VR5 archives renumber, below), the take names inside such an
archive fingerprint the exact disk state it was cut from. Matching names plus an all-in-place
source FAT establish the copy direction disk → archive (verified: all nine takes of the 880EX
image's song `BC` appear by name in the `VS-8EXECR02` set whose catalog entry is
byte-identical to that song's `SONG.VR9`).

The name fingerprint is VR9-only. A VR5 archive renumbers every take into the archive's
sequential cluster space (§7), so its take names match no disk's — on verified media the
overlap between a song's HDD names and its own VR5 archive names is chance collisions
(1-of-46, 1-of-39). Cross-media identity for VR5 songs comes from timestamps instead: the
`SONG.VR5` **created stamp** (§4.4) survives copy, re-save, and archiving — on the verified
VS-1880 image two partition-4 songs match two catalog entries of a later `VS1880EXR06` set to
the second, every common v-track extracting bit-identical even though the archive postdates
the image's disk state (later last-saved stamps, three v-tracks the disk copies lack). The
per-record creation timestamps (§7, +0x28) refine identity to individual takes: a take's
first event dates its recording, and simultaneously-recorded takes share one stamp. VR9 has
no timestamps anywhere, leaving take names as its only fingerprint.

### 4.4 SONG.VRx

`SONG.VR5` is 38 bytes; `SONG.VR9` is 20 bytes (it ends after the format code):

| off | size | field |
|---|---|---|
| 0x00 | 4 | version/flags pair — two u16s; the second is typically `00 0X`, the first varies widely (`00 0C`, `1E14`, `03B4`, `0704`, `1785` … observed; §12) |
| 0x04 | u16 BE | source folder number |
| 0x06 | 12 | ASCII song name (space-padded) |
| 0x12 | 1 | sample-rate byte (decode the low nibble per §3) |
| 0x13 | 1 | format code (§2) |
| 0x14 | 8 | timestamp — created *(VR5 only)* |
| 0x1C | 8 | timestamp — last saved *(VR5 only; ≥ created on all verified media)* |
| 0x24 | u16 | unknown — `0x0000`/`0x0300`/`0x0500`/`0x0600` observed *(VR5 only, §12)* |

**Timestamp layout** (8 bytes; the same layout appears in VR5 event records, §7):

```
[ss, mm, hh, dow, dd, MM, yyyy(u16 BE)]
```

seconds, minutes, hours (24 h), day-of-week (**1 = Saturday … 7 = Friday**), day, month, year.
Verified against 344 real timestamps spanning 2000–2018 with zero day-of-week mismatches; the
ss/mm byte order (indistinguishable from single values) is fixed by creation-order monotonicity
of the VR5 registry (688 consecutive record pairs: 0 order violations as `[ss,mm,…]`, 130 as
`[mm,ss,…]`).

### 4.5 EVENTLST.VR5 (VS-1880 HDD)

```
[0x00] 18-byte header: ASCII "TAKE EVENT LIST " (16 B) + edit-list entry count (u16 BE) @ 0x10
       (same magic as the CD form, §6.1; present on all verified HDD files)
[0x12] Edit list: N × 64 bytes  — undo history, SKIP for extraction
[then] Track data: 18 tracks × 16 v-tracks, positional:
         [16 B] track/v-track name (default "V.T T-VV", or user track name)
         [ 2 B] event count (u16 BE)
         [ count × 64-byte event records ]  — the CURRENT timeline
```

Only records in the track data (not the edit list) form the timeline. See §7 for the record.

### 4.6 EVENTLST.VR9 (VS-880EX HDD)

The VS-880EX does **not** use the VR5 header+track-table layout. It is a flat event **log**:

```
[0x00] live event count N (u16 BE)
[0x02] N × 48-byte event records   — chronological log (creation order)
[then] tail: ghost/optimize-remnant records + zero padding
```

There is no separate "current timeline" table. The timeline is reconstructed by replaying the log
(§8.2). Each record carries an explicit StartFrame/EndFrame/TrimmedFrames and a track/v-track
code.

---

## 5. CD-R "Song Copy Archive" format

A Roland VS backup CD is a single MODE1 data track. Dumps must be **raw** (`cdrdao
--read-raw`, 2352-byte frames). A file that is an exact multiple of 2048 but not 2352 is a
`dd`/"cooked" extraction and should be re-ripped. (An *intact* cooked image is logically
sufficient — its byte offset equals the user-data offset defined below — but `dd` rips of these
discs have repeatedly proven truncated or block-shifted in practice, so the raw requirement
stands as a data-integrity rule, not a format one.)

### 5.1 Frame and user-data geometry

Each 2352-byte frame: 12 B sync (`00 FF×10 00`) · 3 B MSF (BCD) · 1 B mode (`01`) · **2048 B user
data** · 288 B EDC/ECC. All archive structures live in the concatenated 2048-B user-data stream.
MSF is standard BCD; LBA = ((M×60)+S)×75 + F − 150; frame 0 = MSF 00:02:00.

> Throughout §5, offsets are **user-data offsets** (frame N, byte K → udoff = N×2048 + K).

### 5.2 Archive header (user-data offset 0)

| udoff | Size | Field |
|---|---|---|
| 0x00 | 32 | Signature (`VS1880EXR06 …` or `VS-8EXECR02 …`) |
| 0x20 | 4 | Backup-set ID (identical across all discs of one set) |
| 0x24 | 2 | Song count (u16 BE; set-wide — continuation discs repeat the same count and catalog) |
| 0x26 | 2 | Disc index within set (u16 BE, 0-based) |
| 0x28 | 2 | Total discs in set (u16 BE) |
| 0x2A | … | Song catalog (§5.3) |

### 5.3 Song catalog

The catalog begins at udoff **0x2A** on both machines; the entry layouts differ.

**VS-1880** — each entry is a byte-for-byte copy of that song's 38-byte `SONG.VR5` header
(§4.4; verified byte-identical to the on-disc `SONG    VR5` file contents — the CD names of the
fixed metadata files are the 8.3 names space-padded with no dot: `SONG    VR5`, `EVENTLSTVR5`,
`SYSTEM  VR5`, `SYNCTRK VR5`, …). Entries are packed **back-to-back with no separators**: entry
*k* starts at udoff `0x2A + 38k` (so the first entry's name is at udoff 0x30), and the region
after the last entry is zero on all verified media. The sample-rate byte at entry offset +0x12 is live data, not a
constant: `0x01` (44.1 kHz) on most media, but `0x02` (32 kHz) and one `0x41` are
also observed — decode via the §3 low-nibble rule (§12).

**VS-880EX** — each entry is likewise a byte-for-byte copy of that song's 20-byte `SONG.VR9`
header (§4.4): version/flags pair, source SONG number (u16 BE), 12-byte name, sample-rate
byte + format code (`01 01` = 44.1 kHz MT2 on all verified media); **no timestamps**. Entry *k*
starts at udoff `0x2A + 20k`. The source SONG numbers need not be dense (gaps observed). After
a zero gap, an **undocumented table of u16 BE values begins at udoff 0xFCA** — one value per
catalogued song, also mirrored at +0x161C of that song's header blocks (§5.4); magnitudes
roughly track the song's size in 0x8000 blocks but the exact rule is unknown (§12). Do not
treat non-zero bytes there as catalog corruption.

**The catalog can be stale**: it may list songs whose data is absent, or (rarely) omit reality —
always reconcile against the actual files on disc (§5.4).

### 5.4 File storage — the reliable enumeration method

There is **no disc-level table of contents.** (Runs of 32-byte "TOC-like" entries do appear
inside `SONG`/`SYSTEM` file slack, but they are per-song manifests written from a stale firmware
buffer; they may list files that are not on the disc, omit files that are, and carry wrong sizes.
Never use them for enumeration.) Instead, **every file is preceded by its own 32 KB (0x8000)
header block, aligned to a 0x8000 user-data boundary, with file data starting exactly +0x8000
after the header block start.**

**VS-1880 header block fields** (offsets from block start):

| off | field |
|---|---|
| +0x00 | archive signature (block is a full header/catalog copy) |
| +0x241E | song name (12 B) |
| +0x242A | sample-rate byte + format code (= SONG bytes 18–19, §5.3 note) |
| +0x2434 | filename #1 (11 B) |
| +0x2444 | FileID (u16 BE) |
| +0x2446 | filename #2 (11 B) |
| +0x245C | magic `60 BF 51 28` |
| +0x2460 | FileID (u16 LE) |
| +0x2462 | file size (u32 LE) |
| +0x8000 | file data begins |

**VS-880EX header block fields** (offsets from block start):

| off | field |
|---|---|
| +0x00 | archive signature |
| +0x160C | source SONG number (u16 BE) |
| +0x160E | song name (12 B) |
| +0x161A | `01 01` on all verified media (rate byte + format code copy, cf. §5.3) |
| +0x161C | per-song u16 BE (≈ song size in 0x8000 blocks; exact rule unknown, §12) — also tabulated per song at udoff 0xFCA (§5.3) |
| +0x1620 | file count in this song (u16 BE) |
| +0x1622 | file index within song (u16 BE) |
| +0x1624 | filename (11 B + NUL on genuine headers; stale per-file areas can carry a non-NUL 12th byte — validate only the 11 name bytes) |
| +0x1630 | data-block count for this file (u16 BE) = ceil(size/0x8000) |
| +0x1632 | `0x0000` real entry / `0x0001` song-start marker block (skip markers) |
| +0x1634 | FileID (u16 BE) |
| +0x164C | 4-byte tag — constant within a song but **NOT per-song**: the same value recurs across songs and even across unrelated backup sets (only `7A4A7D28` and `7A5BFF24` observed); role unknown; never gate on it |
| +0x1650 | FileID (u16 LE) |
| +0x1652 | file size (u32 LE) |
| +0x8000 | file data begins |

Files are laid out contiguously in source-directory order: next block = current block +
`(1 + ceil(size/0x8000)) × 0x8000` (the +1 is the header block). The only interruption is one
header-only **song-boundary block** (0x8000 bytes, no data of its own) between the last file of
one song and the first file of the next (§5.5 case 3). The chain arithmetic lands exactly on
it, and because a boundary block's per-file bytes are stale (a repeat of the previous file's
entry — sometimes with size 0, sometimes outright garbage; §5.5), a walker that treats it as a
file header desyncs and skips the next song's first file (verified on both machines). A chain
walker must therefore validate every landed-on block with the §5.5 checks and, on any block
that fails them, advance one 0x8000 slot and re-test.

The walk terminates when the landed-on boundary is the start of the disc's trailing TDI filler
run (§10) — the filler, not the physical end of the dump, marks the end of burned file data on
**every** disc. If the current file's data itself runs past the filler start, the file spans to
the next disc of the set (§5.6).

Association of a file to a song is by the song-name field of its header block (VR5) or by the
source-SONG-number field (VR9). Note the VR5 key is the 12-byte name only — two songs with
identical (or identically truncated) names in one set would be ambiguous; no such collision
exists in the verified corpus.

On an index-0 disc the first file's header block is at udoff **0x10000** — block 0 is the
archive header and block 0x8000 the second header copy, neither a file header (§5.5); verified
on both machines. All archive allocation is in 0x8000-byte units, and **0x8000 (32 KB) is also
the page size to use for MT2 cluster padding when decoding CD takes** (§2) — there is no BPB on
CD to read it from.

### 5.5 Header-block validation — signature hits that are NOT file headers

Scanning 0x8000 boundaries for the archive signature is the correct enumeration/recovery method,
but three kinds of signature-bearing blocks are **not** valid file headers and must be rejected:

1. **Block 0 of a disc with disc index 0** is the archive header (§5.2), yet its per-file
   metadata area contains a **stale copy of another file's header** (typically the set's last
   written file — an artifact of the firmware reusing one RAM buffer and writing frame 0 last).
   The named file's data is *not* at +0x8000. On index-0 discs, use block 0 only as the archive
   header; never treat it as a file header.
2. **A second archive-header copy at udoff 0x8000** on index-0 discs. Its per-file metadata
   area holds garbage bytes (not zeros): on VR5 the bytes at +0x245C fail the magic check, on
   VR9 the filename field is zeros and fails the name check.
3. **Song-boundary blocks** (VR9: the `+0x1632 = 1` song-start markers; VR5: analogous stale
   blocks at song-section starts) whose per-file area holds a **stale entry derived from the
   previous song's last file**. On VR9, gate strictly on the +0x1632 flag — a reader that keys
   only on filename validity will extract a bogus duplicate. **VR5 has no such flag**, and its
   boundary blocks always carry the *next* song's name but their per-file bytes vary: of 12
   observed on real media, 4 fully repeat the previous file's FileID and size, 4 repeat the
   FileID with size 0, and 4 hold garbage that also fails the filename/magic checks. Most
   therefore pass checks 1–5 and are rejected only by check 6 below; a walker should treat
   *any* landed-on block that fails validation — whichever check fails — as a boundary
   candidate and skip one 0x8000 slot (§5.4).

Accept a block as a file header only if **all** of:

1. the signature matches;
2. the filename field is a plausible 11-byte name (`[A-Z0-9][A-Z0-9 ]{7}` + `VR5`/`VR9` —
   names never begin with a space);
3. **(VR5 only)** the magic `60 BF 51 28` is present at +0x245C;
4. **(VR9 only)** the marker flag at +0x1632 is 0;
5. the block is **not udoff 0 of an index-0 disc** (case 1 above). This check is load-bearing:
   the stale block-0 entry passes checks 1–4 on **both** machines — verified on every index-0
   disc in the corpus, where block 0 names the set's last written file while the bytes at
   +0x8000 are the second archive-header copy, not that file's data.
6. the block at **+0x8000 does not itself begin with the archive signature**. A true file
   header is followed by file data, and no VS file type's content begins with the signature; a
   header-only block (archive header, second header copy, song-boundary block) is instead
   followed by the *next* header block. This is the only check that rejects **all** VR5
   song-boundary blocks (case 3; some also fail checks 2–3), and it also independently rejects
   cases 1 and 2 — checks 4/5 are kept as cheaper, more explicit gates. When +0x8000 lies at or
   past the disc's data end (the §10 filler start), **reject**: a real header is always
   followed by its file data on the same disc (a header block with all of its data on the next
   disc has never been observed; spanning always splits mid-file, §5.6).

On a **continuation disc** (index > 0), block 0 passes **all six checks** *legitimately* (check
5 does not apply, and +0x8000 holds file data), but it is the repeated header of the file
spanning in from the previous disc (§5.6): the data at +0x8000 is that file's continuation, not
its start. The disc-index field is the only discriminator. Consume it via the §5.6 spanning
logic; never enumerate it as a newly discovered file.

Do **not** magic-gate VR9 blocks: the u32 at +0x164C is a session-scoped tag of unknown role
(§5.4), not a format constant — a reader that requires a fixed value there will reject valid
files. These checks are
needed by **both** enumeration styles: a boundary scan (initial discovery, recovery of damaged
discs, or finding orphan files — files can exist on disc with no catalog or manifest entry)
applies them at every 0x8000 boundary, and the §5.4 chain walk lands on a song-boundary block
at every song transition (verified on both machines) and must apply them to know when to skip
a slot rather than desync on the boundary block's stale size.

### 5.6 Multi-disc spanning

A backup set is one or more discs sharing a set ID (§5.2), ordered by disc index. When a file's
data runs past the end of a disc's data area, it continues on the next disc: **the continuation
disc's block 0 is a repeat of that file's header block, and the remaining file data resumes at
user-data offset 0x8000.** The repeat is byte-exact except for **exactly one byte**: block
offset +0x27, the low byte of the u16 BE disc-index field at +0x26 (verified by full-block diff
on all 8 junctions in the corpus, both machines). There is no explicit resume-offset field —
the reader tracks the byte remainder.

The complete procedure (all quantities verified byte-exact on every junction in the corpus):

- **A disc's data area ends at the start of its trailing TDI filler run (§10), not at the end
  of the dump.** Every finalized disc — including mid-set discs — ends with the filler, whose
  length varies per disc (126–148 frames observed), so the boundary must be *detected* (first
  0x8000-aligned udoff whose frame matches the §10 filler pattern), never computed from the
  disc length. Using end-of-user-data instead over-counts by ~0x43000–0x4A000 on every
  verified disc and corrupts the spanned file mid-stream.
- `avail = fillerStart − dataStart` for the file being read; `remainder = size − avail`.
- Open the next disc by index; require the same set ID; verify block 0 repeats the current
  file's header (name/FileID/size match). Read `remainder` bytes starting at udoff 0x8000 —
  the data runs flush, with no padding between the two pieces.
- The next file's header block is at `0x8000 + remainder` rounded up to the next 0x8000
  boundary; the ordinary §5.4 walk (with §5.5 validation) continues from there. (Every
  observed remainder is already an exact 0x8000 multiple — take sizes and `avail` are both
  0x8000-aligned — so rounded and unrounded variants are indistinguishable on the corpus;
  §12.)
- A file spanning more than two discs repeats the same procedure per disc (not observed in the
  corpus; every verified span crosses exactly one junction).

Verified for both VR5 and VR9 sets and for takes spanning two discs byte-exactly (for MT2
takes, the page-boundary structure runs phase-exact across the junction — no missing,
duplicated, or shifted bytes).

Continuation (index > 0) discs still carry a full archive header + catalog copy at offset 0, so
they look self-describing but cannot be extracted standalone. (Note the disc-index distinction
for block 0: on index-0 discs it is the archive header with stale per-file bytes, §5.5; on
continuation discs it is the genuine header of the spanning file.)

### 5.7 Byte order of file content, and take naming

**CD file content is NOT byte-swapped.** It is stored in native RDAC order and decodes directly.
(The HDD FAT layer pair-swaps subdirectory file data, §4.2; the CD backup writes the un-swapped
bytes.) Verified: a CD take is byte-identical to its HDD counterpart after the HDD swap is
undone.

Take naming differs by machine:

- **VS-1880 renames takes.** The CD FileID is a fresh sequential archive ID (the take's position
  in the archive's cluster space, §7), not the HDD `TAKExxxx` number. The same audio that was
  `TAKE193C.VR5` on HDD may be `TAKE9CC7VR5` on CD.
- **VS-880EX keeps HDD names.** VR9 CD FileIDs equal the HDD FAT start clusters, so
  `TAKE0C53.VR9` on HDD appears as `TAKE0C53VR9` on CD.

---

## 6. EVENTLST on CD

### 6.1 VS-1880 (`EVENTLSTVR5`)

```
[0x00] "TAKE EVENT LIST " (16 B ASCII)
[0x10] registry count N (u16 BE)
[0x12] N × 64-byte records   — full historical registry (creation order, incl. deleted)
[then] V-track table: exactly 288 positional entries (18 tracks × 16 v-tracks):
         [16 B] name  (default "V.T T-VV" OR user track name, e.g. "Bass")
         [ 2 B] event count (u16 BE)
         [ count × 64-byte records ]   — the CURRENT timeline
[then] zero padding / optimize remnants
```

**Extraction uses the V-track table, not the registry.** Parse the table **positionally** — count
exactly 288 entries; do not gate on a `"V.T"` name prefix, because user-named tracks store their
name there instead, and remnant `"V.T…"` strings continue past the 288th entry. Track/v-track come
from entry position (index //16 + 1, index %16 + 1). This is **structurally identical** to the
HDD track data (§4.5) — same records in the same order — but *not* byte-identical: the
take-reference fields at 0x14–0x1C are rewritten into the archive's cluster space, because the
VS-1880 renames takes on CD (§5.7). The same song nonetheless extracts identically from HDD
and CD.

**File-size caveat:** the size declared for `EVENTLST` in its header block (§5.4) and the size
implied by the per-song manifest can disagree; the header-block size is authoritative for reading, and
the logical content ends at the end of the 288th V-track entry — bytes beyond that are padding or
optimize remnants (§9).

### 6.2 VS-880EX (`EVENTLSTVR9`)

Identical to the HDD VR9 log (§4.6): `[u16 BE count][N × 48-byte records][tail remnants]`. On CD
the records carry non-zero StartFrames just like HDD.

---

## 7. Event record structure (unified)

Both records begin identically; VR5 continues to 0x40, VR9 stops at 0x30.

| off | size | field | notes |
|---|---|---|---|
| 0x00 | u32 BE | **StartFrame** | timeline position (VR9 origin = 12) |
| 0x04 | u32 BE | **EndFrame** | |
| 0x08 | u32 BE | **TrimmedFrames** | offset into take audio (skip this many frames) |
| 0x0C | u16 BE | trim-in sub-field | ≈ low16(TrimmedFrames); exact role TBD |
| 0x0E | u16 BE | trim-out / group sub-field | TBD |
| 0x10 | u16 BE | counter / sequence | `0xFFFF` = deleted **in registry/log context only** — live V-track-table records commonly carry `0xFFFF` too; never filter the timeline on this field (§8.1) |
| 0x12 | u16 BE | related sequence | |
| 0x14 | u16 BE | **take START cluster** = TAKE FileID | see below |
| 0x16 | u16 BE | **take END (last) cluster** | always ≥ 0x14 |
| 0x18 | u16 BE | **take cluster count** | = (0x16−0x14)+1 for solo takes |
| 0x1A | u16 BE | start cluster adjusted for trim-in | = 0x14 + floor(trimBytes/clusterBytes), trimBytes = TrimmedFrames × block bytes (§2) |
| 0x1C | u16 BE | end cluster adjusted for trim-out | |
| 0x1E | u16 BE | Valid-like field, role unknown — 1 on most VR5 timeline records, but **0 on every record of some copied songs and 0 on all VR9 log records**; never filter on it (§8.1) | (registry records reuse these bytes) |
| 0x20 | (u16 VR5) | Event ID / (VR9: 1 B v-track code + 1 B flag) | see per-machine note |
| 0x22 | — | VR5: u16 BE = (track−1)×16+(v−1); VR9: 12-B name starts here | |
| 0x24 | 4 B (VR5) | unknown | |
| 0x28 | 8 B (VR5) | timestamp — creation time of the record; layout in §4.4: `[ss,mm,hh,dow,dd,MM,yyyy-BE-u16]` | |
| 0x2E | u16 BE (VR9) | trailing u16 after the name: usually 1 on live records, but small values 2–23 also occur on fully live records; role TBD (§12) | last VR9 field |
| 0x30 | 16 B (VR5) | ASCII: 12-char track/event name + 4 uppercase-hex event-ID digits | see note below |

**The 0x30 marker is not an invariant.** The 12-char name portion is the track/event name: the
*default* renders as `V.T XX- V` (two-digit tracks as `V.T10- 1`, no space), but user-assigned
names (`Strings outr`, `Bass`) and all-spaces names occur on live records (≈12 % of records on
verified media). Never gate parsing on a `V.T` prefix (§6.1); only the trailing 4 hex digits are
structural.

**The take-reference fields are cluster numbers, not opaque IDs** (verified by HDD↔CD diff and by
arithmetic against take file sizes):
- 0x14 = first cluster of the take. On **HDD** this is the take's *filename* number
  (`TAKE%04X`, §4.3) — the FAT start cluster at recording time, which after Song Copy is no
  longer the file's actual start cluster; resolve by filename, never by following the FAT from
  this value directly (§4.3). On **CD (VR5)** it is the take's position in the archive's
  sequential cluster space — likewise the archive filename (`TAKE%04X`) — which is why VR5 CD
  FileIDs are dense and ordered. **VR9 CDs keep the HDD numbers** (§5.7), so their FileIDs are
  neither dense nor ordered.
- 0x16 = last cluster; 0x18 = cluster count; take file size = count × clusterBytes
  (clusterBytes = the BPB cluster size on HDD (§4.2), 0x8000 on CD (§5.4); 32 KB on all
  verified media).
- 0x1A/0x1C = 0x14/0x16 shifted by the trim, in clusters. The 0x1A formula is media-verified on
  MTP solo takes (including a 556,322-frame trim); for MT2 the per-page pad bytes (§2) mean
  `TrimmedFrames × 12` slightly overstates the true on-disk byte position at large trims, so
  the formula is approximate there (unverified). Neither field is needed for extraction.

**VR9 v-track code** (byte at 0x20): `code = (physicalTrack−1) × 8 + (vTrack−1)` (8 v-tracks).
VR5 encodes the analogous value as `(track−1) × 16 + (vTrack−1)` at 0x22 (16 v-tracks). Byte 0x21
on VR9 is a flag: 0 = normal, 1 = tombstone/erase record (and appears on FileID=0 silence events).

**Simultaneously-recorded takes interleave clusters.** A stereo pair recorded together occupies
one interleaved chain (L = X, X+2, X+4…; R = X+1, X+3…), so for each the end−start delta is
≈ 2×count−2 and the two FileIDs are adjacent. Solo takes have delta = count−1. (The exact
allocation rule for >2 simultaneous tracks is not yet pinned down.) On HDD the FAT chain resolves
the interleave transparently; on CD each take is already a contiguous file — but the interleaved
*numbering* persists in rewritten VR5 CD records (stereo records still show delta ≈ 2×count−2
in the archive cluster space), so the delta is not a contiguity check on CD.

---

## 8. Extraction algorithm

Common: parse SONG for sample rate + format code; enumerate takes; decode each referenced take
(§2, remembering MT2 cluster padding and native/no-swap byte order per source); build one output
buffer per (track, v-track); for each event copy `(EndFrame−StartFrame)×16` samples starting
`TrimmedFrames×16` into the take, placed at `StartFrame×16` (minus origin) in the buffer; fill gaps
with silence; write WAV (optionally interleave detected stereo pairs, §8.4). If an event's span
exceeds the take audio available after the trim (truncated chain, incomplete backup — §8.3,
§10), emit the samples that exist, pad the rest of the span with silence, and warn; treat a
degenerate record (EndFrame ≤ StartFrame) as empty and warn.

### 8.1 VS-1880 (timeline is explicit)

Use the V-track table (CD, §6.1) or track data (HDD, §4.5) directly — each v-track's records are
the final timeline. Iterate the records in **stored order** and let each write its `[Start,End)`
range over whatever is there — later records win on overlap (punch-in). Use **every** record in
the table and filter on nothing: the counter field at 0x10 is meaningless here — live table
records commonly carry counter = 0xFFFF (every live record of some tracks, on real media) — and
the 0x1E field, though 1 on most records, is **0 on every record of some copied songs** (§7).
Filtering on either silently drops entire tracks or songs.

### 8.2 VS-880EX (reconstruct from the log)

Replay the log (§4.6 / §6.2) in record order — the file is already a creation-order log, which
the counter field tracks; do **not** sort by counter (the 0xFFFF caveat below would send a live
first record to the end). Per v-track code: each record writes its `[Start,End)` range over
whatever was there (later wins). A record with FileID = 0 (flag byte
0x21 = 1) writes silence (erase). This yields the same later-wins/punch-in semantics as VR5.

Replay caveats (edge cases observed on real media):

- A song's **first** log record sometimes carries counter = 0xFFFF while clearly live; do not
  discard the first record on counter value alone.
- Flag byte 0x21 = 1 also appears on some records with a non-zero FileID; the full semantics are
  unpinned (§12). Treating such records as normal writes has matched audible reality so far.

### 8.3 Take/FAT integrity check (HDD)

Field 0x18 gives the take's expected cluster count without following the chain, so a truncated
FAT chain can be detected up front: resolve the take file by name (`TAKE%04X`, §4.3), follow the
FAT chain from the **directory entry's** first cluster, and if it yields fewer clusters than
0x18 claims (equivalently, if the directory file size ≠ 0x18 × clusterBytes), the take is
corrupt and the extractor should warn rather than emit silence. (On songs never copied, the
directory start cluster equals 0x14 and chaining from 0x14 coincides; on copied songs it does
not — §4.3.)

### 8.4 Stereo pair detection (optional, heuristic)

Two adjacent physical tracks are treated as a stereo pair when they have identical event counts
and every event matches StartFrame and EndFrame across the pair. Output as one interleaved
stereo WAV (left = lower-numbered track). The heuristic can miss pairs edited asymmetrically and
can false-positive on aligned bounced stems; make it switchable.

---

## 9. Song Optimize remnants

"Song Optimize" compacts event tables and deletes orphaned takes but does **not** securely erase:
old records survive after the live data (counter = 0xFFFF, references to takes no longer present,
intact timestamps). On CD this appears as extra `"V.T…"`-shaped bytes after the 288-entry table
(VR5) or after the live count (VR9). Parsers must bound reads (exactly 288 VR5 entries; exactly N
VR9 records) and treat FileIDs with no matching take as remnants.

---

## 10. Backup completeness

A catalogued song is not guaranteed to be fully present. Of 81 songs across 12 backup sets, 79
resolve every referenced take; 2 (each the last song of a set on partially-provided discs)
reference takes that run past the last available disc — genuine incomplete backups, detectable
because a take's data is truncated at end-of-last-disc, or a referenced FileID has no take file.

**Every finalized disc — mid-set discs included — ends with a run of TDI filler frames**
(126–148 observed; the length varies per disc): each filler frame's 2048-byte user data is the
13-byte signature `54 44 49 01 50 01 01 01 01 80 FF FF FF` (`TDI…`) followed by zeros, and the
run starts on a 0x8000 user-data boundary and continues to the end of the disc (verified on
every complete disc in the corpus, both machines). The filler start — not the end of the dump —
is the end of the disc's burned file data, and it is load-bearing for multi-disc reading: §5.4's
chain walk terminates on it and §5.6's spanning remainder is computed against it.

Diagnostics that follow: a dump **lacking** the trailing filler is a truncated/incomplete rip
(two such rips exist in the verification corpus, one of which also carries frames with invalid
MODE1 EDC — checking EDC per frame is a cheap additional damage detector on raw dumps). A set
whose final disc lacks the filler and whose last file is truncated is missing a disc.

---

## 11. Reference implementations

- Randy Gordon's **rdac** (github.com/randygordon/rdac, LGPL) — the original C/Go RDAC decoders
  from which Appendix A derives.
- **VS Wave Export** by Kyle J. Smith (http://www.thegoodlibrary.com/VSWE/, VB6 source) — an
  independent earlier decoder, useful for cross-checking.
- Microsoft, **FAT32 File System Specification** (*fatgen103.doc*) — full reference for the
  standard FAT16/BPB/directory structures; §4.2 reproduces the subset a VS extractor needs, so
  this is only required for edge cases beyond that summary.
- In this repository: `internal/rdac/` (sample-exact Go port of the Appendix A codecs, including
  `DecodeMT2Cluster` page-padding handling), `analysis/tools/vs_archive.py` (streaming raw-.bin
  CD reader: enumeration, validation, multi-disc spanning, `read_file`),
  `analysis/tools/matrix.py` (event parsing + take-resolution harness; source of the §10 counts),
  `analysis/tools/vr9_extract_song.py` (VR9 log-replay extractor prototype),
  `cmd/hdd-extract-file` / `cmd/hdd-ls` (HDD image file access with the Roland swap), and
  `cmd/decode-mt2` (CLI over the MT1/MT2 decoders).

---

## 12. Remaining open questions (do not block extraction)

- Exact roles of record fields 0x0C/0x0E (trim sub-fields), 0x1E (the Valid-like field — 1 on
  most VR5 timeline records, 0 on whole copied songs and on all VR9 logs, §7), VR5 0x24, and
  VR9 0x2E (usually 1 but 2–23 seen on live records).
- VR9 flag byte (0x21) full semantics; meaning of counter = 0xFFFF on a song's first log record
  (see §8.2 caveats for the safe handling).
- Whether a VR5 timeline record can carry take cluster 0 (a silence/erase event, as VR9's
  FileID = 0 records are) — none observed; every verified VR5 timeline record references a real
  take.
- Cluster-allocation stride for >2 simultaneously-recorded takes.
- The 4-byte version/flags pair carried from SONG into catalogs (second u16 typically `00 0X`,
  first u16 varies widely; values vary per song).
- The `SONG.VRx` +0x04 number ("source folder number", §4.4) is neither unique nor stable —
  three songs on one verified VS-880EX partition claim number 7, and one song's number changed
  (5 → 2) between an archive snapshot and a later save of the same song. Its exact semantics
  are unknown; never key song identity on it.
- The VR9 header-block tag at +0x164C (constant within a song, but the same value recurs across
  songs and across unrelated backup sets; only two values observed) and the per-song u16 at
  +0x161C / the udoff-0xFCA table (≈ song size in 0x8000 blocks; exact formula unknown).
- Ear-verification that `0x02` songs extract correctly at 32 kHz (§3); the only such media in
  the corpus is a truncated rip.
- Whether a spanning remainder that is not 0x8000-aligned would be padded to a boundary before
  the next header (§5.6): every observed remainder is already an exact 0x8000 multiple, so the
  question has no test case; files spanning more than two discs are likewise unobserved.
- MT2 padding on 64 KB clusters (§2): the reference decoder asserts 5460 blocks + 16 pad bytes,
  but 5461 whole blocks would fit; no 64 KB-cluster MT2 media in the verification set.
- The catalog/header sample-rate byte (§5.3/§5.4): observed values are `0x01` (44.1 kHz),
  `0x02` (32 kHz per the §3 encoding — the four affected songs' audio has not been
  rate-verified by listening), and a single `0x41` (low nibble 1; the 0x40 bit's meaning is
  unknown). Plain 48 kHz (`0x00`) media remains unobserved.
- The `SONG.VR5` trailing u16 at +0x24 (§4.4): `0x0000`/`0x0300`/`0x0500`/`0x0600` observed;
  role unknown.
- M24 page-padding behavior (§2) — no M24 media in the verification set.
- The MBR partition-offset group 286/302/318/334 (§4.1): asserted by the same layout rule as
  the verified 382–430 group, but unpopulated (all-zero) on all verified media.
- The directory write-stamp constants (§4.2): what the fixed values encode (firmware build
  dates?), and which machine wrote `1998-07-31 11:27:52` — the constant on one of the VS-1880
  image's VR9 partitions (a VS-880, or an earlier VS-880EX firmware).
- Everything VR6/VS-1680: assumed VR5-like, unverified (§1).

---

## Appendix A. RDAC codec — complete decoding specification

### A.1 Provenance and credit

The RDAC decoding algorithm below — the pattern lookup table, the bit-layout strings, the
per-pattern dispatch tables, and the reconstruction rules — derives from **Randy Gordon's**
reference decoder **rdac** (https://github.com/randygordon/rdac, `rdac2wav` and `vs2reaper`;
copyright © 2011–2024 Randy Gordon, randy@integrand.com, licensed LGPL-3.0-or-later), reproduced
here with credit for completeness of this specification. The Go port in `internal/rdac/` has been
verified sample-exact against the reference. **VS Wave Export** (Kyle J. Smith,
http://www.thegoodlibrary.com/VSWE/) is an independent implementation useful for cross-checks.

### A.2 Stream model and decoder state

- The input is a sequence of fixed-size blocks: 16 bytes (MTP, MT1) or 12 bytes (MT2). Each block
  decodes to 16 PCM samples.
- The only inter-block state is `d0`, the previous block's final output sample (`out[15]`),
  initialized to 0 at stream start. `d0` seeds the interpolation of the next block.
- MT2 streams carry cluster-page padding (§2): after every 2730 blocks, skip 8 pad bytes
  (32 KB page; the pad content is arbitrary, not zeros — §2). For 64 KB clusters the reference decoder uses 5460 blocks then 16 pad bytes —
  i.e. two 32 KB pages' worth, **not** the `floor(clusterSize/12)` = 5461 whole blocks that
  would fit; unverified on real media (§2, §12).
- After reconstruction, clamp every sample: MTP to `[−8388608, +8388607]` (signed 24-bit);
  MT1/MT2 to `[−32768, +32767]` (signed 16-bit).

Per-block pipeline:

```
patternIndex = (in[0] & 0xF0) | ((in[2] & 0xF0) >> 4)      // 0..255
patternNumber = LUT[patternIndex]                          // 0..36, table A.3
out[0..15] = 0
unpack differentials per the bit layout for patternNumber  // A.4, A.5
shiftRound(out, shift)                                     // A.6, shift per dispatch table A.7
reconstruct(out, d0)                                       // interpolate2/4/8 or doubleEvens, A.6
clamp; emit out[0..15]; d0 = out[15]
```

The two selector nibbles (high nibbles of bytes 0 and 2) overlap data bits in some layouts; the
LUT is constructed so that only the layout's padding bits influence the selected pattern — data
bits that fall inside the selector nibbles map to the same pattern number.

### A.3 Pattern lookup table (LUT)

256 entries; `patternNumber = LUT[patternIndex]`. Row-major, 16 entries per row (row r covers
indices 16r … 16r+15):

```
row 0  (0x00): 0  0  0  0  1  1  1  1  2  2  2  2  3  3  3  3
row 1  (0x10): 0  0  0  0  1  1  1  1  2  2  2  2  3  3  3  3
row 2  (0x20): 0  0  0  0  1  1  1  1  2  2  2  2  3  3  3  3
row 3  (0x30): 0  0  0  0  1  1  1  1  2  2  2  2  3  3  3  3
row 4  (0x40): 4  4  4  4  5  5  5  5  6  6  6  6  7  7  7  7
row 5  (0x50): 4  4  4  4  5  5  5  5  6  6  6  6  7  7  7  7
row 6  (0x60): 4  4  4  4  5  5  5  5  6  6  6  6  7  7  7  7
row 7  (0x70): 4  4  4  4  5  5  5  5  6  6  6  6  7  7  7  7
row 8  (0x80): 8  8  8  8  9  9  9  9 10 10 10 10 11 11 11 11
row 9  (0x90): 8  8  8  8  9  9  9  9 10 10 10 10 11 11 11 11
row 10 (0xA0): 8  8  8  8  9  9  9  9 10 10 10 10 11 11 11 11
row 11 (0xB0): 8  8  8  8  9  9  9  9 10 10 10 10 11 11 11 11
row 12 (0xC0): 12 12 13 13 14 14 15 15 16 16 17 17 18 18 19 19
row 13 (0xD0): 12 12 13 13 14 14 15 15 16 16 17 17 18 18 19 19
row 14 (0xE0): 20 20 21 21 22 22 23 23 24 24 25 26 27 28 29 30
row 15 (0xF0): 20 20 21 21 22 22 23 23 24 24 31 32 33 34 35 36
```

The same LUT serves MTP, MT1, and MT2; the three formats differ in their dispatch tables (A.7).

### A.4 Bit-layout strings

A layout is written as one character per input bit. For a 16-byte block the string is 128
characters (shown in groups of 8 with spaces for readability); for a 12-byte MT2 block, 96
characters. Group *k* (characters 8k…8k+7) describes input byte *k*, written **MSB first**
(character 0 of a group = bit 7 of the byte, character 7 = bit 0).

Symbols: `p` = padding (bit is ignored by unpacking; padding bits in bytes 0 and 2 host the
selector nibbles); `1`–`9`,`a`–`g` = the bit belongs to output sample 0–15 respectively
(`1`→sample 0 … `g`→sample 15).

16-byte layouts (MTP and MT1):

```
A  = "ppp88888 88888888 pppggggg gggggggg 87777776 66666655 gffffffe eeeeeedd
      55554444 44444333 ddddcccc cccccbbb 33322222 22111111 bbbaaaaa aa999999"
B  = "pp888888 88888887 ppgggggg gggggggf 77777666 66666555 fffffeee eeeeeddd
      55544444 44443333 dddccccc ccccbbbb 33222222 22111111 bbaaaaaa aa999999"
B3 = "ppp88888 88888887 pppggggg gggggggf 77777666 66666555 fffffeee eeeeeddd
      55544444 44443333 dddccccc ccccbbbb 33222222 22111111 bbaaaaaa aa999999"
B4 = "pppp8888 88888887 ppppgggg gggggggf 77777766 66666555 ffffffee eeeeeddd
      55554444 44433333 ddddcccc cccbbbbb 33222222 21111111 bbaaaaaa a9999999"
C  = "ppp88888 88888877 pppggggg ggggggff 77776666 66665555 ffffeeee eeeedddd
      55444444 44443333 ddcccccc ccccbbbb 33222222 22111111 bbaaaaaa aa999999"
D  = "pp888888 88877777 ppgggggg gggfffff 77666666 66555555 ffeeeeee eedddddd
      54444444 44333333 dccccccc ccbbbbbb 32222222 21111111 baaaaaaa a9999999"
E  = "pppp8888 88888877 ppppgggg ggggggff 77776666 66665555 ffffeeee eeeedddd
      55444444 44443333 ddcccccc ccccbbbb 33222222 22111111 bbaaaaaa aa999999"
F  = "pppp8888 88887777 ppppgggg ggggffff 77766666 66655555 fffeeeee eeeddddd
      55444444 44333333 ddcccccc ccbbbbbb 32222222 21111111 baaaaaaa a9999999"
```

12-byte layouts (MT2):

```
12A = "pp888888 88888777 ppgggggg gggggfff 76666665 55544444 feeeeeed dddccccc
       44333322 22221111 ccbbbbaa aaaa9999"
12B = "pp888888 87777766 ppgggggg gfffffee 66665555 54444444 eeeedddd dccccccc
       33333222 22211111 bbbbbaaa aaa99999"
12C = "pppp8888 88777776 ppppgggg ggfffffe 66666555 55444444 eeeeeddd ddcccccc
       33333222 22211111 bbbbbaaa aaa99999"
12D = "pppp8888 88887777 ppppgggg ggggffff 66666655 55444444 eeeeeedd ddcccccc
       44333322 22221111 ccbbbbaa aaaa9999"
12E = "ppp88888 88887777 pppggggg ggggffff 66666655 55444444 eeeeeedd ddcccccc
       44333322 22221111 ccbbbbaa aaaa9999"
12F = "ppp88888 88888887 pppggggg gggggggf 77766666 55554444 fffeeeee ddddcccc
       44433332 22221111 cccbbbba aaaa9999"
12G = "ppp88888 88888777 pppggggg gggggfff 76666665 55544444 feeeeeed dddccccc
       44333322 22221111 ccbbbbaa aaaa9999"
```

(12A and 12G differ only in byte 0 — `pp888888` vs `ppp88888`; compare character-by-character
when transcribing.)

### A.5 Unpacking algorithm

Each output sample's differential is assembled LSB-first in a fixed scan order, then
sign-extended. Pseudocode for a block of `N` bytes (16 or 12):

```
bitCount[0..15] = 0            // bits collected so far per sample
out[0..15] = 0

for byteIdx = N−1 down to 0:                 // last byte first
    group = layout[8*byteIdx : 8*byteIdx+8]  // 8 chars, MSB-first
    for bit = 0 to 7:                        // LSB of the byte first
        symbol = group[7 − bit]
        if symbol == 'p': continue
        s = sampleIndexOf(symbol)            // '1'→0 … 'g'→15
        if (in[byteIdx] >> bit) & 1:
            out[s] |= 1 << bitCount[s]
        bitCount[s] += 1

for s = 0 to 15:                             // sign-extend at the top bit
    signBit = bitCount[s] − 1
    out[s] = −(out[s] & (1 << signBit)) | out[s]
```

The scan order (bytes descending, bits ascending within each byte) determines bit significance:
the first bit encountered for a sample is its LSB, the last is its MSB/sign bit. The bit width of
each sample is simply the number of occurrences of its symbol in the layout.

### A.6 Reconstruction primitives

**shiftRound(out, n)** — scale differentials, with a rounding bias (skip entirely when the
dispatch table gives no shift):

```
for s in 0..15: out[s] = (out[s] << n) | (1 << (n−1))
```

**interpolate(a, b)** = `floor((a + b) / 2)` (round toward −∞, not toward zero).

The interpolation stages add each sample's decoded differential to a prediction interpolated
from already-final samples. The `+=` order below is normative — later lines consume results of
earlier lines. `d0` is the previous block's final sample.

**interpolate2** (anchors: samples 7, 15):

```
out[3]  += interpolate(d0,      out[7])
out[1]  += interpolate(d0,      out[3])
out[5]  += interpolate(out[3],  out[7])
out[11] += interpolate(out[7],  out[15])
out[9]  += interpolate(out[7],  out[11])
out[13] += interpolate(out[11], out[15])
out[0]  += interpolate(d0,      out[1])
out[2]  += interpolate(out[1],  out[3])
out[4]  += interpolate(out[3],  out[5])
out[6]  += interpolate(out[5],  out[7])
out[8]  += interpolate(out[7],  out[9])
out[10] += interpolate(out[9],  out[11])
out[12] += interpolate(out[11], out[13])
out[14] += interpolate(out[13], out[15])
```

**interpolate4** (anchors: samples 3, 7, 11, 15):

```
out[1]  += interpolate(d0,      out[3])
out[5]  += interpolate(out[3],  out[7])
out[9]  += interpolate(out[7],  out[11])
out[13] += interpolate(out[11], out[15])
out[0]  += interpolate(d0,      out[1])
out[2]  += interpolate(out[1],  out[3])
out[4]  += interpolate(out[3],  out[5])
out[6]  += interpolate(out[5],  out[7])
out[8]  += interpolate(out[7],  out[9])
out[10] += interpolate(out[9],  out[11])
out[12] += interpolate(out[11], out[13])
out[14] += interpolate(out[13], out[15])
```

**interpolate8** (anchors: all odd samples):

```
out[0]  += interpolate(d0,      out[1])
out[2]  += interpolate(out[1],  out[3])
out[4]  += interpolate(out[3],  out[5])
out[6]  += interpolate(out[5],  out[7])
out[8]  += interpolate(out[7],  out[9])
out[10] += interpolate(out[9],  out[11])
out[12] += interpolate(out[11], out[13])
out[14] += interpolate(out[13], out[15])
```

**doubleEvens** (used instead of interpolation by pattern 30; the reference code names it
`doubleOdds`, counting samples 1-based):

```
for s in {0, 2, 4, 6, 8, 10, 12, 14}: out[s] <<= 1
```

### A.7 Dispatch tables

For each pattern number: which bit layout to unpack with (A.4), the shiftRound amount (`—` =
no shift and no rounding bias), and the reconstruction stage (A.6). `n/a` = pattern number never
observed in that format; a robust decoder should treat it as a decode error (or emit silence)
rather than crash.

**MTP** (16-byte block → 24-bit samples; clamp ±24-bit):

| # | layout | shift | reconstruct | | # | layout | shift | reconstruct |
|---|---|---|---|---|---|---|---|---|
| 0 | B | 6 | interpolate2 | | 19 | C | 8 | interpolate2 |
| 1 | B | 7 | interpolate2 | | 20 | C | 9 | interpolate2 |
| 2 | B | 8 | interpolate2 | | 21 | C | 10 | interpolate2 |
| 3 | B | 9 | interpolate2 | | 22 | C | 11 | interpolate2 |
| 4 | B | 10 | interpolate2 | | 23 | C | 12 | interpolate2 |
| 5 | B | 11 | interpolate2 | | 24 | C | 13 | interpolate2 |
| 6 | D | 10 | interpolate4 | | 25 | F | 12 | interpolate8 |
| 7 | D | 11 | interpolate4 | | 26 | F | 13 | interpolate8 |
| 8 | D | 12 | interpolate4 | | 27 | F | 14 | interpolate8 |
| 9 | D | 13 | interpolate4 | | 28 | F | 15 | interpolate8 |
| 10 | D | 14 | interpolate4 | | 29 | F | 16 | interpolate8 |
| 11 | D | 15 | interpolate4 | | 30 | F | 16 | doubleEvens |
| 12 | A | 5 | interpolate2 | | 31 | E | 14 | interpolate4 |
| 13 | A | 6 | interpolate2 | | 32 | B4 | 4 | interpolate2 |
| 14 | A | 7 | interpolate2 | | 33 | B4 | 5 | interpolate2 |
| 15 | A | 8 | interpolate2 | | 34 | B4 | — | interpolate2 |
| 16 | A | 9 | interpolate2 | | 35 | B4 | 2 | interpolate2 |
| 17 | A | 10 | interpolate2 | | 36 | B4 | 3 | interpolate2 |
| 18 | B3 | 12 | interpolate2 | | | | | |

**MT1** (16-byte block → 16-bit samples; clamp ±16-bit). Same layouts as MTP with every shift 8
lower (the 24→16-bit difference); patterns 0–1, 12–14, and 32–36 do not occur:

| # | layout | shift | reconstruct | | # | layout | shift | reconstruct |
|---|---|---|---|---|---|---|---|---|
| 0–1 | n/a | | | | 19 | C | — | interpolate2 |
| 2 | B | — | interpolate2 | | 20 | C | 1 | interpolate2 |
| 3 | B | 1 | interpolate2 | | 21 | C | 2 | interpolate2 |
| 4 | B | 2 | interpolate2 | | 22 | C | 3 | interpolate2 |
| 5 | B | 3 | interpolate2 | | 23 | C | 4 | interpolate2 |
| 6 | D | 2 | interpolate4 | | 24 | C | 5 | interpolate2 |
| 7 | D | 3 | interpolate4 | | 25 | F | 4 | interpolate8 |
| 8 | D | 4 | interpolate4 | | 26 | F | 5 | interpolate8 |
| 9 | D | 5 | interpolate4 | | 27 | F | 6 | interpolate8 |
| 10 | D | 6 | interpolate4 | | 28 | F | 7 | interpolate8 |
| 11 | D | 7 | interpolate4 | | 29 | F | 8 | interpolate8 |
| 12–14 | n/a | | | | 30 | F | 8 | doubleEvens |
| 15 | A | — | interpolate2 | | 31 | E | 6 | interpolate4 |
| 16 | A | 1 | interpolate2 | | 32–36 | n/a | | |
| 17 | A | 2 | interpolate2 | | | | | |
| 18 | B3 | 4 | interpolate2 | | | | | |

**MT2** (12-byte block → 16-bit samples; clamp ±16-bit; 12-byte layouts; remember page padding,
A.2). Patterns 12 and 32–36 do not occur:

| # | layout | shift | reconstruct | | # | layout | shift | reconstruct |
|---|---|---|---|---|---|---|---|---|
| 0 | 12A | — | interpolate2 | | 19 | 12E | 2 | interpolate2 |
| 1 | 12A | 1 | interpolate2 | | 20 | 12E | 3 | interpolate2 |
| 2 | 12A | 2 | interpolate2 | | 21 | 12E | 4 | interpolate2 |
| 3 | 12A | 3 | interpolate2 | | 22 | 12E | 5 | interpolate2 |
| 4 | 12A | 4 | interpolate2 | | 23 | 12E | 6 | interpolate2 |
| 5 | 12A | 5 | interpolate2 | | 24 | 12E | 7 | interpolate2 |
| 6 | 12B | 4 | interpolate4 | | 25 | 12C | 6 | interpolate8 |
| 7 | 12B | 5 | interpolate4 | | 26 | 12C | 7 | interpolate8 |
| 8 | 12B | 6 | interpolate4 | | 27 | 12C | 8 | interpolate8 |
| 9 | 12B | 7 | interpolate4 | | 28 | 12C | 9 | interpolate8 |
| 10 | 12B | 8 | interpolate4 | | 29 | 12C | 10 | interpolate8 |
| 11 | 12B | 9 | interpolate4 | | 30 | 12C | 10 | doubleEvens |
| 12 | n/a | | | | 31 | 12D | 8 | interpolate4 |
| 13 | 12F | — | interpolate2 | | 32–36 | n/a | | |
| 14 | 12F | 1 | interpolate2 | | | | | |
| 15 | 12F | 2 | interpolate2 | | | | | |
| 16 | 12F | 3 | interpolate2 | | | | | |
| 17 | 12F | 4 | interpolate2 | | | | | |
| 18 | 12G | 6 | interpolate2 | | | | | |

### A.8 Uncompressed formats

M16 and M24 are plain little-endian signed PCM, framed only nominally (32/48 bytes = 16 samples):
read 16-bit or 24-bit LE samples directly. No pattern decoding, no interpolation, no `d0` state.
See the §2 M24 caveat regarding possible page padding.

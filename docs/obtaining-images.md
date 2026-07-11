# Obtaining VS-series images

`vsx` extracts from **images you make yourself** off the original media — it
does not talk to the recorder or the drive. This page covers how to produce
images `vsx` can read, and — just as important — how to tell whether a rip is
trustworthy, because the ripping chain will not tell you on its own.

## What `vsx` needs

| Source | Give `vsx` | Not this |
|---|---|---|
| HDD (VS-1880 / VS-880EX live disk) | a **full raw disk image** — every byte from LBA 0, so the Roland MBR is at the front | a single extracted partition |
| CD "Song Copy Archive" | a **raw, 2352-byte-frame** dump per disc (`.bin`) | a cooked / `dd` / `.iso` (2048-byte user-data only) dump |

Raw matters because `vsx` reads the media *structurally* — the Roland 12-partition
MBR and byte-swapped FAT16 (§4), the CD header-block walk and multi-disc spanning
(§5), and the trailing TDI filler that marks a disc's real data end (§10). A cooked
CD dump has thrown that framing away: `vsx` will still attempt it, but it reports a
`§5` cooked-rip deviation and cannot validate geometry, spanning, or per-sector
integrity. Rip raw.

## HDD images (VS-1880, VS-880EX)

Pull the drive from the recorder and attach it to a Mac/Linux host with an
IDE-to-USB adapter (any generic one works; a Cables-to-Go IDE-USB adapter was
used for the reference corpus).

1. Make sure the OS has **not mounted** the drive (macOS: open Disk Utility and
   confirm it is not mounted; do not "Initialize"). The Roland filesystem is not
   one macOS understands, so it usually won't mount — but check.
2. Find the **raw** device node:
   ```sh
   diskutil list          # macOS  — note the /dev/diskN of the VS drive
   lsblk                   # Linux  — note /dev/sdX
   ```
3. Image the **whole disk** to a file (use the `r`aw node on macOS — `rdiskN` — for
   speed):
   ```sh
   # macOS
   sudo dd if=/dev/rdisk4 of="$HOME/VSX/vs-1880.img" bs=1m status=progress
   # Linux
   sudo dd if=/dev/sdb    of="$HOME/VSX/vs-1880.img" bs=1M status=progress
   ```

> ⚠️ **Check the device number twice.** `dd` writing (or reading the wrong `if=`)
> against the wrong node can destroy another disk. Confirm the size and identity
> in `diskutil list` / `lsblk` before running, and never point `of=` at a device.

Roland VS drives are small by modern standards, but the VS-1880 image in the
reference corpus is ~60 GB — make sure you have the space.

## CD "Song Copy Archive" dumps

Rip each disc raw with `cdrdao` (any MMC drive; the reference corpus used an Apple
USB SuperDrive):

```sh
cdrdao read-cd --read-raw --driver generic-mmc \
       --datafile vs-cd-2.bin vs-cd-2.toc
```

- `--read-raw` is required — it writes full 2352-byte sectors (user data **plus**
  the EDC/ECC), which is what `vsx` reads and what makes later integrity checks
  possible. `vsx` uses the `.bin`; the `.toc` is not needed.
- **Multi-disc sets:** rip every disc of the set (`vs-cd-4a.bin`, `vs-cd-4b.bin`, …),
  then either put them in one directory and point `vsx` at the directory, or name
  the disc files together on the command line
  (`vsx --list vs-cd-4a.bin vs-cd-4b.bin`). Either way `vsx` groups discs by the
  set ID in each disc's header and orders them by disc index — filenames and
  argument order don't matter (§5.6). A set needs *all* its discs present to
  reconstruct takes that span a disc boundary.

## Read errors, ECC, and verifying your rip

This is the part the tools won't do for you. **`cdrdao` and the drive do not verify
the CD-ROM data EDC**, so the error you *see* and the error that actually matters
are usually different things.

### The benign error you'll probably see

`cdrdao` frequently ends a raw read with something like:

```
ERROR: SCSI command failed:
ERROR:   sense key 0x5: ILLEGAL REQUEST.
ERROR:   additional sense code: 0x26          (invalid field in parameter list)
ERROR: Read error while copying data from track.
```

Sense key `0x5` / ASC `0x26` means the **drive rejected a parameter of the raw
`READ CD` command** — a well-known quirk of some USB drives (the Apple SuperDrive
among them) for raw / sub-channel reads, typically as it runs off the end of the
track. It is a *command* rejection, not a *media* read failure, and it usually
leaves the bulk track data intact. Don't panic at it — but don't take it as proof
the rip is good either. Verify independently (below).

### The dangerous error you *won't* see

A physically degraded sector passes through the whole chain **silently**. The drive
applies its low-level CIRC correction and hands back the raw sector; `cdrdao` does
not run or check the CD-ROM L-EC (the P/Q Reed-Solomon + EDC in the sector); so a
sector whose data is wrong gets written into your `.bin` with no error raised. It
then decodes to a burst of noise in one track — about one 2048-byte sector ≈ tens
of milliseconds of audio. (One such sector exists in the reference corpus:
`vs-cd-6a.bin` frame 313043, audible as a ~63 ms noise burst in one v-track.)

### How to verify a rip

- **Run `vsx` and read stderr.** Best-effort mode reports a `§5` deviation for a
  cooked rip, a `§10` deviation when a disc is missing its trailing TDI filler
  (a **truncated** rip — re-rip it), and a `§10` per-frame EDC deviation naming
  the exact frame (or contiguous frame run) of any raw sector whose stored MODE1
  EDC does not match its bytes — the physically corrupt sector the ripping chain
  passes through silently. That last check runs only on a raw dump; a cooked rip
  has thrown the EDC away.
- **Check EDC independently.** A raw-image EDC/ECC checker such as
  [`edccchk`](https://github.com/claunia/edccchk) reports which sectors fail EDC,
  as MSF timestamps. Do **not** run an EDC/ECC *regenerator* (e.g. `ecm`-style
  tools, ECCScan) on a suspect image — those recompute the parity to match the
  *current* bytes, which makes a corrupt sector "pass" and freezes the damage in.

### If a sector is bad

Recovery is limited and often impossible from a single dump on a single drive:

- The parity is preserved in a raw dump, so software L-EC (RSPC) correction *can*
  in principle repair a sector — but only within the code's capacity, and a
  `--read-raw` dump carries no C2 error pointers, so the correction is the weaker
  blind decode. The one tool that actually does this correction is **IsoBuster**
  (Windows; run under CrossOver/Wine or a VM — there is no macOS build and no Go
  library for it). If IsoBuster can't recover it, no software will on the same
  bytes.
- The real fix is a **cleaner physical read**: clean the disc and re-rip, ideally
  on a *different* drive (different optics/firmware often reads a marginal sector
  that one drive can't). With only one drive this option is off the table.
- Otherwise it's a small, localized loss — flagged, not silent, which is the point.

## See also

- [`ROLAND-VS-FORMAT-SPEC.md`](../ROLAND-VS-FORMAT-SPEC.md) §4 (HDD), §5 (CD),
  §5.6 (spanning), §10 (backup completeness, TDI filler, EDC as a damage detector).
- [ADR-0002](./adr/0002-best-effort-and-strict-modes.md) — best-effort vs strict
  postures, i.e. how `vsx` reports vs gates on the deviations described here.

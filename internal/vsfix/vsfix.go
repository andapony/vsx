// Package vsfix builds synthetic Roland VS-1880 (VR5) and VS-880EX (VR9) "Song
// Copy Archive" CD dumps in memory, byte-for-byte against
// ROLAND-VS-FORMAT-SPEC.md, so the extraction pipeline can be tested without the
// out-of-repo media corpus (ADR-0005). It is fixture support imported from
// _test.go files across packages; it is deliberately a normal package (not
// _test) so several test packages can share it.
//
// This file is the VR9 builder; vr5.go is its VS-1880 counterpart, and setfix.go
// / setfix_vr5.go cut a single-disc archive into a multi-disc backup set across a
// §5.6 junction. Each single-disc builder emits an index-0 archive: an archive
// header block (§5.2) whose per-file area is a stale-but-plausible entry (§5.5
// case 1), a second header copy (§5.5 case 2), then each song's files laid out
// contiguously — every file preceded by its own 0x8000 header block (§5.4) — with
// a marker "song-boundary" block (§5.5 case 3) between songs, and a trailing TDI
// filler run (§10, omittable for a truncated-rip fixture). Exercising all three
// §5.5 rejection cases is the point: a correct walker must skip them.
package vsfix

import (
	"encoding/binary"
)

const (
	blockSize = 0x8000 // archive allocation + MT2 page unit (§5.4)
	frameSize = 2352   // raw MODE1 frame
	udPerFR   = 2048   // user-data bytes per frame
)

// VR9Sig is the VS-880EX archive signature (§1/§5.2), 32 bytes.
var VR9Sig = []byte("VS-8EXECR02 Song Copy Archives  ")

// fillerSig is the §10 TDI filler-frame signature.
var fillerSig = []byte{0x54, 0x44, 0x49, 0x01, 0x50, 0x01, 0x01, 0x01, 0x01, 0x80, 0xFF, 0xFF, 0xFF}

// Take is one take audio file: its FileID (= HDD FAT start cluster, which VR9
// keeps on CD, §5.7 — the number events reference at record offset 0x14) and
// the raw native-order MT2 stream that is its file content (§5.7: CD content is
// not byte-swapped).
type Take struct {
	FileID uint16
	Name   string // 8-char base, e.g. "TAKE0C53"; the "VR9" extension is added
	MT2    []byte
}

// Event is one 48-byte VR9 event-log record (§7): a placement of a take's audio
// on the timeline. Frames are pre-origin values as stored on media (VR9 origin
// is 12, §3). FileID 0 with Tombstone writes silence (erase, §8.2).
type Event struct {
	Start, End, Trimmed uint32
	FileID              uint16
	Track, VTrack       int // 1-based physical track / v-track
	Tombstone           bool
	Name                string // 12-char track/event name
}

// Song groups an event log and its takes under one source SONG number and name.
type Song struct {
	Number uint16
	Name   string
	Events []Event
	Takes  []Take
	// OmitEventList drops the EVENTLST file from the song's on-disc files (§5.4),
	// the "no event list found; nothing to extract" deviation both List and
	// Extract must report.
	OmitEventList bool
}

// Disc describes a single-disc, index-0 VR9 archive to synthesize.
type Disc struct {
	SetID    [4]byte
	Songs    []Song
	NoFiller bool // omit the trailing TDI filler run (a truncated-rip fixture, §10)
}

// BuildRaw returns a raw 2352-byte-frame CD dump of the described archive.
func (d Disc) BuildRaw() []byte {
	return wrapRaw(d.userData())
}

// BuildCooked returns a "cooked" (2048-byte-sector) dump: the raw user-data
// stream with no frame wrapper, as a dd/ISO rip would produce (§5).
func (d Disc) BuildCooked() []byte {
	return d.userData()
}

// userData assembles the concatenated user-data stream (§5.1) of the archive.
func (d Disc) userData() []byte {
	var ud []byte

	// Block 0: archive header (§5.2). Its per-file metadata area carries a
	// stale, plausible file entry (§5.5 case 1); the block is rejected as a
	// file header only by the index-0 and +0x8000-is-signature checks.
	hdr := d.archiveHeaderBlock()
	writeVR9FileFields(hdr, staleEntry(d))
	ud = append(ud, hdr...)

	// Block 0x8000: a second archive-header copy (§5.5 case 2) — signature
	// present, filename field left zero so the name check rejects it.
	ud = append(ud, d.archiveHeaderBlock()...)

	// Files, contiguous in source order, with a boundary block before every
	// song after the first (§5.4/§5.5 case 3).
	for si, song := range d.Songs {
		if si > 0 {
			ud = append(ud, boundaryBlock(d, song)...)
		}
		files := song.files()
		for fi, f := range files {
			hb := d.archiveHeaderBlock() // every file header block is a full catalog copy (§5.4)
			writeVR9FileFields(hb, fileEntry(song, f, fi, len(files)))
			ud = append(ud, hb...)
			ud = append(ud, padBlocks(f.data)...)
		}
	}

	// Trailing TDI filler run (§10), block-aligned. Omitted for a
	// truncated-rip fixture, which the walk must flag as an incomplete dump.
	if !d.NoFiller {
		for i := 0; i < 2; i++ { // two blocks' worth of filler frames
			ud = append(ud, fillerBlock()...)
		}
	}
	return ud
}

// file is one on-disc file: its per-file header fields plus raw data.
type file struct {
	name   string // 11-char CD filename (8+3), e.g. "EVENTLSTVR9"
	fileID uint16
	data   []byte
}

// files returns a song's on-disc files in source-directory order: the event
// list first, then the takes.
func (s Song) files() []file {
	var fs []file
	if !s.OmitEventList {
		fs = append(fs, file{name: "EVENTLSTVR9", fileID: 0, data: s.eventLog()})
	}
	for _, t := range s.Takes {
		fs = append(fs, file{name: pad8(t.Name) + "VR9", fileID: t.FileID, data: t.MT2})
	}
	return fs
}

// eventLog encodes the VR9 event log file (§6.2/§4.6): a u16 BE live count then
// that many 48-byte records.
func (s Song) eventLog() []byte {
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, uint16(len(s.Events)))
	for _, e := range s.Events {
		out = append(out, e.record()...)
	}
	return out
}

// record encodes one 48-byte VR9 event record (§7).
func (e Event) record() []byte {
	r := make([]byte, 48)
	binary.BigEndian.PutUint32(r[0x00:], e.Start)
	binary.BigEndian.PutUint32(r[0x04:], e.End)
	binary.BigEndian.PutUint32(r[0x08:], e.Trimmed)
	binary.BigEndian.PutUint16(r[0x14:], e.FileID) // take start cluster = FileID
	binary.BigEndian.PutUint16(r[0x16:], e.FileID) // take end cluster (solo)
	binary.BigEndian.PutUint16(r[0x18:], 1)        // cluster count
	r[0x20] = byte((e.Track-1)*8 + (e.VTrack - 1)) // v-track code (§7)
	if e.Tombstone {
		r[0x21] = 1
	}
	copy(r[0x22:0x22+12], pad12(e.Name))
	return r
}

// archiveHeaderBlock builds a 0x8000 block that opens with the archive
// signature and header fields (§5.2). Every file header block is a full header
// copy, so this is the base for both.
func (d Disc) archiveHeaderBlock() []byte {
	b := make([]byte, blockSize)
	copy(b, VR9Sig)
	copy(b[0x20:0x24], d.SetID[:])
	binary.BigEndian.PutUint16(b[0x24:], uint16(len(d.Songs))) // song count (set-wide)
	binary.BigEndian.PutUint16(b[0x26:], 0)                    // disc index 0
	binary.BigEndian.PutUint16(b[0x28:], 1)                    // total discs
	// Song catalog (§5.3): 20-byte VR9 entries at 0x2A + 20k.
	for k, s := range d.Songs {
		off := 0x2A + 20*k
		binary.BigEndian.PutUint16(b[off+0x04:], s.Number)
		copy(b[off+0x06:off+0x06+12], pad12(s.Name))
		b[off+0x12] = 0x01 // rate 44.1 kHz
		b[off+0x13] = 0x01 // format MT2
	}
	return b
}

// vr9Fields are the per-file header fields written into a header block's
// metadata area (§5.4, VS-880EX table).
type vr9Fields struct {
	songNumber uint16
	songName   string
	fileCount  uint16
	fileIndex  uint16
	filename   string // 11-char CD name
	blockCount uint16
	marker     uint16 // 0 real / 1 song-start marker
	fileID     uint16
	size       uint32
}

// writeVR9FileFields writes the §5.4 VS-880EX per-file fields into a header
// block at their fixed offsets.
func writeVR9FileFields(b []byte, f vr9Fields) {
	binary.BigEndian.PutUint16(b[0x160C:], f.songNumber)
	copy(b[0x160E:0x160E+12], pad12(f.songName))
	b[0x161A] = 0x01 // rate 44.1 kHz
	b[0x161B] = 0x01 // format MT2
	binary.BigEndian.PutUint16(b[0x1620:], f.fileCount)
	binary.BigEndian.PutUint16(b[0x1622:], f.fileIndex)
	copy(b[0x1624:0x1624+11], []byte(f.filename))
	b[0x1624+11] = 0x00 // NUL terminator on genuine headers
	binary.BigEndian.PutUint16(b[0x1630:], f.blockCount)
	binary.BigEndian.PutUint16(b[0x1632:], f.marker)
	binary.BigEndian.PutUint16(b[0x1634:], f.fileID)
	copy(b[0x164C:0x1650], []byte{0x7A, 0x4A, 0x7D, 0x28}) // session tag (§5.4)
	binary.LittleEndian.PutUint16(b[0x1650:], f.fileID)
	binary.LittleEndian.PutUint32(b[0x1652:], f.size)
}

// fileEntry builds the per-file fields for a genuine file header.
func fileEntry(s Song, f file, idx, count int) vr9Fields {
	return vr9Fields{
		songNumber: s.Number,
		songName:   s.Name,
		fileCount:  uint16(count),
		fileIndex:  uint16(idx),
		filename:   f.name,
		blockCount: uint16(ceilBlocks(len(f.data))),
		marker:     0,
		fileID:     f.fileID,
		size:       uint32(len(f.data)),
	}
}

// staleEntry is the plausible-but-stale per-file entry that block 0 carries
// (§5.5 case 1): a copy of the set's last file. It passes the signature,
// filename, and marker checks.
func staleEntry(d Disc) vr9Fields {
	last := d.Songs[len(d.Songs)-1]
	files := last.files()
	f := files[len(files)-1]
	return fileEntry(last, f, len(files)-1, len(files))
}

// boundaryBlock builds a §5.5 case-3 song-boundary block: a header-only block
// with the marker flag set. Its per-file bytes are a stale copy of the previous
// song's data — but the marker is the strict VR9 gate.
func boundaryBlock(d Disc, next Song) []byte {
	b := d.archiveHeaderBlock()
	e := fileEntry(next, next.files()[0], 0, len(next.files()))
	e.marker = 1 // song-start marker (§5.4 +0x1632)
	writeVR9FileFields(b, e)
	return b
}

// ---- byte helpers ----

// padBlocks pads data up to a whole number of 0x8000 blocks.
func padBlocks(data []byte) []byte {
	n := ceilBlocks(len(data)) * blockSize
	out := make([]byte, n)
	copy(out, data)
	return out
}

// ceilBlocks is ceil(n / 0x8000), at least 1 for a non-empty file.
func ceilBlocks(n int) int {
	if n == 0 {
		return 1
	}
	return (n + blockSize - 1) / blockSize
}

// fillerBlock returns one 0x8000 block of TDI filler payloads (§10).
func fillerBlock() []byte {
	b := make([]byte, blockSize)
	for off := 0; off < blockSize; off += udPerFR {
		copy(b[off:], fillerSig)
	}
	return b
}

// wrapRaw wraps a user-data stream into raw 2352-byte MODE1 frames. The mode byte
// and the §10 EDC are the fields extraction reads; sync/MSF/ECC are left zero. A
// real raw dump carries a correct per-frame EDC, so vsx's §10 damage detector
// (cd.Image.CorruptFrames) treats a frame whose stored EDC does not match its
// bytes as corrupt — a fixture must therefore burn the real EDC or every one of
// its frames would read as damaged.
func wrapRaw(ud []byte) []byte {
	frames := (len(ud) + udPerFR - 1) / udPerFR
	out := make([]byte, frames*frameSize)
	for i := 0; i < frames; i++ {
		f := out[i*frameSize : (i+1)*frameSize]
		f[15] = 0x01 // mode
		copy(f[16:16+udPerFR], ud[i*udPerFR:min((i+1)*udPerFR, len(ud))])
	}
	RepairEDC(out)
	return out
}

// RepairEDC recomputes and rewrites the §10 EDC of every MODE1 frame in a raw
// dump, in place. A test that pokes a raw dump's user-data bytes after BuildRaw
// (e.g. to corrupt a signature) calls it to keep the dump physically valid —
// otherwise the changed bytes no longer match their stored EDC and vsx's §10
// damage detector reports a corrupt sector, a different fault than the test means
// to inject.
func RepairEDC(raw []byte) {
	for off := 0; off+frameSize <= len(raw); off += frameSize {
		f := raw[off : off+frameSize]
		e := edc(f[:2064]) // EDC over sync + header + user data (§10)
		f[2064], f[2065], f[2066], f[2067] = byte(e), byte(e>>8), byte(e>>16), byte(e>>24)
	}
}

// edc computes the MODE1 EDC (ROLAND-VS-FORMAT-SPEC.md §10): the reflected CRC-32
// with polynomial 0xD8018001, init 0, no final XOR, stored little-endian at frame
// offset 2064. It is re-derived here straight from the spec rather than imported
// from internal/cd, so the fixture stays an independent oracle for the reader's
// own EDC check (ADR-0005): a shared bug in one cannot mask a bug in the other.
func edc(b []byte) uint32 {
	var crc uint32
	for _, v := range b {
		crc ^= uint32(v)
		for k := 0; k < 8; k++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xD8018001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

func pad8(s string) string  { return padTo(s, 8) }
func pad12(s string) []byte { return []byte(padTo(s, 12)) }

// padTo space-pads or truncates s to exactly n bytes (VS names are space-padded).
func padTo(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	b := make([]byte, n-len(s))
	for i := range b {
		b[i] = ' '
	}
	return s + string(b)
}

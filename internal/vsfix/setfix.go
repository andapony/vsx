package vsfix

import "encoding/binary"

// This file extends the VR9 fixture builder to multi-disc backup sets: it lays
// the archive out as a tape of 0x8000 blocks exactly as a single disc would
// (§5.4), then cuts the tape mid-file across a disc junction and reconstructs
// the two discs per §5.6 — disc 0 ends its data with a TDI filler run (§10),
// and disc 1 opens with a byte-exact repeat of the spanning file's header block
// (disc-index field bumped) before the remaining file data resumes at 0x8000.
// Exercising a genuine junction is the point: the reader must stitch the file
// back byte-exactly.

// VR9Set describes a two-disc VS-880EX backup set in which exactly one take
// straddles the disc-0 → disc-1 junction (§5.6).
type VR9Set struct {
	SetID           [4]byte
	Songs           []Song
	SpanFileID      uint16      // the take whose data crosses the junction
	SpanAvailBlocks int         // whole 0x8000 data blocks of that take burned on disc 0
	Disc0Trunc      *Disc0Trunc // when set, disc 0 is a truncated, filler-less rip (#31)
}

// Disc0Trunc turns disc 0 of a set into a truncated, filler-less rip (§10) for
// the #31 regression: a non-terminal disc that lacks a trailing filler run and
// whose user-data length is not a 0x8000 multiple, so a naive fallback would push
// every later disc off the chain-walk grid and hide its songs.
type Disc0Trunc struct {
	// Junk over-counts disc 0 past its true data end: this many 0x8000 blocks of
	// non-header bytes sit between the spanning file's last on-disc block and the
	// dump's end. It is the residue a block-aligned fallback (Option A) splices
	// into the spanning file and the junction reconstruction (Option B) drops.
	Junk int
	// TailFrames appends this many whole 2048-byte frames of a partial block, so
	// disc 0's user-data length is not a 0x8000 multiple (the real rip was cut ten
	// frames into a block).
	TailFrames int
}

// tapeFileRec records where one file's header and data blocks sit in the tape,
// so the cut point can be placed inside a chosen file's data.
type tapeFileRec struct {
	fileID              uint16
	headerIdx           int // block index of the file's header block
	dataIdx, dataBlocks int // first data block index and count
}

// tape lays the archive out as a list of 0x8000 blocks (no filler), mirroring
// Disc.userData's block order, and records each file's block span.
func (s VR9Set) tape() ([][]byte, []tapeFileRec) {
	d := Disc{SetID: s.SetID, Songs: s.Songs}
	var blocks [][]byte
	var recs []tapeFileRec

	hdr := d.archiveHeaderBlock()
	writeVR9FileFields(hdr, staleEntry(d)) // block 0: archive header (§5.5 case 1)
	blocks = append(blocks, hdr)
	blocks = append(blocks, d.archiveHeaderBlock()) // block 0x8000: second copy

	for si, song := range s.Songs {
		if si > 0 {
			blocks = append(blocks, boundaryBlock(d, song))
		}
		files := song.files()
		for fi, f := range files {
			hb := d.archiveHeaderBlock()
			writeVR9FileFields(hb, fileEntry(song, f, fi, len(files)))
			headerIdx := len(blocks)
			blocks = append(blocks, hb)
			data := splitBlocks(padBlocks(f.data))
			dataIdx := len(blocks)
			blocks = append(blocks, data...)
			recs = append(recs, tapeFileRec{fileID: f.fileID, headerIdx: headerIdx, dataIdx: dataIdx, dataBlocks: len(data)})
		}
	}
	return blocks, recs
}

// BuildDiscsRaw returns the set's discs as raw 2352-byte-frame dumps in disc
// index order. The span take's data is split at SpanAvailBlocks: those blocks
// stay on disc 0 (followed by filler), and the rest resume on disc 1 at 0x8000,
// behind a repeat of the file's header block.
func (s VR9Set) BuildDiscsRaw() [][]byte {
	blocks, recs := s.tape()
	return cutDiscs(blocks, recs, s.SpanFileID, s.SpanAvailBlocks, s.Disc0Trunc)
}

// cutDiscs splits a tape of 0x8000 blocks into two raw disc dumps at a junction
// inside the span file's data (§5.6), machine-agnostically: disc 0 holds the
// blocks up to the cut then a filler run (§10); disc 1 opens with a byte-exact
// repeat of the span file's header block (disc-index field bumped) and resumes
// the remaining data at 0x8000. The archive-header disc-index/total fields are
// patched so each disc reads as its place in a two-disc set (§5.2). When trunc is
// non-nil, disc 0 is a truncated, filler-less rip instead (§10, #31): the filler
// is dropped and the over-count residue is appended so its length falls off the
// 0x8000 grid.
func cutDiscs(blocks [][]byte, recs []tapeFileRec, spanFileID uint16, spanAvailBlocks int, trunc *Disc0Trunc) [][]byte {
	sf, ok := findTapeFile(recs, spanFileID)
	if !ok {
		panic("vsfix: SpanFileID is not a file in the set")
	}
	if spanAvailBlocks < 1 || spanAvailBlocks >= sf.dataBlocks {
		panic("vsfix: SpanAvailBlocks must leave data on both discs")
	}
	cut := sf.dataIdx + spanAvailBlocks // disc 0 holds blocks[:cut]

	disc0 := cloneBlocks(blocks[:cut])
	setU16BE(disc0[0], 0x26, 0) // disc index 0
	setU16BE(disc0[0], 0x28, 2) // total discs

	rep := cloneBlock(blocks[sf.headerIdx]) // repeated span-file header (§5.6)
	setU16BE(rep, 0x26, 1)                  // disc index 1 (the one differing byte at +0x27)
	setU16BE(rep, 0x28, 2)
	disc1 := append([][]byte{rep}, cloneBlocks(blocks[cut:])...)

	disc0ud := withFiller(disc0)
	if trunc != nil {
		disc0ud = truncatedUserData(disc0, trunc)
	}
	return [][]byte{
		wrapRaw(disc0ud),
		wrapRaw(withFiller(disc1)),
	}
}

// truncatedUserData joins disc 0's blocks with no filler run (§10), then appends
// the over-count residue: Junk whole 0x8000 blocks of non-header bytes followed by
// a partial block of TailFrames 2048-byte frames, so the dump lacks a filler and
// its user-data length is not a 0x8000 multiple (#31).
func truncatedUserData(blocks [][]byte, trunc *Disc0Trunc) []byte {
	ud := joinBlocks(blocks)
	// The junk carries a distinctive non-zero pattern, not zeros: a block-aligned
	// fallback (Option A) that keeps it splices these bytes into the spanning file,
	// which a silent-take fixture would otherwise hide (junk-of-zeros decodes to the
	// same silence as the true continuation). The junction reconstruction (Option B)
	// must drop it. The pattern is not the archive signature, so the chain walk
	// still rejects each junk block as a non-header.
	junk := make([]byte, trunc.Junk*blockSize)
	for i := range junk {
		junk[i] = 0xDA
	}
	ud = append(ud, junk...)
	ud = append(ud, make([]byte, trunc.TailFrames*udPerFR)...)
	return ud
}

// findTapeFile returns the tape record for the file with the given FileID.
func findTapeFile(recs []tapeFileRec, id uint16) (tapeFileRec, bool) {
	for _, r := range recs {
		if r.fileID == id {
			return r, true
		}
	}
	return tapeFileRec{}, false
}

// splitBlocks chops a block-aligned byte stream into 0x8000 blocks.
func splitBlocks(data []byte) [][]byte {
	var out [][]byte
	for off := 0; off < len(data); off += blockSize {
		out = append(out, data[off:off+blockSize])
	}
	return out
}

// withFiller joins a disc's blocks and appends a two-block TDI filler run (§10).
func withFiller(blocks [][]byte) []byte {
	ud := joinBlocks(blocks)
	for i := 0; i < 2; i++ {
		ud = append(ud, fillerBlock()...)
	}
	return ud
}

// joinBlocks concatenates a disc's 0x8000 blocks into one user-data stream.
func joinBlocks(blocks [][]byte) []byte {
	var ud []byte
	for _, b := range blocks {
		ud = append(ud, b...)
	}
	return ud
}

// cloneBlock / cloneBlocks copy blocks so patching disc-index bytes on one disc
// never mutates the shared tape.
func cloneBlock(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

func cloneBlocks(bs [][]byte) [][]byte {
	out := make([][]byte, len(bs))
	for i, b := range bs {
		out[i] = cloneBlock(b)
	}
	return out
}

// setU16BE writes a big-endian u16 at a block offset (disc-index / total-disc
// header fields, §5.2).
func setU16BE(b []byte, off int, v uint16) { binary.BigEndian.PutUint16(b[off:], v) }

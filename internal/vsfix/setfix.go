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
	SpanFileID      uint16 // the take whose data crosses the junction
	SpanAvailBlocks int    // whole 0x8000 data blocks of that take burned on disc 0
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
	return cutDiscs(blocks, recs, s.SpanFileID, s.SpanAvailBlocks)
}

// cutDiscs splits a tape of 0x8000 blocks into two raw disc dumps at a junction
// inside the span file's data (§5.6), machine-agnostically: disc 0 holds the
// blocks up to the cut then a filler run (§10); disc 1 opens with a byte-exact
// repeat of the span file's header block (disc-index field bumped) and resumes
// the remaining data at 0x8000. The archive-header disc-index/total fields are
// patched so each disc reads as its place in a two-disc set (§5.2).
func cutDiscs(blocks [][]byte, recs []tapeFileRec, spanFileID uint16, spanAvailBlocks int) [][]byte {
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

	return [][]byte{
		wrapRaw(withFiller(disc0)),
		wrapRaw(withFiller(disc1)),
	}
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
	var ud []byte
	for _, b := range blocks {
		ud = append(ud, b...)
	}
	for i := 0; i < 2; i++ {
		ud = append(ud, fillerBlock()...)
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

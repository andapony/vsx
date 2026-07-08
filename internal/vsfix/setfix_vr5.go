package vsfix

// VR5Set is the VS-1880 counterpart of VR9Set: a two-disc backup set with one
// take straddling the disc-0 → disc-1 junction (§5.6). It reuses the VR5 block
// builders so the layout (SONG/EVENTLST/take files, boundary blocks, §5.4
// header fields) matches a single-disc VR5 archive exactly, then cuts a chosen
// take's data across the junction.
type VR5Set struct {
	SetID           [4]byte
	Songs           []VR5Song
	SpanFileID      uint16 // the take whose data crosses the junction
	SpanAvailBlocks int    // whole 0x8000 data blocks of that take burned on disc 0
}

// tape lays the VR5 archive out as 0x8000 blocks (no filler), mirroring
// VR5Disc.userData's block order, and records each file's block span.
func (s VR5Set) tape() ([][]byte, []tapeFileRec) {
	d := VR5Disc{SetID: s.SetID, Songs: s.Songs}
	var blocks [][]byte
	var recs []tapeFileRec

	hdr := d.archiveHeaderBlock()
	writeVR5FileFields(hdr, staleVR5Entry(d)) // block 0: archive header (§5.5 case 1)
	blocks = append(blocks, hdr)
	blocks = append(blocks, d.archiveHeaderBlock()) // block 0x8000: second copy

	for si, song := range s.Songs {
		if si > 0 {
			blocks = append(blocks, d.boundaryBlock(song))
		}
		for _, f := range song.files() {
			hb := d.archiveHeaderBlock()
			writeVR5FileFields(hb, f.fields(song))
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
// index order: disc 0 holds the span take's first SpanAvailBlocks data blocks
// then a TDI filler run (§10); disc 1 opens with a byte-exact repeat of the
// take's header block (disc-index field bumped, §5.6) and resumes the remaining
// data at 0x8000.
func (s VR5Set) BuildDiscsRaw() [][]byte {
	blocks, recs := s.tape()
	return cutDiscs(blocks, recs, s.SpanFileID, s.SpanAvailBlocks)
}

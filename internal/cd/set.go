package cd

import (
	"encoding/binary"
	"fmt"
)

// §5.2 archive-header field offsets (user-data offset 0), shared by both
// machines: the set ID, the 0-based disc index, and the total disc count are
// what a multi-disc backup set is grouped and ordered by.
const (
	offSetID      = 0x20 // 4 B set ID (identical across all discs of one set)
	offDiscIndex  = 0x26 // u16 BE, 0-based
	offTotalDiscs = 0x28 // u16 BE
	headerFields  = 0x2A // bytes needed to cover every field above
)

// ArchiveHeader carries the §5.2 header fields that identify a disc's place in a
// backup set. The signature and song catalog are read elsewhere; this is only
// the set-membership data grouping and ordering need.
type ArchiveHeader struct {
	SetID      [4]byte
	DiscIndex  uint16 // 0-based position within the set
	TotalDiscs uint16
}

// ArchiveHeader reads the §5.2 set-membership fields from user-data offset 0.
func (im *Image) ArchiveHeader() (ArchiveHeader, error) {
	b, err := im.ReadUserData(0, headerFields)
	if err != nil {
		return ArchiveHeader{}, fmt.Errorf("cd: reading archive header: %w", err)
	}
	var h ArchiveHeader
	copy(h.SetID[:], b[offSetID:offSetID+4])
	h.DiscIndex = binary.BigEndian.Uint16(b[offDiscIndex:])
	h.TotalDiscs = binary.BigEndian.Uint16(b[offTotalDiscs:])
	return h, nil
}

// segment is one disc's contribution to a set's logical user-data stream: the
// half-open logical range [logStart, logStart+length) maps onto that disc's
// physical user-data starting at physStart.
type segment struct {
	disc      int
	logStart  int64
	physStart int64
	length    int64
}

// Set presents the discs of one backup set as a single contiguous user-data
// stream (§5.6): each disc contributes its file data from physical offset 0 (or
// from 0x8000 on a continuation disc, skipping the repeated block-0 header) up
// to its trailing TDI filler start (§10). A file whose data runs past one disc's
// filler resumes at the next disc's 0x8000, so a read that crosses the junction
// returns the burned bytes flush, with no gap, duplicated header, or filler.
//
// Set satisfies the same read surface as *Image, so the machine-specific chain
// walk and take reads run over a multi-disc source unchanged.
type Set struct {
	discs      []*Image
	segs       []segment
	total      int64 // logical user-data length
	missFiller []int // ordered disc positions lacking a §10 filler run
	cooked     bool
}

// NewSet builds a Set over discs already ordered by disc index (position 0 is
// the index-0 disc). Each disc's data end is detected at its TDI filler start
// (§10), never computed from the dump length; a disc lacking the filler falls
// back to its whole user-data length and is recorded in MissingFiller so the
// caller can report it (§10). Block 0 of every continuation disc is the repeated
// header of the spanning file (§5.6) and is omitted from the stream.
func NewSet(discs []*Image) (*Set, error) {
	if len(discs) == 0 {
		return nil, fmt.Errorf("cd: empty backup set")
	}
	s := &Set{discs: discs}
	for i, im := range discs {
		physStart := int64(0)
		if i > 0 {
			physStart = blockSize // skip the continuation disc's repeated header
		}
		end, ok := im.FillerStart()
		if !ok {
			end = im.UserDataLen()
			s.missFiller = append(s.missFiller, i)
		}
		length := end - physStart
		if length < 0 {
			length = 0
		}
		s.segs = append(s.segs, segment{disc: i, logStart: s.total, physStart: physStart, length: length})
		s.total += length
		if im.Cooked() {
			s.cooked = true
		}
	}
	return s, nil
}

// UserDataLen returns the length of the stitched logical user-data stream.
func (s *Set) UserDataLen() int64 { return s.total }

// FillerStart reports the logical length as the set's data end: the chain walk
// terminates there. A set defines its own data end from the per-disc filler
// detection in NewSet, so this always succeeds; per-disc truncation is surfaced
// through MissingFiller instead.
func (s *Set) FillerStart() (int64, bool) { return s.total, true }

// Cooked reports whether any disc of the set is a cooked (2048-sector) rip (§5).
func (s *Set) Cooked() bool { return s.cooked }

// MissingFiller returns the ordered disc positions (indices into the set) whose
// dump lacks a trailing TDI filler run, so its data end had to be guessed (§10).
func (s *Set) MissingFiller() []int { return s.missFiller }

// ReadUserData returns n bytes of the stitched stream starting at logical offset
// udoff, crossing disc junctions transparently. A read that runs past the end of
// the stream is an error — the same contract as *Image.
func (s *Set) ReadUserData(udoff int64, n int) ([]byte, error) {
	if udoff < 0 || n < 0 {
		return nil, fmt.Errorf("cd: invalid set read udoff=%d n=%d", udoff, n)
	}
	if udoff+int64(n) > s.total {
		return nil, fmt.Errorf("cd: set read [%d,%d) runs past user-data end %d", udoff, udoff+int64(n), s.total)
	}
	out := make([]byte, n)
	filled := 0
	for filled < n {
		cur := udoff + int64(filled)
		seg := s.segmentAt(cur)
		if seg == nil {
			return nil, fmt.Errorf("cd: set offset %d falls in no disc segment", cur)
		}
		local := cur - seg.logStart
		want := int(seg.length - local)
		if want > n-filled {
			want = n - filled
		}
		chunk, err := s.discs[seg.disc].ReadUserData(seg.physStart+local, want)
		if err != nil {
			return nil, fmt.Errorf("cd: reading disc %d: %w", seg.disc, err)
		}
		copy(out[filled:filled+want], chunk)
		filled += want
	}
	return out, nil
}

// ContinuationHeaders returns the block-0 bytes of every continuation disc that
// file data occupying the logical range [dataStart, dataStart+dataLen) spans
// into — the repeated header blocks (§5.6) the stitched stream skips over. Each
// is n bytes read from that disc's physical user-data offset 0. The machine-
// specific walk compares its file header against these to verify the §5.6
// repeat (name/FileID/size match); the result is empty for a range that stays on
// one disc.
func (s *Set) ContinuationHeaders(dataStart, dataLen int64, n int) ([][]byte, error) {
	end := dataStart + dataLen
	var out [][]byte
	for p := 1; p < len(s.segs); p++ {
		ls := s.segs[p].logStart
		if ls >= end {
			break
		}
		if ls > dataStart { // the file continues onto disc p, so its block 0 repeats the header
			b, err := s.discs[p].ReadUserData(0, n)
			if err != nil {
				return nil, fmt.Errorf("cd: reading continuation header on disc %d: %w", p, err)
			}
			out = append(out, b)
		}
	}
	return out, nil
}

// segmentAt returns the segment containing logical offset off, or nil when off
// is past the stream. The disc count is tiny, so a linear scan is fine.
func (s *Set) segmentAt(off int64) *segment {
	for i := range s.segs {
		seg := &s.segs[i]
		if off >= seg.logStart && off < seg.logStart+seg.length {
			return seg
		}
	}
	return nil
}

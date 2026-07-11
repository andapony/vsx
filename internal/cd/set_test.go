package cd

import (
	"bytes"
	"testing"
)

// solidBlock returns one 0x8000 block filled with byte v — a distinct,
// independently-known payload so a stitched read can be checked against the
// bytes that went in.
func solidBlock(v byte) []byte {
	b := make([]byte, blockSize)
	for i := range b {
		b[i] = v
	}
	return b
}

// fillerBlk returns one 0x8000 block of TDI filler payloads (§10): the frame at
// its start matches the filler pattern FillerStart scans for.
func fillerBlk() []byte {
	b := make([]byte, blockSize)
	for off := 0; off < blockSize; off += userDataSize {
		copy(b[off:], fillerSig)
	}
	return b
}

// cookedImage wraps a user-data byte stream as a cooked (2048-sector) Image, the
// simplest geometry for exercising Set stitching without frame wrapping.
func cookedImage(t *testing.T, ud []byte) *Image {
	t.Helper()
	img, err := New(bytes.NewReader(ud), int64(len(ud)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return img
}

// TestSetStitchesAcrossJunction is the core §5.6 invariant: a Set presents the
// discs' file data as one contiguous user-data stream — each disc's bytes from 0
// (block 0 skipped on continuation discs) up to its TDI filler start (§10) —
// with no gap, no duplicated repeated-header block, and no filler bytes. A read
// that straddles the disc-0/disc-1 junction must return exactly the bytes that
// were burned, in order.
func TestSetStitchesAcrossJunction(t *testing.T) {
	// Disc 0: two data blocks then filler. fillerStart = 0x10000.
	disc0 := concat(solidBlock(0xA0), solidBlock(0xA1), fillerBlk())
	// Disc 1: block 0 is the repeated spanning-file header (0xB0, skipped by the
	// Set), then two continuation data blocks, then filler.
	disc1 := concat(solidBlock(0xB0), solidBlock(0xB1), solidBlock(0xB2), fillerBlk())

	set, err := NewSet([]*Image{cookedImage(t, disc0), cookedImage(t, disc1)})
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}

	// Logical stream = A0,A1 (disc 0, pre-filler) ++ B1,B2 (disc 1, post-header).
	want := concat(solidBlock(0xA0), solidBlock(0xA1), solidBlock(0xB1), solidBlock(0xB2))
	if got := set.UserDataLen(); got != int64(len(want)) {
		t.Fatalf("UserDataLen = %#x, want %#x", got, len(want))
	}

	full, err := set.ReadUserData(0, len(want))
	if err != nil {
		t.Fatalf("ReadUserData(full): %v", err)
	}
	if !bytes.Equal(full, want) {
		t.Fatalf("stitched stream does not match the burned bytes")
	}

	// A read straddling the junction (last 0x100 of A1 through first 0x100 of B1)
	// must be byte-exact across the boundary — the spanning acceptance criterion.
	start := int64(2*blockSize - 0x100)
	span, err := set.ReadUserData(start, 0x200)
	if err != nil {
		t.Fatalf("ReadUserData(span): %v", err)
	}
	if !bytes.Equal(span, want[start:start+0x200]) {
		t.Fatalf("junction-straddling read is not byte-exact")
	}

	// The set defines its own data end; FillerStart reports the logical length so
	// the chain walk terminates there.
	end, ok := set.FillerStart()
	if !ok || end != int64(len(want)) {
		t.Fatalf("FillerStart = (%#x,%v), want (%#x,true)", end, ok, len(want))
	}
}

// TestSetMissingFillerReported flags a disc whose data end cannot be located:
// without a trailing filler run (§10) the set cannot know where the disc's data
// stops, so the position is surfaced for a deviation and the disc contributes
// its whole user-data length as a best-effort fallback.
func TestSetMissingFillerReported(t *testing.T) {
	disc0 := concat(solidBlock(0xA0), fillerBlk())
	disc1 := concat(solidBlock(0xB0), solidBlock(0xB1)) // no filler: truncated

	set, err := NewSet([]*Image{cookedImage(t, disc0), cookedImage(t, disc1)})
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	miss := set.MissingFiller()
	if len(miss) != 1 || miss[0] != 1 {
		t.Fatalf("MissingFiller = %v, want [1]", miss)
	}
}

// TestSetLastDiscMissingFillerKeepsFullLength guards #31 acceptance criterion #4:
// the block-aligned fallback applies only to non-terminal discs. The last disc
// shifts no later segment, so its full — possibly off-grid — length is kept to
// bound its own tail (best-effort trailing audio), never rounded down.
func TestSetLastDiscMissingFillerKeepsFullLength(t *testing.T) {
	disc0 := concat(solidBlock(0xA0), fillerBlk()) // fillerStart = 0x8000
	// Disc 1 is the last disc, missing its filler, with an off-grid tail: one data
	// block after block 0, then ten frames of a partial block.
	disc1 := concat(solidBlock(0xB0), solidBlock(0xB1), partialFrames(10))

	set, err := NewSet([]*Image{cookedImage(t, disc0), cookedImage(t, disc1)})
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	if miss := set.MissingFiller(); len(miss) != 1 || miss[0] != 1 {
		t.Fatalf("MissingFiller = %v, want [1]", miss)
	}

	// Disc 0 contributes 0x8000 (one block, pre-filler); disc 1 contributes its
	// whole user-data past block 0, tail included — 0x8000 + 20480 — not rounded.
	disc1Len := int64(blockSize + 10*userDataSize)
	wantTotal := int64(blockSize) + disc1Len
	if got := set.UserDataLen(); got != wantTotal {
		t.Fatalf("UserDataLen = %#x, want %#x (last disc's full length kept)", got, wantTotal)
	}
}

// partialFrames returns n whole 2048-byte user-data frames of zeros — a partial
// 0x8000 block that leaves a dump's user-data length off the block grid, the way
// a rip cut mid-block does (§10, #31).
func partialFrames(n int) []byte { return make([]byte, n*userDataSize) }

// TestSetAlignsMissingFillerFallback locks Option A of #31: when a non-terminal
// disc lacks a trailing filler run (§10) its data end is unknown, so the set
// falls back to the disc length — but rounded down to a 0x8000 block boundary, so
// the next disc's segment (and every 0x8000-aligned file header on it) stays on
// the chain-walk grid. An unaligned fallback would shift the whole downstream disc
// off the grid and hide all its songs.
func TestSetAlignsMissingFillerFallback(t *testing.T) {
	// Disc 0: two data blocks, a third (over-count) block, then a partial block of
	// ten frames — no filler. Its user-data length is 3 blocks + 20480, off grid.
	disc0 := concat(solidBlock(0xA0), solidBlock(0xA1), solidBlock(0xEE), partialFrames(10))
	// Disc 1: block 0 is the repeated spanning-file header (skipped), one data
	// block, then filler.
	disc1 := concat(solidBlock(0xB0), solidBlock(0xB1), fillerBlk())

	set, err := NewSet([]*Image{cookedImage(t, disc0), cookedImage(t, disc1)})
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	if miss := set.MissingFiller(); len(miss) != 1 || miss[0] != 0 {
		t.Fatalf("MissingFiller = %v, want [0]", miss)
	}

	// Disc 0 contributes its length rounded down to 3 whole blocks; disc 1 then
	// contributes its single post-header block. The total is a whole number of
	// blocks, so disc 1's data begins on the grid.
	wantTotal := int64(3*blockSize + blockSize)
	if got := set.UserDataLen(); got != wantTotal {
		t.Fatalf("UserDataLen = %#x, want %#x (disc 0 rounded down to a block boundary)", got, wantTotal)
	}
	junctionLog := int64(3 * blockSize)
	if junctionLog%blockSize != 0 {
		t.Fatalf("disc 1 starts off the block grid at %#x", junctionLog)
	}
	got, err := set.ReadUserData(junctionLog, blockSize)
	if err != nil {
		t.Fatalf("ReadUserData(junction): %v", err)
	}
	if !bytes.Equal(got, solidBlock(0xB1)) {
		t.Fatalf("block at the block-aligned junction is not disc 1's data")
	}
}

// TestSetDataEndReseamsExactly locks the Option B primitive of #31: once a caller
// has reconstructed a truncated disc's true data end from the continuation disc's
// junction (§5.6), SetDataEnd overrides the disc's fallback length and pulls every
// following segment back onto that exact boundary — dropping the over-count
// residue the block-aligned fallback kept.
func TestSetDataEndReseamsExactly(t *testing.T) {
	disc0 := concat(solidBlock(0xA0), solidBlock(0xA1), solidBlock(0xEE), partialFrames(10))
	disc1 := concat(solidBlock(0xB0), solidBlock(0xB1), fillerBlk())

	set, err := NewSet([]*Image{cookedImage(t, disc0), cookedImage(t, disc1)})
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}

	// The true data end is two blocks in (the 0xEE block is over-count residue).
	set.SetDataEnd(0, 2*blockSize)

	wantTotal := int64(2*blockSize + blockSize)
	if got := set.UserDataLen(); got != wantTotal {
		t.Fatalf("UserDataLen = %#x, want %#x after reseam", got, wantTotal)
	}
	// Disc 1's data now begins immediately after disc 0's two real blocks, with the
	// over-count 0xEE block dropped.
	want := concat(solidBlock(0xA0), solidBlock(0xA1), solidBlock(0xB1))
	full, err := set.ReadUserData(0, len(want))
	if err != nil {
		t.Fatalf("ReadUserData(full): %v", err)
	}
	if !bytes.Equal(full, want) {
		t.Fatalf("reseamed stream spliced the wrong bytes at the junction")
	}
}

// TestArchiveHeaderReadsSetFields reads the §5.2 archive-header fields a backup
// set is grouped and ordered by: the set ID, the 0-based disc index, and the
// total disc count.
func TestArchiveHeaderReadsSetFields(t *testing.T) {
	ud := make([]byte, blockSize)
	copy(ud[0x20:0x24], []byte{1, 2, 3, 4})
	ud[0x27] = 0x05 // disc index low byte (u16 BE at 0x26)
	ud[0x29] = 0x08 // total discs low byte (u16 BE at 0x28)
	img := cookedImage(t, ud)

	h, err := img.ArchiveHeader()
	if err != nil {
		t.Fatalf("ArchiveHeader: %v", err)
	}
	if h.SetID != [4]byte{1, 2, 3, 4} {
		t.Fatalf("SetID = %v", h.SetID)
	}
	if h.DiscIndex != 5 || h.TotalDiscs != 8 {
		t.Fatalf("DiscIndex/TotalDiscs = %d/%d, want 5/8", h.DiscIndex, h.TotalDiscs)
	}
}

// concat joins byte slices — a tiny helper the fixtures above read cleanly with.
func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

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

package cd

import (
	"bytes"
	"hash/crc32"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stdlibEDC is an independent oracle for the MODE1 EDC (§10): the reflected
// CRC-32 with polynomial 0xD8018001, init 0, no final XOR, computed here through
// Go's battle-tested hash/crc32 engine rather than the production table in edc.go.
// Go's crc32.Update complements the CRC at both ends (the IEEE convention), so
// seeding it with 0xFFFFFFFF cancels the leading complement (^0xFFFFFFFF == 0)
// and the outer ^ cancels the trailing one, leaving the raw init-0 CRC vsx needs.
func stdlibEDC(b []byte) uint32 {
	tab := crc32.MakeTable(0xD8018001)
	return ^crc32.Update(0xFFFFFFFF, tab, b)
}

// frameWithEDC wraps 2048 bytes of user data into a 2352-byte MODE1 frame whose
// stored EDC is correct — computed over the frame's first 2064 bytes (12 B sync +
// 4 B header + 2048 B user data) and written little-endian at [2064,2068). The
// EDC comes from the independent stdlibEDC oracle, so a frame this builds is
// "clean" only if the production detector's own EDC agrees with it.
func frameWithEDC(userData []byte) []byte {
	f := rawFrame(userData)
	edc := stdlibEDC(f[:2064])
	f[2064] = byte(edc)
	f[2065] = byte(edc >> 8)
	f[2066] = byte(edc >> 16)
	f[2067] = byte(edc >> 24)
	return f
}

// TestCorruptFramesDetectsBrokenFrame is the §10 damage detector's core claim
// (acceptance criterion 1): a raw dump with a single EDC-broken frame reports
// exactly that frame's index and no other. The break is a flipped user-data byte
// left un-recomputed — the shape of a physically misread sector.
func TestCorruptFramesDetectsBrokenFrame(t *testing.T) {
	var dump []byte
	for i := 0; i < 5; i++ {
		dump = append(dump, frameWithEDC(bytes.Repeat([]byte{byte(i)}, 2048))...)
	}
	// Corrupt frame 3's first user-data byte (physical offset 3*2352 + 16),
	// leaving its stored EDC describing the pre-corruption bytes.
	dump[3*2352+16] ^= 0xFF

	img, err := New(bytes.NewReader(dump), int64(len(dump)))
	require.NoError(t, err)

	corrupt, err := img.CorruptFrames()
	require.NoError(t, err)
	assert.Equal(t, []int{3}, corrupt, "only the EDC-broken frame is flagged")
}

// TestCorruptFramesCleanDumpReportsNone confirms a raw dump whose every frame
// carries a correct EDC is reported clean (acceptance criterion 1). Because the
// fixture's EDCs come from the independent stdlibEDC oracle, an empty result also
// proves the production computeEDC agrees with that oracle on real bytes.
func TestCorruptFramesCleanDumpReportsNone(t *testing.T) {
	var dump []byte
	for i := 0; i < 8; i++ {
		dump = append(dump, frameWithEDC(bytes.Repeat([]byte{byte(i * 37)}, 2048))...)
	}
	img, err := New(bytes.NewReader(dump), int64(len(dump)))
	require.NoError(t, err)

	corrupt, err := img.CorruptFrames()
	require.NoError(t, err)
	assert.Empty(t, corrupt, "a dump of clean frames has no corrupt sectors")
}

// TestCorruptFramesCookedDumpHasNoEDC verifies §5/§10: a cooked (dd) dump has no
// frame wrapper and so no per-frame EDC, so the detector reports nothing rather
// than misreading user data as a broken checksum. The cooked data-integrity risk
// is surfaced as a §5 deviation elsewhere.
func TestCorruptFramesCookedDumpHasNoEDC(t *testing.T) {
	ud := append(bytes.Repeat([]byte{0x11}, 2048), bytes.Repeat([]byte{0x22}, 2048)...)
	img, err := New(bytes.NewReader(ud), int64(len(ud)))
	require.NoError(t, err)
	require.True(t, img.Cooked())

	corrupt, err := img.CorruptFrames()
	require.NoError(t, err)
	assert.Nil(t, corrupt, "a cooked dump carries no EDC to check")
}

// TestComputeEDCMatchesOracle pins the production EDC to the §10 definition two
// independent ways: it equals the stdlib-CRC oracle for varied inputs, and it
// satisfies the reflected-CRC self-consistency invariant — the EDC over a buffer
// followed by that EDC's little-endian bytes folds back to zero.
func TestComputeEDCMatchesOracle(t *testing.T) {
	cases := [][]byte{
		{},
		{0x00},
		{0x01, 0x02, 0x03, 0x04},
		bytes.Repeat([]byte{0xAB}, 2064),
		append([]byte{0xDE, 0xAD, 0xBE, 0xEF}, bytes.Repeat([]byte{0x5A}, 2060)...),
	}
	for _, b := range cases {
		edc := computeEDC(b)
		assert.Equal(t, stdlibEDC(b), edc, "computeEDC must match the stdlib CRC oracle")

		var le [4]byte
		le[0], le[1], le[2], le[3] = byte(edc), byte(edc>>8), byte(edc>>16), byte(edc>>24)
		assert.Zero(t, computeEDC(append(append([]byte{}, b...), le[:]...)),
			"EDC over data||EDC folds back to zero")
	}
}

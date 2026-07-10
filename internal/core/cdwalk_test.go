package core

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lookaheadSource is a minimal cdSource for testing validCDHeader in isolation:
// it answers the single §5.5-check-6 look-ahead read (the block at +0x8000) with
// fixed bytes, so a layout's validity can be exercised against a hand-crafted
// header without building a whole fixture image. next is what the look-ahead
// returns (zero-padded to the requested length); a nil next answers with zeros
// (plausible file data). err, when set, is returned instead.
type lookaheadSource struct {
	next []byte
	err  error
}

func (s lookaheadSource) ReadUserData(_ int64, n int) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	b := make([]byte, n)
	copy(b, s.next)
	return b, nil
}

func (lookaheadSource) UserDataLen() int64         { return 1 << 40 }
func (lookaheadSource) FillerStart() (int64, bool) { return 0, false }

// vr9HeaderBlock hand-builds a vr9HeaderSpan-byte VS-880EX header with a valid
// signature, a plausible filename, and the given marker flag (0 = real file
// header, non-zero = song-boundary block).
func vr9HeaderBlock(marker uint16) []byte {
	hdr := make([]byte, vr9HeaderSpan)
	copy(hdr, sigVR9)
	copy(hdr[offFilename:], "TAKE0100VR9")
	binary.BigEndian.PutUint16(hdr[offMarker:], marker)
	return hdr
}

// vr5HeaderBlock hand-builds a vr5HeaderSpan-byte VS-1880 header with a valid
// signature, a plausible filename, and the given +0x245C magic bytes.
func vr5HeaderBlock(magic []byte) []byte {
	hdr := make([]byte, vr5HeaderSpan)
	copy(hdr, sigVR5)
	copy(hdr[vr5OffFilename:], "TAKE9CC7VR5")
	copy(hdr[vr5OffMagic:], magic)
	return hdr
}

// TestVR9LayoutValidity exercises the VS-880EX layout's §5.5 validity check in
// isolation: the machine-specific gate is the clear song-boundary marker flag
// (check 4), and a header otherwise valid but with the marker set — the very
// block the chain walk must skip — is rejected. The shared checks (signature,
// plausible name, look-ahead) are covered too, so the whole predicate is pinned
// without a fixture image.
func TestVR9LayoutValidity(t *testing.T) {
	var lay cdLayout = vr9{}
	const udoff, end = int64(firstFileHeader), int64(1 << 30)
	src := lookaheadSource{} // look-ahead answers with plausible (non-signature) data

	assert.True(t, lay.accept(vr9HeaderBlock(0)), "a clear marker flag passes the VR9 gate")
	assert.False(t, lay.accept(vr9HeaderBlock(1)), "a set marker flag fails the VR9 gate")

	require.True(t, validCDHeader(src, lay, vr9HeaderBlock(0), udoff, end),
		"a well-formed VR9 header is accepted")

	// The song-boundary block: signature and name are fine, but the marker is set.
	assert.False(t, validCDHeader(src, lay, vr9HeaderBlock(1), udoff, end),
		"a VR9 song-boundary block (marker set) is rejected")

	// Shared checks.
	bad := vr9HeaderBlock(0)
	copy(bad, sigVR5) // wrong signature (check 1)
	assert.False(t, validCDHeader(src, lay, bad, udoff, end), "a wrong signature is rejected")

	badName := vr9HeaderBlock(0)
	copy(badName[offFilename:], " AKE0100VR9") // leading space (check 2)
	assert.False(t, validCDHeader(src, lay, badName, udoff, end), "an implausible name is rejected")

	sigAhead := lookaheadSource{next: []byte(sigVR9)} // check 6: file data starts with the signature
	assert.False(t, validCDHeader(sigAhead, lay, vr9HeaderBlock(0), udoff, end),
		"a look-ahead that finds another archive signature is rejected")

	assert.False(t, validCDHeader(src, lay, vr9HeaderBlock(0), end-blockSize, end),
		"a header whose data block would fall at/after the data end is rejected")
}

// TestVR5LayoutValidity exercises the VS-1880 layout's §5.5 validity check in
// isolation: VR5 has no marker flag, so its machine-specific gate is the
// `60 BF 51 28` magic at +0x245C (check 3), which is what rejects a markerless
// song-boundary block whose stale per-file area lacks the magic.
func TestVR5LayoutValidity(t *testing.T) {
	var lay cdLayout = vr5{}
	const udoff, end = int64(firstFileHeader), int64(1 << 30)
	src := lookaheadSource{}

	assert.True(t, lay.accept(vr5HeaderBlock(vr5Magic)), "the constant magic passes the VR5 gate")
	assert.False(t, lay.accept(vr5HeaderBlock([]byte{0, 0, 0, 0})), "a missing magic fails the VR5 gate")

	require.True(t, validCDHeader(src, lay, vr5HeaderBlock(vr5Magic), udoff, end),
		"a well-formed VR5 header is accepted")

	// The song-boundary block: signature and name survive, but the magic is stale.
	assert.False(t, validCDHeader(src, lay, vr5HeaderBlock([]byte{0xDE, 0xAD, 0xBE, 0xEF}), udoff, end),
		"a VR5 boundary block (no magic) is rejected")

	sigAhead := lookaheadSource{next: []byte(sigVR5)}
	assert.False(t, validCDHeader(sigAhead, lay, vr5HeaderBlock(vr5Magic), udoff, end),
		"a look-ahead that finds another archive signature is rejected")
}

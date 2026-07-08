package cd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawFrame wraps 2048 bytes of user data into a 2352-byte MODE1 raw frame:
// 16 header bytes (12 sync + 3 MSF + 1 mode) + 2048 user data + 288 EDC/ECC.
// The header and ECC bytes are irrelevant to user-data extraction, so this
// helper leaves them zero except for the mode byte.
func rawFrame(userData []byte) []byte {
	require := func(cond bool) {
		if !cond {
			panic("rawFrame: userData must be exactly 2048 bytes")
		}
	}
	require(len(userData) == 2048)
	f := make([]byte, 2352)
	f[15] = 0x01 // mode
	copy(f[16:16+2048], userData)
	return f
}

// TestReadUserDataStitchesAcrossFrames verifies the core geometry claim (§5.1):
// concatenating the 2048-byte user-data payloads of consecutive raw frames
// yields one continuous stream, so a read that straddles a frame boundary must
// skip the 288-byte ECC of the first frame and the 16-byte header of the next.
func TestReadUserDataStitchesAcrossFrames(t *testing.T) {
	ud0 := bytes.Repeat([]byte{0xAA}, 2048)
	ud1 := bytes.Repeat([]byte{0xBB}, 2048)
	img, err := New(bytes.NewReader(append(rawFrame(ud0), rawFrame(ud1)...)), 2*2352)
	require.NoError(t, err)

	assert.EqualValues(t, 2*2048, img.UserDataLen())

	// A read straddling the frame boundary: last 4 bytes of frame 0's user
	// data and first 4 of frame 1's.
	got, err := img.ReadUserData(2048-4, 8)
	require.NoError(t, err)
	assert.Equal(t, []byte{0xAA, 0xAA, 0xAA, 0xAA, 0xBB, 0xBB, 0xBB, 0xBB}, got)
}

// TestReadUserDataBounds verifies that a read running past the end of the
// user-data stream is an error rather than a short read or a panic.
func TestReadUserDataBounds(t *testing.T) {
	img, err := New(bytes.NewReader(rawFrame(bytes.Repeat([]byte{0x01}, 2048))), 2352)
	require.NoError(t, err)

	_, err = img.ReadUserData(2046, 4)
	assert.Error(t, err, "read past end of user data must error")
}

// fillerFrame returns a 2048-byte TDI filler payload (§10): the 13-byte
// signature followed by zeros.
func fillerFrame() []byte {
	ud := make([]byte, 2048)
	copy(ud, []byte{0x54, 0x44, 0x49, 0x01, 0x50, 0x01, 0x01, 0x01, 0x01, 0x80, 0xFF, 0xFF, 0xFF})
	return ud
}

// TestFillerStartFindsTrailingRun verifies §10: the disc's burned data ends at
// the first 0x8000-aligned user-data offset whose frame is TDI filler, and that
// offset — not the end of the dump — is what FillerStart reports.
func TestFillerStartFindsTrailingRun(t *testing.T) {
	const blk = 0x8000
	var dump []byte
	// 16 frames (= one 0x8000 block) of ordinary data, then a filler run.
	for i := 0; i < 16; i++ {
		dump = append(dump, rawFrame(bytes.Repeat([]byte{0x5A}, 2048))...)
	}
	for i := 0; i < 20; i++ {
		dump = append(dump, rawFrame(fillerFrame())...)
	}
	img, err := New(bytes.NewReader(dump), int64(len(dump)))
	require.NoError(t, err)

	start, ok := img.FillerStart()
	require.True(t, ok, "a finalized disc has a trailing filler run")
	assert.EqualValues(t, blk, start, "filler starts at the first 0x8000 boundary that is filler")
}

// TestFillerStartAbsentOnTruncatedDump verifies §10's diagnostic hook: a dump
// with no trailing filler is a truncated/incomplete rip, reported by the
// absence of a filler start.
func TestFillerStartAbsentOnTruncatedDump(t *testing.T) {
	var dump []byte
	for i := 0; i < 20; i++ {
		dump = append(dump, rawFrame(bytes.Repeat([]byte{0x5A}, 2048))...)
	}
	img, err := New(bytes.NewReader(dump), int64(len(dump)))
	require.NoError(t, err)

	_, ok := img.FillerStart()
	assert.False(t, ok, "no filler run means a truncated dump")
}

// TestCookedDumpIsDetected verifies that a dump whose length is a multiple of
// 2048 but not 2352 — a dd/"cooked" extraction (§5) — is recognized as cooked
// and read as a contiguous user-data stream (no frame wrapper to strip).
func TestCookedDumpIsDetected(t *testing.T) {
	// Two 2048-byte "sectors" with no frame wrapper.
	ud := append(bytes.Repeat([]byte{0x11}, 2048), bytes.Repeat([]byte{0x22}, 2048)...)
	img, err := New(bytes.NewReader(ud), int64(len(ud)))
	require.NoError(t, err)

	assert.True(t, img.Cooked(), "a 2048-multiple non-2352 dump is a cooked rip")
	assert.EqualValues(t, 2*2048, img.UserDataLen())
	got, err := img.ReadUserData(2048-2, 4)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x11, 0x11, 0x22, 0x22}, got)
}

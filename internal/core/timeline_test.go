package core

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeTake decodes an MT2 take through the production Decoder — the trusted,
// independently-verified codec (ADR-0004) used here as an oracle so timeline
// tests can assert where a take's samples land without re-deriving the codec.
func decodeTake(t *testing.T, mt2 []byte) PCM {
	t.Helper()
	pcm, err := NewDecoder().Decode(FormatMT2, mt2, blockSize)
	require.NoError(t, err)
	return pcm
}

// mt2Bytes returns n distinguishable MT2 blocks (a per-take fill byte) so a
// take's decoded samples are non-trivial and unique, making placement testable.
func mt2Bytes(fill byte, nBlocks int) []byte { return bytes.Repeat([]byte{fill}, 12*nBlocks) }

// TestBuildVR9TracksPlacement is the §8.2 timeline specification as a test:
// VR9 origin = 12 shifts frame positions, gaps fill with silence, a trim skips
// into the take, later records win on overlap, and an erase writes silence.
func TestBuildVR9TracksPlacement(t *testing.T) {
	takeA := mt2Bytes(0x11, 4) // 64 samples
	takeB := mt2Bytes(0x22, 4)
	takeC := mt2Bytes(0x33, 8) // 128 samples
	takeD := mt2Bytes(0x44, 4)
	takeE := mt2Bytes(0x55, 4)
	takeF := mt2Bytes(0x66, 4)
	takes := map[uint16]PCM{
		0xA: decodeTake(t, takeA), 0xB: decodeTake(t, takeB), 0xC: decodeTake(t, takeC),
		0xD: decodeTake(t, takeD), 0xE: decodeTake(t, takeE), 0xF: decodeTake(t, takeF),
	}

	events := []vr9Event{
		// code 0 (T1/V1): a take placed at the origin, no trim.
		{start: 12, end: 16, trimmed: 0, fileID: 0xA, code: 0},
		// code 1 (T1/V2): placed after a gap — 128 leading silence samples.
		{start: 20, end: 24, trimmed: 0, fileID: 0xB, code: 1},
		// code 8 (T2/V1): trimmed 2 frames into the take.
		{start: 12, end: 16, trimmed: 2, fileID: 0xC, code: 8},
		// code 16 (T3/V1): D then E over the same range — E wins.
		{start: 12, end: 16, trimmed: 0, fileID: 0xD, code: 16},
		{start: 12, end: 16, trimmed: 0, fileID: 0xE, code: 16},
		// code 24 (T4/V1): F then an erase (fileID 0) over the same range — silence.
		{start: 12, end: 16, trimmed: 0, fileID: 0xF, code: 24},
		{start: 12, end: 16, trimmed: 0, fileID: 0, code: 24},
	}

	tracks, devs := buildVR9Tracks(events, takes, SongRef{Number: 1, Name: "S"}, audioSpec{sampleRate: 44100, format: FormatMT2, clusterSize: blockSize}, false)
	assert.Empty(t, devs)

	byCode := map[[2]int]TrackResult{}
	for _, tr := range tracks {
		byCode[[2]int{tr.Track, tr.VTrack}] = tr
	}

	// code 0: whole take A at the origin.
	t1v1 := byCode[[2]int{1, 1}]
	require.Len(t, t1v1.PCM.Samples, 64)
	assert.Equal(t, takes[0xA].Samples, t1v1.PCM.Samples)
	assert.Equal(t, 16, t1v1.PCM.BitDepth)
	assert.EqualValues(t, 44100, t1v1.Take.SampleRate)

	// code 1: 128 samples of silence then take B.
	t1v2 := byCode[[2]int{1, 2}]
	require.Len(t, t1v2.PCM.Samples, 192)
	assert.Equal(t, make([]int32, 128), t1v2.PCM.Samples[:128], "gap before the event is silence")
	assert.Equal(t, takes[0xB].Samples, t1v2.PCM.Samples[128:192])

	// code 8: trimmed 2 frames (32 samples) into take C.
	t2v1 := byCode[[2]int{2, 1}]
	require.Len(t, t2v1.PCM.Samples, 64)
	assert.Equal(t, takes[0xC].Samples[32:96], t2v1.PCM.Samples, "trim skips TrimmedFrames×16 into the take")

	// code 16: later record (E) wins over D.
	t3v1 := byCode[[2]int{3, 1}]
	assert.Equal(t, takes[0xE].Samples, t3v1.PCM.Samples, "later record wins on overlap")

	// code 24: erase writes silence over the earlier take.
	t4v1 := byCode[[2]int{4, 1}]
	assert.Equal(t, make([]int32, 64), t4v1.PCM.Samples, "erase record overwrites with silence")
}

// TestParseVR9LogBoundsCount verifies the §9 bound: the log's declared count
// caps how many records are read, so trailing optimize remnants are never parsed
// as live events, and a count that overruns the data is reported.
func TestParseVR9LogBoundsCount(t *testing.T) {
	// Header count = 1, one 48-byte record, then remnant bytes that must be
	// ignored.
	data := make([]byte, 2+48+48)
	data[0], data[1] = 0x00, 0x01 // count = 1
	for i := 2 + 48; i < len(data); i++ {
		data[i] = 0xFF // remnant garbage past the live count
	}
	events, devs := parseVR9Log(data)
	assert.Len(t, events, 1, "only the declared live records are parsed")
	assert.Empty(t, devs)

	// A count that overruns the available bytes is a deviation.
	over := []byte{0x00, 0x05} // claims 5 records, holds none
	_, devs = parseVR9Log(over)
	assert.NotEmpty(t, devs, "an over-declared count is reported")
}

// TestBuildVR9TracksSkipsEmpty verifies that a v-track with no take-bearing
// records produces no TrackResult (acceptance: empty v-tracks yield no file).
func TestBuildVR9TracksSkipsEmpty(t *testing.T) {
	events := []vr9Event{{start: 12, end: 16, fileID: 0xA, code: 0}}
	takes := map[uint16]PCM{0xA: decodeTake(t, mt2Bytes(0x11, 4))}

	tracks, _ := buildVR9Tracks(events, takes, SongRef{Number: 1}, audioSpec{sampleRate: 44100, format: FormatMT2, clusterSize: blockSize}, false)
	require.Len(t, tracks, 1)
	assert.Equal(t, 1, tracks[0].Track)
	assert.Equal(t, 1, tracks[0].VTrack)
}

// TestBuildVR9TracksTruncatedTake verifies §8/§10 handling of a take shorter
// than the event's span: emit what exists, pad the rest with silence, and warn.
func TestBuildVR9TracksTruncatedTake(t *testing.T) {
	// Event wants 4 frames (64 samples) but the take is only 2 blocks (32).
	events := []vr9Event{{start: 12, end: 16, fileID: 0xA, code: 0}}
	takes := map[uint16]PCM{0xA: decodeTake(t, mt2Bytes(0x11, 2))}

	tracks, devs := buildVR9Tracks(events, takes, SongRef{Number: 1}, audioSpec{sampleRate: 44100, format: FormatMT2, clusterSize: blockSize}, false)
	require.Len(t, tracks, 1)
	require.Len(t, tracks[0].PCM.Samples, 64)
	assert.Equal(t, takes[0xA].Samples, tracks[0].PCM.Samples[:32])
	assert.Equal(t, make([]int32, 32), tracks[0].PCM.Samples[32:], "span beyond the take is padded with silence")
	assert.NotEmpty(t, devs, "a truncated take is reported as a deviation")
}

package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectTracks fully consumes an extraction, returning the tracks and (after
// completion) the deviations.
func collectTracks(t *testing.T, r Result) ([]TrackResult, []Deviation) {
	t.Helper()
	var got []TrackResult
	for tr, err := range r.Tracks() {
		require.NoError(t, err)
		got = append(got, tr)
	}
	return got, r.Deviations()
}

// anyNonZero reports whether any sample is non-zero.
func anyNonZero(s []int32) bool {
	for _, v := range s {
		if v != 0 {
			return true
		}
	}
	return false
}

// TestExtractFlagSetRecordWritesAudio locks the §8.2 caveat: a record whose
// 0x21 flag byte is set but which carries a real take (non-zero FileID) must be
// laid down as ordinary audio, not treated as an erase. Gating silence on the
// flag would silently drop this v-track's audio.
func TestExtractFlagSetRecordWritesAudio(t *testing.T) {
	disc := vsfix.Disc{
		SetID: [4]byte{1, 1, 1, 1},
		Songs: []vsfix.Song{{
			Number: 1, Name: "FLAG",
			Takes: []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: mt2Bytes(0x11, 4)}},
			Events: []vsfix.Event{
				// Flag byte set, but a real take — a normal write per §8.2.
				{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1, Tombstone: true},
			},
		}},
	}
	path := filepath.Join(t.TempDir(), "flag.bin")
	require.NoError(t, os.WriteFile(path, disc.BuildRaw(), 0o644))

	r, err := Extract(path, Options{})
	require.NoError(t, err)
	tracks, _ := collectTracks(t, r)

	require.Len(t, tracks, 1, "the flag=1 record's v-track is emitted")
	assert.True(t, anyNonZero(tracks[0].PCM.Samples), "flag=1 with a real take writes audio, not silence")
}

// TestExtractVR9EndToEnd drives a synthetic single-disc VR9 archive through the
// whole pipeline — detection, header-block walk, event-log replay, take decode,
// timeline build — and verifies the per-v-track results emerge with the right
// identity and audio, including a deviation for a referenced-but-absent take.
func TestExtractVR9EndToEnd(t *testing.T) {
	disc := vsfix.Disc{
		SetID: [4]byte{9, 9, 9, 9},
		Songs: []vsfix.Song{{
			Number: 3, Name: "TRACER",
			Takes: []vsfix.Take{
				{FileID: 0x0100, Name: "TAKE0100", MT2: silentMT2(4)}, // 64 samples
				{FileID: 0x0200, Name: "TAKE0200", MT2: silentMT2(2)},
			},
			Events: []vsfix.Event{
				// T1/V1: whole take at the origin (frames 12..16).
				{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1},
				// T2/V3: references a take that is not on the disc (§10).
				{Start: 12, End: 16, FileID: 0xDEAD, Track: 2, VTrack: 3},
			},
		}},
	}
	path := filepath.Join(t.TempDir(), "vr9.bin")
	require.NoError(t, os.WriteFile(path, disc.BuildRaw(), 0o644))

	r, err := Extract(path, Options{})
	require.NoError(t, err)
	tracks, devs := collectTracks(t, r)

	require.Len(t, tracks, 2)

	assert.Equal(t, 3, tracks[0].Song.Number)
	assert.Equal(t, "TRACER", tracks[0].Song.Name)
	assert.Equal(t, 1, tracks[0].Track)
	assert.Equal(t, 1, tracks[0].VTrack)
	assert.Len(t, tracks[0].PCM.Samples, 64)
	assert.EqualValues(t, 44100, tracks[0].Take.SampleRate)

	assert.Equal(t, 2, tracks[1].Track)
	assert.Equal(t, 3, tracks[1].VTrack)

	// The dangling take reference is reported.
	found := false
	for _, d := range devs {
		if d.SpecRef == "§10" {
			found = true
		}
	}
	assert.True(t, found, "a referenced take with no file is a deviation")
}

// silentMT2 returns n silent MT2 blocks (12 zero bytes each), which decode to
// 16n zero samples (§2).
func silentMT2(nBlocks int) []byte { return make([]byte, 12*nBlocks) }

// TestWalkVR9EnumeratesFiles verifies the §5.4 chain walk: every file is found
// via its own 0x8000 header block, in source order, with the source SONG number
// and FileID the §5.4 fields carry — and the archive-header block, its second
// copy, and (with two songs) the song-boundary block are all skipped (§5.5).
func TestWalkVR9EnumeratesFiles(t *testing.T) {
	disc := vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{
			{Number: 5, Name: "SONG A", Takes: []vsfix.Take{
				{FileID: 0x0C53, Name: "TAKE0C53", MT2: silentMT2(4)},
				{FileID: 0x0C57, Name: "TAKE0C57", MT2: silentMT2(2)},
			}},
			{Number: 7, Name: "SONG B", Takes: []vsfix.Take{
				{FileID: 0x1000, Name: "TAKE1000", MT2: silentMT2(3)},
			}},
		},
	}
	img := imageOf(t, disc.BuildRaw())

	files, devs, err := walkVR9(img)
	require.NoError(t, err)
	assert.Empty(t, devs, "a well-formed disc walks without deviations")

	// Song A: EVENTLST + 2 takes; Song B: EVENTLST + 1 take = 5 files total.
	require.Len(t, files, 5)

	// The two takes of song A, resolved by FileID and song number.
	assert.Equal(t, "TAKE0C53VR9", files[1].filename)
	assert.EqualValues(t, 0x0C53, files[1].fileID)
	assert.EqualValues(t, 5, files[1].songNumber)
	assert.EqualValues(t, 12*4, files[1].size)

	// Song B's take followed the boundary block without desync.
	last := files[4]
	assert.Equal(t, "TAKE1000VR9", last.filename)
	assert.EqualValues(t, 7, last.songNumber)
	assert.Equal(t, "SONG B", last.songName)
}

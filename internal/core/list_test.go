package core

import (
	"bytes"
	"testing"
	"time"

	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSongInfoDuration verifies SongInfo renders its own timeline length as a
// duration through the samples-per-frame framing (issue #27, seam 2), so the CLI
// no longer hardcodes that constant. 500 frames × 16 samples ÷ 8000 Hz = 1s.
func TestSongInfoDuration(t *testing.T) {
	assert.Equal(t, time.Second, SongInfo{Frames: 500, SampleRate: 8000}.Duration())
	// A zero rate is a defined zero duration, not a divide-by-zero panic.
	assert.Zero(t, SongInfo{Frames: 100, SampleRate: 0}.Duration())
}

// twoSongVR9 builds a synthetic 2-song VR9 CD disc (songs numbered 1 and 2),
// each with one populated v-track, as an in-memory dump.
func twoSongVR9() []byte {
	disc := vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{
			{Number: 1, Name: "AONE", Takes: []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: mt2Bytes(0x00, 4)}},
				Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1}}},
			{Number: 2, Name: "BTWO", Takes: []vsfix.Take{{FileID: 0x0200, Name: "TAKE0200", MT2: mt2Bytes(0x00, 4)}},
				Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0200, Track: 1, VTrack: 1}}},
		},
	}
	return disc.BuildRaw()
}

func TestOptionsSongsExtractsOnlySelected(t *testing.T) {
	// Song selection is pure enumeration, so a fake decoder stands in for the
	// codec: only which songs are emitted is under test, not their audio.
	raw := twoSongVR9()
	r, err := extractReader(bytes.NewReader(raw), int64(len(raw)), silentDecoder{},
		Options{Songs: []SongKey{{Partition: 0, Ordinal: 2}}})
	require.NoError(t, err)
	tracks, _ := collectTracks(t, r)
	require.Len(t, tracks, 1, "only the selected song is emitted")
	assert.Equal(t, "BTWO", tracks[0].Song.Name)
}

func TestListReturnsSongCatalogWithoutDecoding(t *testing.T) {
	songs, devs := mustListBytes(t, twoSongVR9(), Options{})
	assert.Empty(t, devs)
	require.Len(t, songs, 2)

	assert.Equal(t, "AONE", songs[0].Name)
	assert.Equal(t, SongKey{Partition: 0, Ordinal: 1}, songs[0].Key)
	assert.Equal(t, "VR9", songs[0].Machine)
	assert.Equal(t, 1, songs[0].VTracks)
	assert.Positive(t, songs[0].SampleRate)
	assert.Positive(t, songs[1].Frames)
}

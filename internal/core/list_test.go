package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twoSongVR9 writes a synthetic 2-song VR9 CD disc (songs numbered 1 and 2),
// each with one populated v-track.
func twoSongVR9(t *testing.T) string {
	t.Helper()
	disc := vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{
			{Number: 1, Name: "AONE", Takes: []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: mt2Bytes(0x00, 4)}},
				Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1}}},
			{Number: 2, Name: "BTWO", Takes: []vsfix.Take{{FileID: 0x0200, Name: "TAKE0200", MT2: mt2Bytes(0x00, 4)}},
				Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0200, Track: 1, VTrack: 1}}},
		},
	}
	path := filepath.Join(t.TempDir(), "two.bin")
	require.NoError(t, os.WriteFile(path, disc.BuildRaw(), 0o644))
	return path
}

func TestOptionsSongsExtractsOnlySelected(t *testing.T) {
	path := twoSongVR9(t)
	r, err := Extract(path, Options{Songs: []SongKey{{Partition: 0, Ordinal: 2}}})
	require.NoError(t, err)
	tracks, _ := collectTracks(t, r)
	require.Len(t, tracks, 1, "only the selected song is emitted")
	assert.Equal(t, "BTWO", tracks[0].Song.Name)
}

func TestListReturnsSongCatalogWithoutDecoding(t *testing.T) {
	path := twoSongVR9(t) // existing helper
	songs, devs, err := List(path, Options{})
	require.NoError(t, err)
	assert.Empty(t, devs)
	require.Len(t, songs, 2)

	assert.Equal(t, "AONE", songs[0].Name)
	assert.Equal(t, SongKey{Partition: 0, Ordinal: 1}, songs[0].Key)
	assert.Equal(t, "VR9", songs[0].Machine)
	assert.Equal(t, 1, songs[0].VTracks)
	assert.Positive(t, songs[0].SampleRate)
	assert.Positive(t, songs[1].Frames)
}

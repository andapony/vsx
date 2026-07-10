package core

import (
	"bytes"
	"testing"

	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractProgress drives an in-memory extraction with a fake decoder (progress
// is an enumeration concern, so no codec is needed) and returns the Result.
func extractProgress(t *testing.T, raw []byte, opts Options) Result {
	t.Helper()
	r, err := extractReader(bytes.NewReader(raw), int64(len(raw)), silentDecoder{}, opts)
	require.NoError(t, err)
	return r
}

// TestExtractReportsProgress verifies the Options.Progress callback: an
// extraction reports an identifying phase, then one extracting milestone per
// song (with the 1-based index, the enumerated total, and the song name), and a
// final done phase — enough for a caller to render "song i/N (name)".
func TestExtractReportsProgress(t *testing.T) {
	disc := vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{
			{Number: 1, Name: "AONE", Takes: []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: silentMT2(4)}},
				Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1}}},
			{Number: 2, Name: "BTWO", Takes: []vsfix.Take{{FileID: 0x0200, Name: "TAKE0200", MT2: silentMT2(2)}},
				Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0200, Track: 1, VTrack: 1}}},
		},
	}
	var events []Progress
	r := extractProgress(t, disc.BuildRaw(), Options{Progress: func(p Progress) { events = append(events, p) }})
	collectTracks(t, r) // drain the stream so all progress fires

	require.NotEmpty(t, events)
	assert.Equal(t, ProgressIdentifying, events[0].Phase, "the first milestone is identifying the Source")
	assert.Equal(t, ProgressDone, events[len(events)-1].Phase, "the last milestone is done")

	var extracting []Progress
	for _, e := range events {
		if e.Phase == ProgressExtracting {
			extracting = append(extracting, e)
		}
	}
	require.Len(t, extracting, 2, "one extracting milestone per song")
	assert.Equal(t, 1, extracting[0].Song)
	assert.Equal(t, 2, extracting[0].TotalSongs)
	assert.Equal(t, "AONE", extracting[0].SongName)
	assert.Equal(t, 2, extracting[1].Song)
	assert.Equal(t, "BTWO", extracting[1].SongName)
}

// TestExtractProgressNilIsSafe verifies extraction is unaffected when no
// Progress callback is supplied (the default).
func TestExtractProgressNilIsSafe(t *testing.T) {
	tracks, _ := collectTracks(t, extractProgress(t, progressDisc(), Options{}))
	assert.NotEmpty(t, tracks)
}

func progressDisc() []byte {
	disc := vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{{Number: 1, Name: "ONE",
			Takes:  []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: silentMT2(4)}},
			Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1}}}},
	}
	return disc.BuildRaw()
}

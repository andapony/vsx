package core

import (
	"bytes"
	"testing"
	"time"

	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// detailBytes enumerates an in-memory disc into the verbose per-song view, the
// Detail counterpart to mustListBytes.
func detailBytes(t *testing.T, raw []byte, opts Options) ([]SongDetail, []Deviation) {
	t.Helper()
	d, devs, err := enumerateReader(bytes.NewReader(raw), int64(len(raw)), opts, parsedSong.detail)
	require.NoError(t, err)
	return d, devs
}

// TestDetailPerVTrackRows checks the verbose view's per-v-track rows: one row per
// populated v-track, carrying its track/v-track, user name, event count, length
// in frames, and the first/last event-record timestamps (§7).
func TestDetailPerVTrackRows(t *testing.T) {
	e1 := time.Date(2001, 2, 27, 10, 0, 0, 0, time.UTC)
	e2 := time.Date(2001, 2, 28, 11, 0, 0, 0, time.UTC)
	disc := vsfix.VR5Disc{SetID: [4]byte{5, 5, 5, 5}, Songs: []vsfix.VR5Song{{
		Number: 12, Name: "MIXDOWN", Created: tsCreated, Saved: tsSaved,
		Takes: []vsfix.VR5Take{{FileID: 0x9CC7, Name: "TAKE9CC7", MTP: mtpBytes(8)}},
		VTracks: []vsfix.VR5VTrack{
			{Track: 1, VTrack: 1, Name: "Bass", Events: []vsfix.VR5Event{
				{Start: 0, End: 4, FileID: 0x9CC7, Stamp: e1},
				{Start: 4, End: 8, FileID: 0x9CC7, Stamp: e2},
			}},
			{Track: 3, VTrack: 16, Name: "", Events: []vsfix.VR5Event{
				{Start: 0, End: 4, FileID: 0x9CC7, Stamp: e1},
			}},
		},
	}}}
	details, devs := detailBytes(t, disc.BuildRaw(), Options{})
	assert.Empty(t, devs)
	require.Len(t, details, 1)
	d := details[0]
	assert.Equal(t, "MIXDOWN", d.Info.Name)
	require.Len(t, d.Tracks, 2, "only populated v-tracks get a row")

	bass := d.Tracks[0]
	assert.Equal(t, 1, bass.Track)
	assert.Equal(t, 1, bass.VTrack)
	assert.Equal(t, "Bass", bass.Name)
	assert.Equal(t, 2, bass.Events)
	assert.Equal(t, 8, bass.Frames, "length is the v-track's own end frame")
	assert.Equal(t, e1, bass.First, "first event stamp")
	assert.Equal(t, e2, bass.Last, "last event stamp")

	def := d.Tracks[1]
	assert.Equal(t, 3, def.Track)
	assert.Equal(t, 16, def.VTrack)
	assert.Empty(t, def.Name, "a default V.T name is not user-assigned")
	assert.Equal(t, 1, def.Events)
	assert.Equal(t, e1, def.First)
	assert.Equal(t, e1, def.Last)
}

// TestDetailAgreesWithExtract locks the by-construction agreement (§8): the
// verbose view's v-track set and lengths, derived from the same parsed timeline,
// match exactly what Extract writes for the same song.
func TestDetailAgreesWithExtract(t *testing.T) {
	disc := vsfix.VR5Disc{SetID: [4]byte{5, 5, 5, 5}, Songs: []vsfix.VR5Song{{
		Number: 12, Name: "MIXDOWN",
		Takes: []vsfix.VR5Take{{FileID: 0x9CC7, Name: "TAKE9CC7", MTP: mtpBytes(4)}},
		VTracks: []vsfix.VR5VTrack{
			{Track: 1, VTrack: 1, Name: "Bass", Events: []vsfix.VR5Event{{Start: 0, End: 4, FileID: 0x9CC7}}},
			{Track: 2, VTrack: 5, Name: "Keys", Events: []vsfix.VR5Event{{Start: 0, End: 4, FileID: 0x9CC7}}},
		},
	}}}
	raw := disc.BuildRaw()

	details, _ := detailBytes(t, raw, Options{})
	require.Len(t, details, 1)

	tracks, devs := collectTracks(t, mustExtractBytes(t, raw, Options{}))
	assert.Empty(t, devs, "the disc extracts cleanly")

	// Same v-track set, and each row's frame length scales to the extracted
	// sample count through the shared samples-per-frame framing.
	require.Len(t, details[0].Tracks, len(tracks))
	byVT := map[[2]int]int{} // (track,vtrack) -> extracted sample count
	for _, tr := range tracks {
		byVT[[2]int{tr.Track, tr.VTrack}] = len(tr.PCM.Samples)
	}
	for _, row := range details[0].Tracks {
		samples, ok := byVT[[2]int{row.Track, row.VTrack}]
		require.True(t, ok, "detail v-track %d/%d was extracted", row.Track, row.VTrack)
		assert.Equal(t, samples, row.Frames*samplesPerFrame,
			"detail length agrees with the extracted audio for v-track %d/%d", row.Track, row.VTrack)
	}
}

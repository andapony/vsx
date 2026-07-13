package core

import (
	"testing"
	"time"

	"github.com/andapony/vsx/internal/hddfix"
	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tsCreated/tsSaved are the two SONG.VR5 header timestamps (§4.4) the fixtures
// stamp and List must surface — distinct instants so a swap would be caught.
var (
	tsCreated = time.Date(2001, 2, 27, 9, 15, 30, 0, time.UTC)
	tsSaved   = time.Date(2001, 3, 10, 18, 42, 5, 0, time.UTC)
)

// vr5TimestampSong is a one-v-track VR5 CD song carrying both header timestamps.
func vr5TimestampSong() vsfix.VR5Song {
	return vsfix.VR5Song{
		Number: 12, Name: "MIXDOWN", Created: tsCreated, Saved: tsSaved,
		Takes: []vsfix.VR5Take{{FileID: 0x9CC7, Name: "TAKE9CC7", MTP: mtpBytes(4)}},
		VTracks: []vsfix.VR5VTrack{{Track: 1, VTrack: 1, Name: "Bass",
			Events: []vsfix.VR5Event{{Start: 0, End: 4, FileID: 0x9CC7}}}},
	}
}

// TestListSurfacesVR5TimestampsCD verifies the CD path decodes a song's
// created/last-saved stamps from the SONG.VR5 copy the catalog carries (§5.3) and
// surfaces them in the catalog row — no take decoded.
func TestListSurfacesVR5TimestampsCD(t *testing.T) {
	disc := vsfix.VR5Disc{SetID: [4]byte{5, 5, 5, 5}, Songs: []vsfix.VR5Song{vr5TimestampSong()}}
	songs, devs := mustListBytes(t, disc.BuildRaw(), Options{})
	assert.Empty(t, devs, "a well-formed VR5 disc lists without deviations")
	require.Len(t, songs, 1)
	assert.Equal(t, tsCreated, songs[0].Created)
	assert.Equal(t, tsSaved, songs[0].Saved)
}

// TestListSurfacesVR5TimestampsHDD verifies the HDD path decodes the same stamps
// from the song directory's SONG.VR5 file (§4.4), so List on an HDD image and on
// a CD copy of the same song report identical stamps.
func TestListSurfacesVR5TimestampsHDD(t *testing.T) {
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{Songs: []hddfix.Song{{
		Number: 12, Name: "MIXDOWN", Ext: "VR5", Format: 0x05,
		Created: tsCreated, Saved: tsSaved,
		Takes:  []hddfix.Take{{NameCluster: 0x0100, Content: mtpBytes(4)}},
		Events: []hddfix.Event{{Start: 0, End: 4, NameCluster: 0x0100, Track: 1, VTrack: 1}},
	}}}}}
	songs, devs := mustListBytes(t, disk.Build(), Options{})
	assert.Empty(t, devs, "a well-formed VR5 HDD image lists without deviations")
	require.Len(t, songs, 1)
	assert.Equal(t, tsCreated, songs[0].Created)
	assert.Equal(t, tsSaved, songs[0].Saved)
}

// TestListVR9HasNoTimestamps pins that VR9 songs (which stamp nothing anywhere,
// §4.4) carry the zero Time in both columns — the display layer's placeholder.
func TestListVR9HasNoTimestamps(t *testing.T) {
	songs, _ := mustListBytes(t, twoSongVR9(), Options{})
	require.NotEmpty(t, songs)
	for _, s := range songs {
		assert.True(t, s.Created.IsZero(), "VR9 song %q has no created stamp", s.Name)
		assert.True(t, s.Saved.IsZero(), "VR9 song %q has no saved stamp", s.Name)
	}
}

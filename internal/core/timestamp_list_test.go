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
// §4.4) carry the zero Time in all three timestamp columns — the placeholder.
func TestListVR9HasNoTimestamps(t *testing.T) {
	songs, _ := mustListBytes(t, twoSongVR9(), Options{})
	require.NotEmpty(t, songs)
	for _, s := range songs {
		assert.True(t, s.Created.IsZero(), "VR9 song %q has no created stamp", s.Name)
		assert.True(t, s.Saved.IsZero(), "VR9 song %q has no saved stamp", s.Name)
		assert.True(t, s.Modified.IsZero(), "VR9 records have no event timestamp (§7)")
	}
}

// The two event-record stamps a re-saved-but-unedited song carries: both predate
// the song's last-saved stamp (tsSaved), so Modified — the latest event — is
// earlier than Saved (a re-save without edits bumps saved but adds no event).
var (
	tsEvent1 = time.Date(2001, 2, 27, 10, 0, 0, 0, time.UTC)
	tsEvent2 = time.Date(2001, 2, 28, 11, 30, 0, 0, time.UTC)
)

// TestListModifiedIsLatestEventStampCD verifies the CD VR5 path surfaces the
// maximum event-record timestamp (§7) as Modified, and that for a re-saved song
// it is earlier than the header's Saved stamp.
func TestListModifiedIsLatestEventStampCD(t *testing.T) {
	song := vr5TimestampSong()
	song.VTracks[0].Events = []vsfix.VR5Event{
		{Start: 0, End: 4, FileID: 0x9CC7, Stamp: tsEvent1},
		{Start: 4, End: 8, FileID: 0x9CC7, Stamp: tsEvent2},
	}
	disc := vsfix.VR5Disc{SetID: [4]byte{5, 5, 5, 5}, Songs: []vsfix.VR5Song{song}}
	songs, devs := mustListBytes(t, disc.BuildRaw(), Options{})
	assert.Empty(t, devs)
	require.Len(t, songs, 1)
	assert.Equal(t, tsEvent2, songs[0].Modified, "Modified is the latest event stamp")
	assert.True(t, songs[0].Modified.Before(songs[0].Saved), "re-saved-but-unedited: Modified < Saved")
}

// TestListModifiedIsLatestEventStampHDD verifies the same over the HDD VR5 event
// list (§4.5), which shares the record parse with the CD form.
func TestListModifiedIsLatestEventStampHDD(t *testing.T) {
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{Songs: []hddfix.Song{{
		Number: 12, Name: "MIXDOWN", Ext: "VR5", Format: 0x05,
		Created: tsCreated, Saved: tsSaved,
		Takes: []hddfix.Take{{NameCluster: 0x0100, Content: mtpBytes(4)}},
		Events: []hddfix.Event{
			{Start: 0, End: 4, NameCluster: 0x0100, Track: 1, VTrack: 1, Stamp: tsEvent1},
			{Start: 4, End: 8, NameCluster: 0x0100, Track: 1, VTrack: 1, Stamp: tsEvent2},
		},
	}}}}}
	songs, devs := mustListBytes(t, disk.Build(), Options{})
	assert.Empty(t, devs)
	require.Len(t, songs, 1)
	assert.Equal(t, tsEvent2, songs[0].Modified)
	assert.True(t, songs[0].Modified.Before(songs[0].Saved), "re-saved-but-unedited: Modified < Saved")
}

// TestListModifiedZeroWithoutStampedEvents pins the placeholder cases: a VR5 song
// whose events carry no stamp (all-zero 0x28) has a zero Modified, distinct from
// its dated Created/Saved.
func TestListModifiedZeroWithoutStampedEvents(t *testing.T) {
	song := vr5TimestampSong() // its single event leaves Stamp zero
	disc := vsfix.VR5Disc{SetID: [4]byte{5, 5, 5, 5}, Songs: []vsfix.VR5Song{song}}
	songs, _ := mustListBytes(t, disc.BuildRaw(), Options{})
	require.Len(t, songs, 1)
	assert.False(t, songs[0].Created.IsZero(), "header stamps are still present")
	assert.True(t, songs[0].Modified.IsZero(), "no stamped event ⇒ Modified is the placeholder")
}

// TestListModifiedZeroWithNoEvents pins the AC's distinct "song with zero events"
// case: a VR5 song with an empty timeline (no v-track entries at all) still lists,
// with its header stamps present but Modified the zero placeholder.
func TestListModifiedZeroWithNoEvents(t *testing.T) {
	disc := vsfix.VR5Disc{SetID: [4]byte{5, 5, 5, 5}, Songs: []vsfix.VR5Song{
		{Number: 20, Name: "EMPTY", Created: tsCreated, Saved: tsSaved}, // no VTracks, no Takes
	}}
	songs, _ := mustListBytes(t, disc.BuildRaw(), Options{})
	require.Len(t, songs, 1)
	assert.Equal(t, 0, songs[0].VTracks, "no populated v-tracks")
	assert.Equal(t, tsCreated, songs[0].Created, "header stamps still present")
	assert.True(t, songs[0].Modified.IsZero(), "zero events ⇒ Modified is the placeholder")
}

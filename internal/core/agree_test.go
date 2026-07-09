package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andapony/vsx/internal/hddfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHDDListExtractAgreeOnPrologueDeviations pins the prologue half of the
// by-construction guarantee: a defective song — a valid SONG header with an
// unknown sample-rate byte, then no EVENTLST — makes the prologue emit both a
// rate deviation and a no-event-list deviation. List and Extract run the one
// parseHDDSong prologue, so they report the identical set. (Before the prologue
// was shared, Extract dropped the rate deviation when the event list was absent,
// while List kept it; this song has no takes, so Extract adds no take/build
// deviations and the two sets can be compared whole.)
func TestHDDListExtractAgreeOnPrologueDeviations(t *testing.T) {
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{Songs: []hddfix.Song{{
		Number: 1, Name: "BAD", Ext: "VR9",
		Rate:          0x05, // low nibble ≥ 5 ⇒ unknown rate (§3) deviation
		OmitEventList: true, // ⇒ "no EVENTLST" (§4.3) deviation
	}}}}}
	path := filepath.Join(t.TempDir(), "defective.img")
	require.NoError(t, os.WriteFile(path, disk.Build(), 0o644))

	listDevs := listDeviations(t, path)
	extractDevs := extractDeviations(t, path)

	assert.Equal(t, listDevs, extractDevs,
		"List and Extract report the same prologue deviations for the same defective song")
	assert.Len(t, listDevs, 2, "both the unknown-rate and no-event-list deviations are reported")
}

// listDeviations runs List over a source and returns its deviations.
func listDeviations(t *testing.T, path string) []Deviation {
	t.Helper()
	_, devs, err := List(path, Options{})
	require.NoError(t, err)
	return devs
}

// extractDeviations fully consumes an Extract over a source and returns its
// deviations (available only once the track iterator is drained).
func extractDeviations(t *testing.T, path string) []Deviation {
	t.Helper()
	r, err := Extract(path, Options{})
	require.NoError(t, err)
	_, devs := collectTracks(t, r)
	return devs
}

// TestListStatsAgreeWithExtract pins the by-construction guarantee for the
// catalog stats: over one songTimeline, summarizeVTracks (List) reports the same
// populated v-track count and frame length that buildTracks (Extract) actually
// produces — the number of TrackResults and the longest track's frame span.
// Both now read the one vtrackStats rule, so any change to "populated" or
// "length" that broke the agreement would fail here rather than silently
// diverging List from Extract.
func TestListStatsAgreeWithExtract(t *testing.T) {
	// A populated v-track, an erase-only v-track (not populated), and a second
	// populated v-track that runs longer — so both count and length are exercised.
	events := []vr9Event{
		{start: 12, end: 20, fileID: 0xA, code: 0}, // T1/V1: populated, 8 frames
		{start: 12, end: 16, fileID: 0, code: 1},   // T1/V2: erase only, not populated
		{start: 20, end: 40, fileID: 0xB, code: 8}, // T2/V1: populated, 28 frames (the longest)
	}
	takes := map[uint16]PCM{
		0xA: decodeTake(t, mt2Bytes(0x11, 8)),
		0xB: decodeTake(t, mt2Bytes(0x22, 32)),
	}
	st := vr9Timeline(events)

	tracks, _ := buildTracks(st, takes, SongRef{Number: 1},
		audioSpec{sampleRate: 44100, format: FormatMT2, clusterSize: blockSize}, false)
	vtracks, frames := summarizeVTracks(st)

	assert.Equal(t, len(tracks), vtracks, "List's populated count equals the tracks Extract builds")

	longest := 0
	for _, tr := range tracks {
		if n := len(tr.PCM.Samples) / samplesPerFrame; n > longest {
			longest = n
		}
	}
	assert.Equal(t, longest, frames, "List's frame length equals Extract's longest track")
}

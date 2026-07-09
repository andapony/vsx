package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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

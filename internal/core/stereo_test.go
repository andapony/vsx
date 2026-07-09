package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// song1 is a fixed SongRef the stereo tests replay their v-tracks under.
var song1 = SongRef{Number: 1, Name: "S"}

// mt2Spec is the audioSpec the stereo tests decode against (MT2, 44.1 kHz).
func mt2Spec() audioSpec {
	return audioSpec{sampleRate: 44100, format: FormatMT2, clusterSize: blockSize}
}

// findTrack returns the single result whose left track is t, or fails.
func findTrack(t *testing.T, trs []TrackResult, track int) TrackResult {
	t.Helper()
	for _, tr := range trs {
		if tr.Track == track {
			return tr
		}
	}
	t.Fatalf("no result for track %d", track)
	return TrackResult{}
}

// TestPairMatchedAdjacentTracks is the §8.4 heuristic as a test: two adjacent
// physical tracks that each have exactly one populated v-track with identical
// event geometry (same counts, same Start/End frames) collapse, under --stereo,
// into one stereo result — the lower track the left channel, the higher the
// right — replacing the two monos. Without --stereo the same input stays two
// mono v-tracks.
func TestPairMatchedAdjacentTracks(t *testing.T) {
	takeL := decodeTake(t, mt2Bytes(0x11, 4))
	takeR := decodeTake(t, mt2Bytes(0x22, 4))
	takes := map[uint16]PCM{0xA: takeL, 0xB: takeR}

	// code 0 = T1/V1, code 8 = T2/V1: identical Start/End frames.
	events := []vr9Event{
		{start: 12, end: 16, fileID: 0xA, code: 0},
		{start: 12, end: 16, fileID: 0xB, code: 8},
	}

	// Off by default: two mono v-tracks, no stereo result.
	mono, devs := buildTracks(vr9Timeline(events), takes, song1, mt2Spec(), false)
	require.Empty(t, devs)
	require.Len(t, mono, 2)
	for _, tr := range mono {
		assert.Nil(t, tr.Right, "no pairing without --stereo")
	}

	// With --stereo: one interleaved stereo result, left = lower track.
	st, devs := buildTracks(vr9Timeline(events), takes, song1, mt2Spec(), true)
	require.Empty(t, devs)
	require.Len(t, st, 1, "the matched pair collapses to one result")
	pair := st[0]
	assert.Equal(t, 1, pair.Track, "left channel is the lower track")
	assert.Equal(t, 2, pair.PairTrack, "right channel is the adjacent higher track")
	require.NotNil(t, pair.Right, "the result is stereo")
	assert.Equal(t, takeL.Samples, pair.PCM.Samples, "left channel is the lower track's audio")
	assert.Equal(t, takeR.Samples, pair.Right.Samples, "right channel is the higher track's audio")
}

// TestNoPairWhenTrackHasMultipleVTracks verifies the conservative guard: a
// physical track with more than one populated v-track is ambiguous and is never
// paired, even with an adjacent single-v-track neighbour whose events would
// otherwise match.
func TestNoPairWhenTrackHasMultipleVTracks(t *testing.T) {
	takes := map[uint16]PCM{
		0xA: decodeTake(t, mt2Bytes(0x11, 4)),
		0xB: decodeTake(t, mt2Bytes(0x22, 4)),
		0xC: decodeTake(t, mt2Bytes(0x33, 4)),
	}
	// T1 has two populated v-tracks (codes 0,1); T2/V1 (code 8) matches T1/V1.
	events := []vr9Event{
		{start: 12, end: 16, fileID: 0xA, code: 0},
		{start: 12, end: 16, fileID: 0xB, code: 1},
		{start: 12, end: 16, fileID: 0xC, code: 8},
	}
	trs, devs := buildTracks(vr9Timeline(events), takes, song1, mt2Spec(), true)
	require.Empty(t, devs)
	require.Len(t, trs, 3, "ambiguous track stays mono; nothing pairs")
	for _, tr := range trs {
		assert.Nil(t, tr.Right, "no stereo result formed")
	}
}

// TestNoPairWhenEventsDiffer verifies that adjacent single-v-track tracks whose
// event geometry differs (here an End frame) are left as two monos.
func TestNoPairWhenEventsDiffer(t *testing.T) {
	takes := map[uint16]PCM{0xA: decodeTake(t, mt2Bytes(0x11, 4)), 0xB: decodeTake(t, mt2Bytes(0x22, 8))}
	events := []vr9Event{
		{start: 12, end: 16, fileID: 0xA, code: 0}, // T1/V1
		{start: 12, end: 20, fileID: 0xB, code: 8}, // T2/V1: different End
	}
	trs, devs := buildTracks(vr9Timeline(events), takes, song1, mt2Spec(), true)
	require.Empty(t, devs)
	require.Len(t, trs, 2, "mismatched events do not pair")
	for _, tr := range trs {
		assert.Nil(t, tr.Right)
	}
}

// TestNoPairWhenTracksNotAdjacent verifies that two single-v-track tracks with
// matching events but a gap between them (an empty track in between) are not a
// pair — only physically adjacent tracks pair.
func TestNoPairWhenTracksNotAdjacent(t *testing.T) {
	takes := map[uint16]PCM{0xA: decodeTake(t, mt2Bytes(0x11, 4)), 0xB: decodeTake(t, mt2Bytes(0x22, 4))}
	// T1/V1 (code 0) and T3/V1 (code 16): track 2 is empty between them.
	events := []vr9Event{
		{start: 12, end: 16, fileID: 0xA, code: 0},
		{start: 12, end: 16, fileID: 0xB, code: 16},
	}
	trs, devs := buildTracks(vr9Timeline(events), takes, song1, mt2Spec(), true)
	require.Empty(t, devs)
	require.Len(t, trs, 2, "non-adjacent tracks do not pair")
	assert.Nil(t, findTrack(t, trs, 1).Right)
	assert.Nil(t, findTrack(t, trs, 3).Right)
}

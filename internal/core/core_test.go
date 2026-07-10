package core

import (
	"bytes"
	"testing"

	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResultTracksStreamsLazily verifies the bounded-memory guarantee:
// consuming only a prefix of a Result's tracks drives the underlying walk
// exactly that far and no further, so a large Source is never materialized.
// It backs a Result with a generator that records how many results it
// produced, consumes three, breaks, and asserts the generator was pulled
// exactly three times.
func TestResultTracksStreamsLazily(t *testing.T) {
	const huge = 1_000_000
	pulled := 0
	seq := func(yield func(TrackResult, error) bool) {
		for i := 0; i < huge; i++ {
			pulled++
			if !yield(TrackResult{VTrack: i}, nil) {
				return
			}
		}
	}
	r := Result{tracks: seq}

	seen := 0
	for tr, err := range r.Tracks() {
		require.NoError(t, err)
		require.Equal(t, seen, tr.VTrack, "results stream in order")
		seen++
		if seen == 3 {
			break
		}
	}
	assert.Equal(t, 3, seen, "consumed exactly the requested prefix")
	assert.Equal(t, 3, pulled, "generator pulled lazily, not fully materialized")
}

// TestEmptyResultTracksIsSafe verifies that a zero-value Result yields no
// tracks and does not panic, so callers can range over Tracks()
// unconditionally.
func TestEmptyResultTracksIsSafe(t *testing.T) {
	var r Result
	for range r.Tracks() {
		require.Fail(t, "zero-value Result should yield no tracks")
	}
}

// TestResultDeviations verifies that the deviations gathered during a walk are
// surfaced through Deviations() with their location, spec reference, and
// severity intact.
func TestResultDeviations(t *testing.T) {
	devs := []Deviation{
		{Location: "song 3 / v-track 12", SpecRef: "§5.5", Severity: SeverityWarning, Message: "unknown field value"},
	}
	r := Result{deviations: &devs}
	assert.Equal(t, devs, r.Deviations())
}

// TestExtractNonexistentSourceErrors verifies that pointing the path API at a
// file that cannot be opened returns an error rather than an empty success.
func TestExtractNonexistentSourceErrors(t *testing.T) {
	_, err := Extract("/nonexistent/does-not-exist.img", Options{})
	require.Error(t, err)
}

// TestExtractUnidentifiableSourceErrors verifies that a source whose bytes match
// no known archive and whose length is not even valid CD geometry is a hard
// error, not a silent empty success (issue #3: unidentifiable input exits with
// an error).
func TestExtractUnidentifiableSourceErrors(t *testing.T) {
	raw := []byte("placeholder")
	_, err := extractReader(bytes.NewReader(raw), int64(len(raw)), NewDecoder(), Options{})
	require.Error(t, err)
}

// TestExtractEmptyArchiveIsSafe verifies that a well-formed VR9 archive with no
// audio (one song, an empty event log) extracts cleanly: no tracks, no
// deviations, safe to range over.
func TestExtractEmptyArchiveIsSafe(t *testing.T) {
	disc := vsfix.Disc{SetID: [4]byte{1, 2, 3, 4}, Songs: []vsfix.Song{{Number: 1, Name: "EMPTY"}}}

	r := mustExtractBytes(t, disc.BuildRaw(), Options{})
	for range r.Tracks() {
		require.Fail(t, "an empty archive should yield no tracks")
	}
	assert.Empty(t, r.Deviations())
}

// TestSeverityString verifies each Severity renders a stable lowercase word, so
// the one shared Deviation rendering can show it (issue #27, seam 1).
func TestSeverityString(t *testing.T) {
	assert.Equal(t, "info", SeverityInfo.String())
	assert.Equal(t, "warning", SeverityWarning.String())
	assert.Equal(t, "error", SeverityError.String())
}

// TestDeviationStringRendersSeverityAndSpecRef verifies the shared Deviation
// String form carries the Severity core computes (never shown before issue #27)
// alongside the spec clause, location, and message, in one line the CLI can
// print at every site.
func TestDeviationStringRendersSeverityAndSpecRef(t *testing.T) {
	d := Deviation{Location: "song 3 / v-track 12", SpecRef: "§5.5",
		Severity: SeverityError, Message: "referenced take is absent"}
	assert.Equal(t, "deviation [error §5.5] song 3 / v-track 12: referenced take is absent", d.String())
}

// TestDeviationStringOmitsBlankSpecRef verifies a deviation with no spec clause
// (a request-level departure such as an unknown --song key) still renders
// cleanly, with the severity but no dangling bracket space.
func TestDeviationStringOmitsBlankSpecRef(t *testing.T) {
	d := Deviation{Location: "song selection", Severity: SeverityWarning,
		Message: "no song 9 on this source"}
	assert.Equal(t, "deviation [warning] song selection: no song 9 on this source", d.String())
}

// TestUnknownSongKeyYieldsDeviation verifies that a --song key matching no song
// on the Source surfaces as a warning deviation from the single extraction walk
// (issue #27, seam 3) — no separate enumeration pass — naming the key and
// pointing at --list. It matches no song, so nothing is extracted.
func TestUnknownSongKeyYieldsDeviation(t *testing.T) {
	raw := twoSongVR9()
	r, err := extractReader(bytes.NewReader(raw), int64(len(raw)), silentDecoder{},
		Options{Songs: []SongKey{{Partition: 0, Ordinal: 9}}})
	require.NoError(t, err)
	tracks, devs := collectTracks(t, r)
	assert.Empty(t, tracks, "an unknown key matches no song")
	require.Len(t, devs, 1)
	assert.Contains(t, devs[0].Message, "no song 9")
	assert.Contains(t, devs[0].Message, "--list")
	assert.Equal(t, SeverityWarning, devs[0].Severity)
	// A request-level departure carries no spec clause, so it renders with a
	// bare severity — not with the flag name inside the brackets.
	assert.Empty(t, devs[0].SpecRef)
	assert.Equal(t, "deviation [warning] song selection: "+
		"no song 9 on this source; run 'vsx --list' to see available songs", devs[0].String())
}

// TestKnownSongKeyYieldsNoUnknownDeviation verifies the mirror: a valid
// selection extracts its song and adds no unknown-key deviation.
func TestKnownSongKeyYieldsNoUnknownDeviation(t *testing.T) {
	raw := twoSongVR9()
	r, err := extractReader(bytes.NewReader(raw), int64(len(raw)), silentDecoder{},
		Options{Songs: []SongKey{{Partition: 0, Ordinal: 2}}})
	require.NoError(t, err)
	tracks, devs := collectTracks(t, r)
	require.Len(t, tracks, 1)
	assert.Empty(t, devs, "a valid selection adds no deviation")
}

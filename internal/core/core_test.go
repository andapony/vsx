package core

import (
	"os"
	"path/filepath"
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
	r := newResult(seq, nil)

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
	r := newResult(nil, devs)
	assert.Equal(t, devs, r.Deviations())
}

// TestExtractNonexistentSourceErrors verifies that pointing Extract at a path
// that cannot be opened returns an error rather than an empty success.
func TestExtractNonexistentSourceErrors(t *testing.T) {
	_, err := Extract(filepath.Join(t.TempDir(), "does-not-exist.img"), Options{})
	require.Error(t, err)
}

// TestExtractUnidentifiableSourceErrors verifies that a source whose bytes match
// no known archive and whose length is not even valid CD geometry is a hard
// error, not a silent empty success (issue #3: unidentifiable input exits with
// an error).
func TestExtractUnidentifiableSourceErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "src.img")
	require.NoError(t, os.WriteFile(path, []byte("placeholder"), 0o644))

	_, err := Extract(path, Options{})
	require.Error(t, err)
}

// TestExtractEmptyArchiveIsSafe verifies that a well-formed VR9 archive with no
// audio (one song, an empty event log) extracts cleanly: no tracks, no
// deviations, safe to range over.
func TestExtractEmptyArchiveIsSafe(t *testing.T) {
	disc := vsfix.Disc{SetID: [4]byte{1, 2, 3, 4}, Songs: []vsfix.Song{{Number: 1, Name: "EMPTY"}}}
	path := filepath.Join(t.TempDir(), "empty.bin")
	require.NoError(t, os.WriteFile(path, disc.BuildRaw(), 0o644))

	r, err := Extract(path, Options{})
	require.NoError(t, err)
	for range r.Tracks() {
		require.Fail(t, "an empty archive should yield no tracks")
	}
	assert.Empty(t, r.Deviations())
}

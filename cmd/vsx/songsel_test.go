package main

import (
	"testing"

	"github.com/andapony/vsx/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSongSelCommaListInOneOccurrence verifies a single --song occurrence may
// carry a comma list, each key parsed and accumulated in order.
func TestSongSelCommaListInOneOccurrence(t *testing.T) {
	var s songSel
	require.NoError(t, s.Set("1,2.7,3"))
	assert.Equal(t, []core.SongKey{
		{Partition: 0, Ordinal: 1},
		{Partition: 2, Ordinal: 7},
		{Partition: 0, Ordinal: 3},
	}, s.keys)
}

// TestSongSelRepeatedFlagAccumulates verifies repeated --song occurrences (each
// a separate Set call) append rather than replace, so keys from every
// occurrence survive.
func TestSongSelRepeatedFlagAccumulates(t *testing.T) {
	var s songSel
	require.NoError(t, s.Set("1"))
	require.NoError(t, s.Set("2,3"))
	assert.Equal(t, []core.SongKey{
		{Ordinal: 1}, {Ordinal: 2}, {Ordinal: 3},
	}, s.keys)
}

// TestSongSelTrimsAndSkipsBlanks verifies surrounding whitespace is trimmed and
// empty parts (a trailing comma, doubled commas) are skipped rather than
// rejected, so "1, ,2," yields exactly two keys.
func TestSongSelTrimsAndSkipsBlanks(t *testing.T) {
	var s songSel
	require.NoError(t, s.Set("1, ,2,"))
	assert.Equal(t, []core.SongKey{{Ordinal: 1}, {Ordinal: 2}}, s.keys)
}

// TestSongSelMalformedKeyErrors verifies a malformed part fails the whole Set
// with the parse error, surfacing the bad value.
func TestSongSelMalformedKeyErrors(t *testing.T) {
	var s songSel
	err := s.Set("1,nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

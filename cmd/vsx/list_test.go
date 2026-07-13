package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/andapony/vsx/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tabColumnStarts returns the visual start column of each tab-separated field
// once tabs are expanded against 8-wide tab stops — the same rule a terminal
// applies. A field ending exactly on a tab stop pushes the next tab a full stop
// further, which is how an 8-char header can shove its later columns out of line
// with the data rows.
func tabColumnStarts(line string) []int {
	fields := strings.Split(line, "\t")
	starts := make([]int, len(fields))
	col := 0
	for i, f := range fields {
		starts[i] = col
		col += len([]rune(f))
		if i < len(fields)-1 {
			col = (col/8 + 1) * 8 // advance to the next tab stop strictly past col
		}
	}
	return starts
}

// TestListHeaderSharesTabStopsWithRows pins issue #33: the header and every data
// row must line up at the same tab stops for every column, with both single- and
// double-digit v-track counts (the corpus has songs with 1 and with 54 v-tracks).
func TestListHeaderSharesTabStopsWithRows(t *testing.T) {
	// A VR9 row (placeholder timestamps) and a VR5 row (dated timestamps) so both
	// the "-" and the full "yyyy-MM-dd hh:mm:ss" cases must share the header's tab
	// stops for the wide CREATED/SAVED columns as well as the narrow ones.
	stamp := time.Date(2001, 2, 27, 12, 34, 56, 0, time.UTC)
	songs := []core.SongInfo{
		{Key: core.SongKey{Ordinal: 1}, StoredNumber: 1, Machine: "VR9", VTracks: 1, Name: "SONG ONE"},
		{Key: core.SongKey{Ordinal: 2}, StoredNumber: 2, Machine: "VR5", VTracks: 54, Name: "SONG TWO",
			Created: stamp, Saved: stamp},
	}
	var stdout, stderr bytes.Buffer
	require.Equal(t, exitOK, runList(songs, nil, &stdout, &stderr))

	header := strings.SplitN(stderr.String(), "\n", 2)[0]
	want := tabColumnStarts(header)
	for _, row := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		assert.Equal(t, want, tabColumnStarts(row),
			"row %q does not share the header's tab stops (header %q)", row, header)
	}
}

func TestListFlagPrintsTabSeparatedCatalog(t *testing.T) {
	src := writeDisc(t, twoSongTracerDisc())
	code, stdout, stderr := runCLI("--list", src)
	require.Equal(t, exitOK, code, "stderr: %s", stderr)

	// Header on stderr (human framing), never on stdout.
	assert.Contains(t, stderr, "NAME")
	assert.NotContains(t, stdout, "NAME")

	// Data rows: tab-separated, name last, one per song.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.Len(t, lines, 2)
	fields := strings.Split(lines[0], "\t")
	require.Len(t, fields, 8)              // KEY SONG# MACHINE VTRK LENGTH CREATED SAVED NAME
	assert.Equal(t, "1", fields[0])        // CD key = bare number
	assert.Equal(t, "SONG ONE", fields[7]) // name last (twoSongTracerDisc song 1 is "SONG ONE")
	assert.NotContains(t, stdout, ".wav")  // nothing extracted
}

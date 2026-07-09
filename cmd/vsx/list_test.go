package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	require.Len(t, fields, 6)              // KEY SONG# MACHINE V-TRACKS DURATION NAME
	assert.Equal(t, "1", fields[0])        // CD key = bare number
	assert.Equal(t, "SONG ONE", fields[5]) // name last (twoSongTracerDisc song 1 is "SONG ONE")
	assert.NotContains(t, stdout, ".wav")  // nothing extracted
}

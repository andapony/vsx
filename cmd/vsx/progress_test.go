package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/andapony/vsx/internal/core"
	"github.com/stretchr/testify/assert"
)

func TestFormatProgress(t *testing.T) {
	p := core.Progress{Phase: core.ProgressExtracting, Song: 3, TotalSongs: 12, SongName: "MIXDOWN"}
	line := formatProgress(p, 47, 80*time.Second)
	assert.Contains(t, line, "song 3/12 (MIXDOWN)")
	assert.Contains(t, line, "47 v-track(s)")
	assert.Contains(t, line, "1m20s", "elapsed is rounded to whole seconds")

	// No enumerated total yet → no denominator.
	noTotal := formatProgress(core.Progress{Phase: core.ProgressExtracting, Song: 3}, 0, time.Second)
	assert.Contains(t, noTotal, "song 3")
	assert.NotContains(t, noTotal, "/")

	assert.Contains(t, formatProgress(core.Progress{Phase: core.ProgressIdentifying}, 0, 2*time.Second), "identifying")
}

func TestStatusLineDrawsAndCoordinates(t *testing.T) {
	var buf bytes.Buffer
	s := newStatusLine(&buf, true)

	s.progress(core.Progress{Phase: core.ProgressExtracting, Song: 1, TotalSongs: 2, SongName: "AONE"})
	assert.Contains(t, buf.String(), "\r", "a transient line uses a carriage return")
	assert.Contains(t, buf.String(), "song 1/2 (AONE)")

	// A permanent line clears the transient line before writing.
	buf.Reset()
	s.logf("deviation [X] loc: msg\n")
	out := buf.String()
	assert.Contains(t, out, "deviation [X] loc: msg\n")
	assert.Less(t, strings.Index(out, "\r\033[K"), strings.Index(out, "deviation"),
		"the transient line is cleared before the permanent one is written")

	// Done removes the line.
	buf.Reset()
	s.progress(core.Progress{Phase: core.ProgressDone})
	assert.Contains(t, buf.String(), "\r\033[K")
}

func TestStatusLineDisabledIsClean(t *testing.T) {
	var buf bytes.Buffer
	s := newStatusLine(&buf, false)
	s.progress(core.Progress{Phase: core.ProgressExtracting, Song: 1, TotalSongs: 2, SongName: "AONE"})
	s.trackWritten()
	s.logf("deviation here\n")
	s.finish()
	assert.Equal(t, "deviation here\n", buf.String(),
		"a disabled status line writes only permanent messages — no CR or ANSI")
}

// TestNonTTYRunHasNoProgressEscapes locks the manifest/pipe-cleanliness contract:
// because the test streams are plain buffers (not a TTY), the run emits no
// carriage returns or ANSI escapes, yet the summary still prints.
func TestNonTTYRunHasNoProgressEscapes(t *testing.T) {
	src := writeDisc(t, tracerDisc())
	code, stdout, stderr := runCLI("-o", t.TempDir(), src)
	assert.Equal(t, exitOK, code, "stderr: %s", stderr)
	assert.NotContains(t, stderr, "\r", "no carriage-return progress on a non-TTY stderr")
	assert.NotContains(t, stderr, "\033", "no ANSI escapes on a non-TTY stderr")
	assert.Contains(t, stderr, "extracted", "the summary still prints")
	assert.NotContains(t, stdout, "\033", "stdout manifest stays clean")
}

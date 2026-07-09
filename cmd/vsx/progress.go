package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/andapony/vsx/internal/core"
)

// isTTY reports whether w is a terminal, so the transient progress line is only
// drawn for an interactive user and never pollutes a pipe, file, or CI log.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// statusLine renders a single, self-overwriting progress line on stderr while an
// extraction runs. It coordinates with the permanent stderr output (deviations,
// pair notices, the summary): logf erases the transient line, writes its
// message, and redraws underneath, so the two never tangle. When disabled (not a
// TTY, or -q/-v) every method degrades to a plain write or a no-op, so the piped
// and verbose paths behave exactly as before.
type statusLine struct {
	w        io.Writer
	on       bool
	start    time.Time
	last     core.Progress
	vtracks  int
	lastDraw time.Time
	drawn    bool
}

func newStatusLine(w io.Writer, on bool) *statusLine {
	return &statusLine{w: w, on: on, start: time.Now()}
}

// progress records a core milestone and refreshes the line (clearing it on Done).
func (s *statusLine) progress(p core.Progress) {
	s.last = p
	if p.Phase == core.ProgressDone {
		s.clear()
		return
	}
	s.draw(false)
}

// trackWritten bumps the running v-track count (the refresh happens when the
// track's manifest line is emitted).
func (s *statusLine) trackWritten() { s.vtracks++ }

// emit writes a permanent manifest line to out (stdout), clearing the transient
// stderr line first and redrawing it after — so a manifest path scrolls cleanly
// above a persistent progress line. Only the clear/redraw touch stderr (s.w);
// out receives just the line, so the stdout manifest stays free of any carriage
// returns or ANSI (the story-#29 contract). With the line disabled it is a plain
// Fprintln.
func (s *statusLine) emit(out io.Writer, line string) {
	s.clear()
	fmt.Fprintln(out, line)
	s.draw(true)
}

// draw paints the transient line, throttled to at most ~10 Hz so a fast run does
// not thrash the terminal. force bypasses the throttle (used right after a
// permanent line is written, so the status line reappears immediately).
func (s *statusLine) draw(force bool) {
	if !s.on || s.last.Phase == core.ProgressDone {
		return
	}
	now := time.Now()
	if !force && s.drawn && now.Sub(s.lastDraw) < 100*time.Millisecond {
		return
	}
	s.lastDraw = now
	fmt.Fprintf(s.w, "\r\033[K%s", formatProgress(s.last, s.vtracks, now.Sub(s.start)))
	s.drawn = true
}

// clear erases the transient line if one is showing.
func (s *statusLine) clear() {
	if s.on && s.drawn {
		fmt.Fprint(s.w, "\r\033[K")
		s.drawn = false
	}
}

// logf writes a permanent stderr line, clearing the transient progress line
// first and redrawing it afterward. With the status line disabled it is just a
// plain Fprintf.
func (s *statusLine) logf(format string, a ...any) {
	s.clear()
	fmt.Fprintf(s.w, format, a...)
	s.draw(true)
}

// finish removes the transient line for good (call once the run is done, before
// the summary is printed as ordinary output).
func (s *statusLine) finish() { s.clear() }

// formatProgress renders one status line from the latest milestone, the running
// v-track count, and elapsed time — e.g. "vsx: song 3/12 (MIXDOWN) — 47
// v-track(s) — 1m20s". Kept pure so it can be unit-tested.
func formatProgress(p core.Progress, vtracks int, elapsed time.Duration) string {
	el := elapsed.Round(time.Second)
	if p.Phase == core.ProgressExtracting {
		loc := fmt.Sprintf("song %d", p.Song)
		if p.TotalSongs > 0 {
			loc = fmt.Sprintf("song %d/%d", p.Song, p.TotalSongs)
		}
		if p.SongName != "" {
			loc += " (" + p.SongName + ")"
		}
		return fmt.Sprintf("vsx: %s — %d v-track(s) — %s", loc, vtracks, el)
	}
	return fmt.Sprintf("vsx: identifying source — %s", el)
}

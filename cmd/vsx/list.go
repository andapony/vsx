package main

import (
	"fmt"
	"io"
	"time"

	"github.com/andapony/vsx/internal/core"
)

// runList prints the song catalog: a tab-separated data row per song to stdout
// (name last, so the variable-width field never breaks the columns), with the
// header and any enumeration deviations on stderr. Returns the process exit code.
func runList(songs []core.SongInfo, devs []core.Deviation, stdout, stderr io.Writer) int {
	// Header labels stay ≤7 chars so none fills a full 8-wide tab stop; an
	// 8-char label (the old "V-TRACKS"/"DURATION") advances the next tab a whole
	// stop past the data rows' tab, knocking every later column out of line (#33).
	fmt.Fprintln(stderr, "KEY\tSONG#\tMACHINE\tVTRK\tLENGTH\tNAME")
	for _, s := range songs {
		fmt.Fprintf(stdout, "%s\t%d\t%s\t%d\t%s\t%s\n",
			s.Key.String(), s.StoredNumber, s.Machine, s.VTracks, mmss(s.Duration()), s.Name)
	}
	for _, d := range devs {
		fmt.Fprintln(stderr, d)
	}
	return exitOK
}

// mmss renders a duration as m:ss (the samples-per-frame framing lives in core's
// SongInfo.Duration). A zero duration yields "0:00".
func mmss(d time.Duration) string {
	secs := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

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
	// Narrow labels stay ≤7 chars so none fills a full 8-wide tab stop; an 8-char
	// label (the old "V-TRACKS"/"DURATION") advances the next tab a whole stop past
	// the data rows' tab, knocking every later column out of line (#33). The
	// timestamp labels are instead padded to the fixed timestamp width so header
	// and data reach the same tab stop for those wide columns too (§4.4/#34).
	fmt.Fprintf(stderr, "KEY\tSONG#\tMACHINE\tVTRK\tLENGTH\t%s\t%s\t%s\tNAME\n",
		padStamp("CREATED"), padStamp("SAVED"), padStamp("MODIFIED"))
	for _, s := range songs {
		fmt.Fprintf(stdout, "%s\t%d\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			s.Key.String(), s.StoredNumber, s.Machine, s.VTracks, mmss(s.Duration()),
			padStamp(fmtStamp(s.Created)), padStamp(fmtStamp(s.Saved)), padStamp(fmtStamp(s.Modified)), s.Name)
	}
	for _, d := range devs {
		fmt.Fprintln(stderr, d)
	}
	return exitOK
}

// stampWidth is the fixed column width a rendered timestamp occupies:
// len("2006-01-02 15:04:05"). Every timestamp cell (dated or placeholder) and its
// header label are padded to this width so a single tab still lands the next
// column at the same tab stop on both the header and every data row, keeping the
// wide timestamp columns aligned (#33/#34).
const stampWidth = 19

// fmtStamp renders a SONG.VR5/event timestamp (§4.4) in a fixed, locale-
// independent form; the zero Time — a VR9 song, or a slot the media never
// stamped — renders as the "-" placeholder rather than a year-0 date.
func fmtStamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

// padStamp left-justifies a timestamp cell (or its header label) to stampWidth so
// the column stays tab-aligned across dated, placeholder, and header rows.
func padStamp(s string) string {
	return fmt.Sprintf("%-*s", stampWidth, s)
}

// mmss renders a duration as m:ss (the samples-per-frame framing lives in core's
// SongInfo.Duration). A zero duration yields "0:00".
func mmss(d time.Duration) string {
	secs := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

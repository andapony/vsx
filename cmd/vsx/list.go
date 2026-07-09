package main

import (
	"fmt"
	"io"

	"github.com/andapony/vsx/internal/core"
)

// runList prints the song catalog: a tab-separated data row per song to stdout
// (name last, so the variable-width field never breaks the columns), with the
// header and any enumeration deviations on stderr. Returns the process exit code.
func runList(songs []core.SongInfo, devs []core.Deviation, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "KEY\tSONG#\tMACHINE\tV-TRACKS\tDURATION\tNAME")
	for _, s := range songs {
		fmt.Fprintf(stdout, "%s\t%d\t%s\t%d\t%s\t%s\n",
			s.Key.String(), s.StoredNumber, s.Machine, s.VTracks, mmss(s.Frames, s.SampleRate), s.Name)
	}
	for _, d := range devs {
		fmt.Fprintf(stderr, "deviation [%s] %s: %s\n", d.SpecRef, d.Location, d.Message)
	}
	return exitOK
}

// mmss renders a frame count as m:ss using the song's sample rate (16 samples per
// frame). Zero rate yields "0:00".
func mmss(frames, rate int) string {
	if rate <= 0 {
		return "0:00"
	}
	secs := frames * 16 / rate
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

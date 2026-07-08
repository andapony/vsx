// Command vsx extracts audio from Roland VS-series storage media (HDD images
// and CD "Song Copy Archive" dumps) into WAV files.
//
// It follows the Unix stdout/stderr contract: the machine-readable extraction
// manifest is written to stdout, while human-facing diagnostics — deviations
// and errors — go to stderr. Exit status distinguishes a clean run from one
// that recovered audio despite deviations, and from outright failure.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/andapony/vsx/internal/core"
)

// Exit codes. Best-effort mode (ADR-0002) exits non-zero when any deviation
// occurred, distinct from a usage error or a fatal failure.
const (
	exitOK         = 0 // clean run, no deviations
	exitDeviations = 1 // audio recovered, but the Source deviated from the spec
	exitUsage      = 2 // bad invocation
	exitError      = 3 // fatal: the Source could not be processed
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses args, drives an extraction, and returns the process exit code. It
// takes the output streams as parameters so it is exercisable in tests.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vsx", flag.ContinueOnError)
	fs.SetOutput(stderr)
	strict := fs.Bool("strict", false, "conformance gate: abort on the first deviation with no output")
	fs.Usage = func() { usage(stderr, fs) }

	if err := fs.Parse(args); err != nil {
		return exitUsage // flag has already written usage/diagnostics to stderr
	}
	if fs.NArg() != 1 {
		usage(stderr, fs)
		return exitUsage
	}

	result, err := core.Extract(fs.Arg(0), core.Options{Strict: *strict})
	if err != nil {
		fmt.Fprintf(stderr, "vsx: %v\n", err)
		return exitError
	}

	// stdout carries the manifest, one line per extracted (song, v-track).
	for tr, err := range result.Tracks() {
		if err != nil {
			fmt.Fprintf(stderr, "vsx: %v\n", err)
			return exitError
		}
		fmt.Fprintf(stdout, "%03d\t%d\t%d\t%d\n",
			tr.Song.Number, tr.Track, tr.VTrack, len(tr.PCM.Samples))
	}

	// stderr carries diagnostics; any deviation flips the exit code.
	deviated := false
	for _, d := range result.Deviations() {
		fmt.Fprintf(stderr, "deviation [%s] %s: %s\n", d.SpecRef, d.Location, d.Message)
		deviated = true
	}
	if deviated {
		return exitDeviations
	}
	return exitOK
}

func usage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprint(w, "vsx — extract audio from Roland VS-series media to WAV\n\n")
	fmt.Fprint(w, "usage: vsx [flags] <source>\n\n")
	fmt.Fprint(w, "  <source>  path to an HDD image or a CD backup-set directory\n\n")
	fmt.Fprint(w, "flags:\n")
	fs.PrintDefaults()
}

// Command vsx extracts audio from Roland VS-series storage media (HDD images
// and CD "Song Copy Archive" dumps) into WAV files.
//
// It follows the Unix stdout/stderr contract: the machine-readable extraction
// manifest — one line per written WAV — is written to stdout, while human-facing
// diagnostics (deviations, the end-of-run summary, and errors) go to stderr.
// Exit status distinguishes a clean run from one that recovered audio despite
// deviations, and from outright failure.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/andapony/vsx/internal/core"
	"github.com/andapony/vsx/internal/wav"
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

// run parses args, drives an extraction, writes WAVs, and returns the process
// exit code. It takes the output streams as parameters so it is exercisable in
// tests.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vsx", flag.ContinueOnError)
	fs.SetOutput(stderr)
	strict := fs.Bool("strict", false, "conformance gate: abort on the first deviation with no output")
	as := fs.String("as", "", "force the source profile when detection is ambiguous (vr9|vr5)")
	outDir := fs.String("o", ".", "output directory to write song folders into")
	verbose := fs.Bool("v", false, "verbose: log each extracted v-track to stderr")
	quiet := fs.Bool("q", false, "quiet: suppress deviations and the summary")
	fs.Usage = func() { usage(stderr, fs) }

	if err := fs.Parse(args); err != nil {
		return exitUsage // flag has already written usage/diagnostics to stderr
	}
	if fs.NArg() != 1 {
		usage(stderr, fs)
		return exitUsage
	}

	result, err := core.Extract(fs.Arg(0), core.Options{Strict: *strict, As: *as})
	if err != nil {
		fmt.Fprintf(stderr, "vsx: %v\n", err)
		return exitError
	}

	// Write one mono WAV per populated v-track, listing each on the stdout
	// manifest. Deviations are collected during the walk and reported after.
	songs := map[int]bool{}
	nTracks := 0
	for tr, err := range result.Tracks() {
		if err != nil {
			fmt.Fprintf(stderr, "vsx: %v\n", err)
			return exitError
		}
		path, err := writeTrack(*outDir, tr)
		if err != nil {
			fmt.Fprintf(stderr, "vsx: %v\n", err)
			return exitError
		}
		fmt.Fprintln(stdout, path)
		songs[tr.Song.Number] = true
		nTracks++
		if *verbose && !*quiet {
			fmt.Fprintf(stderr, "extracted %s (%d samples @ %d Hz)\n", path, len(tr.PCM.Samples), tr.Take.SampleRate)
		}
	}

	devs := result.Deviations()
	if !*quiet {
		for _, d := range devs {
			fmt.Fprintf(stderr, "deviation [%s] %s: %s\n", d.SpecRef, d.Location, d.Message)
		}
		fmt.Fprintf(stderr, "vsx: extracted %d v-track(s) across %d song(s); %d deviation(s)\n",
			nTracks, len(songs), len(devs))
	}

	if len(devs) > 0 {
		return exitDeviations
	}
	return exitOK
}

// writeTrack encodes one v-track's PCM to a WAV file under
// "<outDir>/<NN> - <name>/T<track>-V<vtrack>.wav" and returns the path written.
// The song folder is created on demand, so v-tracks of the same song share it.
func writeTrack(outDir string, tr core.TrackResult) (string, error) {
	wavBytes, err := wav.Encode(tr.PCM.Samples, tr.Take.SampleRate, tr.PCM.BitDepth)
	if err != nil {
		return "", fmt.Errorf("encoding song %d T%d-V%d: %w", tr.Song.Number, tr.Track, tr.VTrack, err)
	}
	dir := filepath.Join(outDir, songDir(tr.Song))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	name := fmt.Sprintf("T%d-V%d.wav", tr.Track, tr.VTrack)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, wavBytes, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// songDir builds a song's output folder name: the zero-padded catalog number
// (always present, so two songs with identical names stay distinct) followed by
// the song name.
func songDir(s core.SongRef) string {
	return fmt.Sprintf("%02d - %s", s.Number, sanitize(s.Name))
}

// sanitize strips path separators from a song name so it is safe as a single
// path component.
func sanitize(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, string(os.PathSeparator), "_")
	return strings.TrimSpace(name)
}

func usage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprint(w, "vsx — extract audio from Roland VS-series media to WAV\n\n")
	fmt.Fprint(w, "usage: vsx [flags] <source>\n\n")
	fmt.Fprint(w, "  <source>  path to an HDD image or a CD backup-set dump\n\n")
	fmt.Fprint(w, "flags:\n")
	fs.PrintDefaults()
}

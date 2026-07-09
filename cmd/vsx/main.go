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
	as := fs.String("as", "", "force the source type when detection is ambiguous (hdd|cd|vr9|vr5)")
	stereo := fs.Bool("stereo", false, "pair adjacent matched tracks into one interleaved stereo WAV (§8.4 heuristic)")
	outDir := fs.String("o", ".", "output directory to write song folders into")
	verbose := fs.Bool("v", false, "verbose: log each extracted v-track to stderr")
	quiet := fs.Bool("q", false, "quiet: suppress deviations and the summary")
	list := fs.Bool("list", false, "list the songs on the source and exit (no extraction)")
	var songs songSel
	fs.Var(&songs, "song", "extract only the given song(s) by list key (repeatable; e.g. --song 2.7)")
	fs.Usage = func() { usage(stderr, fs) }

	if err := fs.Parse(args); err != nil {
		return exitUsage // flag has already written usage/diagnostics to stderr
	}
	if fs.NArg() != 1 {
		usage(stderr, fs)
		return exitUsage
	}

	if *list {
		catalog, devs, err := core.List(fs.Arg(0), core.Options{As: *as})
		if err != nil {
			fmt.Fprintf(stderr, "vsx: %v\n", err)
			return exitError
		}
		if len(songs.keys) > 0 {
			fmt.Fprintln(stderr, "vsx: --song ignored with --list")
		}
		return runList(catalog, devs, stdout, stderr)
	}

	if len(songs.keys) > 0 {
		catalog, _, err := core.List(fs.Arg(0), core.Options{As: *as})
		if err != nil {
			fmt.Fprintf(stderr, "vsx: %v\n", err)
			return exitError
		}
		have := map[core.SongKey]bool{}
		for _, si := range catalog {
			have[si.Key] = true
		}
		var missing []string
		for _, k := range songs.keys {
			if !have[k] {
				missing = append(missing, k.String())
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(stderr, "vsx: no song %s on this source; run 'vsx --list' to see available songs\n", strings.Join(missing, ", "))
			return exitUsage
		}
	}

	// A live progress line is drawn only for an interactive user, and never
	// alongside -v (whose per-track lines are the progress) or -q. When off, the
	// status line degrades to plain writes, so piped/CI output is unchanged.
	status := newStatusLine(stderr, isTTY(stderr) && !*quiet && !*verbose)
	opts := core.Options{As: *as, Stereo: *stereo, Progress: status.progress, Songs: songs.keys}

	result, err := core.Extract(fs.Arg(0), opts)
	if err != nil {
		status.finish()
		fmt.Fprintf(stderr, "vsx: %v\n", err)
		return exitError
	}

	if *strict {
		return runStrict(result, *outDir, *quiet, stdout, stderr, status)
	}
	return runBestEffort(result, *outDir, *verbose, *quiet, stdout, stderr, status)
}

// runBestEffort extracts in the default posture (ADR-0002): every recoverable
// v-track is written as it streams, and the exit code is non-zero if any
// deviation occurred — the audio is written either way.
//
// Deviations are streamed in context rather than batched at the end (issue #28):
// core's Deviations() slice grows as the walk advances (core.go documents that a
// song's deviations are gathered as its tracks are consumed), so after each
// v-track any newly-surfaced deviations are flushed to stderr, next to the audio
// they concern. For a song that yields audio this puts its deviations right after
// its first track and before the next song's output — so someone watching a long
// run sees each problem in place instead of a dump after everything. A song that
// yields no tracks (e.g. no event list, or all-empty v-tracks) contributes no
// track to flush against, so its deviations surface at the next flush — after the
// following song's first track, or, for the last song, at the post-loop flush.
func runBestEffort(result core.Result, outDir string, verbose, quiet bool, stdout, stderr io.Writer, status *statusLine) int {
	songs := map[int]bool{}
	nTracks := 0

	// flush reports every deviation not yet printed, advancing printed to the
	// current total. Quiet mode still advances printed (suppressing only the
	// output), so the post-loop summary count and the exit-code gate stay correct.
	// Deviations go through status.logf so a live progress line is cleared first.
	printed := 0
	flush := func() {
		devs := result.Deviations()
		if !quiet {
			for _, d := range devs[printed:] {
				status.logf("deviation [%s] %s: %s\n", d.SpecRef, d.Location, d.Message)
			}
		}
		printed = len(devs)
	}

	for tr, err := range result.Tracks() {
		if err != nil {
			status.finish()
			fmt.Fprintf(stderr, "vsx: %v\n", err)
			return exitError
		}
		path, err := writeTrack(outDir, tr)
		if err != nil {
			status.finish()
			fmt.Fprintf(stderr, "vsx: %v\n", err)
			return exitError
		}
		songs[tr.Song.Number] = true
		nTracks++
		status.trackWritten()
		status.emit(stdout, path)
		if !quiet {
			reportPair(status, tr)
		}
		if verbose && !quiet {
			status.logf("extracted %s (%d samples @ %d Hz)\n", path, len(tr.PCM.Samples), tr.Take.SampleRate)
		}
		flush()
	}

	flush() // trailing deviations: a no-track last song, or a Source with no audio.
	status.finish()
	if !quiet {
		fmt.Fprintf(stderr, "vsx: extracted %d v-track(s) across %d song(s); %d deviation(s)\n",
			nTracks, len(songs), printed)
	}

	if printed > 0 {
		return exitDeviations
	}
	return exitOK
}

// runStrict is the conformance gate (ADR-0002 / issue #7): the first deviation
// anywhere aborts the whole run with no output. Because deviations surface
// lazily as songs are replayed, tracks are buffered (never written) until the
// run is known clean; the moment any deviation appears, the buffer is discarded
// and nothing is written. This makes the verdict independent of how many songs
// a Source contains — one deviation fails the whole run. A clean image writes
// all its output and exits zero.
//
// Buffering deliberately trades away core's one-song-at-a-time streaming: the
// "no partial output" guarantee requires holding a clean run's PCM until the end.
// That is acceptable here because strict is a validation gate, not the bulk
// recovery path (best-effort streams and stays bounded).
func runStrict(result core.Result, outDir string, quiet bool, stdout, stderr io.Writer, status *statusLine) int {
	var buffered []core.TrackResult
	for tr, err := range result.Tracks() {
		if err != nil {
			status.finish()
			fmt.Fprintf(stderr, "vsx: %v\n", err)
			return exitError
		}
		// A song's deviations are recorded before its tracks are yielded, so a
		// deviation is visible here before this song's audio would be buffered.
		if devs := result.Deviations(); len(devs) > 0 {
			return strictAbort(devs, quiet, status, stderr)
		}
		buffered = append(buffered, tr)
	}
	// Catch deviations from a song that yielded no tracks (e.g. a missing event
	// list) or from enumeration on a Source with no audio at all.
	if devs := result.Deviations(); len(devs) > 0 {
		return strictAbort(devs, quiet, status, stderr)
	}
	status.finish()

	for _, tr := range buffered {
		path, err := writeTrack(outDir, tr)
		if err != nil {
			fmt.Fprintf(stderr, "vsx: %v\n", err)
			return exitError
		}
		fmt.Fprintln(stdout, path)
		if !quiet {
			reportPair(status, tr)
		}
	}
	if !quiet {
		fmt.Fprintf(stderr, "vsx: strict: clean image; %d v-track(s) written\n", len(buffered))
	}
	return exitOK
}

// strictAbort reports the first deviation and returns the strict-failure exit
// code, having written no output.
func strictAbort(devs []core.Deviation, quiet bool, status *statusLine, stderr io.Writer) int {
	status.finish()
	if !quiet {
		d := devs[0]
		fmt.Fprintf(stderr, "deviation [%s] %s: %s\n", d.SpecRef, d.Location, d.Message)
		fmt.Fprintf(stderr, "vsx: strict: aborted on first deviation; no output written\n")
	}
	return exitDeviations
}

// writeTrack encodes one v-track's PCM to a WAV file under
// "<outDir>/<NN> - <name>/<label>[ <track name>].wav" and returns the path
// written. label is "T<track>-V<vtrack>" for a mono v-track, or
// "T<lo>+<hi>-V<vtrack>" for a §8.4 stereo pair (so the pairing is visible in
// the filename); a stereo result is encoded interleaved (left = lower track). A
// user-assigned track name is appended when present (§6.1), so named tracks are
// easy to find; the T/V indices always lead so files sort predictably even when
// names are blank. The song folder is created on demand, so v-tracks of the
// same song share it.
func writeTrack(outDir string, tr core.TrackResult) (string, error) {
	label := trackLabel(tr)
	wavBytes, err := encodeTrack(tr)
	if err != nil {
		return "", fmt.Errorf("encoding song %d %s: %w", tr.Song.Number, label, err)
	}
	dir := filepath.Join(outDir, songDir(tr.Song))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	name := label
	if n := sanitize(tr.Name); n != "" {
		name += " " + n
	}
	path := filepath.Join(dir, name+".wav")
	if err := os.WriteFile(path, wavBytes, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// encodeTrack encodes a result to WAV bytes: interleaved stereo when it is a
// §8.4 pair (Right non-nil), mono otherwise.
func encodeTrack(tr core.TrackResult) ([]byte, error) {
	if tr.Right != nil {
		return wav.EncodeStereo(tr.PCM.Samples, tr.Right.Samples, tr.Take.SampleRate, tr.PCM.BitDepth)
	}
	return wav.Encode(tr.PCM.Samples, tr.Take.SampleRate, tr.PCM.BitDepth)
}

// trackLabel is the leading "T…-V…" filename component: "T<track>-V<vtrack>"
// for a mono v-track, "T<lo>+<hi>-V<vtrack>" for a stereo pair.
func trackLabel(tr core.TrackResult) string {
	if tr.Right != nil {
		return fmt.Sprintf("T%d+%d-V%d", tr.Track, tr.PairTrack, tr.VTrack)
	}
	return fmt.Sprintf("T%d-V%d", tr.Track, tr.VTrack)
}

// reportPair notes each formed §8.4 stereo pair on stderr so a false positive is
// visible (issue #8); it is a no-op for a mono result. The report is independent
// of -v because a pairing decision always warrants a look. It goes through
// status.logf so a live progress line is cleared around it.
func reportPair(status *statusLine, tr core.TrackResult) {
	if tr.Right == nil {
		return
	}
	status.logf("vsx: stereo pair: song %d tracks %d+%d (§8.4)\n",
		tr.Song.Number, tr.Track, tr.PairTrack)
}

// songDir builds a song's output folder name: the stable key (unique per Source)
// followed by the song name, so two songs never collide even across HDD
// partitions (where the device song number is not globally unique).
func songDir(s core.SongRef) string {
	return fmt.Sprintf("%s - %s", s.Key.FolderName(), sanitize(s.Name))
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
	fmt.Fprint(w, "  <source>  path to an HDD image, a single CD backup-set dump, or a\n")
	fmt.Fprint(w, "            directory of one set's disc dumps (multi-disc, §5.6)\n\n")
	fmt.Fprint(w, "  vsx --list <source>          list the songs on a source (no extraction)\n")
	fmt.Fprint(w, "  vsx --song 2.7 <source>      extract only the given song(s)\n\n")
	fmt.Fprint(w, "flags:\n")
	fs.PrintDefaults()
}

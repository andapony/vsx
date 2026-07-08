// Package core is the extraction façade: it opens a Source (an HDD image or a
// CD Song Copy Archive backup set, identified by its bytes) and streams the
// audio it recovers as per-(song, v-track) results, reporting every departure
// from ROLAND-VS-FORMAT-SPEC.md as a Deviation rather than failing on it
// (best-effort mode, ADR-0002). The format/structure walk is built from
// scratch against the spec (ADR-0001); only the RDAC codec behind the Decoder
// seam is vendored (ADR-0004).
package core

import (
	"fmt"
	"iter"
	"os"
	"strings"

	"github.com/andapony/vsx/internal/cd"
	"github.com/andapony/vsx/internal/hdd"
)

// Options controls a single extraction run.
//
// The best-effort/strict posture (ADR-0002) is deliberately not an Option: core
// always extracts best-effort and reports every Deviation it finds, and the
// caller decides what to do with them. The strict conformance gate — withhold
// all output the moment any deviation appears — is an output policy the command
// layer applies over a best-effort Result, not an extraction mode.
type Options struct {
	// As forces the Source type when byte-level detection is ambiguous on
	// damaged media; "" means autodetect. "hdd" forces the HDD live-disk path
	// (§4); "cd" forces the CD archive path and autodetects the machine; "vr9"/
	// "vr5" force the CD path as that machine when no archive signature is found
	// (§5.2). It is the --as override.
	As string
}

// Severity ranks how far a Deviation departs from the spec.
type Severity int

const (
	SeverityInfo    Severity = iota // benign or informational
	SeverityWarning                 // recoverable with a documented guess/default
	SeverityError                   // audio may be lost at this location
)

// Deviation is any respect in which a Source departs from
// ROLAND-VS-FORMAT-SPEC.md (CONTEXT.md "Deviation"): a missing take, an
// unknown field value, a truncated rip, a corrupt FAT chain. vsx reports
// deviations rather than treating them as fatal errors.
type Deviation struct {
	Location string   // where in the Source (e.g. "song 3 / v-track 12")
	SpecRef  string   // the spec clause the input violates (e.g. "§5.5")
	Severity Severity // how serious the departure is
	Message  string   // human-readable description
}

// cookedRipDeviation is the §5/§10 data-integrity warning for a cooked (dd)
// rip: such dumps are frequently truncated or block-shifted, so best-effort
// attempts them with this warning while the strict gate (command layer) turns
// any deviation into a hard abort. Both the single-disc and backup-set paths
// raise it identically.
func cookedRipDeviation() Deviation {
	return Deviation{Location: "disc", SpecRef: "§5", Severity: SeverityWarning,
		Message: "cooked (dd) rip; raw 2352-byte-frame dumps are recommended for data integrity"}
}

// SongRef identifies a song within a Source.
type SongRef struct {
	Number int    // the song's catalog number (always present in output names)
	Name   string // the user-assigned song name, if any
}

// Take carries the resolved take/cluster metadata that produced a v-track's
// audio: enough to locate and decode the underlying byte stream.
type Take struct {
	FirstCluster int    // starting cluster of the take
	ClusterCount int    // number of clusters the take occupies
	ClusterSize  int    // storage cluster size in bytes (for MT2 page-padding)
	Format       Format // RDAC format code
	SampleRate   int    // native sample rate in Hz
}

// TrackResult is one populated (song, v-track): its decoded mono PCM plus the
// resolved metadata that produced it. Empty v-tracks yield no TrackResult.
type TrackResult struct {
	Song   SongRef
	Track  int    // physical track index (1-based)
	VTrack int    // virtual-track index within the track
	Name   string // user-assigned track name, "" when the name is the default/blank (§6.1)
	PCM    PCM
	Take   Take
}

// Result is the streaming outcome of an extraction. Tracks yields per-(song,
// v-track) results lazily so a large Source is never held in memory all at
// once; Deviations reports the departures gathered during the walk.
//
// Deviations accumulate as the walk progresses: enumeration deviations are
// present immediately, but those found while replaying a song are gathered only
// as that song's tracks are consumed. Range over Tracks to completion before
// reading Deviations to see the full set.
type Result struct {
	tracks     iter.Seq2[TrackResult, error]
	deviations *[]Deviation
}

// newResult builds a Result from a lazy track sequence and a fixed set of
// deviations. Callers observe a Result only through Tracks and Deviations.
func newResult(tracks iter.Seq2[TrackResult, error], deviations []Deviation) Result {
	return Result{tracks: tracks, deviations: &deviations}
}

// Tracks returns a lazy iterator over the extracted per-v-track results. It is
// always safe to range over, including on a zero-value Result.
func (r Result) Tracks() iter.Seq2[TrackResult, error] {
	if r.tracks == nil {
		return func(func(TrackResult, error) bool) {}
	}
	return r.tracks
}

// Deviations returns the departures from the spec observed for this Source.
func (r Result) Deviations() []Deviation {
	if r.deviations == nil {
		return nil
	}
	return *r.deviations
}

// Extract opens the Source at sourcePath, identifies it, and returns a streaming
// Result. This build handles the single-disc CD path for both machines — the
// VS-880EX (VR9, issue #3) and the VS-1880 (VR5, issue #4); HDD sources are
// identified but reported as not yet supported. The Source file stays open for
// the lifetime of the track iterator and is closed when iteration ends.
func Extract(sourcePath string, opts Options) (Result, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return Result{}, fmt.Errorf("core: stat source: %w", err)
	}
	// A directory is a multi-disc CD backup set (§5.6): its dumps are grouped
	// into one Source. A single file is one HDD image or one CD disc.
	if info.IsDir() {
		return extractSet(sourcePath, opts)
	}

	f, err := os.Open(sourcePath)
	if err != nil {
		return Result{}, fmt.Errorf("core: opening source: %w", err)
	}
	devs := &[]Deviation{}

	// HDD path (§4): a Roland live disk is identified structurally — its MBR and
	// a partition BPB's "Roland  " OEM ID — before the CD signature check, since
	// an HDD image carries no CD archive signature at user-data offset 0.
	// --as=hdd forces it; any other override forces the CD path; "" autodetects,
	// trying HDD first and falling through to CD when the image is not Roland.
	asLower := strings.ToLower(strings.TrimSpace(opts.As))
	forceHDD := asLower == "hdd"
	forceCD := asLower != "" && !forceHDD
	if forceHDD || !forceCD {
		vol, herr := hdd.Open(f, info.Size())
		if herr == nil {
			return newHDDResult(f, vol, devs)
		}
		if forceHDD {
			f.Close()
			return Result{}, fmt.Errorf("core: --as=hdd forced but this image is not a Roland live disk: %w", herr)
		}
		// autodetect, not a Roland disk: fall through to the CD path.
	}

	img, err := cd.New(f, info.Size())
	if err != nil {
		f.Close()
		return Result{}, fmt.Errorf("core: %w", err)
	}

	// "cd" forces the CD path but leaves the machine to signature autodetection;
	// "vr9"/"vr5" additionally force the machine when no signature is present.
	cdOverride := opts.As
	if asLower == "cd" {
		cdOverride = ""
	}
	p, err := detect(img, cdOverride)
	if err != nil {
		f.Close()
		return Result{}, err
	}
	if p.kind != kindCD {
		f.Close()
		return Result{}, fmt.Errorf("core: source identified but not yet supported by this build (only single-disc CD); machine=%v", p.machine)
	}

	if img.Cooked() {
		*devs = append(*devs, cookedRipDeviation())
	}

	var inner iter.Seq2[TrackResult, error]
	switch p.machine {
	case machineVR9:
		inner, err = extractVR9(img, NewDecoder(), devs)
	case machineVR5:
		inner, err = extractVR5(img, NewDecoder(), devs)
	default:
		f.Close()
		return Result{}, fmt.Errorf("core: source identified but not yet supported by this build; machine=%v", p.machine)
	}
	if err != nil {
		f.Close()
		return Result{}, err
	}

	return Result{tracks: streamClosing(f, inner), deviations: devs}, nil
}

// extractSet groups a directory of CD dumps into one multi-disc backup set
// (§5.6) and streams its audio. Grouping deviations (foreign files, missing
// discs) are present immediately; the machine-specific walk then runs over the
// stitched reader exactly as it does for a single disc. The set's disc files
// stay open for the lifetime of the track iterator and are all closed when it
// ends. A directory can only be a CD set, so --as=hdd is a usage error here.
func extractSet(dir string, opts Options) (Result, error) {
	if strings.EqualFold(strings.TrimSpace(opts.As), "hdd") {
		return Result{}, fmt.Errorf("core: --as=hdd but %q is a directory (an HDD source is a single image, not a directory)", dir)
	}

	set, err := openBackupSet(dir, opts)
	if err != nil {
		return Result{}, err
	}
	devs := &[]Deviation{}
	*devs = append(*devs, set.devs...)
	if set.cooked {
		*devs = append(*devs, cookedRipDeviation())
	}

	var inner iter.Seq2[TrackResult, error]
	switch set.machine {
	case machineVR9:
		inner, err = extractVR9(set.reader, NewDecoder(), devs)
	case machineVR5:
		inner, err = extractVR5(set.reader, NewDecoder(), devs)
	default:
		closeAll(set.files)
		return Result{}, fmt.Errorf("core: backup set machine not supported by this build; machine=%v", set.machine)
	}
	if err != nil {
		closeAll(set.files)
		return Result{}, err
	}
	return Result{tracks: streamClosingAll(set.files, inner), deviations: devs}, nil
}

// newHDDResult builds a streaming Result over a Roland live disk, keeping the
// Source file open for the lifetime of the track iterator and closing it when
// iteration ends — the same ownership contract as the CD path.
func newHDDResult(f *os.File, vol *hdd.Volume, devs *[]Deviation) (Result, error) {
	inner, err := extractHDD(vol, NewDecoder(), devs)
	if err != nil {
		f.Close()
		return Result{}, err
	}
	return Result{tracks: streamClosing(f, inner), deviations: devs}, nil
}

// streamClosing wraps a per-track iterator so the Source file is closed once
// iteration ends (whether drained or abandoned early) — the file must stay open
// for the whole lazy walk, since PCM is read on demand.
func streamClosing(f *os.File, inner iter.Seq2[TrackResult, error]) iter.Seq2[TrackResult, error] {
	return streamClosingAll([]*os.File{f}, inner)
}

// streamClosingAll is streamClosing for a multi-disc set: it closes every disc
// file once iteration ends. All disc files must stay open for the whole lazy
// walk, since a spanned take reads across discs on demand.
func streamClosingAll(files []*os.File, inner iter.Seq2[TrackResult, error]) iter.Seq2[TrackResult, error] {
	return func(yield func(TrackResult, error) bool) {
		defer closeAll(files)
		for tr, e := range inner {
			if !yield(tr, e) {
				return
			}
		}
	}
}

// closeAll closes every file handle, ignoring errors (best effort on teardown).
func closeAll(files []*os.File) {
	for _, f := range files {
		f.Close()
	}
}

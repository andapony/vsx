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

	"github.com/andapony/vsx/internal/cd"
)

// Options controls a single extraction run.
type Options struct {
	// Strict selects the pass/fail conformance gate that aborts on the first
	// deviation with no output, instead of the default best-effort posture
	// (ADR-0002 / CONTEXT.md).
	Strict bool
	// As forces the Source profile ("vr9"/"vr5") when byte-level detection
	// finds no known archive signature; "" means autodetect (§5.2). It is the
	// --as override.
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
	Track  int // physical track index (1-based)
	VTrack int // virtual-track index within the track
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
// Result. For this slice the only extractor is the single-disc VS-880EX (VR9) CD
// path (issue #3); other machines and HDD sources are identified but reported as
// not yet supported. The Source file stays open for the lifetime of the track
// iterator and is closed when iteration ends.
func Extract(sourcePath string, opts Options) (Result, error) {
	f, err := os.Open(sourcePath)
	if err != nil {
		return Result{}, fmt.Errorf("core: opening source: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return Result{}, fmt.Errorf("core: stat source: %w", err)
	}
	img, err := cd.New(f, info.Size())
	if err != nil {
		f.Close()
		return Result{}, fmt.Errorf("core: %w", err)
	}

	p, err := detect(img, opts.As)
	if err != nil {
		f.Close()
		return Result{}, err
	}
	if p.kind != kindCD || p.machine != machineVR9 {
		f.Close()
		return Result{}, fmt.Errorf("core: source identified but not yet supported by this build (only single-disc VS-880EX CD); machine=%v", p.machine)
	}

	devs := &[]Deviation{}
	if img.Cooked() {
		*devs = append(*devs, Deviation{Location: "disc", SpecRef: "§5",
			Severity: SeverityWarning, Message: "cooked (dd) rip; raw 2352-byte-frame dumps are recommended for data integrity"})
	}
	inner, err := extractVR9(img, NewDecoder(), devs)
	if err != nil {
		f.Close()
		return Result{}, err
	}

	tracks := func(yield func(TrackResult, error) bool) {
		defer f.Close()
		for tr, e := range inner {
			if !yield(tr, e) {
				return
			}
		}
	}
	return Result{tracks: tracks, deviations: devs}, nil
}

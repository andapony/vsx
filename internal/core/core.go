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
)

// Options controls a single extraction run.
type Options struct {
	// Strict selects the pass/fail conformance gate that aborts on the first
	// deviation with no output, instead of the default best-effort posture
	// (ADR-0002 / CONTEXT.md).
	Strict bool
	// SourceType forces the Source kind ("hdd" or "cd") when byte-level
	// detection is ambiguous on damaged media; "" means autodetect.
	SourceType string
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
type Result struct {
	tracks     iter.Seq2[TrackResult, error]
	deviations []Deviation
}

// newResult builds a Result from a lazy track sequence and the deviations
// gathered so far. It is the internal constructor the Source walk will use;
// callers observe a Result only through Tracks and Deviations.
func newResult(tracks iter.Seq2[TrackResult, error], deviations []Deviation) Result {
	return Result{tracks: tracks, deviations: deviations}
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
func (r Result) Deviations() []Deviation { return r.deviations }

// Extract opens the Source at sourcePath and returns a streaming Result.
//
// This is the pipeline façade; the format/structure walk that populates the
// stream lands in later slices. For now it validates that the Source is
// openable and returns an empty (but safe-to-range) Result.
func Extract(sourcePath string, opts Options) (Result, error) {
	f, err := os.Open(sourcePath)
	if err != nil {
		return Result{}, fmt.Errorf("core: opening source: %w", err)
	}
	f.Close()

	empty := func(func(TrackResult, error) bool) {}
	return newResult(empty, nil), nil
}

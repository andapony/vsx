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
	// As forces the Source type and/or machine when byte-level detection is
	// ambiguous on damaged media; its zero value autodetects. It is the typed
	// --as override — build it with ParseAs, which rejects unrecognized values at
	// the flag boundary.
	As SourceOverride

	// Stereo enables the conservative §8.4 stereo-pair heuristic: two adjacent
	// physical tracks that each have exactly one populated v-track with matching
	// event geometry are emitted as one interleaved stereo TrackResult instead of
	// two monos. Off by default; unpaired v-tracks always stay mono. It is the
	// --stereo flag.
	Stereo bool

	// Progress, when non-nil, is called at coarse extraction milestones so a
	// caller can render progress on a long Source. Core makes no progress calls
	// when it is nil, and the callback must not block. Calls arrive on the
	// goroutine driving the track iterator, in order.
	Progress func(Progress)

	// Songs restricts extraction to these songs; empty means all.
	Songs []SongKey
}

// extractCtx carries the per-run values every extractor needs, so the extractor
// signatures stay narrow as the pipeline grows.
type extractCtx struct {
	dec    Decoder
	devs   *[]Deviation
	stereo bool
	report func(Progress)
	songs  []SongKey // Options.Songs filter; empty means all
}

// selected reports whether key is in the filter (empty filter = everything).
func (c extractCtx) selected(key SongKey) bool {
	if len(c.songs) == 0 {
		return true
	}
	for _, k := range c.songs {
		if k == key {
			return true
		}
	}
	return false
}

// ProgressPhase is the coarse stage an extraction has reached.
type ProgressPhase int

const (
	ProgressIdentifying ProgressPhase = iota // opening, detecting, and enumerating the Source
	ProgressExtracting                       // decoding one song's takes and building its v-tracks
	ProgressDone                             // every song has been processed
)

// Progress is a coarse extraction milestone (Options.Progress). During
// ProgressExtracting, TotalSongs is the number of songs enumerated on the
// Source, Song is the 1-based index now being processed, and SongName is its
// name — enough to render "song i/N (name)". The other phases carry no counts.
type Progress struct {
	Phase      ProgressPhase
	Song       int
	TotalSongs int
	SongName   string
}

// progressFn normalizes an optional Progress callback to a non-nil no-op, so
// call sites never need a nil check.
func progressFn(f func(Progress)) func(Progress) {
	if f == nil {
		return func(Progress) {}
	}
	return f
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
	Key    SongKey // stable identity for the output folder and --song selection
	Number int     // the song's stored device number (SONG.VRx), for display
	Name   string  // the user-assigned song name, if any
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
//
// A §8.4 stereo pair (Options.Stereo) is carried as a single TrackResult with
// Right non-nil: PCM is then the left channel (the lower-numbered physical
// track, named by Track), Right the right channel, and PairTrack the higher
// track. When Right is nil the result is an ordinary mono v-track.
type TrackResult struct {
	Song   SongRef
	Track  int    // physical track index (1-based); the left/lower track of a stereo pair
	VTrack int    // virtual-track index within the track
	Name   string // user-assigned track name, "" when the name is the default/blank (§6.1)
	PCM    PCM
	Take   Take

	// Right, when non-nil, is the right channel of a §8.4 stereo pair; PairTrack
	// is that channel's (higher) physical track index. Both are zero/nil for a
	// mono result.
	Right     *PCM
	PairTrack int
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

// sourceHandle is the outcome of identifying a single-file Source (a directory
// is a backup set instead, handled separately by openBackupSet): the opened
// file plus, on the HDD path, the opened Volume, or on the CD path, the
// detected Image and machine. identifySource is the single place that decides
// HDD-vs-CD and, on CD, the machine — Extract and List both call it so the
// identify/dispatch order can never drift between them.
type sourceHandle struct {
	f       *os.File
	isHDD   bool
	vol     *hdd.Volume
	img     *cd.Image
	machine machine
	cooked  bool // CD path only: a cooked (dd) rip (§5/§10)
}

// identifySource opens sourcePath (a single file) and identifies it exactly as
// the former Extract prologue did: the HDD live-disk check first (unless --as
// forces the CD path), then CD archive detection. The returned handle's file
// is open on success and the caller owns closing it; on error the file (if
// opened) is already closed.
func identifySource(sourcePath string, info os.FileInfo, opts Options) (sourceHandle, error) {
	f, err := os.Open(sourcePath)
	if err != nil {
		return sourceHandle{}, fmt.Errorf("core: opening source: %w", err)
	}

	// HDD path (§4): a Roland live disk is identified structurally — its MBR and
	// a partition BPB's "Roland  " OEM ID — before the CD signature check, since
	// an HDD image carries no CD archive signature at user-data offset 0.
	// --as=hdd forces it; any CD override forces the CD path; the zero override
	// autodetects, trying HDD first and falling through to CD when the image is
	// not Roland.
	forceHDD := opts.As.kind == kindHDD
	forceCD := opts.As.kind == kindCD
	if forceHDD || !forceCD {
		vol, herr := hdd.Open(f, info.Size())
		if herr == nil {
			return sourceHandle{f: f, isHDD: true, vol: vol}, nil
		}
		if forceHDD {
			f.Close()
			return sourceHandle{}, fmt.Errorf("core: --as=hdd forced but this image is not a Roland live disk: %w", herr)
		}
		// autodetect, not a Roland disk: fall through to the CD path.
	}

	img, err := cd.New(f, info.Size())
	if err != nil {
		f.Close()
		return sourceHandle{}, fmt.Errorf("core: %w", err)
	}

	// The machine override ("vr9"/"vr5") forces the machine when no signature is
	// present; kindCD alone ("cd") leaves the machine to signature autodetection.
	p, err := detect(img, opts.As.machine)
	if err != nil {
		f.Close()
		return sourceHandle{}, err
	}
	if p.kind != kindCD {
		// Defensive: the HDD path is taken above and detect only ever identifies
		// a CD archive here, so a non-CD kind at this point is unexpected.
		f.Close()
		return sourceHandle{}, fmt.Errorf("core: unexpected non-CD source kind after CD detection; machine=%v", p.machine)
	}

	return sourceHandle{f: f, img: img, machine: p.machine, cooked: img.Cooked()}, nil
}

// Extract opens the Source at sourcePath, identifies it, and returns a streaming
// Result. It handles every Source this build supports: HDD live-disk images
// (§4), single-disc CD Song Copy Archives, and multi-disc CD backup sets (a
// directory, §5.6), for both machines — the VS-880EX (VR9) and the VS-1880
// (VR5). The Source file(s) stay open for the lifetime of the track iterator and
// are closed when iteration ends.
func Extract(sourcePath string, opts Options) (Result, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return Result{}, fmt.Errorf("core: stat source: %w", err)
	}
	// A directory is a multi-disc CD backup set (§5.6): its dumps are grouped
	// into one Source. A single file is one HDD image or one CD disc.
	if info.IsDir() {
		paths, err := discPathsInDir(sourcePath)
		if err != nil {
			return Result{}, fmt.Errorf("core: reading directory %q: %w", sourcePath, err)
		}
		return extractSet(paths, opts)
	}
	report := progressFn(opts.Progress)
	report(Progress{Phase: ProgressIdentifying})

	h, err := identifySource(sourcePath, info, opts)
	if err != nil {
		return Result{}, err
	}
	devs := &[]Deviation{}

	if h.isHDD {
		ctx := extractCtx{dec: NewDecoder(), devs: devs, stereo: opts.Stereo, report: report, songs: opts.Songs}
		return newHDDResult(h.f, h.vol, ctx)
	}

	if h.cooked {
		*devs = append(*devs, cookedRipDeviation())
	}

	mf := formatFor(h.machine)
	if mf == nil {
		h.f.Close()
		return Result{}, fmt.Errorf("core: source identified but not yet supported by this build; machine=%v", h.machine)
	}
	ctx := extractCtx{dec: NewDecoder(), devs: devs, stereo: opts.Stereo, report: report, songs: opts.Songs}
	inner, err := extractCD(h.img, mf, ctx)
	if err != nil {
		h.f.Close()
		return Result{}, err
	}

	return Result{tracks: streamClosing(h.f, inner), deviations: devs}, nil
}

// ExtractSet treats the given disc-image files as one multi-disc CD backup set
// (§5.6) — the same grouping a directory of those files gets — and streams its
// audio. Use it when the discs are passed as separate paths rather than a folder.
func ExtractSet(paths []string, opts Options) (Result, error) { return extractSet(paths, opts) }

// extractSet groups a list of CD dump files into one multi-disc backup set
// (§5.6) and streams its audio. Grouping deviations (foreign files, missing
// discs) are present immediately; the machine-specific walk then runs over the
// stitched reader exactly as it does for a single disc. The set's disc files
// stay open for the lifetime of the track iterator and are all closed when it
// ends. A CD backup set can only be a CD source, so --as=hdd is a usage error
// here.
func extractSet(paths []string, opts Options) (Result, error) {
	if opts.As.kind == kindHDD {
		return Result{}, fmt.Errorf("core: --as=hdd is not valid for a multi-disc CD backup set (an HDD source is a single image)")
	}
	report := progressFn(opts.Progress)
	report(Progress{Phase: ProgressIdentifying})

	set, err := openBackupSet(paths, opts)
	if err != nil {
		return Result{}, err
	}
	devs := &[]Deviation{}
	*devs = append(*devs, set.devs...)
	if set.cooked {
		*devs = append(*devs, cookedRipDeviation())
	}

	mf := formatFor(set.machine)
	if mf == nil {
		closeAll(set.files)
		return Result{}, fmt.Errorf("core: backup set machine not supported by this build; machine=%v", set.machine)
	}
	ctx := extractCtx{dec: NewDecoder(), devs: devs, stereo: opts.Stereo, report: report, songs: opts.Songs}
	inner, err := extractCD(set.reader, mf, ctx)
	if err != nil {
		closeAll(set.files)
		return Result{}, err
	}
	return Result{tracks: streamClosingAll(set.files, inner), deviations: devs}, nil
}

// newHDDResult builds a streaming Result over a Roland live disk, keeping the
// Source file open for the lifetime of the track iterator and closing it when
// iteration ends — the same ownership contract as the CD path.
func newHDDResult(f *os.File, vol *hdd.Volume, ctx extractCtx) (Result, error) {
	inner, err := extractHDD(vol, ctx)
	if err != nil {
		f.Close()
		return Result{}, err
	}
	return Result{tracks: streamClosing(f, inner), deviations: ctx.devs}, nil
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

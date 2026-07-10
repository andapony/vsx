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
	"io"
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

// unmatchedSongDeviations reports one warning Deviation per requested --song key
// that the walk never enumerated (present is the set of keys it saw). This lets
// core flag an unknown/unrequestable song from the single walk it already
// performs (issue #27, seam 3), so the command layer no longer runs a separate
// enumeration pass just to validate keys — a real saving on a multi-GB image.
// An empty filter requests everything, so it yields nothing.
func (c extractCtx) unmatchedSongDeviations(present map[SongKey]bool) []Deviation {
	var out []Deviation
	seen := map[SongKey]bool{}
	for _, k := range c.songs {
		if present[k] || seen[k] {
			continue
		}
		seen[k] = true
		// SpecRef is left blank: an unknown --song key is a request-level
		// departure, not a violation of a spec clause (the field's documented
		// meaning), so it renders as "deviation [warning] song selection: …".
		out = append(out, Deviation{
			Location: "song selection",
			Severity: SeverityWarning,
			Message: fmt.Sprintf("no song %s on this source; run 'vsx --list' to see available songs",
				k),
		})
	}
	return out
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

// String renders a Severity as a stable lowercase word for display. An
// out-of-range value (never produced by core) falls back to a numeric form
// rather than panicking.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	default:
		return fmt.Sprintf("severity(%d)", int(s))
	}
}

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

// String is the one shared rendering of a Deviation (issue #27): a single line
// carrying the severity, spec clause, location, and message, so every reporting
// site in the command layer prints the same form — and the Severity core
// computes finally reaches the user. A blank SpecRef (a request-level departure
// with no spec clause, e.g. an unknown --song key) renders without a dangling
// separator: "deviation [warning] song selection: …".
func (d Deviation) String() string {
	ref := d.SpecRef
	if ref != "" {
		ref = " " + ref
	}
	return fmt.Sprintf("deviation [%s%s] %s: %s", d.Severity, ref, d.Location, d.Message)
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

// identifiedSource is the outcome of identifying a single Source from its bytes
// (a directory is a backup set instead, handled separately by openBackupSet):
// on the HDD path the opened Volume, or on the CD path the detected Image and
// machine. identifyReader is the single place that decides HDD-vs-CD and, on CD,
// the machine — extractReader and listReader both call it so the identify/
// dispatch order can never drift between them. It holds no file handle: the
// caller (a path adapter over an *os.File, or a test over a bytes.Reader) owns
// the underlying read surface's lifetime.
type identifiedSource struct {
	isHDD   bool
	vol     *hdd.Volume
	img     *cd.Image
	machine machine
	cooked  bool // CD path only: a cooked (dd) rip (§5/§10)
}

// identifyReader identifies a single Source from a random-access read surface and
// its length, exactly as the former path prologue did: the HDD live-disk check
// first (unless --as forces the CD path), then CD archive detection. It reads
// but does not own r — it opens and closes nothing.
func identifyReader(r io.ReaderAt, size int64, opts Options) (identifiedSource, error) {
	// HDD path (§4): a Roland live disk is identified structurally — its MBR and
	// a partition BPB's "Roland  " OEM ID — before the CD signature check, since
	// an HDD image carries no CD archive signature at user-data offset 0.
	// --as=hdd forces it; any CD override forces the CD path; the zero override
	// autodetects, trying HDD first and falling through to CD when the image is
	// not Roland.
	forceHDD := opts.As.kind == kindHDD
	forceCD := opts.As.kind == kindCD
	if forceHDD || !forceCD {
		vol, herr := hdd.Open(r, size)
		if herr == nil {
			return identifiedSource{isHDD: true, vol: vol}, nil
		}
		if forceHDD {
			return identifiedSource{}, fmt.Errorf("core: --as=hdd forced but this image is not a Roland live disk: %w", herr)
		}
		// autodetect, not a Roland disk: fall through to the CD path.
	}

	img, err := cd.New(r, size)
	if err != nil {
		return identifiedSource{}, fmt.Errorf("core: %w", err)
	}

	// The machine override ("vr9"/"vr5") forces the machine when no signature is
	// present; kindCD alone ("cd") leaves the machine to signature autodetection.
	p, err := detect(img, opts.As.machine)
	if err != nil {
		return identifiedSource{}, err
	}
	if p.kind != kindCD {
		// Defensive: the HDD path is taken above and detect only ever identifies
		// a CD archive here, so a non-CD kind at this point is unexpected.
		return identifiedSource{}, fmt.Errorf("core: unexpected non-CD source kind after CD detection; machine=%v", p.machine)
	}

	return identifiedSource{img: img, machine: p.machine, cooked: img.Cooked()}, nil
}

// Extract opens the Source at sourcePath, identifies it, and returns a streaming
// Result. It handles every Source this build supports: HDD live-disk images
// (§4), single-disc CD Song Copy Archives, and multi-disc CD backup sets (a
// directory, §5.6), for both machines — the VS-880EX (VR9) and the VS-1880
// (VR5). It is the thin path adapter over extractReader: it opens the file(s),
// delegates the byte-level walk with the production Decoder, and keeps the
// Source file(s) open for the lifetime of the track iterator, closing them when
// iteration ends.
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

	f, err := os.Open(sourcePath)
	if err != nil {
		return Result{}, fmt.Errorf("core: opening source: %w", err)
	}
	res, err := extractReader(f, info.Size(), NewDecoder(), opts)
	if err != nil {
		f.Close()
		return Result{}, err
	}
	// The file stays open for the whole lazy walk (PCM is read on demand) and is
	// closed when iteration ends.
	return Result{tracks: streamClosing(f, res.tracks), deviations: res.deviations}, nil
}

// extractReader is the ReaderAt entry the path API sits on: it identifies a
// single Source from its bytes and streams it, decoding takes through the
// supplied Decoder (the production one on the path API, a fake in tests). It
// owns no handle and closes nothing — the caller keeps r alive for the lazy walk
// — so the whole extraction runs diskless and codec-free when driven from an
// in-memory reader with a fake decoder.
func extractReader(r io.ReaderAt, size int64, dec Decoder, opts Options) (Result, error) {
	report := progressFn(opts.Progress)
	report(Progress{Phase: ProgressIdentifying})

	id, err := identifyReader(r, size, opts)
	if err != nil {
		return Result{}, err
	}
	devs := &[]Deviation{}
	ctx := extractCtx{dec: dec, devs: devs, stereo: opts.Stereo, report: report, songs: opts.Songs}

	if id.isHDD {
		inner, err := extractHDD(id.vol, ctx)
		if err != nil {
			return Result{}, err
		}
		return Result{tracks: inner, deviations: devs}, nil
	}

	if id.cooked {
		*devs = append(*devs, cookedRipDeviation())
	} else {
		// Raw dumps carry a per-frame EDC (§10): verify it so a physically corrupt
		// sector — which the codec would otherwise decode into noise silently — is
		// reported. A cooked rip has no EDC, so its §5 warning stands in instead.
		// Unlike the O(1) cooked check, this is a full-disc read, so it runs only on
		// the extract path (which reads the whole disc anyway); List stays a fast
		// catalog and does not scan.
		corrupt, cerr := id.img.CorruptFrames()
		*devs = append(*devs, edcScanDeviations("disc", corrupt, cerr)...)
	}
	mf := formatFor(id.machine)
	if mf == nil {
		return Result{}, fmt.Errorf("core: source identified but not yet supported by this build; machine=%v", id.machine)
	}
	inner, err := extractCD(id.img, mf, ctx)
	if err != nil {
		return Result{}, err
	}
	return Result{tracks: inner, deviations: devs}, nil
}

// ExtractSet treats the given disc-image files as one multi-disc CD backup set
// (§5.6) — the same grouping a directory of those files gets — and streams its
// audio. Use it when the discs are passed as separate paths rather than a folder.
func ExtractSet(paths []string, opts Options) (Result, error) { return extractSet(paths, opts) }

// extractSet is the path adapter for a multi-disc backup set: it opens each disc
// file into a byte input and delegates to extractSetReader with the production
// Decoder.
func extractSet(paths []string, opts Options) (Result, error) {
	return extractSetReader(discInputsFromPaths(paths), NewDecoder(), opts)
}

// extractSetReader groups disc byte-inputs into one multi-disc backup set (§5.6)
// and streams its audio through the supplied Decoder. Grouping deviations
// (foreign files, missing discs) are present immediately; the machine-specific
// walk then runs over the stitched reader exactly as it does for a single disc.
// The chosen set's inputs stay open for the lifetime of the track iterator and
// are all closed when it ends. A CD backup set can only be a CD source, so
// --as=hdd is a usage error here; the discs are closed before it returns.
func extractSetReader(discs []discInput, dec Decoder, opts Options) (Result, error) {
	if opts.As.kind == kindHDD {
		closeInputs(discs)
		return Result{}, errHDDBackupSet()
	}
	report := progressFn(opts.Progress)
	report(Progress{Phase: ProgressIdentifying})

	set, err := openBackupSet(discs, opts)
	if err != nil {
		return Result{}, err
	}
	devs := &[]Deviation{}
	*devs = append(*devs, set.devs...)
	if set.cooked {
		*devs = append(*devs, cookedRipDeviation())
	}
	*devs = append(*devs, setEDCDeviations(set)...)

	mf := formatFor(set.machine)
	if mf == nil {
		closeInputs(set.discs)
		return Result{}, fmt.Errorf("core: backup set machine not supported by this build; machine=%v", set.machine)
	}
	ctx := extractCtx{dec: dec, devs: devs, stereo: opts.Stereo, report: report, songs: opts.Songs}
	inner, err := extractCD(set.reader, mf, ctx)
	if err != nil {
		closeInputs(set.discs)
		return Result{}, err
	}
	return Result{tracks: streamClosingInputs(set.discs, inner), deviations: devs}, nil
}

// errHDDBackupSet is the usage error both set entries raise for --as=hdd: an HDD
// source is a single image, never a multi-disc CD backup set.
func errHDDBackupSet() error {
	return fmt.Errorf("core: --as=hdd is not valid for a multi-disc CD backup set (an HDD source is a single image)")
}

// streamClosing wraps a per-track iterator so the single Source file is closed
// once iteration ends (whether drained or abandoned early) — the file must stay
// open for the whole lazy walk, since PCM is read on demand. It is the one-file
// case of streamClosingInputs.
func streamClosing(f *os.File, inner iter.Seq2[TrackResult, error]) iter.Seq2[TrackResult, error] {
	return streamClosingInputs([]discInput{{close: func() { f.Close() }}}, inner)
}

// streamClosingInputs closes every disc input once iteration ends. All inputs
// must stay open for the whole lazy walk, since a spanned take reads across
// discs on demand.
func streamClosingInputs(discs []discInput, inner iter.Seq2[TrackResult, error]) iter.Seq2[TrackResult, error] {
	return func(yield func(TrackResult, error) bool) {
		defer closeInputs(discs)
		for tr, e := range inner {
			if !yield(tr, e) {
				return
			}
		}
	}
}

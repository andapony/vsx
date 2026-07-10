package core

// cdLayout is the per-machine CD archive layout the one shared chain walk is
// parameterized by (§5.4/§5.5). Its methods capture every respect in which the
// VS-1880 (VR5) and VS-880EX (VR9) archives diverge — the header field offsets
// folded into parse and validity, the machine-specific §5.5 validity gate, the
// block-count derivation, the song grouping, and the catalog-number resolution —
// so walkCD, extractCD, and listCD carry no machine knowledge. Each adapter
// implements it (there is no struct to hand-wire), so a machine that forgets or
// mis-types a piece fails to build, not silently corrupts a walk; a third
// machine slots in as a third adapter that must satisfy every method, editing no
// walk (ADR-0003).
type cdLayout interface {
	// machineName is the SongInfo.Machine tag this archive reports ("VR5"/"VR9").
	machineName() string
	// sig is the 32-byte archive signature a genuine file header opens with, and
	// the signature file data never begins with (§5.5 checks 1 and 6).
	sig() string
	// nameOff is the header offset of the 11-byte filename field the plausible-
	// name gate reads (§5.5 check 2).
	nameOff() int
	// headerSpan is how many bytes of a header block the walk must read to see
	// every field parse, validity, and block-count need.
	headerSpan() int
	// identityFields are the header byte ranges a spanned file's repeated header
	// must match on a continuation disc (§5.6).
	identityFields() [][2]int
	// eventListRef is the spec clause cited when a song's event list cannot be
	// read (§6.1 the VR5 V-track table, §6.2 the VR9 event log).
	eventListRef() string
	// accept is the machine-specific §5.5 gate applied after the shared checks:
	// VR5's `60 BF 51 28` magic (check 3), VR9's clear song-boundary marker flag
	// (check 4). It is the only validity difference between the machines, so it is
	// what the isolated validity tests exercise.
	accept(hdr []byte) bool
	// parse reads a validated header's §5.4 fields into a fileEntry.
	parse(hdr []byte, udoff int64) fileEntry
	// blocks derives how many data blocks the file occupies, for the chain step
	// to the next header (§5.4): VR5 from the file size, VR9 from the stored
	// block count.
	blocks(hdr []byte, fe fileEntry) int64
	// group partitions the walked files into songs in walk order (§5.4): VR9 by
	// stored song number, VR5 by header song name.
	group(files []fileEntry) []songGroup
	// songNumber resolves a song group's catalog number (§4.4/§5.4): VR9 reads it
	// from the header, VR5 from the song's SONG file with a walk-order fallback
	// (and a deviation) when no SONG file is present.
	songNumber(img cdSource, g songGroup, index int) (int, []Deviation)
}

// machineFormat is the seam behind which a recorder family's format sits
// (ADR-0003). It has two jobs. The first, on every source, is the event-list →
// timeline reduction (parseTimeline): turning a song's raw event-list bytes into
// a machine-neutral songTimeline that the neutral build and summarize paths
// consume unchanged. The second, on the CD path only, is to be the per-machine
// CD archive layout (the embedded cdLayout) that parameterizes the one shared
// chain walk — the offsets, validity gate, block-count derivation, and song
// grouping that the VS-1880 (VR5) and VS-880EX (VR9) archives differ in. VR5 and
// VR9 are the two adapters; a third machine slots in as a third adapter and one
// more case in the formatFor resolver — no dispatch switch at any call site, and
// no walk, changes. Folding cdLayout into the seam means the compiler enforces
// that every adapter supplies every layout piece.
type machineFormat interface {
	parseTimeline(data []byte) (songTimeline, []Deviation)
	cdLayout
}

// vtrackGroup is one v-track's placement on the timeline: its 1-based track and
// v-track, its user-assigned name ("" when the name is the default or blank),
// and its current-timeline events in replay order. Both machines reduce to this
// shape.
type vtrackGroup struct {
	track, vtrack int
	name          string
	events        []timelineEvent
}

// songTimeline is a song's parsed timeline, machine-neutral: the per-v-track
// groups plus the origin frame the events are measured from (§3 — VR9 subtracts
// 12, VR5 subtracts 0). Every machine-specific rule — the event-record layout,
// the VR9 code→track/v-track mapping, the VR5 positional table and name
// defaulting, the origin — is resolved by the adapter before this type is
// formed, so the consumers below need no machine knowledge.
type songTimeline struct {
	origin int
	groups []vtrackGroup
}

// parsedSong is the machine-neutral result of a Source's per-song prologue: the
// song's identity (ref), its catalog machine tag ("VR5"/"VR9"), its audio spec,
// and its parsed song timeline. One prologue produces it per song; Extract builds
// tracks from it and List summarises it, so the two agree by construction. On a
// prologue that fails partway (no SONG file, unreadable or absent event list),
// the timeline is empty — Extract then decodes and builds nothing, List renders
// whatever fields were reached — and the returned deviations say why, so neither
// consumer needs an is-ok flag to check.
type parsedSong struct {
	ref     SongRef
	machine string
	aud     audioSpec
	st      songTimeline
}

// songInfo reduces a parsed song to the catalog entry List reports: identity,
// machine tag, and sample rate straight from the parse, plus the populated
// v-track count and frame length from the neutral timeline. Both the HDD and CD
// summarizers build their SongInfo through this one reduction, so the
// parsedSong → SongInfo mapping lives in a single place.
func (ps parsedSong) songInfo() SongInfo {
	info := SongInfo{
		Key:          ps.ref.Key,
		StoredNumber: ps.ref.Number,
		Name:         ps.ref.Name,
		Machine:      ps.machine,
		SampleRate:   ps.aud.sampleRate,
	}
	info.VTracks, info.Frames = summarizeVTracks(ps.st)
	return info
}

// formatFor resolves a detected machine identity to its behavior adapter — the
// single dispatch point replacing the per-call switch. It returns nil for an
// unidentified machine, so callers surface the "unsupported machine" deviation
// rather than a switch default.
func formatFor(m machine) machineFormat {
	switch m {
	case machineVR5:
		return vr5{}
	case machineVR9:
		return vr9{}
	default:
		return nil
	}
}

// machineForExt maps an HDD song directory's extension (§4.3: "SONG    VR5" /
// "…VR9") to a machine identity, so the HDD path resolves its adapter through
// formatFor exactly as the CD path resolves one from the archive signature.
func machineForExt(ext string) machine {
	switch ext {
	case "VR5":
		return machineVR5
	case "VR9":
		return machineVR9
	default:
		return machineUnknown
	}
}

// buildTracks replays a parsed songTimeline against its decoded takes into one
// TrackResult per populated v-track (§8), then pairs adjacent mono v-tracks into
// stereo when requested. It is the single neutral build path both machines and
// both source kinds share: every machine difference is already resolved into the
// songTimeline, so this needs no machine knowledge. A v-track with no
// take-bearing event yields no TrackResult.
func buildTracks(st songTimeline, takes map[uint16]PCM, song SongRef, aud audioSpec, stereo bool) ([]TrackResult, []Deviation) {
	var built []builtTrack
	var devs []Deviation
	for _, g := range st.groups {
		tr, ok, d := buildVTrack(g, takes, st.origin, song, aud)
		devs = append(devs, d...)
		if ok {
			built = append(built, builtTrack{result: tr, events: g.events})
		}
	}
	return pairTracks(built, stereo), devs
}

// gatherRefs collects the take FileIDs a parsed timeline references, in
// first-seen order, together with the highest 0x18 cluster count claimed for
// each (for the §8.3 HDD integrity check). Erase records (FileID 0) reference no
// take and are skipped. It is the neutral replacement for the per-machine ref
// gatherers — the songTimeline already holds every event.
func gatherRefs(st songTimeline) ([]uint16, map[uint16]int) {
	var refs []uint16
	counts := map[uint16]int{}
	seen := map[uint16]bool{}
	for _, g := range st.groups {
		for _, e := range g.events {
			addRef(&refs, counts, seen, e.fileID, e.clusterCount)
		}
	}
	return refs, counts
}

// addRef records a take reference (skipping erases, FileID 0) and tracks the
// largest cluster count claimed for it.
func addRef(refs *[]uint16, counts map[uint16]int, seen map[uint16]bool, id, clusterCount uint16) {
	if id == 0 {
		return
	}
	if !seen[id] {
		seen[id] = true
		*refs = append(*refs, id)
	}
	if c := int(clusterCount); c > counts[id] {
		counts[id] = c
	}
}

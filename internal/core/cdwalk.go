package core

import (
	"fmt"
	"iter"
	"strings"
)

// cdLayout is the per-machine CD archive layout the one shared chain walk is
// parameterized by (§5.4/§5.5). It captures every respect in which the VS-1880
// (VR5) and VS-880EX (VR9) archives diverge — the header field offsets folded
// into parse and validity, the machine-specific §5.5 validity gate, the block-
// count derivation, the song grouping, and the catalog-number resolution — so
// walkCD, extractCD, and listCD carry no machine knowledge. Each adapter supplies
// one via machineFormat.layout(); a third machine slots in as a third layout,
// editing no walk.
type cdLayout struct {
	// machineName is the SongInfo.Machine tag this archive reports ("VR5"/"VR9").
	machineName string
	// sig is the 32-byte archive signature a genuine file header opens with, and
	// the signature file data never begins with (§5.5 checks 1 and 6).
	sig string
	// nameOff is the header offset of the 11-byte filename field the plausible-
	// name gate reads (§5.5 check 2).
	nameOff int
	// headerSpan is how many bytes of a header block the walk must read to see
	// every field parse, validity, and block-count need.
	headerSpan int
	// identityFields are the header byte ranges a spanned file's repeated header
	// must match on a continuation disc (§5.6).
	identityFields [][2]int
	// eventListRef is the spec clause cited when a song's event list cannot be
	// read (§6.1 the VR5 V-track table, §6.2 the VR9 event log).
	eventListRef string
	// accept is the machine-specific §5.5 gate applied after the shared checks:
	// VR5's `60 BF 51 28` magic (check 3), VR9's clear song-boundary marker flag
	// (check 4). It is the only validity difference between the machines, so it is
	// what the isolated validity tests exercise.
	accept func(hdr []byte) bool
	// parse reads a validated header's §5.4 fields into a fileEntry.
	parse func(hdr []byte, udoff int64) fileEntry
	// blocks derives how many data blocks the file occupies, for the chain step
	// to the next header (§5.4): VR5 from the file size, VR9 from the stored
	// block count.
	blocks func(hdr []byte, fe fileEntry) int64
	// group partitions the walked files into songs in walk order (§5.4): VR9 by
	// stored song number, VR5 by header song name.
	group func(files []fileEntry) []songGroup
	// songNumber resolves a song group's catalog number (§4.4/§5.4): VR9 reads it
	// from the header, VR5 from the song's SONG file with a walk-order fallback
	// (and a deviation) when no SONG file is present.
	songNumber func(img cdSource, g songGroup, index int) (int, []Deviation)
}

// validCDHeader applies the §5.5 acceptance rules a landed-on block must pass to
// be a real file header: the archive signature is present (check 1), the filename
// is plausible (check 2), the machine-specific gate passes (check 3/4), and the
// block at +0x8000 is file data — not another archive signature, and before the
// disc's data end (check 6, which rejects every song-boundary block). The index-0
// block-0 check does not apply to a forward chain walk that starts at the first
// file header. It is a pure function of the block bytes plus one look-ahead read,
// so a layout's validity can be unit-tested against a crafted header without a
// full fixture image.
func validCDHeader(img cdSource, lay cdLayout, hdr []byte, udoff, end int64) bool {
	if string(hdr[:32]) != lay.sig { // check 1
		return false
	}
	if !plausibleName(hdr[lay.nameOff : lay.nameOff+11]) { // check 2
		return false
	}
	if !lay.accept(hdr) { // check 3 (VR5 magic) / check 4 (VR9 marker)
		return false
	}
	dataOff := udoff + blockSize // check 6
	if dataOff >= end {
		return false
	}
	next, err := img.ReadUserData(dataOff, 32)
	if err != nil || string(next) == lay.sig {
		return false
	}
	return true
}

// walkCD enumerates a CD archive's files by the §5.4 chain walk, parameterized by
// a per-machine layout: it validates every landed-on block with validCDHeader,
// skips the header copies and song-boundary blocks, and steps to the next header
// by the layout's block-count derivation. The walk ends at the §10 filler run; a
// dump lacking it is reported as a truncated rip and walked to end-of-data. It is
// the single walk both machines and both consumers (extract, list) share.
func walkCD(img cdSource, lay cdLayout) ([]fileEntry, []Deviation, error) {
	var devs []Deviation
	end, ok := img.FillerStart()
	if !ok {
		devs = append(devs, Deviation{
			Location: "disc",
			SpecRef:  "§10",
			Severity: SeverityWarning,
			Message:  "no trailing TDI filler run; dump is likely a truncated/incomplete rip",
		})
		end = img.UserDataLen()
	}

	var files []fileEntry
	udoff := int64(firstFileHeader)
	for udoff+blockSize <= end {
		hdr, err := img.ReadUserData(udoff, lay.headerSpan)
		if err != nil {
			return files, devs, fmt.Errorf("core: reading header block at %#x: %w", udoff, err)
		}
		if !validCDHeader(img, lay, hdr, udoff, end) {
			// A header copy or song-boundary block (§5.5): skip one slot and
			// re-test, exactly as the §5.4 chain walk prescribes.
			udoff += blockSize
			continue
		}
		fe := lay.parse(hdr, udoff)
		files = append(files, fe)
		devs = append(devs, verifyJunction(img, hdr, fe.dataOff, fe.size, lay.headerSpan,
			lay.identityFields, "file "+strings.TrimRight(fe.filename, " "))...)
		udoff += (1 + lay.blocks(hdr, fe)) * blockSize
	}
	return files, devs, nil
}

// groupBy partitions walked files into songs, preserving first-seen (walk) order
// for deterministic output. keyOf extracts the grouping key (VR9 by stored song
// number, VR5 by header song name); newGroup seeds a song's header the first time
// its key is seen. It is the shared skeleton behind both machines' §5.4 grouping.
func groupBy[K comparable](files []fileEntry, keyOf func(fileEntry) K, newGroup func(fileEntry) songGroup) []songGroup {
	idx := map[K]int{}
	var groups []songGroup
	for _, f := range files {
		k := keyOf(f)
		gi, ok := idx[k]
		if !ok {
			idx[k] = len(groups)
			groups = append(groups, newGroup(f))
			gi = len(groups) - 1
		}
		groups[gi].files = append(groups[gi].files, f)
	}
	return groups
}

// extractCD enumerates a CD archive for one machine and returns a lazy iterator
// over its per-v-track results, appending enumeration deviations to devs
// immediately and replay deviations as each song is consumed. Files are grouped
// into songs by the layout, one song processed at a time so a large Source is
// never fully materialized. Every machine difference is resolved through the
// layout and mf.parseTimeline, so this path is shared by both machines.
func extractCD(img cdSource, mf machineFormat, ctx extractCtx) (iter.Seq2[TrackResult, error], error) {
	lay := mf.layout()
	files, wdevs, err := walkCD(img, lay)
	if err != nil {
		return nil, err
	}
	*ctx.devs = append(*ctx.devs, wdevs...)
	groups := lay.group(files)

	return func(yield func(TrackResult, error) bool) {
		for i, g := range groups {
			number, ndevs := lay.songNumber(img, g, i)
			key := cdSongKey(number)
			if !ctx.selected(key) {
				continue
			}
			ctx.report(Progress{Phase: ProgressExtracting, Song: i + 1, TotalSongs: len(groups), SongName: g.name})
			tracks, sdevs := extractCDSong(img, mf, g, number, ndevs, key, ctx.dec, ctx.stereo)
			*ctx.devs = append(*ctx.devs, sdevs...)
			for _, tr := range tracks {
				if !yield(tr, nil) {
					return
				}
			}
		}
		ctx.report(Progress{Phase: ProgressDone})
	}, nil
}

// extractCDSong replays one song's event list against its takes into per-v-track
// results. number, ndevs, and key are resolved by the caller from the layout's
// song-number rule before any take is read, so Options.Songs filtering happens
// ahead of this call; takes are resolved by FileID (§5.7). The parse to a neutral
// timeline goes through mf, and only the event-list spec clause differs per
// machine (via the layout), so the replay is otherwise machine-neutral.
func extractCDSong(img cdSource, mf machineFormat, g songGroup, number int, ndevs []Deviation, key SongKey, dec Decoder, stereo bool) ([]TrackResult, []Deviation) {
	devs := ndevs
	loc := fmt.Sprintf("song %d", number)

	elst, ok := findEventList(g.files)
	if !ok {
		return nil, append(devs, Deviation{Location: loc, SpecRef: "§5.4", Severity: SeverityError,
			Message: "no EVENTLST file found for song; nothing to extract"})
	}
	data, err := img.ReadUserData(elst.dataOff, int(elst.size))
	if err != nil {
		return nil, append(devs, Deviation{Location: loc, SpecRef: mf.layout().eventListRef, Severity: SeverityError,
			Message: fmt.Sprintf("reading event list: %v", err)})
	}
	st, edevs := mf.parseTimeline(data)
	devs = append(devs, edevs...)

	sampleRate, rateDev := rateFromByte(elst.rateByte, loc)
	if rateDev != nil {
		devs = append(devs, *rateDev)
	}
	format := Format(elst.formatCode)

	refs, _ := gatherRefs(st) // CD has no §8.3 cluster-count check; counts unused
	takes, takeDevs := decodeTakes(img, dec, g.files, refs, format, loc)
	devs = append(devs, takeDevs...)

	tracks, tlDevs := buildTracks(st, takes, SongRef{Key: key, Number: number, Name: g.name},
		audioSpec{sampleRate: sampleRate, format: format, clusterSize: blockSize}, stereo)
	devs = append(devs, tlDevs...)
	return tracks, devs
}

// listCD enumerates a CD archive's songs for one machine and summarises each from
// its event list, reusing the same chain walk and grouping extractCD uses but
// never resolving or decoding a take. It is the single list path both machines
// share.
func listCD(img cdSource, mf machineFormat) ([]SongInfo, []Deviation) {
	lay := mf.layout()
	files, devs, err := walkCD(img, lay)
	if err != nil {
		devs = append(devs, Deviation{Location: "disc", SpecRef: "§5.4", Severity: SeverityError,
			Message: fmt.Sprintf("walking archive: %v", err)})
		return nil, devs
	}
	var songs []SongInfo
	for i, g := range lay.group(files) {
		number, ndevs := lay.songNumber(img, g, i)
		devs = append(devs, ndevs...)
		key := cdSongKey(number)
		info, sdevs := summarizeCDSong(img, mf, g, number, key)
		devs = append(devs, sdevs...)
		songs = append(songs, info)
	}
	return songs, devs
}

// summarizeCDSong reads and parses one song's event list (exactly as
// extractCDSong does up to the point takes would be resolved) and reduces it to a
// catalog entry: the populated v-track count and timeline length come straight
// from the neutral timeline, so List and Extract agree by construction.
func summarizeCDSong(img cdSource, mf machineFormat, g songGroup, number int, key SongKey) (SongInfo, []Deviation) {
	lay := mf.layout()
	loc := fmt.Sprintf("song %d", number)
	base := SongInfo{Key: key, StoredNumber: number, Name: g.name, Machine: lay.machineName}

	elst, ok := findEventList(g.files)
	if !ok {
		return base, []Deviation{{Location: loc, SpecRef: "§5.4", Severity: SeverityError,
			Message: "no EVENTLST file found for song; nothing to extract"}}
	}
	data, err := img.ReadUserData(elst.dataOff, int(elst.size))
	if err != nil {
		return base, []Deviation{{Location: loc, SpecRef: lay.eventListRef, Severity: SeverityError,
			Message: fmt.Sprintf("reading event list: %v", err)}}
	}
	st, devs := mf.parseTimeline(data)

	sampleRate, rateDev := rateFromByte(elst.rateByte, loc)
	if rateDev != nil {
		devs = append(devs, *rateDev)
	}

	base.VTracks, base.Frames = summarizeVTracks(st)
	base.SampleRate = sampleRate
	return base, devs
}

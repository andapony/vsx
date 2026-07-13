package core

import (
	"fmt"
	"iter"
	"strings"
)

// Machine-neutral CD archive geometry (§5.4), shared by every machine's walk so
// the neutral path depends on no one adapter's file.
const (
	// blockSize is the archive cluster size: every header, header copy, song-
	// boundary block, and data block is one 0x8000-byte cluster.
	blockSize = 0x8000

	// firstFileHeader is the udoff of the first file header on an index-0 disc:
	// block 0 is the archive header, block 0x8000 the second copy (§5.4).
	firstFileHeader = 0x10000
)

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
	if string(hdr[:32]) != lay.sig() { // check 1
		return false
	}
	if !plausibleName(hdr[lay.nameOff() : lay.nameOff()+11]) { // check 2
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
	if err != nil || string(next) == lay.sig() {
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
		hdr, err := img.ReadUserData(udoff, lay.headerSpan())
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
		devs = append(devs, verifyJunction(img, hdr, fe.dataOff, fe.size, lay.headerSpan(),
			lay.identityFields(), "file "+strings.TrimRight(fe.filename, " "))...)
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
	files, wdevs, err := walkCD(img, mf)
	if err != nil {
		return nil, err
	}
	*ctx.devs = append(*ctx.devs, wdevs...)
	groups := mf.group(files)

	return func(yield func(TrackResult, error) bool) {
		present := make(map[SongKey]bool, len(groups))
		for i, g := range groups {
			number, ndevs := mf.songNumber(img, g, i)
			key := cdSongKey(number)
			present[key] = true
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
		*ctx.devs = append(*ctx.devs, ctx.unmatchedSongDeviations(present)...)
		ctx.report(Progress{Phase: ProgressDone})
	}, nil
}

// parseCDSong runs one CD song's prologue: locate and read its EVENTLST, parse it
// through mf into the machine-neutral parsedSong both List and Extract consume,
// and resolve the rate/format from the event-list file header. number, key, and
// the song-number-resolution deviations (ndevs) are resolved by the caller from
// the layout's song-number rule before any take is read (so Options.Songs
// filtering happens ahead of extraction); ndevs lead the returned deviations, in
// the one canonical order — song number, event list, then rate. A failure leaves
// the timeline empty with the reason in the deviations. Only the event-list spec
// clause differs per machine (via the layout); the rest is machine-neutral.
func parseCDSong(img cdSource, mf machineFormat, g songGroup, number int, key SongKey, ndevs []Deviation) (parsedSong, []Deviation) {
	loc := fmt.Sprintf("song %d", number)
	ps := parsedSong{ref: SongRef{Key: key, Number: number, Name: g.name}, machine: mf.machineName()}
	ps.created, ps.saved = mf.songStamps(img, g)
	devs := append([]Deviation{}, ndevs...)

	elst, ok := findEventList(g.files)
	if !ok {
		return ps, append(devs, Deviation{Location: loc, SpecRef: "§5.4", Severity: SeverityError,
			Message: "no EVENTLST file found for song; nothing to extract"})
	}
	data, err := img.ReadUserData(elst.dataOff, int(elst.size))
	if err != nil {
		return ps, append(devs, Deviation{Location: loc, SpecRef: mf.eventListRef(), Severity: SeverityError,
			Message: fmt.Sprintf("reading event list: %v", err)})
	}
	st, edevs := mf.parseTimeline(data)
	devs = append(devs, edevs...)

	sampleRate, rateDev := rateFromByte(elst.rateByte, loc)
	if rateDev != nil {
		devs = append(devs, *rateDev)
	}
	ps.aud = audioSpec{sampleRate: sampleRate, format: Format(elst.formatCode), clusterSize: blockSize}
	ps.st = st
	return ps, devs
}

// extractCDSong replays one song's event list against its takes into per-v-track
// results, through the shared parseCDSong prologue. Takes are resolved by FileID
// (§5.7). On a prologue that produced no timeline the reference gathering is
// empty, so no take is read and no track is built.
func extractCDSong(img cdSource, mf machineFormat, g songGroup, number int, ndevs []Deviation, key SongKey, dec Decoder, stereo bool) ([]TrackResult, []Deviation) {
	ps, devs := parseCDSong(img, mf, g, number, key, ndevs)
	loc := fmt.Sprintf("song %d", number)

	refs, _ := gatherRefs(ps.st) // CD has no §8.3 cluster-count check; counts unused
	takes, takeDevs := decodeTakes(img, dec, g.files, refs, ps.aud.format, loc)
	devs = append(devs, takeDevs...)

	tracks, tlDevs := buildTracks(ps.st, takes, ps.ref, ps.aud, stereo)
	devs = append(devs, tlDevs...)
	return tracks, devs
}

// listCD enumerates a CD archive's songs for one machine and summarises each from
// its event list, reusing the same chain walk and grouping extractCD uses but
// never resolving or decoding a take. It is the single list path both machines
// share.
func listCD(img cdSource, mf machineFormat) ([]SongInfo, []Deviation) {
	files, devs, err := walkCD(img, mf)
	if err != nil {
		devs = append(devs, Deviation{Location: "disc", SpecRef: "§5.4", Severity: SeverityError,
			Message: fmt.Sprintf("walking archive: %v", err)})
		return nil, devs
	}
	var songs []SongInfo
	for i, g := range mf.group(files) {
		number, ndevs := mf.songNumber(img, g, i)
		key := cdSongKey(number)
		info, sdevs := summarizeCDSong(img, mf, g, number, key, ndevs)
		devs = append(devs, sdevs...)
		songs = append(songs, info)
	}
	return songs, devs
}

// summarizeCDSong reduces one CD song to a catalog entry through the same
// parseCDSong prologue extractCDSong runs, then summarises the neutral timeline —
// so the two report identical prologue deviations and agree on the v-track count
// and length by construction. It decodes no take.
func summarizeCDSong(img cdSource, mf machineFormat, g songGroup, number int, key SongKey, ndevs []Deviation) (SongInfo, []Deviation) {
	ps, devs := parseCDSong(img, mf, g, number, key, ndevs)
	return ps.songInfo(), devs
}

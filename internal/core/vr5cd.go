package core

import (
	"encoding/binary"
	"fmt"
	"iter"
	"strings"
)

// VS-1880 header-block field offsets, from the block start (§5.4). Unlike the
// VS-880EX layout, the VR5 header block carries no source SONG number and no
// data-block count; the block count is derived from the file size, and the song
// number is read from the song's SONG file (§4.4).
const (
	vr5OffSongName   = 0x241E // 12 B
	vr5OffRateFormat = 0x242A // rate byte + format code (= SONG bytes 18–19)
	vr5OffFilename   = 0x2434 // 11 B filename #1
	vr5OffFileID     = 0x2444 // u16 BE
	vr5OffMagic      = 0x245C // 4 B constant magic
	vr5OffSize       = 0x2462 // u32 LE

	// vr5HeaderSpan is how many bytes of a header block we must read to see
	// every field above; +0x2466 covers the u32 size at 0x2462.
	vr5HeaderSpan = 0x2466
)

// vr5Magic is the §5.4/§5.5 constant at +0x245C that marks a genuine VR5 file
// header (check 3): a boundary block's stale per-file area fails it.
var vr5Magic = []byte{0x60, 0xBF, 0x51, 0x28}

// vr5IdentityFields are the VS-1880 header byte ranges that a continuation
// disc's repeated header must match for a spanned file (§5.6): the filename, the
// FileID, and the u32 size.
var vr5IdentityFields = [][2]int{
	{vr5OffFilename, vr5OffFilename + 11},
	{vr5OffFileID, vr5OffFileID + 2},
	{vr5OffSize, vr5OffSize + 4},
}

// walkVR5 enumerates a VS-1880 CD archive's files by the §5.4 chain walk,
// validating every landed-on block with the §5.5 checks and skipping the header
// copies and song-boundary blocks. VR5 has no song-boundary marker flag, so a
// boundary block is caught by the magic check (case 3) and the +0x8000 check.
// The walk ends at the §10 filler run; a dump lacking it is reported as a
// truncated rip and walked to end-of-data.
func walkVR5(img cdSource) ([]fileEntry, []Deviation, error) {
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
		hdr, err := img.ReadUserData(udoff, vr5HeaderSpan)
		if err != nil {
			return files, devs, fmt.Errorf("core: reading header block at %#x: %w", udoff, err)
		}
		if !validVR5FileHeader(img, hdr, udoff, end) {
			// A header copy or song-boundary block (§5.5): skip one slot and
			// re-test, exactly as the §5.4 chain walk prescribes.
			udoff += blockSize
			continue
		}
		fe := parseVR5Header(hdr, udoff)
		files = append(files, fe)
		devs = append(devs, verifyJunction(img, hdr, fe.dataOff, fe.size, vr5HeaderSpan,
			vr5IdentityFields, "file "+strings.TrimRight(fe.filename, " "))...)
		// VR5 stores no block count; derive it from the file size (§5.4:
		// next block = current + (1 + ceil(size/0x8000)) × 0x8000).
		blocks := (fe.size + blockSize - 1) / blockSize
		if blocks == 0 {
			blocks = 1
		}
		udoff += (1 + blocks) * blockSize
	}
	return files, devs, nil
}

// validVR5FileHeader applies the §5.5 acceptance rules for a VS-1880 block: the
// signature is present, the filename is plausible, the `60 BF 51 28` magic is at
// +0x245C, and the block at +0x8000 is file data (not another archive signature,
// and before the disc's data end). The index-0 block-0 check does not apply to a
// forward chain walk that starts at the first file header.
func validVR5FileHeader(img cdSource, hdr []byte, udoff, end int64) bool {
	if string(hdr[:32]) != sigVR5 { // check 1
		return false
	}
	if !plausibleName(hdr[vr5OffFilename : vr5OffFilename+11]) { // check 2
		return false
	}
	if string(hdr[vr5OffMagic:vr5OffMagic+4]) != string(vr5Magic) { // check 3
		return false
	}
	// check 6: a real header is followed by its file data, which never begins
	// with the archive signature, and always lies before the filler start. This
	// is the check that rejects every VR5 song-boundary block (§5.5 case 3).
	dataOff := udoff + blockSize
	if dataOff >= end {
		return false
	}
	next, err := img.ReadUserData(dataOff, 32)
	if err != nil || string(next) == sigVR5 {
		return false
	}
	return true
}

// parseVR5Header reads the §5.4 VS-1880 fields of a validated file header block.
// songNumber is left zero — VR5 header blocks do not carry it; it is resolved
// per song from the SONG file (§4.4).
func parseVR5Header(hdr []byte, udoff int64) fileEntry {
	return fileEntry{
		songName:   trimName(hdr[vr5OffSongName : vr5OffSongName+12]),
		filename:   string(hdr[vr5OffFilename : vr5OffFilename+11]),
		fileID:     binary.BigEndian.Uint16(hdr[vr5OffFileID:]),
		rateByte:   hdr[vr5OffRateFormat],
		formatCode: hdr[vr5OffRateFormat+1],
		dataOff:    udoff + blockSize,
		size:       int64(binary.LittleEndian.Uint32(hdr[vr5OffSize:])),
	}
}

// vr5EventListMagic opens the VR5 CD (and HDD) event list (§6.1/§4.5).
const vr5EventListMagic = "TAKE EVENT LIST "

// vr5TrackCount / vr5VTracksPerTrack define the fixed 18×16 = 288-entry VR5
// V-track table (§6.1).
const (
	vr5TrackCount      = 18
	vr5VTracksPerTrack = 16
	vr5TableEntries    = vr5TrackCount * vr5VTracksPerTrack

	vr5RecordSize = 64 // fixed VR5 event-record length (§7)
)

// vr5Entry is one positional V-track-table entry (§6.1): its 1-based track and
// v-track (from table position), its stored name, and its current-timeline
// events. Empty entries (event count 0) are retained so positions stay aligned.
type vr5Entry struct {
	track, vtrack int
	name          string
	events        []timelineEvent
}

// parseVR5EventList parses a VS-1880 CD event list (§6.1): the 16-byte magic, a
// u16 BE registry count whose N×64-byte historical registry is skipped, then
// exactly 288 positional V-track entries (18 tracks × 16 v-tracks). Each entry
// is a 16-byte name, a u16 BE event count, and that many 64-byte records — the
// current timeline. The table is parsed positionally with no `"V.T"` name
// gating; reads are bounded to 288 entries so optimize remnants past the table
// (§9) are never parsed as events.
func parseVR5EventList(data []byte) ([]vr5Entry, []Deviation) {
	var devs []Deviation
	if len(data) < 0x12 {
		return nil, []Deviation{{Location: "event list", SpecRef: "§6.1", Severity: SeverityError,
			Message: "event list shorter than its 18-byte header"}}
	}
	if string(data[:16]) != vr5EventListMagic {
		devs = append(devs, Deviation{Location: "event list", SpecRef: "§6.1", Severity: SeverityWarning,
			Message: "event list missing the \"TAKE EVENT LIST \" magic; parsing positionally anyway"})
	}
	registry := int(binary.BigEndian.Uint16(data[0x10:]))

	// Skip the historical registry: 16-byte magic + u16 count + N×64 records.
	off := 0x12 + registry*vr5RecordSize

	entries := make([]vr5Entry, 0, vr5TableEntries)
	for i := 0; i < vr5TableEntries; i++ {
		if off+18 > len(data) {
			devs = append(devs, Deviation{Location: "event list", SpecRef: "§6.1", Severity: SeverityError,
				Message: fmt.Sprintf("V-track table truncated: only %d of %d entries present", i, vr5TableEntries)})
			break
		}
		name := trimName(data[off : off+16])
		count := int(binary.BigEndian.Uint16(data[off+16:]))
		off += 18

		evs := make([]timelineEvent, 0, count)
		for j := 0; j < count; j++ {
			if off+vr5RecordSize > len(data) {
				devs = append(devs, Deviation{Location: "event list", SpecRef: "§6.1", Severity: SeverityError,
					Message: fmt.Sprintf("track %d/v-track %d declares %d records but the table ends after %d",
						i/vr5VTracksPerTrack+1, i%vr5VTracksPerTrack+1, count, j)})
				break
			}
			r := data[off : off+vr5RecordSize]
			evs = append(evs, timelineEvent{
				start:        binary.BigEndian.Uint32(r[0x00:]),
				end:          binary.BigEndian.Uint32(r[0x04:]),
				trimmed:      binary.BigEndian.Uint32(r[0x08:]),
				fileID:       binary.BigEndian.Uint16(r[0x14:]),
				clusterCount: binary.BigEndian.Uint16(r[0x18:]),
			})
			off += vr5RecordSize
		}
		entries = append(entries, vr5Entry{
			track:  i/vr5VTracksPerTrack + 1,
			vtrack: i%vr5VTracksPerTrack + 1,
			name:   name,
			events: evs,
		})
	}
	return entries, devs
}

// vr5Origin is the VS-1880 timeline origin (§3): VR5 StartFrames are absolute
// from zero, so nothing is subtracted (unlike VR9's origin of 12).
const vr5Origin = 0

// buildVR5Tracks replays the parsed V-track table into one PCM buffer per
// populated v-track (§8.1), reusing the shared timeline kernel with the VR5
// origin. Track/v-track come from table position; a user-assigned track name is
// carried into the result so the writer can append it to the filename.
func buildVR5Tracks(entries []vr5Entry, takes map[uint16]PCM, song SongRef, aud audioSpec, stereo bool) ([]TrackResult, []Deviation) {
	var built []builtTrack
	var devs []Deviation
	for _, ent := range entries {
		tr, ok, d := buildVTrack(ent.events, takes, vr5Origin, song, ent.track, ent.vtrack,
			userTrackName(ent.name), aud)
		devs = append(devs, d...)
		if ok {
			built = append(built, builtTrack{result: tr, events: ent.events})
		}
	}
	return pairTracks(built, stereo), devs
}

// userTrackName returns a table entry's name only when it is user-assigned
// (§6.1): default and blank names do not belong in a filename. The default
// renders as "V.T T-VV" — "V.T" followed by a space and single-digit track, or
// "V.T10- 1" with no space for two-digit tracks (§7) — so a default is always
// "V.T" followed by a space or a digit. A genuine name that merely starts with
// "V.T" (e.g. "V.Trumpet", a letter after "V.T") is kept.
func userTrackName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(name, "V.T"); ok {
		if rest == "" || rest[0] == ' ' || (rest[0] >= '0' && rest[0] <= '9') {
			return ""
		}
	}
	return name
}

// extractVR5 enumerates a VS-1880 CD archive and returns a lazy iterator over
// its per-v-track results, appending enumeration deviations to devs immediately
// and replay deviations as each song is consumed. Files are grouped into songs
// by header song name (§5.4), one song processed at a time so a large Source is
// never fully materialized.
func extractVR5(img cdSource, ctx extractCtx) (iter.Seq2[TrackResult, error], error) {
	files, wdevs, err := walkVR5(img)
	if err != nil {
		return nil, err
	}
	*ctx.devs = append(*ctx.devs, wdevs...)
	groups := groupVR5Songs(files)

	return func(yield func(TrackResult, error) bool) {
		for i, g := range groups {
			number, ndevs := vr5SongNumber(img, g.files, i)
			key := cdSongKey(number)
			if !ctx.selected(key) {
				continue
			}
			ctx.report(Progress{Phase: ProgressExtracting, Song: i + 1, TotalSongs: len(groups), SongName: g.name})
			tracks, sdevs := extractVR5Song(g, number, ndevs, key, ctx.dec, img, ctx.stereo)
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

// groupVR5Songs partitions the enumerated files into songs by header song name
// (§5.4: VR5 associates a file to a song by its 12-byte name), preserving
// first-seen (walk) order for deterministic output.
func groupVR5Songs(files []fileEntry) []songGroup {
	idx := map[string]int{}
	var groups []songGroup
	for _, f := range files {
		gi, ok := idx[f.songName]
		if !ok {
			idx[f.songName] = len(groups)
			groups = append(groups, songGroup{name: f.songName})
			gi = len(groups) - 1
		}
		groups[gi].files = append(groups[gi].files, f)
	}
	return groups
}

// extractVR5Song replays one song's V-track table against its takes into
// per-v-track results. number, numDevs, and key are resolved by the caller
// (extractVR5) from the song's SONG file (§4.4) before any take is read, so
// Options.Songs filtering happens ahead of this call; takes are resolved by
// FileID, which on VR5 CD is the take's archive filename number (§5.7).
func extractVR5Song(g songGroup, number int, numDevs []Deviation, key SongKey, dec Decoder, img cdSource, stereo bool) ([]TrackResult, []Deviation) {
	devs := numDevs
	loc := fmt.Sprintf("song %d", number)

	elst, ok := findEventList(g.files)
	if !ok {
		return nil, append(devs, Deviation{Location: loc, SpecRef: "§5.4", Severity: SeverityError,
			Message: "no EVENTLST file found for song; nothing to extract"})
	}
	data, err := img.ReadUserData(elst.dataOff, int(elst.size))
	if err != nil {
		return nil, append(devs, Deviation{Location: loc, SpecRef: "§6.1", Severity: SeverityError,
			Message: fmt.Sprintf("reading event list: %v", err)})
	}
	entries, edevs := parseVR5EventList(data)
	devs = append(devs, edevs...)

	sampleRate, rateDev := rateFromByte(elst.rateByte, loc)
	if rateDev != nil {
		devs = append(devs, *rateDev)
	}
	format := Format(elst.formatCode)

	var refs []uint16
	for _, ent := range entries {
		for _, e := range ent.events {
			refs = append(refs, e.fileID)
		}
	}
	takes, takeDevs := decodeTakes(img, dec, g.files, refs, format, loc)
	devs = append(devs, takeDevs...)

	tracks, tlDevs := buildVR5Tracks(entries, takes, SongRef{Key: key, Number: number, Name: g.name},
		audioSpec{sampleRate: sampleRate, format: format, clusterSize: blockSize}, stereo)
	devs = append(devs, tlDevs...)
	return tracks, devs
}

// vr5SongNumber resolves a song's catalog number from its `SONG    VR5` file
// (§4.4: source folder number at content offset 0x04). When no SONG file is
// present, it falls back to the walk position and reports a deviation, so output
// still carries a distinct, stable number per song.
func vr5SongNumber(img cdSource, files []fileEntry, index int) (int, []Deviation) {
	for _, f := range files {
		if !strings.HasPrefix(f.filename, "SONG") {
			continue
		}
		content, err := img.ReadUserData(f.dataOff, 6)
		if err != nil {
			break
		}
		return int(binary.BigEndian.Uint16(content[0x04:])), nil
	}
	return index + 1, []Deviation{{Location: fmt.Sprintf("song #%d", index+1), SpecRef: "§4.4", Severity: SeverityWarning,
		Message: "no SONG file for this song; catalog number inferred from disc order"}}
}

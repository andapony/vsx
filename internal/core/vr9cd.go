package core

import (
	"encoding/binary"
	"fmt"
	"iter"
	"strings"
)

// VS-880EX header-block field offsets, from the block start (§5.4).
const (
	blockSize = 0x8000

	offSongNumber = 0x160C // u16 BE
	offSongName   = 0x160E // 12 B
	offRateFormat = 0x161A // rate byte + format code
	offFilename   = 0x1624 // 11 B name + NUL
	offBlockCount = 0x1630 // u16 BE = ceil(size/0x8000)
	offMarker     = 0x1632 // u16: 0 real / 1 song-start marker
	offFileID     = 0x1634 // u16 BE
	offSize       = 0x1652 // u32 LE

	// headerSpan is how many bytes of a header block we must read to see every
	// field above; +0x1656 covers the u32 size at 0x1652.
	headerSpan = 0x1656

	// firstFileHeader is the udoff of the first file header on an index-0 disc:
	// block 0 is the archive header, block 0x8000 the second copy (§5.4).
	firstFileHeader = 0x10000
)

// vr9IdentityFields are the VS-880EX header byte ranges that a continuation
// disc's repeated header must match for a spanned file (§5.6): the filename, the
// FileID, and the u32 size.
var vr9IdentityFields = [][2]int{
	{offFilename, offFilename + 11},
	{offFileID, offFileID + 2},
	{offSize, offSize + 4},
}

// fileEntry is one enumerated on-disc file: where its data lives and the
// identity fields that associate it to a song and to event records (§5.4).
type fileEntry struct {
	songNumber uint16
	songName   string
	filename   string // 11-char CD name, e.g. "TAKE0C53VR9"
	fileID     uint16 // = event record's take start cluster (§5.7)
	rateByte   byte
	formatCode byte
	dataOff    int64 // user-data offset where file data begins (header + 0x8000)
	size       int64
}

// walkVR9 enumerates a VS-880EX CD archive's files by the §5.4 chain walk,
// validating every landed-on block with the §5.5 checks and skipping the header
// copies and song-boundary blocks that are not file headers. The walk ends at
// the §10 filler run; a dump lacking it is reported as a truncated rip
// (deviation) and walked to end-of-data.
func walkVR9(img cdSource) ([]fileEntry, []Deviation, error) {
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
		hdr, err := img.ReadUserData(udoff, headerSpan)
		if err != nil {
			return files, devs, fmt.Errorf("core: reading header block at %#x: %w", udoff, err)
		}
		if !validFileHeader(img, hdr, udoff, end) {
			// A header copy or song-boundary block (§5.5): skip one slot and
			// re-test, exactly as the §5.4 chain walk prescribes.
			udoff += blockSize
			continue
		}
		fe := parseHeader(hdr, udoff)
		files = append(files, fe)
		devs = append(devs, verifyJunction(img, hdr, fe.dataOff, fe.size, headerSpan,
			vr9IdentityFields, "file "+strings.TrimRight(fe.filename, " "))...)
		blockCount := int64(binary.BigEndian.Uint16(hdr[offBlockCount:]))
		udoff += (1 + blockCount) * blockSize
	}
	return files, devs, nil
}

// validFileHeader applies the §5.5 acceptance rules for a VS-880EX block: the
// signature is present, the filename is plausible, the marker flag is clear,
// and the block at +0x8000 is file data (not another archive signature, and not
// at/past the disc's data end). The VR5-only magic and the index-0 block-0
// checks do not apply to a forward chain walk that starts at the first file
// header.
func validFileHeader(img cdSource, hdr []byte, udoff, end int64) bool {
	if string(hdr[:32]) != sigVR9 { // check 1
		return false
	}
	if !plausibleName(hdr[offFilename : offFilename+11]) { // check 2
		return false
	}
	if binary.BigEndian.Uint16(hdr[offMarker:]) != 0 { // check 4: song-boundary marker
		return false
	}
	// check 6: a real header is followed by its file data, which never begins
	// with the archive signature, and always lies before the filler start.
	dataOff := udoff + blockSize
	if dataOff >= end {
		return false
	}
	next, err := img.ReadUserData(dataOff, 32)
	if err != nil || string(next) == sigVR9 {
		return false
	}
	return true
}

// plausibleName reports whether an 11-byte filename field is a valid VS name
// (§5.5 check 2): [A-Z0-9] then seven of [A-Z0-9 ], then a "VR5"/"VR9"
// extension. Names never begin with a space.
func plausibleName(name []byte) bool {
	if len(name) != 11 {
		return false
	}
	ext := string(name[8:11])
	if ext != "VR9" && ext != "VR5" {
		return false
	}
	if !isNameChar(name[0]) || name[0] == ' ' {
		return false
	}
	for _, c := range name[1:8] {
		if !isNameChar(c) {
			return false
		}
	}
	return true
}

func isNameChar(c byte) bool {
	return c == ' ' || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// parseHeader reads the §5.4 fields of a validated file header block.
func parseHeader(hdr []byte, udoff int64) fileEntry {
	return fileEntry{
		songNumber: binary.BigEndian.Uint16(hdr[offSongNumber:]),
		songName:   trimName(hdr[offSongName : offSongName+12]),
		filename:   string(hdr[offFilename : offFilename+11]),
		fileID:     binary.BigEndian.Uint16(hdr[offFileID:]),
		rateByte:   hdr[offRateFormat],
		formatCode: hdr[offRateFormat+1],
		dataOff:    udoff + blockSize,
		size:       int64(binary.LittleEndian.Uint32(hdr[offSize:])),
	}
}

// trimName renders a space-padded VS name field as a Go string, dropping
// trailing spaces and NULs.
func trimName(b []byte) string {
	return strings.TrimRight(string(b), " \x00")
}

// vr9OriginFrames is the VS-880EX timeline origin (§3): normal audio events
// carry StartFrame = 12, so 12 frames are subtracted to place samples at zero.
const vr9OriginFrames = 12

// vr9Event is one parsed VS-880EX event-log record (§7): a take placement on the
// timeline. Frames are the raw stored values (pre-origin). code is the 0-based
// v-track code at record offset 0x20. A record is an erase (writes silence) iff
// fileID is 0 (§8.2); the 0x21 flag byte is deliberately not gated on, because
// §8.2 warns it is also set on some records carrying a real take, which must be
// laid down as ordinary audio.
type vr9Event struct {
	start, end, trimmed uint32
	fileID              uint16
	clusterCount        uint16 // event 0x18: for the §8.3 HDD integrity check
	code                int
}

// buildVR9Tracks replays a VS-880EX event log into one PCM buffer per populated
// v-track (§8.2): records group by v-track code in log order, then each group is
// replayed by the shared timeline kernel with the VR9 origin. A v-track with no
// take-bearing record yields no TrackResult. The VR9 log carries no per-track
// name, so results carry the default (empty) Name.
func buildVR9Tracks(events []vr9Event, takes map[uint16]PCM, song SongRef, aud audioSpec, stereo bool) ([]TrackResult, []Deviation) {
	// Group events by v-track code, preserving log order, and remember the
	// code ordering so output is deterministic.
	byCode := map[int][]vr9Event{}
	var codeOrder []int
	for _, e := range events {
		if _, seen := byCode[e.code]; !seen {
			codeOrder = append(codeOrder, e.code)
		}
		byCode[e.code] = append(byCode[e.code], e)
	}

	var built []builtTrack
	var devs []Deviation
	for _, code := range codeOrder {
		evs := make([]timelineEvent, len(byCode[code]))
		for i, e := range byCode[code] {
			evs[i] = timelineEvent{start: e.start, end: e.end, trimmed: e.trimmed, fileID: e.fileID, clusterCount: e.clusterCount}
		}
		track := code/8 + 1
		vtrack := code%8 + 1
		tr, ok, d := buildVTrack(evs, takes, vr9OriginFrames, song, track, vtrack, "", aud)
		devs = append(devs, d...)
		if ok {
			built = append(built, builtTrack{result: tr, events: evs})
		}
	}
	return pairTracks(built, stereo), devs
}

// vr9RecordSize is the fixed VS-880EX event-record length (§1/§7).
const vr9RecordSize = 48

// parseVR9Log parses a VS-880EX event log (§6.2/§4.6): a u16 BE live count then
// that many 48-byte records. Only the live records form the timeline; the tail
// (optimize remnants, §9) is bounded out by the count. A count that overruns the
// available bytes is reported as a truncated event list.
func parseVR9Log(data []byte) ([]vr9Event, []Deviation) {
	var devs []Deviation
	if len(data) < 2 {
		return nil, []Deviation{{Location: "event list", SpecRef: "§6.2", Severity: SeverityError,
			Message: "event list shorter than its 2-byte header"}}
	}
	count := int(binary.BigEndian.Uint16(data))
	events := make([]vr9Event, 0, count)
	for i := 0; i < count; i++ {
		off := 2 + i*vr9RecordSize
		if off+vr9RecordSize > len(data) {
			devs = append(devs, Deviation{Location: "event list", SpecRef: "§6.2", Severity: SeverityError,
				Message: fmt.Sprintf("event list declares %d records but holds only %d", count, i)})
			break
		}
		r := data[off : off+vr9RecordSize]
		events = append(events, vr9Event{
			start:        binary.BigEndian.Uint32(r[0x00:]),
			end:          binary.BigEndian.Uint32(r[0x04:]),
			trimmed:      binary.BigEndian.Uint32(r[0x08:]),
			fileID:       binary.BigEndian.Uint16(r[0x14:]),
			clusterCount: binary.BigEndian.Uint16(r[0x18:]),
			code:         int(r[0x20]),
		})
	}
	return events, devs
}

// songGroup is the files of one song, in walk order, keyed by its source SONG
// number (§5.4 associates VR9 files to songs by that number).
type songGroup struct {
	number uint16
	name   string
	files  []fileEntry
}

// groupSongs partitions the enumerated files by source SONG number, preserving
// first-seen (walk) order for deterministic output.
func groupSongs(files []fileEntry) []songGroup {
	idx := map[uint16]int{}
	var groups []songGroup
	for _, f := range files {
		gi, ok := idx[f.songNumber]
		if !ok {
			idx[f.songNumber] = len(groups)
			groups = append(groups, songGroup{number: f.songNumber, name: f.songName})
			gi = len(groups) - 1
		}
		groups[gi].files = append(groups[gi].files, f)
	}
	return groups
}

// extractVR9 enumerates a VS-880EX CD archive and returns a lazy iterator over
// its per-v-track results, appending deviations found during enumeration to
// devs immediately and those found during replay as each song is consumed. The
// iterator processes one song at a time so a large Source is never fully
// materialized (bounded memory, per the foundation).
func extractVR9(img cdSource, dec Decoder, devs *[]Deviation, stereo bool) (iter.Seq2[TrackResult, error], error) {
	files, wdevs, err := walkVR9(img)
	if err != nil {
		return nil, err
	}
	*devs = append(*devs, wdevs...)
	groups := groupSongs(files)

	return func(yield func(TrackResult, error) bool) {
		for _, g := range groups {
			tracks, sdevs := extractSong(img, dec, g, stereo)
			*devs = append(*devs, sdevs...)
			for _, tr := range tracks {
				if !yield(tr, nil) {
					return
				}
			}
		}
	}, nil
}

// extractSong replays one song's event log against its takes into per-v-track
// results. Takes are resolved by FileID (§5.7: VR9 CD FileIDs equal the HDD FAT
// start clusters the event records carry) and decoded on demand.
func extractSong(img cdSource, dec Decoder, g songGroup, stereo bool) ([]TrackResult, []Deviation) {
	loc := fmt.Sprintf("song %d", g.number)
	elst, ok := findEventList(g.files)
	if !ok {
		return nil, []Deviation{{Location: loc, SpecRef: "§5.4", Severity: SeverityError,
			Message: "no EVENTLST file found for song; nothing to extract"}}
	}
	data, err := img.ReadUserData(elst.dataOff, int(elst.size))
	if err != nil {
		return nil, []Deviation{{Location: loc, SpecRef: "§6.2", Severity: SeverityError,
			Message: fmt.Sprintf("reading event list: %v", err)}}
	}
	events, devs := parseVR9Log(data)

	sampleRate, rateDev := rateFromByte(elst.rateByte, loc)
	if rateDev != nil {
		devs = append(devs, *rateDev)
	}
	format := Format(elst.formatCode)

	refs := make([]uint16, 0, len(events))
	for _, e := range events {
		refs = append(refs, e.fileID)
	}
	takes, takeDevs := decodeTakes(img, dec, g.files, refs, format, loc)
	devs = append(devs, takeDevs...)

	tracks, tlDevs := buildVR9Tracks(events, takes, SongRef{Number: int(g.number), Name: g.name},
		audioSpec{sampleRate: sampleRate, format: format, clusterSize: blockSize}, stereo)
	devs = append(devs, tlDevs...)
	return tracks, devs
}

// findEventList returns the song's EVENTLST file.
func findEventList(files []fileEntry) (fileEntry, bool) {
	for _, f := range files {
		if strings.HasPrefix(f.filename, "EVENTLST") {
			return f, true
		}
	}
	return fileEntry{}, false
}

// decodeTakes decodes every referenced take into a FileID→PCM map, reading each
// take file's bytes from the image and decoding through the seam. Takes are
// resolved by FileID — the number in the take's archive filename (`TAKE%04X`) —
// which the event record's 0x14 field carries on both machines (§5.7: VR9 keeps
// HDD start clusters, VR5 renames into a dense archive cluster space, but either
// way the referenced ID equals the take's header FileID). refs is the list of
// FileIDs the timeline references (0 = erase, skipped). Referenced takes with no
// file on disc are left absent (the timeline builder reports them, §10).
func decodeTakes(img cdSource, dec Decoder, files []fileEntry, refs []uint16, format Format, loc string) (map[uint16]PCM, []Deviation) {
	byID := map[uint16]fileEntry{}
	for _, f := range files {
		if strings.HasPrefix(f.filename, "TAKE") {
			byID[f.fileID] = f
		}
	}
	var devs []Deviation
	takes := map[uint16]PCM{}
	for _, id := range refs {
		if id == 0 {
			continue // erase record references no take
		}
		if _, done := takes[id]; done {
			continue
		}
		f, ok := byID[id]
		if !ok {
			continue // reported by the timeline builder as a missing take
		}
		raw, rdevs := readFileData(img, f.dataOff, f.size, loc, id)
		devs = append(devs, rdevs...)
		if raw == nil {
			continue
		}
		pcm, err := dec.Decode(format, raw, blockSize)
		if err != nil {
			devs = append(devs, Deviation{Location: loc, SpecRef: "§2", Severity: SeverityError,
				Message: fmt.Sprintf("decoding take %#04x: %v", id, err)})
			continue
		}
		takes[id] = pcm
	}
	return takes, devs
}

// rateFromByte decodes the §3 sample-rate byte (low nibble). Unobserved or
// unknown encodings default to 44.1 kHz with a deviation, so extraction
// proceeds rather than failing on a rate field.
func rateFromByte(b byte, loc string) (int, *Deviation) {
	switch b & 0x0F {
	case 0:
		return 48000, nil
	case 1:
		return 44100, nil
	case 2:
		return 32000, nil
	case 3:
		return 96000, nil
	case 4:
		return 88200, nil
	default:
		return 44100, &Deviation{Location: loc, SpecRef: "§3", Severity: SeverityWarning,
			Message: fmt.Sprintf("unknown sample-rate byte %#02x; assuming 44.1 kHz", b)}
	}
}

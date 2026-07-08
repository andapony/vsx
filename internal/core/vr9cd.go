package core

import (
	"encoding/binary"
	"fmt"
	"iter"
	"strings"

	"github.com/andapony/vsx/internal/cd"
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
func walkVR9(img *cd.Image) ([]fileEntry, []Deviation, error) {
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
func validFileHeader(img *cd.Image, hdr []byte, udoff, end int64) bool {
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

// samplesPerFrame is the RDAC framing constant: one frame is 16 samples (§3).
const samplesPerFrame = 16

// vr9Event is one parsed VS-880EX event-log record (§7): a take placement on the
// timeline. Frames are the raw stored values (pre-origin). code is the 0-based
// v-track code at record offset 0x20. A record is an erase (writes silence) iff
// fileID is 0 (§8.2); the 0x21 flag byte is deliberately not gated on, because
// §8.2 warns it is also set on some records carrying a real take, which must be
// laid down as ordinary audio.
type vr9Event struct {
	start, end, trimmed uint32
	fileID              uint16
	code                int
}

// buildVR9Tracks replays a VS-880EX event log into one PCM buffer per populated
// v-track (§8.2): records are applied in stored order, each writing its
// [Start,End) range over whatever is there (later wins); gaps stay silent; an
// erase (fileID 0 / tombstone) writes silence; VR9 origin = 12 is applied. A
// v-track with no take-bearing record yields no TrackResult.
func buildVR9Tracks(events []vr9Event, takes map[uint16]PCM, song SongRef, sampleRate int, format Format) ([]TrackResult, []Deviation) {
	var devs []Deviation

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

	bitDepth := bitDepthForFormat(format)
	var out []TrackResult
	for _, code := range codeOrder {
		evs := byCode[code]
		track := code/8 + 1
		vtrack := code%8 + 1
		loc := fmt.Sprintf("song %d / track %d / v-track %d", song.Number, track, vtrack)

		hasAudio := false
		length := 0
		for _, e := range evs {
			if e.fileID != 0 {
				hasAudio = true
			}
			if end := (int(e.end) - vr9OriginFrames) * samplesPerFrame; end > length {
				length = end
			}
		}
		if !hasAudio {
			continue // empty v-track: no file
		}

		buf := make([]int32, length)
		var firstCluster uint16
		for _, e := range evs {
			if e.end <= e.start {
				devs = append(devs, Deviation{Location: loc, SpecRef: "§8", Severity: SeverityWarning,
					Message: fmt.Sprintf("degenerate event (EndFrame %d ≤ StartFrame %d); skipped", e.end, e.start)})
				continue
			}
			at := (int(e.start) - vr9OriginFrames) * samplesPerFrame
			span := (int(e.end) - int(e.start)) * samplesPerFrame
			// later-wins: clear the range first so a short/erase write does not
			// leave a previous take's tail showing through.
			clearRange(buf, at, span)
			if e.fileID == 0 {
				continue // erase (§8.2): the cleared range is the result
			}
			if firstCluster == 0 {
				firstCluster = e.fileID
			}
			take, ok := takes[e.fileID]
			if !ok {
				devs = append(devs, Deviation{Location: loc, SpecRef: "§10", Severity: SeverityError,
					Message: fmt.Sprintf("event references take %#04x with no take file; span filled with silence", e.fileID)})
				continue
			}
			trim := int(e.trimmed) * samplesPerFrame
			copied := overlay(buf, at, span, take.Samples, trim)
			if copied < span {
				devs = append(devs, Deviation{Location: loc, SpecRef: "§10", Severity: SeverityWarning,
					Message: fmt.Sprintf("take %#04x shorter than event span; %d samples padded with silence", e.fileID, span-copied)})
			}
		}

		out = append(out, TrackResult{
			Song:   song,
			Track:  track,
			VTrack: vtrack,
			PCM:    PCM{Samples: buf, BitDepth: bitDepth},
			Take: Take{
				FirstCluster: int(firstCluster),
				ClusterSize:  blockSize,
				Format:       format,
				SampleRate:   sampleRate,
			},
		})
	}
	return out, devs
}

// clearRange zeroes buf over [at, at+span), clamped to the buffer, so a
// later-winning record's silence overwrites earlier audio.
func clearRange(buf []int32, at, span int) {
	lo, hi := clamp(len(buf), at, span)
	for i := lo; i < hi; i++ {
		buf[i] = 0
	}
}

// overlay copies up to span samples from src[srcOff:] into buf at position at,
// clamped to both buffer and source. It returns the number of destination
// samples that received real audio (fewer than span means the source ran out —
// a truncated take, §10).
func overlay(buf []int32, at, span int, src []int32, srcOff int) int {
	lo, hi := clamp(len(buf), at, span)
	copied := 0
	for i := lo; i < hi; i++ {
		si := srcOff + (i - at)
		if si < 0 || si >= len(src) {
			continue
		}
		buf[i] = src[si]
		copied++
	}
	return copied
}

// clamp returns the [lo,hi) intersection of [at,at+span) with [0,length).
func clamp(length, at, span int) (int, int) {
	lo, hi := at, at+span
	if lo < 0 {
		lo = 0
	}
	if hi > length {
		hi = length
	}
	if lo > hi {
		lo = hi
	}
	return lo, hi
}

// bitDepthForFormat returns the PCM bit depth a decoded format yields (§2).
func bitDepthForFormat(f Format) int {
	switch f {
	case FormatMTP, FormatM24:
		return 24
	default:
		return 16
	}
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
			start:   binary.BigEndian.Uint32(r[0x00:]),
			end:     binary.BigEndian.Uint32(r[0x04:]),
			trimmed: binary.BigEndian.Uint32(r[0x08:]),
			fileID:  binary.BigEndian.Uint16(r[0x14:]),
			code:    int(r[0x20]),
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
func extractVR9(img *cd.Image, dec Decoder, devs *[]Deviation) (iter.Seq2[TrackResult, error], error) {
	files, wdevs, err := walkVR9(img)
	if err != nil {
		return nil, err
	}
	*devs = append(*devs, wdevs...)
	groups := groupSongs(files)

	return func(yield func(TrackResult, error) bool) {
		for _, g := range groups {
			tracks, sdevs := extractSong(img, dec, g)
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
func extractSong(img *cd.Image, dec Decoder, g songGroup) ([]TrackResult, []Deviation) {
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

	takes, takeDevs := decodeTakes(img, dec, g.files, events, format, loc)
	devs = append(devs, takeDevs...)

	tracks, tlDevs := buildVR9Tracks(events, takes, SongRef{Number: int(g.number), Name: g.name}, sampleRate, format)
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

// decodeTakes decodes every take referenced by the song's events into a
// FileID→PCM map, reading each take file's bytes from the image and decoding
// through the seam. Referenced takes with no file on disc are left absent (the
// timeline builder reports them, §10).
func decodeTakes(img *cd.Image, dec Decoder, files []fileEntry, events []vr9Event, format Format, loc string) (map[uint16]PCM, []Deviation) {
	byID := map[uint16]fileEntry{}
	for _, f := range files {
		if strings.HasPrefix(f.filename, "TAKE") {
			byID[f.fileID] = f
		}
	}
	var devs []Deviation
	takes := map[uint16]PCM{}
	for _, e := range events {
		if e.fileID == 0 {
			continue // erase record references no take
		}
		if _, done := takes[e.fileID]; done {
			continue
		}
		f, ok := byID[e.fileID]
		if !ok {
			continue // reported by the timeline builder as a missing take
		}
		raw, err := img.ReadUserData(f.dataOff, int(f.size))
		if err != nil {
			devs = append(devs, Deviation{Location: loc, SpecRef: "§5.4", Severity: SeverityError,
				Message: fmt.Sprintf("reading take %#04x: %v", e.fileID, err)})
			continue
		}
		pcm, err := dec.Decode(format, raw, blockSize)
		if err != nil {
			devs = append(devs, Deviation{Location: loc, SpecRef: "§2", Severity: SeverityError,
				Message: fmt.Sprintf("decoding take %#04x: %v", e.fileID, err)})
			continue
		}
		takes[e.fileID] = pcm
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

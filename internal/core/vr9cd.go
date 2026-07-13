package core

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// VS-880EX header-block field offsets, from the block start (§5.4).
const (
	offSongNumber = 0x160C // u16 BE
	offSongName   = 0x160E // 12 B
	offRateFormat = 0x161A // rate byte + format code
	offFilename   = 0x1624 // 11 B name + NUL
	offBlockCount = 0x1630 // u16 BE = ceil(size/0x8000)
	offMarker     = 0x1632 // u16: 0 real / 1 song-start marker
	offFileID     = 0x1634 // u16 BE
	offSize       = 0x1652 // u32 LE

	// vr9HeaderSpan is how many bytes of a header block we must read to see every
	// field above; +0x1656 covers the u32 size at 0x1652.
	vr9HeaderSpan = 0x1656
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

// vr9 is the VS-880EX event-list adapter (ADR-0003): a flat event log whose
// records each carry a v-track code, replayed at origin 12 (§3).
type vr9 struct{}

// The VS-880EX CD archive layout (§5.4/§5.5) the shared chain walk runs on: a
// header validated by its clear song-boundary marker flag (check 4), a stored
// block count driving the chain step, and songs grouped by the source SONG
// number the header carries. Each method is one piece of the cdLayout seam, so
// the compiler enforces that vr9 supplies them all.

func (vr9) machineName() string                     { return "VR9" }
func (vr9) sig() string                             { return sigVR9 }
func (vr9) nameOff() int                            { return offFilename }
func (vr9) headerSpan() int                         { return vr9HeaderSpan }
func (vr9) identityFields() [][2]int                { return vr9IdentityFields }
func (vr9) eventListRef() string                    { return "§6.2" }
func (vr9) parse(hdr []byte, udoff int64) fileEntry { return parseHeader(hdr, udoff) }
func (vr9) group(files []fileEntry) []songGroup     { return groupSongs(files) }

// accept is the VR9 §5.5 gate (check 4): a real header's marker flag is clear; a
// song-boundary block sets it.
func (vr9) accept(hdr []byte) bool {
	return binary.BigEndian.Uint16(hdr[offMarker:]) == 0
}

// blocks reads the data-block count the VR9 header stores (§5.4).
func (vr9) blocks(hdr []byte, _ fileEntry) int64 {
	return int64(binary.BigEndian.Uint16(hdr[offBlockCount:]))
}

// songNumber returns the source SONG number the VR9 header carries (§5.4).
func (vr9) songNumber(_ cdSource, g songGroup, _ int) (int, []Deviation) {
	return int(g.number), nil
}

// songStamps returns the zero pair: the VS-880EX carries no timestamps anywhere
// (§4.4), so VR9 songs render the placeholder in both columns.
func (vr9) songStamps(_ cdSource, _ songGroup) (created, saved time.Time) {
	return time.Time{}, time.Time{}
}

// parseTimeline reduces a VS-880EX event log (§6.2/§8.2) to a machine-neutral
// songTimeline: records group by v-track code in log order, each code mapping to
// its 1-based track and v-track. The VR9 log carries no per-track name, so every
// group's name is the default (empty).
func (vr9) parseTimeline(data []byte) (songTimeline, []Deviation) {
	events, devs := parseVR9Log(data)
	return songTimeline{origin: vr9OriginFrames, groups: groupVR9Events(events)}, devs
}

// groupVR9Events groups a parsed VS-880EX event log by v-track code (§8.2),
// preserving first-seen (log) order so output is deterministic, and maps each
// code to its 1-based track (code/8+1) and v-track (code%8+1).
func groupVR9Events(events []vr9Event) []vtrackGroup {
	byCode := map[int][]vr9Event{}
	var codeOrder []int
	for _, e := range events {
		if _, seen := byCode[e.code]; !seen {
			codeOrder = append(codeOrder, e.code)
		}
		byCode[e.code] = append(byCode[e.code], e)
	}
	groups := make([]vtrackGroup, 0, len(codeOrder))
	for _, code := range codeOrder {
		evs := make([]timelineEvent, len(byCode[code]))
		for i, e := range byCode[code] {
			evs[i] = timelineEvent{start: e.start, end: e.end, trimmed: e.trimmed, fileID: e.fileID, clusterCount: e.clusterCount}
		}
		groups = append(groups, vtrackGroup{track: code/8 + 1, vtrack: code%8 + 1, events: evs})
	}
	return groups
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

// groupSongs partitions the enumerated files by source SONG number (§5.4).
func groupSongs(files []fileEntry) []songGroup {
	return groupBy(files,
		func(f fileEntry) uint16 { return f.songNumber },
		func(f fileEntry) songGroup { return songGroup{number: f.songNumber, name: f.songName} })
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

package core

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// vr5SongHeaderLen is the SONG.VR5 header length (§4.4): 38 bytes, enough to
// cover the created (0x14) and last-saved (0x1C) timestamps songStamps reads.
const vr5SongHeaderLen = 38

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

// vr5BlockCount derives how many data blocks a VS-1880 file occupies from its
// size: VR5 stores no block count, so it is ceil(size/0x8000), floored at one so
// even a zero-length file advances the §5.4 chain by a whole block.
func vr5BlockCount(_ []byte, fe fileEntry) int64 {
	blocks := (fe.size + blockSize - 1) / blockSize
	if blocks == 0 {
		blocks = 1
	}
	return blocks
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
				stamp:        decodeStamp(r[0x28 : 0x28+8]), // record creation time (§7)
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

// vr5 is the VS-1880 event-list adapter (ADR-0003): a positional 288-entry
// V-track table (§6.1), replayed at origin 0 (§3), carrying user track names.
type vr5 struct{}

// The VS-1880 CD archive layout (§5.4/§5.5) the shared chain walk runs on: a
// header validated by the `60 BF 51 28` magic at +0x245C (check 3, which also
// rejects VR5's markerless song-boundary blocks), a block count derived from the
// file size, and songs grouped by the header song name with the catalog number
// resolved from each song's SONG file. Each method is one piece of the cdLayout
// seam, so the compiler enforces that vr5 supplies them all.

func (vr5) machineName() string                     { return "VR5" }
func (vr5) sig() string                             { return sigVR5 }
func (vr5) nameOff() int                            { return vr5OffFilename }
func (vr5) headerSpan() int                         { return vr5HeaderSpan }
func (vr5) identityFields() [][2]int                { return vr5IdentityFields }
func (vr5) eventListRef() string                    { return "§6.1" }
func (vr5) parse(hdr []byte, udoff int64) fileEntry { return parseVR5Header(hdr, udoff) }
func (vr5) blocks(hdr []byte, fe fileEntry) int64   { return vr5BlockCount(hdr, fe) }
func (vr5) group(files []fileEntry) []songGroup     { return groupVR5Songs(files) }

// accept is the VR5 §5.5 gate (check 3): the constant magic marks a genuine VR5
// file header; a boundary block's stale per-file area fails it.
func (vr5) accept(hdr []byte) bool {
	return string(hdr[vr5OffMagic:vr5OffMagic+4]) == string(vr5Magic)
}

// songNumber resolves the catalog number from the song's SONG file (§4.4): VR5
// headers carry no source SONG number, so it falls back to walk order.
func (vr5) songNumber(img cdSource, g songGroup, index int) (int, []Deviation) {
	return vr5SongNumber(img, g.files, index)
}

// songStamps decodes the created/last-saved timestamps from the song's SONG file
// (§4.4) — on CD this is the byte-for-byte SONG.VR5 copy (§5.3), so the stamps
// equal the on-disc song's for an unmodified archive. A missing or short SONG
// file yields the zero pair, rendered as the placeholder.
func (vr5) songStamps(img cdSource, g songGroup) (created, saved time.Time) {
	for _, f := range g.files {
		if !strings.HasPrefix(f.filename, "SONG") {
			continue
		}
		content, err := img.ReadUserData(f.dataOff, vr5SongHeaderLen)
		if err != nil {
			break
		}
		return decodeStamp(headerStamp(content, 0x14)), decodeStamp(headerStamp(content, 0x1C))
	}
	return time.Time{}, time.Time{}
}

// parseTimeline reduces a VS-1880 V-track table (§6.1) to a machine-neutral
// songTimeline: each positional entry maps to a v-track group at its
// table-derived track and v-track, with its name normalized so only a
// user-assigned name (not the "V.T…" default) survives into the filename.
func (vr5) parseTimeline(data []byte) (songTimeline, []Deviation) {
	entries, devs := parseVR5EventList(data)
	return songTimeline{origin: vr5Origin, groups: groupVR5Entries(entries)}, devs
}

// groupVR5Entries maps each positional V-track-table entry (§6.1) to a
// machine-neutral group, applying the §6.1/§7 default-name rule so a default or
// blank name yields no filename suffix. Empty positions are retained; the
// neutral build drops them (no take-bearing event → no TrackResult).
func groupVR5Entries(entries []vr5Entry) []vtrackGroup {
	groups := make([]vtrackGroup, len(entries))
	for i, ent := range entries {
		groups[i] = vtrackGroup{track: ent.track, vtrack: ent.vtrack, name: userTrackName(ent.name), events: ent.events}
	}
	return groups
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

// groupVR5Songs partitions the enumerated files into songs by header song name
// (§5.4: VR5 associates a file to a song by its 12-byte name).
func groupVR5Songs(files []fileEntry) []songGroup {
	return groupBy(files,
		func(f fileEntry) string { return f.songName },
		func(f fileEntry) songGroup { return songGroup{name: f.songName} })
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

package vsfix

import "encoding/binary"

// VR5Sig is the VS-1880 archive signature (§1/§5.2), 32 bytes.
var VR5Sig = []byte("VS1880EXR06 Song Copy Archives  ")

// VR5 header-block field offsets, from the block start (§5.4, VS-1880 table).
const (
	vr5OffSongName  = 0x241E // 12 B
	vr5OffRate      = 0x242A // rate byte
	vr5OffFormat    = 0x242B // format code
	vr5OffFilename  = 0x2434 // 11 B filename #1
	vr5OffFileIDBE  = 0x2444 // u16 BE
	vr5OffFilename2 = 0x2446 // 11 B filename #2
	vr5OffMagic     = 0x245C // 4 B magic
	vr5OffFileIDLE  = 0x2460 // u16 LE
	vr5OffSize      = 0x2462 // u32 LE
)

// vr5Magic is the §5.4/§5.5 constant that marks a genuine VR5 file header.
var vr5Magic = []byte{0x60, 0xBF, 0x51, 0x28}

// VR5Take is one take audio file: its FileID (the take's position in the
// archive's dense cluster space, which the V-track-table records reference at
// 0x14, §5.7) and its raw MTP stream (native order — CD content is not
// byte-swapped, §5.7).
type VR5Take struct {
	FileID uint16
	Name   string // 8-char base, e.g. "TAKE9CC7"; the "VR5" extension is added
	MTP    []byte
}

// VR5Event is one 64-byte VS-1880 V-track-table record (§7): a placement of a
// take on the timeline. Frames are absolute (VR5 origin is 0, §3). FileID 0 is
// an erase (writes silence).
type VR5Event struct {
	Start, End, Trimmed uint32
	FileID              uint16
}

// VR5VTrack is one populated entry of the 288-entry V-track table (§6.1): the
// 1-based track/v-track it sits at, its stored name (a user name, or "" for the
// synthesized default), and its current-timeline events.
type VR5VTrack struct {
	Track, VTrack int
	Name          string
	Events        []VR5Event
}

// VR5Song is one VS-1880 song: its catalog number and name, the populated
// V-track-table entries, and the take files they reference.
type VR5Song struct {
	Number  uint16
	Name    string
	VTracks []VR5VTrack
	Takes   []VR5Take
}

// VR5Disc describes a single-disc, index-0 VS-1880 archive to synthesize.
type VR5Disc struct {
	SetID    [4]byte
	Songs    []VR5Song
	NoFiller bool // omit the trailing TDI filler run (a truncated-rip fixture, §10)
}

// BuildRaw returns a raw 2352-byte-frame CD dump of the described archive.
func (d VR5Disc) BuildRaw() []byte { return wrapRaw(d.userData()) }

// BuildCooked returns a "cooked" (2048-byte-sector) dump: the raw user-data
// stream with no frame wrapper, as a dd/ISO rip would produce (§5).
func (d VR5Disc) BuildCooked() []byte { return d.userData() }

// userData assembles the concatenated user-data stream (§5.1) of the archive.
func (d VR5Disc) userData() []byte {
	var ud []byte

	// Block 0: archive header (§5.2) with a stale-but-plausible file entry in
	// its per-file area (§5.5 case 1): magic present so it is rejected only by
	// the index-0 and +0x8000-is-signature checks.
	hdr := d.archiveHeaderBlock()
	writeVR5FileFields(hdr, staleVR5Entry(d))
	ud = append(ud, hdr...)

	// Block 0x8000: a second archive-header copy (§5.5 case 2) — signature
	// present but the +0x245C magic left zero, so the magic check rejects it.
	ud = append(ud, d.archiveHeaderBlock()...)

	// Files, contiguous in source order, with a boundary block before every
	// song after the first (§5.4/§5.5 case 3).
	for si, song := range d.Songs {
		if si > 0 {
			ud = append(ud, d.boundaryBlock(song)...)
		}
		for _, f := range song.files() {
			hb := d.archiveHeaderBlock()
			writeVR5FileFields(hb, f.fields(song))
			ud = append(ud, hb...)
			ud = append(ud, padBlocks(f.data)...)
		}
	}

	if !d.NoFiller {
		for i := 0; i < 2; i++ {
			ud = append(ud, fillerBlock()...)
		}
	}
	return ud
}

// vr5File is one on-disc file: its 11-char CD name, FileID, and raw data.
type vr5File struct {
	name   string
	fileID uint16
	data   []byte
}

// fields builds the per-file header fields for a genuine VR5 file header.
func (f vr5File) fields(s VR5Song) vr5Fields {
	return vr5Fields{songName: s.Name, filename: f.name, fileID: f.fileID, size: uint32(len(f.data))}
}

// files returns a song's on-disc files in source-directory order: the SONG
// header, the event list, then the takes (§4.3 fixed names, space-padded to the
// CD 8.3 form).
func (s VR5Song) files() []vr5File {
	fs := []vr5File{
		{name: "SONG    VR5", fileID: 0, data: s.songFile()},
		{name: "EVENTLSTVR5", fileID: 0, data: s.eventList()},
	}
	for _, t := range s.Takes {
		fs = append(fs, vr5File{name: pad8(t.Name) + "VR5", fileID: t.FileID, data: t.MTP})
	}
	return fs
}

// songFile encodes a 38-byte SONG.VR5 header (§4.4): the source folder number
// (= the catalog number) at 0x04, the name at 0x06, and rate+format at 0x12–13.
func (s VR5Song) songFile() []byte {
	b := make([]byte, 38)
	binary.BigEndian.PutUint16(b[0x04:], s.Number)
	copy(b[0x06:0x06+12], pad12(s.Name))
	b[0x12] = 0x01 // rate 44.1 kHz (§3 low nibble)
	b[0x13] = 0x05 // format MTP (§2)
	return b
}

// eventList encodes a VS-1880 CD event list (§6.1): the magic, an empty
// historical registry, exactly 288 positional V-track entries, then a trailing
// "V.T"-shaped optimize remnant (§9) that a positional parser must ignore.
func (s VR5Song) eventList() []byte {
	byPos := map[int]VR5VTrack{}
	for _, vt := range s.VTracks {
		byPos[(vt.Track-1)*16+(vt.VTrack-1)] = vt
	}

	out := make([]byte, 18)
	copy(out, "TAKE EVENT LIST ")
	// registry count at 0x10 left 0: no historical registry to skip.

	for i := 0; i < 288; i++ {
		vt, populated := byPos[i]
		entry := make([]byte, 18)
		if populated && vt.Name != "" {
			copy(entry[:16], pad16(vt.Name))
		} else {
			copy(entry[:16], pad16("V.T")) // default name (§6.1)
		}
		binary.BigEndian.PutUint16(entry[16:], uint16(len(vt.Events)))
		out = append(out, entry...)
		for _, e := range vt.Events {
			out = append(out, e.record(i)...)
		}
	}

	// Optimize remnant past the 288th entry (§9): a stray "V.T"-named block a
	// naive scanner would mistake for a 289th live entry.
	remnant := make([]byte, 18)
	copy(remnant[:16], pad16("V.T remnant"))
	binary.BigEndian.PutUint16(remnant[16:], 1)
	out = append(out, remnant...)
	out = append(out, make([]byte, 64)...) // its bogus record
	return out
}

// record encodes one 64-byte VR5 event record (§7). pos is the table position,
// mirrored into the redundant 0x22 track/v-track field.
func (e VR5Event) record(pos int) []byte {
	r := make([]byte, 64)
	binary.BigEndian.PutUint32(r[0x00:], e.Start)
	binary.BigEndian.PutUint32(r[0x04:], e.End)
	binary.BigEndian.PutUint32(r[0x08:], e.Trimmed)
	binary.BigEndian.PutUint16(r[0x14:], e.FileID) // take start cluster = FileID
	binary.BigEndian.PutUint16(r[0x16:], e.FileID) // end cluster (solo)
	binary.BigEndian.PutUint16(r[0x18:], 1)        // cluster count
	binary.BigEndian.PutUint16(r[0x22:], uint16(pos))
	return r
}

// archiveHeaderBlock builds a 0x8000 block opening with the VR5 signature and
// header fields (§5.2), including the §5.3 song catalog (38-byte SONG.VR5
// copies). Every file header block is a full header copy, so this is the base.
func (d VR5Disc) archiveHeaderBlock() []byte {
	b := make([]byte, blockSize)
	copy(b, VR5Sig)
	copy(b[0x20:0x24], d.SetID[:])
	binary.BigEndian.PutUint16(b[0x24:], uint16(len(d.Songs))) // song count (set-wide)
	binary.BigEndian.PutUint16(b[0x26:], 0)                    // disc index 0
	binary.BigEndian.PutUint16(b[0x28:], 1)                    // total discs
	for k, s := range d.Songs {
		copy(b[0x2A+38*k:], s.songFile()) // catalog entry = SONG.VR5 copy (§5.3)
	}
	return b
}

// vr5Fields are the per-file header fields written into a VR5 header block's
// metadata area (§5.4, VS-1880 table).
type vr5Fields struct {
	songName string
	filename string
	fileID   uint16
	size     uint32
}

// writeVR5FileFields writes the §5.4 VS-1880 per-file fields at their fixed
// offsets, including the two filename copies, both FileID encodings, and the
// magic that check 3 gates on.
func writeVR5FileFields(b []byte, f vr5Fields) {
	copy(b[vr5OffSongName:vr5OffSongName+12], pad12(f.songName))
	b[vr5OffRate] = 0x01   // rate 44.1 kHz
	b[vr5OffFormat] = 0x05 // format MTP
	copy(b[vr5OffFilename:vr5OffFilename+11], []byte(f.filename))
	binary.BigEndian.PutUint16(b[vr5OffFileIDBE:], f.fileID)
	copy(b[vr5OffFilename2:vr5OffFilename2+11], []byte(f.filename))
	copy(b[vr5OffMagic:vr5OffMagic+4], vr5Magic)
	binary.LittleEndian.PutUint16(b[vr5OffFileIDLE:], f.fileID)
	binary.LittleEndian.PutUint32(b[vr5OffSize:], f.size)
}

// staleVR5Entry is the plausible-but-stale per-file entry block 0 carries (§5.5
// case 1): a copy of the set's last file, magic present.
func staleVR5Entry(d VR5Disc) vr5Fields {
	last := d.Songs[len(d.Songs)-1]
	files := last.files()
	return files[len(files)-1].fields(last)
}

// boundaryBlock builds a §5.5 case-3 VR5 song-boundary block: a header-only
// block naming the next song, carrying a stale-but-magic-bearing entry of the
// previous song's last file. It passes checks 1–3 and 5 and is rejected only by
// check 6 (the block at +0x8000 is the next file header, which starts with the
// signature) — the hardest boundary case, exactly what §5.5 warns about.
func (d VR5Disc) boundaryBlock(next VR5Song) []byte {
	b := d.archiveHeaderBlock()
	e := staleVR5Entry(d)
	e.songName = next.Name // boundary blocks carry the *next* song's name (§5.5)
	writeVR5FileFields(b, e)
	return b
}

// pad16 space-pads or truncates s to exactly 16 bytes (VS name-field width).
func pad16(s string) []byte { return []byte(padTo(s, 16)) }

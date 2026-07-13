// Package hddfix builds synthetic Roland VS live-disk (HDD) images in memory,
// byte-for-byte against ROLAND-VS-FORMAT-SPEC.md §4, so the HDD extraction
// pipeline can be tested without the out-of-repo media corpus (ADR-0005). It is
// the HDD analogue of package vsfix (which builds CD archives): a normal package
// (not _test) so several test packages can share it.
//
// A built image is a Roland 12-partition MBR (§4.1) over one or more FAT16
// partitions (§4.2) whose OEM ID is "Roland  ". Each partition holds a
// one-level song-directory tree (§4.3): the FAT16 root lists SONGxxxx.<ext>
// subdirectories, each containing SONG.<ext> (§4.4), EVENTLST.<ext> (§4.5/§4.6)
// and TAKExxxx.<ext> take files. The Roland byte-pair swap quirk is applied
// exactly where §4.2 prescribes: the root directory is written unswapped, while
// subdirectory entries and all file content are 16-bit byte-pair swapped.
package hddfix

import (
	"encoding/binary"
	"time"
)

const (
	sectorSize = 512
	// nMBREntries is the number of Roland MBR partition slots (§4.1): the four
	// standard entries plus the two extended groups a stock 4-entry parser misses.
	nMBREntries = 12
)

// mbrOffsets is the §4.1 partition-entry offset order: standard (446/462/478/
// 494), extended-live (382/398/414/430), extended-unused (286/302/318/334).
var mbrOffsets = [nMBREntries]int{446, 462, 478, 494, 382, 398, 414, 430, 286, 302, 318, 334}

// Take is one take audio file in a song directory.
type Take struct {
	// NameCluster is the take's recording FAT cluster: the file is named
	// TAKE%04X after it and the event records reference it at 0x14 (§4.3/§7).
	// After a Song Copy this is no longer the file's actual FAT start cluster —
	// which is exactly what the builder reproduces: the on-disk directory entry
	// points at freshly-allocated clusters, so NameCluster ≠ start cluster and a
	// reader that chains from NameCluster directly reads the wrong data (§4.3).
	NameCluster uint16
	// Content is the native (un-swapped) codec stream; the builder stores it
	// byte-pair swapped per §4.2.
	Content []byte
	// CorruptToClusters, when > 0, cuts the on-disk FAT chain to this many
	// clusters while the directory entry keeps its full size — a truncated take
	// the §8.3 integrity check must flag rather than emit as silence.
	CorruptToClusters int
}

// Event is one take placement on a v-track's timeline (§7). The builder encodes
// it into the song's EVENTLST in the machine-appropriate form (§4.5 positional
// table for VR5, §4.6 flat log for VR9).
type Event struct {
	Start, End, Trimmed uint32
	NameCluster         uint16 // event 0x14/0x16 take reference (= a Take.NameCluster)
	Count               uint16 // event 0x18 cluster count; 0 ⇒ 1
	Track, VTrack       int    // 1-based physical track / v-track
	Tombstone           bool   // VR9 flag byte / erase (FileID 0 also erases)
	Name                string // 12-char track/event name
}

// Song is one song subdirectory: its catalog number/name, machine extension,
// rate+format bytes (SONG.VRx §4.4), timeline events, and take files.
type Song struct {
	Number  uint16
	Name    string
	Ext     string    // "VR5" or "VR9"
	Rate    byte      // SONG.VRx 0x12 low nibble; 0 ⇒ 1 (44.1 kHz)
	Format  byte      // SONG.VRx 0x13 format code (§2)
	Created time.Time // SONG.VR5 0x14 timestamp (§4.4); VR5 only, zero ⇒ absent
	Saved   time.Time // SONG.VR5 0x1C timestamp (§4.4); VR5 only, zero ⇒ absent
	Events  []Event
	Takes   []Take
	// OmitEventList drops the EVENTLST.<ext> file from the song directory (§4.3),
	// the "no event list; nothing to extract" deviation both List and Extract
	// must report after the SONG header parses.
	OmitEventList bool
	// OmitSong drops the SONG.<ext> file from the directory (§4.4), the "no SONG
	// file; cannot determine format or rate" deviation reported before the header
	// is parsed — while the song's machine extension is still known from the
	// directory entry.
	OmitSong bool
}

// Partition is one FAT16 partition of song directories.
type Partition struct {
	Songs []Song
	// SectorsPerCluster overrides the default of 2 (1024-byte clusters). Real
	// media uses 64 (32 KB); tests use a small value to keep images tiny while
	// still exercising the BPB→codec cluster-size plumbing.
	SectorsPerCluster int
}

// Disk describes a whole Roland HDD image to synthesize.
type Disk struct {
	Partitions []Partition
}

// Build assembles the raw disk image: an MBR (§4.1) followed by each partition's
// FAT16 filesystem, partitions laid contiguously from LBA 1.
func (d Disk) Build() []byte {
	// Build each partition's bytes first so we know their sizes for the MBR.
	var parts [][]byte
	for _, p := range d.Partitions {
		parts = append(parts, p.build())
	}

	// Lay partitions from LBA 1 (sector 0 is the MBR). Record each partition's
	// start LBA and sector count for its MBR entry.
	type placed struct{ startLBA, sectors uint32 }
	var placement []placed
	lba := uint32(1)
	for _, pb := range parts {
		secs := uint32(len(pb) / sectorSize)
		placement = append(placement, placed{startLBA: lba, sectors: secs})
		lba += secs
	}

	mbr := make([]byte, sectorSize)
	for i, pl := range placement {
		if i >= nMBREntries {
			break
		}
		off := mbrOffsets[i]
		mbr[off+4] = 0x06 // partition type: FAT16 (informational, §4.1)
		binary.LittleEndian.PutUint32(mbr[off+8:], pl.startLBA)
		binary.LittleEndian.PutUint32(mbr[off+12:], pl.sectors)
	}
	mbr[0x1FE] = 0x55 // boot signature (§4.1)
	mbr[0x1FF] = 0xAA

	out := mbr
	for _, pb := range parts {
		out = append(out, pb...)
	}
	return out
}

// pbuild assembles one FAT16 partition. Clusters are allocated contiguously from
// cluster 2 in a deterministic order so tests can reason about placement.
type pbuild struct {
	spc          int // sectors per cluster
	clusterBytes int
	// chunks is one allocated cluster run: its starting cluster, the FAT links
	// to write, and the swapped bytes to store.
	chunks   []chunk
	nextClus uint16
}

// chunk is a placed cluster run: firstClus is its FAT start; clusters lists the
// cluster numbers in chain order (some may be dropped from the FAT to simulate a
// truncated chain); data is the already-swapped bytes to write (cluster-padded).
type chunk struct {
	firstClus uint16
	clusters  []uint16
	liveLinks int // how many clusters actually chain (< len ⇒ truncated, §8.3)
	data      []byte
}

// alloc reserves nClusters contiguous clusters and returns their numbers.
func (b *pbuild) alloc(n int) []uint16 {
	cs := make([]uint16, n)
	for i := 0; i < n; i++ {
		cs[i] = b.nextClus
		b.nextClus++
	}
	return cs
}

// store places raw (unswapped) content into a fresh cluster run, swapping it per
// §4.2, and returns the run's first cluster. liveClusters caps how many clusters
// the FAT chain links (for the truncated-take case); 0 means the full run.
func (b *pbuild) store(content []byte, liveClusters int) uint16 {
	n := clusterCount(len(content), b.clusterBytes)
	if n == 0 {
		n = 1
	}
	cs := b.alloc(n)
	padded := make([]byte, n*b.clusterBytes)
	copy(padded, content)
	live := n
	if liveClusters > 0 && liveClusters < n {
		live = liveClusters
	}
	b.chunks = append(b.chunks, chunk{
		firstClus: cs[0],
		clusters:  cs,
		liveLinks: live,
		data:      pairSwap(padded),
	})
	return cs[0]
}

// build renders the partition's bytes: BPB, two FAT copies, root directory, and
// the data region.
func (p Partition) build() []byte {
	spc := p.SectorsPerCluster
	if spc == 0 {
		spc = 2 // 1024-byte clusters by default
	}
	b := &pbuild{spc: spc, clusterBytes: spc * sectorSize, nextClus: 2}

	// Allocate and store every object, in song-then-file order, collecting the
	// root-directory entries (one per song subdirectory).
	var rootEntries []dirEntry
	for i, s := range p.Songs {
		subFirst := b.buildSong(s)
		rootEntries = append(rootEntries, dirEntry{
			name8:     songDirBase(i), // "SONG0000", "SONG0001", ... — creation order
			ext3:      s.Ext,
			attr:      attrSubdir,
			firstClus: subFirst,
			size:      0,
		})
	}

	nClusters := int(b.nextClus) - 2
	if nClusters < 1 {
		nClusters = 1
	}

	// Geometry (§4.2). reserved=1, numFATs=2, rootEntCount=16.
	const reserved, numFATs, rootEntCount = 1, 2, 16
	sectorsPerFAT := ceilDiv((nClusters+2)*2, sectorSize)
	rootDirSectors := ceilDiv(rootEntCount*32, sectorSize)
	dataStartSec := reserved + numFATs*sectorsPerFAT + rootDirSectors
	totalSectors := dataStartSec + nClusters*spc

	img := make([]byte, totalSectors*sectorSize)

	// BPB / boot sector (§4.2).
	bpb := img[:sectorSize]
	copy(bpb[0x03:0x0B], []byte("Roland  ")) // OEM ID (§4.1)
	binary.LittleEndian.PutUint16(bpb[0x0B:], sectorSize)
	bpb[0x0D] = byte(spc)
	binary.LittleEndian.PutUint16(bpb[0x0E:], reserved)
	bpb[0x10] = numFATs
	binary.LittleEndian.PutUint16(bpb[0x11:], rootEntCount)
	binary.LittleEndian.PutUint16(bpb[0x16:], uint16(sectorsPerFAT))
	bpb[0x1FE] = 0x55
	bpb[0x1FF] = 0xAA

	// FAT: build the entry table, then copy it into both FAT regions.
	fat := make([]uint16, nClusters+2)
	writeCluster := func(clus uint16, data []byte) {
		sec := dataStartSec + (int(clus)-2)*spc
		copy(img[sec*sectorSize:], data)
	}
	for _, ch := range b.chunks {
		for i := 0; i < len(ch.clusters); i++ {
			c := ch.clusters[i]
			switch {
			case i >= ch.liveLinks:
				// Truncated chain (§8.3): links past liveLinks are left free, so
				// the chain ends short of the file's declared size.
				fat[c] = 0x0000
			case i == ch.liveLinks-1:
				fat[c] = 0xFFFF // end of chain
			default:
				fat[c] = ch.clusters[i+1]
			}
		}
		writeCluster(ch.firstClus, ch.data)
	}

	fatBytes := make([]byte, sectorsPerFAT*sectorSize)
	for c, v := range fat {
		binary.LittleEndian.PutUint16(fatBytes[c*2:], v)
	}
	copy(img[reserved*sectorSize:], fatBytes)
	copy(img[(reserved+sectorsPerFAT)*sectorSize:], fatBytes)

	// Root directory (§4.2): unswapped array of 32-byte entries.
	rootSec := reserved + numFATs*sectorsPerFAT
	root := img[rootSec*sectorSize : (rootSec+rootDirSectors)*sectorSize]
	for i, e := range rootEntries {
		copy(root[i*32:], e.bytes())
	}

	return img
}

// buildSong stores a song's files and its subdirectory, returning the
// subdirectory's first cluster. The subdirectory entries and file content are
// byte-pair swapped (§4.2); only the root directory (built by the caller) is not.
func (b *pbuild) buildSong(s Song) uint16 {
	// Reserve the subdirectory's own cluster up front so its "." entry can point
	// at it; a one-cluster subdirectory holds ample room for these few entries.
	subClus := b.alloc(1)[0]

	var entries []dirEntry
	// "." and ".." (attr subdirectory ⇒ the reader skips them as non-files).
	entries = append(entries,
		dirEntry{name8: ".", ext3: "", attr: attrSubdir, firstClus: subClus},
		dirEntry{name8: "..", ext3: "", attr: attrSubdir, firstClus: 0},
	)

	// SONG.<ext> and EVENTLST.<ext> (§4.3 fixed names).
	if !s.OmitSong {
		songFirst := b.store(s.songFile(), 0)
		entries = append(entries, dirEntry{name8: "SONG", ext3: s.Ext, firstClus: songFirst, size: len(s.songFile())})
	}

	if !s.OmitEventList {
		el := s.eventList()
		elFirst := b.store(el, 0)
		entries = append(entries, dirEntry{name8: "EVENTLST", ext3: s.Ext, firstClus: elFirst, size: len(el)})
	}

	// Take files, named TAKE%04X after their recording cluster (§4.3).
	for _, t := range s.Takes {
		first := b.store(t.Content, t.CorruptToClusters)
		size := len(t.Content)
		if t.CorruptToClusters > 0 {
			// The directory entry keeps the take's full declared size even
			// though the on-disk chain is short — the §8.3 mismatch.
			size = clusterCount(len(t.Content), b.clusterBytes) * b.clusterBytes
		}
		entries = append(entries, dirEntry{
			name8:     takeName(t.NameCluster),
			ext3:      s.Ext,
			firstClus: first,
			size:      size,
		})
	}

	// Write the subdirectory: its 32-byte entries, byte-pair swapped.
	sub := make([]byte, b.clusterBytes)
	for i, e := range entries {
		copy(sub[i*32:], e.bytes())
	}
	b.chunks = append(b.chunks, chunk{
		firstClus: subClus,
		clusters:  []uint16{subClus},
		liveLinks: 1,
		data:      pairSwap(sub),
	})
	return subClus
}

const (
	attrFile   = 0x00
	attrSubdir = 0x10
)

// dirEntry is a 32-byte FAT directory entry (§4.2).
type dirEntry struct {
	name8     string
	ext3      string
	attr      byte
	firstClus uint16
	size      int
}

// bytes renders the 32-byte on-disk directory entry (unswapped; callers swap it
// when it lives in a subdirectory).
func (e dirEntry) bytes() []byte {
	b := make([]byte, 32)
	copy(b[0x00:0x08], padName(e.name8, 8))
	copy(b[0x08:0x0B], padName(e.ext3, 3))
	b[0x0B] = e.attr
	binary.LittleEndian.PutUint16(b[0x1A:], e.firstClus)
	binary.LittleEndian.PutUint32(b[0x1C:], uint32(e.size))
	return b
}

// songFile encodes SONG.VRx (§4.4): source folder number at 0x04, name at 0x06,
// rate at 0x12, format at 0x13. VR9 is 20 bytes; VR5 is 38 and additionally
// carries the created/last-saved timestamps at 0x14/0x1C.
func (s Song) songFile() []byte {
	n := 20
	if s.Ext == "VR5" {
		n = 38
	}
	b := make([]byte, n)
	binary.BigEndian.PutUint16(b[0x04:], s.Number)
	copy(b[0x06:0x06+12], padName(s.Name, 12))
	b[0x12] = s.rateByte()
	b[0x13] = s.Format
	if s.Ext == "VR5" {
		copy(b[0x14:0x1C], encodeStamp(s.Created))
		copy(b[0x1C:0x24], encodeStamp(s.Saved))
	}
	return b
}

// encodeStamp renders a time as the 8-byte §4.4 Roland timestamp
// [ss,mm,hh,dow,dd,MM,yyyy(u16 BE)]. A zero time renders as all-zero bytes (the
// "absent" encoding); the day-of-week byte uses Roland's 1 = Saturday … 7 =
// Friday map, though the reader ignores it.
func encodeStamp(t time.Time) []byte {
	b := make([]byte, 8)
	if t.IsZero() {
		return b
	}
	b[0] = byte(t.Second())
	b[1] = byte(t.Minute())
	b[2] = byte(t.Hour())
	b[3] = byte((int(t.Weekday())+1)%7 + 1)
	b[4] = byte(t.Day())
	b[5] = byte(t.Month())
	binary.BigEndian.PutUint16(b[6:], uint16(t.Year()))
	return b
}

func (s Song) rateByte() byte {
	if s.Rate == 0 {
		return 0x01 // 44.1 kHz default (§3)
	}
	return s.Rate
}

// songDirBase renders the i'th song subdirectory's 8-char base name (0-based,
// creation order within the partition): "SONG0000", "SONG0001", and so on. Real
// media names a folder by its own FAT creation slot, independent of the SONG.VRx
// catalog number the song carries as metadata — a Song Copy duplicates a song
// into a new destination folder while its SONG.VRx keeps recording the *source*
// number (§4.4), so two distinct folders (even across partitions) can legitimately
// share the same stored catalog number. Any distinct SONGxxxx base parses
// identically; the extension is what selects the machine.
func songDirBase(i int) string {
	return "SONG" + hex4(uint16(i))
}

// eventList encodes the song's EVENTLST.<ext>: the VR5 positional V-track table
// (§4.5) or the VR9 flat event log (§4.6). Both are byte-identical to their CD
// forms (§6), so the CD parsers read them unchanged.
func (s Song) eventList() []byte {
	if s.Ext == "VR5" {
		return s.eventListVR5()
	}
	return s.eventListVR9()
}

// eventListVR9 encodes a VS-880EX flat log (§4.6): u16 BE live count then N
// 48-byte records.
func (s Song) eventListVR9() []byte {
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, uint16(len(s.Events)))
	for _, e := range s.Events {
		out = append(out, e.recordVR9()...)
	}
	return out
}

// eventListVR5 encodes a VS-1880 event list (§4.5): "TAKE EVENT LIST " magic, an
// empty edit-list registry, then 288 positional entries (18 tracks × 16
// v-tracks), each a 16-byte name, u16 BE count, and its 64-byte records.
func (s Song) eventListVR5() []byte {
	byPos := map[int][]Event{}
	for _, e := range s.Events {
		pos := (e.Track-1)*16 + (e.VTrack - 1)
		byPos[pos] = append(byPos[pos], e)
	}
	out := make([]byte, 18)
	copy(out, "TAKE EVENT LIST ")
	// registry count at 0x10 = 0: no edit list to skip.
	for i := 0; i < 288; i++ {
		evs := byPos[i]
		entry := make([]byte, 18)
		copy(entry[:16], padName("V.T", 16)) // default name (§6.1)
		binary.BigEndian.PutUint16(entry[16:], uint16(len(evs)))
		out = append(out, entry...)
		for _, e := range evs {
			out = append(out, e.recordVR5(i)...)
		}
	}
	return out
}

// recordVR9 encodes a 48-byte VS-880EX event record (§7).
func (e Event) recordVR9() []byte {
	r := make([]byte, 48)
	e.writeCommon(r)
	r[0x20] = byte((e.Track-1)*8 + (e.VTrack - 1)) // v-track code
	if e.Tombstone {
		r[0x21] = 1
	}
	copy(r[0x22:0x22+12], padName(e.Name, 12))
	return r
}

// recordVR5 encodes a 64-byte VS-1880 event record (§7). pos mirrors the table
// position into the redundant 0x22 field.
func (e Event) recordVR5(pos int) []byte {
	r := make([]byte, 64)
	e.writeCommon(r)
	binary.BigEndian.PutUint16(r[0x22:], uint16(pos))
	return r
}

// writeCommon writes the fields both records share (§7): the frame range, the
// take reference at 0x14/0x16, and the cluster count at 0x18.
func (e Event) writeCommon(r []byte) {
	binary.BigEndian.PutUint32(r[0x00:], e.Start)
	binary.BigEndian.PutUint32(r[0x04:], e.End)
	binary.BigEndian.PutUint32(r[0x08:], e.Trimmed)
	binary.BigEndian.PutUint16(r[0x14:], e.NameCluster)
	count := e.Count
	if count == 0 {
		count = 1
	}
	binary.BigEndian.PutUint16(r[0x16:], e.NameCluster+count-1)
	binary.BigEndian.PutUint16(r[0x18:], count)
}

// ---- byte helpers ----

// pairSwap returns b with every adjacent byte pair swapped (the §4.2 Roland
// 16-bit quirk). It is an involution, so the reader applies the same transform
// to undo it. b must be even-length (cluster-aligned).
func pairSwap(b []byte) []byte {
	out := make([]byte, len(b))
	for i := 0; i+1 < len(b); i += 2 {
		out[i] = b[i+1]
		out[i+1] = b[i]
	}
	if len(b)%2 == 1 {
		out[len(b)-1] = b[len(b)-1]
	}
	return out
}

// takeName renders a take's TAKExxxx base from its recording cluster (§4.3).
func takeName(cluster uint16) string { return "TAKE" + hex4(cluster) }

// hex4 formats v as four uppercase hex digits.
func hex4(v uint16) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{
		digits[(v>>12)&0xF], digits[(v>>8)&0xF], digits[(v>>4)&0xF], digits[v&0xF],
	})
}

// padName space-pads or truncates s to exactly n bytes (FAT names are
// space-padded, uppercase).
func padName(s string, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	copy(b, s)
	if len(s) > n {
		copy(b, s[:n])
	}
	return b
}

func clusterCount(nBytes, clusterBytes int) int { return ceilDiv(nBytes, clusterBytes) }

func ceilDiv(a, b int) int { return (a + b - 1) / b }

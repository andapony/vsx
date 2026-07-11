// Package hdd turns a raw Roland VS live-disk image into the song directories
// its audio lives in (ROLAND-VS-FORMAT-SPEC.md §4). It hides the Roland
// 12-partition MBR (§4.1), the FAT16 on-disk structures (§4.2), and the Roland
// 16-bit byte-pair swap quirk behind a directory/file view: callers list Songs,
// list a song's Files, and Read a file's already-unswapped content.
//
// It is machine-agnostic: identifying a song's recorder (VR5 vs VR9) is the
// caller's job, keyed on the song directory's extension (§4.3). It is the HDD
// analogue of package cd, which does the same for CD dumps.
package hdd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrNotRoland reports that an image is not a Roland VS live disk — no partition
// whose BPB carries the "Roland  " OEM ID (§4.1). Callers try the CD path
// instead of failing.
var ErrNotRoland = errors.New("hdd: not a Roland VS live disk")

const sectorSize = 512

// rolandOEM is the §4.1 BPB OEM-ID signature that marks a Roland partition.
var rolandOEM = []byte("Roland  ")

// mbrOffsets are the 12 Roland partition-entry offsets (§4.1): the four standard
// entries, then the two extended groups. The extended 382–430 group holds live
// partitions a stock 4-entry parser silently misses.
var mbrOffsets = []int{446, 462, 478, 494, 382, 398, 414, 430, 286, 302, 318, 334}

// Volume is a Roland VS HDD image: one or more Roland FAT16 partitions of song
// directories, addressed through Songs.
type Volume struct {
	parts []*partition
}

// Open probes the image's MBR and partition BPBs and returns a Volume when at
// least one partition validates as a Roland FAT16 filesystem (§4.1). It reads
// every one of the 12 Roland partition offsets and keeps the entries whose BPB
// validates (OEM ID "Roland  ", 512-byte sectors, a power-of-two cluster size),
// keeping only the first entry per distinct start LBA — the §4.1 detection, in
// its "probe all 12" equivalent form. An image with no such partition is not a
// Roland disk: ErrNotRoland, so the caller can try CD.
func Open(src io.ReaderAt, size int64) (*Volume, error) {
	mbr := make([]byte, sectorSize)
	if _, err := src.ReadAt(mbr, 0); err != nil {
		return nil, fmt.Errorf("hdd: reading MBR: %w", err)
	}

	var parts []*partition
	seen := map[uint32]bool{}
	for _, off := range mbrOffsets {
		startLBA := binary.LittleEndian.Uint32(mbr[off+8:])
		sectors := binary.LittleEndian.Uint32(mbr[off+12:])
		if startLBA == 0 || sectors == 0 || seen[startLBA] {
			continue
		}
		p, ok := openPartition(src, size, startLBA)
		if !ok {
			continue
		}
		seen[startLBA] = true
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return nil, ErrNotRoland
	}
	return &Volume{parts: parts}, nil
}

// partition is one Roland FAT16 filesystem: its geometry (§4.2) plus a cached
// copy of the FAT, all addressed relative to the partition's start byte.
type partition struct {
	src          io.ReaderAt
	startByte    int64 // partition start LBA × 512
	clusterBytes int

	fat              []byte // first FAT copy (u16 LE entries)
	rootDirStartByte int64
	rootEntCount     int
	dataStartSec     int // data region's first sector, relative to partition start
	spc              int // sectors per cluster
	maxCluster       int // highest addressable cluster number
}

// openPartition validates the BPB at startLBA and, on success, returns a ready
// partition. ok is false for a non-Roland or malformed BPB, or one that runs
// past the image — all "not this partition", never a hard error.
func openPartition(src io.ReaderAt, size int64, startLBA uint32) (*partition, bool) {
	startByte := int64(startLBA) * sectorSize
	if startByte+sectorSize > size {
		return nil, false
	}
	bpb := make([]byte, sectorSize)
	if _, err := src.ReadAt(bpb, startByte); err != nil {
		return nil, false
	}
	if string(bpb[0x03:0x0B]) != string(rolandOEM) {
		return nil, false
	}
	bytesPerSector := int(binary.LittleEndian.Uint16(bpb[0x0B:]))
	spc := int(bpb[0x0D])
	if bytesPerSector != sectorSize || spc == 0 || spc&(spc-1) != 0 {
		return nil, false // §4.1: 512 B/sector, cluster size a nonzero power of two
	}
	reserved := int(binary.LittleEndian.Uint16(bpb[0x0E:]))
	numFATs := int(bpb[0x10])
	rootEntCount := int(binary.LittleEndian.Uint16(bpb[0x11:]))
	sectorsPerFAT := int(binary.LittleEndian.Uint16(bpb[0x16:]))
	if reserved == 0 || numFATs == 0 || sectorsPerFAT == 0 {
		return nil, false
	}

	// Derived layout (§4.2), all in sectors relative to the partition start.
	fatStartSec := reserved
	rootDirStartSec := reserved + numFATs*sectorsPerFAT
	rootDirSectors := ceilDiv(rootEntCount*32, sectorSize)
	dataStartSec := rootDirStartSec + rootDirSectors

	fat := make([]byte, sectorsPerFAT*sectorSize)
	if _, err := src.ReadAt(fat, startByte+int64(fatStartSec)*sectorSize); err != nil {
		return nil, false
	}

	return &partition{
		src:              src,
		startByte:        startByte,
		clusterBytes:     spc * sectorSize,
		fat:              fat,
		rootDirStartByte: startByte + int64(rootDirStartSec)*sectorSize,
		rootEntCount:     rootEntCount,
		dataStartSec:     dataStartSec,
		spc:              spc,
		maxCluster:       len(fat)/2 - 1,
	}, true
}

// Song is one song subdirectory in a partition root (§4.3): SONGxxxx.<ext>.
type Song struct {
	Name         string // 8.3 base, e.g. "SONG0001"
	Ext          string // machine extension, "VR5" / "VR9"
	Partition    int    // 1-based ordinal of the partition this song lives in
	Index        int    // 0-based position of this song within its partition
	part         *partition
	firstCluster uint16
}

// ClusterSize is the song's partition cluster size in bytes (§4.2), needed to
// page-pad the MT2 decoder (§2).
func (s Song) ClusterSize() int { return s.part.clusterBytes }

// Songs lists every song subdirectory across all partitions, in
// partition-then-root-order. The FAT16 root directory is read unswapped (§4.2);
// entries that are not machine-extension subdirectories are skipped. Index is
// stamped as the 0-based position within each partition (resetting per
// partition): one FAT partition can hold two directories that share a
// SONGxxxx base but differ in machine extension (e.g. SONG0000.VR9 and
// SONG0000.VR5), so the base name alone is not a unique ordinal — the
// enumeration position is, by construction.
func (v *Volume) Songs() ([]Song, error) {
	var out []Song
	for pi, p := range v.parts {
		root := make([]byte, p.rootEntCount*32)
		if _, err := p.src.ReadAt(root, p.rootDirStartByte); err != nil {
			return nil, fmt.Errorf("hdd: reading root directory: %w", err)
		}
		idx := 0
		for _, e := range parseDir(root) {
			if !e.isSubdir() || !isMachineExt(e.ext) {
				continue
			}
			out = append(out, Song{Name: e.name, Ext: e.ext, Partition: pi + 1, Index: idx, part: p, firstCluster: e.firstCluster})
			idx++
		}
	}
	return out, nil
}

// Entry is a file inside a song directory (§4.3): a SONG/EVENTLST/TAKE file.
type Entry struct {
	Name         string // 8.3 base, e.g. "TAKE193C"
	Ext          string // "VR5" / "VR9"
	Size         int
	part         *partition
	firstCluster uint16
}

// ClusterSize is the entry's partition cluster size in bytes (§4.2).
func (e Entry) ClusterSize() int { return e.part.clusterBytes }

// Files lists the file entries of a song directory. The subdirectory's entries
// are byte-pair unswapped before parsing (§4.2); "." / ".." and any nested
// subdirectory, volume-label, or long-file-name entries are skipped.
func (s Song) Files() ([]Entry, error) {
	raw, _, err := s.part.readChain(s.firstCluster)
	if err != nil {
		return nil, fmt.Errorf("hdd: reading song directory %s: %w", s.Name, err)
	}
	pairSwap(raw) // §4.2: subdirectory entries are byte-pair swapped

	var out []Entry
	for _, e := range parseDir(raw) {
		if e.isSubdir() || e.isVolumeOrLFN() {
			continue
		}
		out = append(out, Entry{
			Name: e.name, Ext: e.ext, Size: int(e.size),
			part: s.part, firstCluster: e.firstCluster,
		})
	}
	return out, nil
}

// Read returns a file's content: its FAT chain followed from the directory
// entry's first cluster (§4.3 — never from the event's cluster value), byte-pair
// unswapped (§4.2), and truncated to the entry size. The second result is the
// number of clusters the chain actually yielded — for the §8.3 integrity check,
// which compares it against the event record's 0x18 cluster count. A chain that
// hits a free/bad link before the file's size implies a truncated take.
func (e Entry) Read() ([]byte, int, error) {
	raw, clusters, err := e.part.readChain(e.firstCluster)
	if err != nil {
		return nil, 0, fmt.Errorf("hdd: reading file %s: %w", e.Name, err)
	}
	pairSwap(raw) // §4.2: file content is byte-pair swapped
	if e.Size < len(raw) {
		raw = raw[:e.Size]
	}
	return raw, clusters, nil
}

// readChain follows the FAT chain from firstClus, reading and concatenating each
// cluster's bytes, and returns the raw (still-swapped) bytes plus the number of
// clusters read. It stops at end-of-chain (≥ 0xFFF8) and at a free (0x0000) or
// bad (0xFFF7) link — the truncated-chain signal (§8.3) — and is loop-guarded.
func (p *partition) readChain(firstClus uint16) ([]byte, int, error) {
	var out []byte
	n := 0
	for clus := firstClus; ; {
		if int(clus) < 2 || int(clus) > p.maxCluster {
			break
		}
		buf := make([]byte, p.clusterBytes)
		off := p.startByte + int64(p.dataStartSec+(int(clus)-2)*p.spc)*sectorSize
		if _, err := p.src.ReadAt(buf, off); err != nil {
			return nil, 0, err
		}
		out = append(out, buf...)
		n++
		if n > p.maxCluster {
			break // corrupt self-referential chain: loop guard
		}
		next := binary.LittleEndian.Uint16(p.fat[int(clus)*2:])
		if next == 0x0000 || next == 0xFFF7 || next >= 0xFFF8 {
			break
		}
		clus = next
	}
	return out, n, nil
}

// rawEntry is a parsed 32-byte FAT directory entry (§4.2).
type rawEntry struct {
	name         string
	ext          string
	attr         byte
	firstCluster uint16
	size         uint32
}

func (e rawEntry) isSubdir() bool      { return e.attr&0x10 != 0 }
func (e rawEntry) isVolumeOrLFN() bool { return e.attr&0x08 != 0 || e.attr == 0x0F }

// parseDir parses a directory's 32-byte entries, stopping at the first
// end-of-directory marker (name byte 0x00) and skipping deleted entries (0xE5).
func parseDir(b []byte) []rawEntry {
	var out []rawEntry
	for off := 0; off+32 <= len(b); off += 32 {
		e := b[off : off+32]
		switch e[0] {
		case 0x00:
			return out // end of directory
		case 0xE5:
			continue // deleted
		}
		out = append(out, rawEntry{
			name:         trimName(e[0:8]),
			ext:          trimName(e[8:11]),
			attr:         e[0x0B],
			firstCluster: binary.LittleEndian.Uint16(e[0x1A:]),
			size:         binary.LittleEndian.Uint32(e[0x1C:]),
		})
	}
	return out
}

// isMachineExt reports whether a directory extension names a supported recorder
// (§4.3). VR6/VS-1680 and later live behind the machine seam (ADR-0003).
func isMachineExt(ext string) bool { return ext == "VR5" || ext == "VR9" }

// pairSwap swaps every adjacent byte pair in place — the §4.2 Roland 16-bit
// quirk. It is an involution, so it both applies and undoes the swap.
func pairSwap(b []byte) {
	for i := 0; i+1 < len(b); i += 2 {
		b[i], b[i+1] = b[i+1], b[i]
	}
}

// trimName renders a space-padded FAT name field as a string, dropping trailing
// spaces and NULs.
func trimName(b []byte) string {
	end := len(b)
	for end > 0 && (b[end-1] == ' ' || b[end-1] == 0x00) {
		end--
	}
	return string(b[:end])
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

package core

import (
	"fmt"
	"strconv"
	"strings"
)

// SongKey is a song's stable, collision-proof identity within one Source.
// Partition is the 1-based Roland partition ordinal on HDD (0 on CD, which has a
// single archive); Ordinal is the SONGxxxx directory ordinal on HDD (unique per
// partition by FAT construction) or the archive catalog number on CD.
type SongKey struct {
	Partition int
	Ordinal   int
}

// String is the friendly selector form shown by --list and accepted by --song:
// "2.7" on HDD, "7" on CD.
func (k SongKey) String() string {
	if k.Partition == 0 {
		return strconv.Itoa(k.Ordinal)
	}
	return fmt.Sprintf("%d.%d", k.Partition, k.Ordinal)
}

// FolderName is the zero-padded on-disk output-folder stem, so folders sort
// correctly in a file browser: "02.007" on HDD, "07" on CD (the historical form,
// so CD output is unchanged).
func (k SongKey) FolderName() string {
	if k.Partition == 0 {
		return fmt.Sprintf("%02d", k.Ordinal)
	}
	return fmt.Sprintf("%02d.%03d", k.Partition, k.Ordinal)
}

// ParseSongKey parses a --song value in either the friendly ("2.7", "7") or
// padded ("02.007") form.
func ParseSongKey(s string) (SongKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return SongKey{}, fmt.Errorf("empty song key")
	}
	if !strings.Contains(s, ".") {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return SongKey{}, fmt.Errorf("invalid song key %q", s)
		}
		return SongKey{Partition: 0, Ordinal: n}, nil
	}
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return SongKey{}, fmt.Errorf("invalid song key %q", s)
	}
	p, err1 := strconv.Atoi(parts[0])
	o, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || p < 1 || o < 0 {
		return SongKey{}, fmt.Errorf("invalid song key %q", s)
	}
	return SongKey{Partition: p, Ordinal: o}, nil
}

// hddSongKey is a song's key on an HDD Source: its partition ordinal and its
// 0-based enumeration position within that partition (unique by construction).
func hddSongKey(partition, index int) SongKey { return SongKey{Partition: partition, Ordinal: index} }

// cdSongKey is a song's key on a CD Source: partition 0 and the archive catalog
// number (unique within the one archive).
func cdSongKey(number int) SongKey { return SongKey{Partition: 0, Ordinal: number} }

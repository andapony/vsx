package hdd

import (
	"bytes"
	"testing"

	"github.com/andapony/vsx/internal/hddfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// volumeOf builds a synthetic disk and opens it as a Volume.
func volumeOf(t *testing.T, d hddfix.Disk) *Volume {
	t.Helper()
	raw := d.Build()
	v, err := Open(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	return v
}

// findFile returns the named entry (by base) from a slice.
func findFile(t *testing.T, files []Entry, base string) Entry {
	t.Helper()
	for _, f := range files {
		if f.Name == base {
			return f
		}
	}
	t.Fatalf("no file %q among %d entries", base, len(files))
	return Entry{}
}

// TestOpenEnumeratesSongsAndReadsAFile is the first end-to-end invariant of the
// reader: a Roland-OEM FAT16 partition is detected, its one song directory is
// listed with the right name/extension, and a take file's content round-trips
// through the FAT layer and the §4.2 byte-pair unswap back to the native stream
// the fixture stored.
func TestOpenEnumeratesSongsAndReadsAFile(t *testing.T) {
	content := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{
		Songs: []hddfix.Song{{
			Number: 1, Name: "SONG ONE", Ext: "VR9", Format: 1,
			Takes: []hddfix.Take{{NameCluster: 0x0100, Content: content}},
		}},
	}}}
	v := volumeOf(t, disk)

	songs, err := v.Songs()
	require.NoError(t, err)
	require.Len(t, songs, 1)
	assert.Equal(t, "VR9", songs[0].Ext)

	files, err := songs[0].Files()
	require.NoError(t, err)

	take := findFile(t, files, "TAKE0100")
	assert.Equal(t, "VR9", take.Ext)
	assert.Equal(t, len(content), take.Size)

	got, clusters, err := take.Read()
	require.NoError(t, err)
	assert.Equal(t, content, got, "take content round-trips through FAT + unswap")
	assert.Equal(t, 1, clusters, "the take occupies one cluster")
}

// TestSongsHavePartitionOrdinal locks the invariant that each song is stamped
// with the 1-based ordinal of the partition it was enumerated from (MBR-offset
// order), so a later SongKey can combine it with the SONGxxxx folder ordinal to
// stay unique across a multi-partition HDD.
func TestSongsHavePartitionOrdinal(t *testing.T) {
	// Two Roland partitions, each with one song directory (each becomes SONG0000).
	onePart := func(name string) hddfix.Partition {
		return hddfix.Partition{Songs: []hddfix.Song{{
			Number: 1, Name: name, Ext: "VR5",
			Takes:  []hddfix.Take{{NameCluster: 0x0100, Content: make([]byte, 16*4)}},
			Events: []hddfix.Event{{Start: 0, End: 4, NameCluster: 0x0100, Track: 1, VTrack: 1}},
		}}}
	}
	v := volumeOf(t, hddfix.Disk{Partitions: []hddfix.Partition{onePart("A"), onePart("B")}})
	songs, err := v.Songs()
	require.NoError(t, err)
	require.Len(t, songs, 2)
	// Partition ordinal is 1-based and distinguishes the two partitions.
	assert.Equal(t, 1, songs[0].Partition)
	assert.Equal(t, 2, songs[1].Partition)
}

// TestOpenReadsExtendedMBROffsets locks the §4.1 invariant that a Roland disk's
// four extended-group partitions (offsets 382–430) are read, not just the four
// standard entries. A five-partition disk fills all four standard slots and one
// extended slot; a stock 4-entry parser would miss the fifth partition's song
// entirely.
func TestOpenReadsExtendedMBROffsets(t *testing.T) {
	var partsFix []hddfix.Partition
	for i := 0; i < 5; i++ {
		partsFix = append(partsFix, hddfix.Partition{Songs: []hddfix.Song{{
			Number: uint16(i + 1), Name: "S", Ext: "VR9", Format: 1,
			Takes: []hddfix.Take{{NameCluster: 0x0100, Content: []byte{0x01, 0x02}}},
		}}})
	}
	v := volumeOf(t, hddfix.Disk{Partitions: partsFix})

	songs, err := v.Songs()
	require.NoError(t, err)
	assert.Len(t, songs, 5, "all five partitions are found, including the extended-offset one")
}

// TestReadTruncatedChainReportsShortClusterCount locks the §8.3 signal: a take
// whose on-disk FAT chain is shorter than its declared size yields fewer
// clusters than a full file, which the core integrity check compares against the
// event's 0x18 count. Read returns the short cluster count and the partial bytes
// rather than pretending the file is whole.
func TestReadTruncatedChainReportsShortClusterCount(t *testing.T) {
	// Two clusters' worth of content, but the chain is cut to one cluster.
	content := make([]byte, 2*1024)
	for i := range content {
		content[i] = byte(i)
	}
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{
		Songs: []hddfix.Song{{
			Number: 1, Name: "S", Ext: "VR9", Format: 1,
			Takes: []hddfix.Take{{NameCluster: 0x0100, Content: content, CorruptToClusters: 1}},
		}},
	}}}
	v := volumeOf(t, disk)
	songs, err := v.Songs()
	require.NoError(t, err)
	files, err := songs[0].Files()
	require.NoError(t, err)

	take := findFile(t, files, "TAKE0100")
	assert.Equal(t, 2*1024, take.Size, "the directory entry still claims the full size")

	got, clusters, err := take.Read()
	require.NoError(t, err)
	assert.Equal(t, 1, clusters, "the chain yields only one of the two claimed clusters")
	assert.Len(t, got, 1024, "only the surviving cluster's bytes are returned")
	assert.Equal(t, content[:1024], got)
}

// TestReadMultiClusterFileUnswaps verifies per-cluster byte-pair unswap (§4.2)
// composes correctly across a chain: a two-cluster take round-trips to the exact
// native bytes the fixture stored.
func TestReadMultiClusterFileUnswaps(t *testing.T) {
	content := make([]byte, 3*1024) // three clusters
	for i := range content {
		content[i] = byte((i*7 + 3) & 0xFF)
	}
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{
		Songs: []hddfix.Song{{
			Number: 1, Name: "S", Ext: "VR9", Format: 1,
			Takes: []hddfix.Take{{NameCluster: 0x0100, Content: content}},
		}},
	}}}
	v := volumeOf(t, disk)
	songs, _ := v.Songs()
	files, _ := songs[0].Files()
	got, clusters, err := findFile(t, files, "TAKE0100").Read()
	require.NoError(t, err)
	assert.Equal(t, 3, clusters)
	assert.Equal(t, content, got)
}

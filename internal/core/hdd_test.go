package core

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/andapony/vsx/internal/hddfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeDisk builds a synthetic HDD image and writes it to a temp file, returning
// the path — the input contract Extract takes (one path in).
func writeDisk(t *testing.T, d hddfix.Disk) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "disk.img")
	require.NoError(t, os.WriteFile(path, d.Build(), 0o644))
	return path
}

// m16 encodes samples as uncompressed little-endian 16-bit PCM (format M16, §2),
// so a take's decoded output is exactly the samples put in — the cleanest way to
// assert audio end-to-end without a codec round-trip.
func m16(samples ...int32) []byte {
	b := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(int16(s)))
	}
	return b
}

// frame16 is one 16-sample frame of distinct values, so a mis-decode or a
// wrong-take resolution is visible.
func frame16() []int32 {
	s := make([]int32, 16)
	for i := range s {
		s[i] = int32(1000 + i)
	}
	return s
}

// TestExtractHDDVR9EndToEnd drives a synthetic single-partition VS-880EX live
// disk through the whole HDD pipeline — MBR/BPB detection, FAT16 song-directory
// walk, SONG/EVENTLST parse, take resolution, flat-log replay — and verifies the
// v-track emerges with the right identity, audio, rate, and (the §4.2 plumbing)
// the partition's BPB cluster size.
func TestExtractHDDVR9EndToEnd(t *testing.T) {
	want := frame16()
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{
		SectorsPerCluster: 2, // 1024-byte clusters
		Songs: []hddfix.Song{{
			Number: 7, Name: "LIVE VR9", Ext: "VR9", Format: byte(FormatM16),
			Takes:  []hddfix.Take{{NameCluster: 0x0100, Content: m16(want...)}},
			Events: []hddfix.Event{{Start: 12, End: 13, NameCluster: 0x0100, Track: 2, VTrack: 3}},
		}},
	}}}
	r, err := Extract(writeDisk(t, disk), Options{})
	require.NoError(t, err)
	tracks, devs := collectTracks(t, r)

	require.Len(t, tracks, 1)
	assert.Equal(t, 7, tracks[0].Song.Number)
	assert.Equal(t, "LIVE VR9", tracks[0].Song.Name)
	assert.Equal(t, 2, tracks[0].Track)
	assert.Equal(t, 3, tracks[0].VTrack)
	assert.EqualValues(t, 44100, tracks[0].Take.SampleRate)
	assert.Equal(t, 1024, tracks[0].Take.ClusterSize, "the BPB cluster size reaches the decode metadata (§4.2)")
	assert.Equal(t, 1, tracks[0].Take.ClusterCount, "the event's 0x18 cluster count is carried into the result")
	assert.Equal(t, want, tracks[0].PCM.Samples, "the take's audio is decoded and placed at the origin")
	assert.Empty(t, devs, "a well-formed disk extracts without deviations")
}

// TestExtractHDDBothMachinesInOnePartition locks the stronger reading of §4.3 /
// user story 6: the machine is resolved per song *directory* by its extension,
// not per partition. Here a single FAT16 root holds both a VR5 and a VR9 song
// directory; each must extract with its own machine's timeline model.
func TestExtractHDDBothMachinesInOnePartition(t *testing.T) {
	vr5Audio, vr9Audio := frame16(), frame16()
	vr9Audio[0] = 77
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{
		Songs: []hddfix.Song{
			{
				Number: 1, Name: "THE VR5", Ext: "VR5", Format: byte(FormatM16),
				Takes:  []hddfix.Take{{NameCluster: 0x0100, Content: m16(vr5Audio...)}},
				Events: []hddfix.Event{{Start: 0, End: 1, NameCluster: 0x0100, Track: 1, VTrack: 1}},
			},
			{
				Number: 2, Name: "THE VR9", Ext: "VR9", Format: byte(FormatM16),
				Takes:  []hddfix.Take{{NameCluster: 0x0200, Content: m16(vr9Audio...)}},
				Events: []hddfix.Event{{Start: 12, End: 13, NameCluster: 0x0200, Track: 1, VTrack: 1}},
			},
		},
	}}}
	r, err := Extract(writeDisk(t, disk), Options{})
	require.NoError(t, err)
	tracks, devs := collectTracks(t, r)

	require.Len(t, tracks, 2)
	byNum := map[int][]int32{}
	for _, tr := range tracks {
		byNum[tr.Song.Number] = tr.PCM.Samples
	}
	assert.Equal(t, vr5Audio, byNum[1], "the VR5 directory used the VR5 origin (0)")
	assert.Equal(t, vr9Audio, byNum[2], "the VR9 directory used the VR9 origin (12)")
	assert.Empty(t, devs)
}

// TestExtractHDDVR5EndToEnd is the VS-1880 counterpart: the positional V-track
// table (§4.5) drives the timeline, and the track/v-track come from table
// position.
func TestExtractHDDVR5EndToEnd(t *testing.T) {
	want := frame16()
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{
		Songs: []hddfix.Song{{
			Number: 3, Name: "LIVE VR5", Ext: "VR5", Format: byte(FormatM16),
			Takes:  []hddfix.Take{{NameCluster: 0x0100, Content: m16(want...)}},
			Events: []hddfix.Event{{Start: 0, End: 1, NameCluster: 0x0100, Track: 5, VTrack: 2}},
		}},
	}}}
	r, err := Extract(writeDisk(t, disk), Options{})
	require.NoError(t, err)
	tracks, devs := collectTracks(t, r)

	require.Len(t, tracks, 1)
	assert.Equal(t, 3, tracks[0].Song.Number)
	assert.Equal(t, 5, tracks[0].Track)
	assert.Equal(t, 2, tracks[0].VTrack)
	assert.Equal(t, want, tracks[0].PCM.Samples)
	assert.Empty(t, devs)
}

// TestExtractHDDMixedMachineDisk locks user story 6 / §4.3: a disk hosting both
// machines' songs resolves the machine per song directory by its extension. Two
// partitions — one VR5 song, one VR9 song — each extract correctly in one run.
func TestExtractHDDMixedMachineDisk(t *testing.T) {
	vr5Audio, vr9Audio := frame16(), frame16()
	vr9Audio[0] = 42 // make the two tracks distinguishable
	disk := hddfix.Disk{Partitions: []hddfix.Partition{
		{Songs: []hddfix.Song{{
			Number: 1, Name: "EIGHTY", Ext: "VR5", Format: byte(FormatM16),
			Takes:  []hddfix.Take{{NameCluster: 0x0100, Content: m16(vr5Audio...)}},
			Events: []hddfix.Event{{Start: 0, End: 1, NameCluster: 0x0100, Track: 1, VTrack: 1}},
		}}},
		{Songs: []hddfix.Song{{
			Number: 2, Name: "EXEX", Ext: "VR9", Format: byte(FormatM16),
			Takes:  []hddfix.Take{{NameCluster: 0x0200, Content: m16(vr9Audio...)}},
			Events: []hddfix.Event{{Start: 12, End: 13, NameCluster: 0x0200, Track: 1, VTrack: 1}},
		}}},
	}}
	r, err := Extract(writeDisk(t, disk), Options{})
	require.NoError(t, err)
	tracks, devs := collectTracks(t, r)

	require.Len(t, tracks, 2)
	byNum := map[int][]int32{}
	for _, tr := range tracks {
		byNum[tr.Song.Number] = tr.PCM.Samples
	}
	assert.Equal(t, vr5Audio, byNum[1], "the VR5 song decoded from its positional table")
	assert.Equal(t, vr9Audio, byNum[2], "the VR9 song decoded from its flat log")
	assert.Empty(t, devs)
}

// TestExtractHDDCopiedTakeResolvesByFilename locks the §4.3 rule that takes are
// resolved by filename, not by chaining from the event's cluster value. The
// event references cluster 0x0100, but no cluster 0x0100 exists in this small
// partition; the take's file (named TAKE0100) actually lives at a low cluster.
// Only filename resolution recovers the audio — a from-event-cluster resolver
// would read free/out-of-range clusters and emit nothing (§4.3 dangle).
func TestExtractHDDCopiedTakeResolvesByFilename(t *testing.T) {
	want := frame16()
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{
		Songs: []hddfix.Song{{
			Number: 9, Name: "COPIED", Ext: "VR9", Format: byte(FormatM16),
			Takes:  []hddfix.Take{{NameCluster: 0x0100, Content: m16(want...)}},
			Events: []hddfix.Event{{Start: 12, End: 13, NameCluster: 0x0100, Track: 1, VTrack: 1}},
		}},
	}}}
	r, err := Extract(writeDisk(t, disk), Options{})
	require.NoError(t, err)
	tracks, devs := collectTracks(t, r)

	require.Len(t, tracks, 1)
	assert.Equal(t, want, tracks[0].PCM.Samples, "resolved by filename TAKE0100, not by event cluster 0x0100")
	assert.Empty(t, devs)
}

// TestExtractHDDTruncatedChainWarns locks the §8.3 integrity check: a take whose
// FAT chain is shorter than its event 0x18 cluster count is reported as
// truncated/corrupt (a warning) rather than emitted as whole silent garbage —
// while its partial audio is still recovered (best-effort, §8).
func TestExtractHDDTruncatedChainWarns(t *testing.T) {
	// 64 frames of audio (1024 samples) spanning two 1024-byte clusters, but the
	// on-disk chain is cut to one cluster; the event still claims two.
	samples := make([]int32, 1024)
	for i := range samples {
		samples[i] = int32(i & 0x7FFF)
	}
	disk := hddfix.Disk{Partitions: []hddfix.Partition{{
		SectorsPerCluster: 2,
		Songs: []hddfix.Song{{
			Number: 4, Name: "CORRUPT", Ext: "VR9", Format: byte(FormatM16),
			Takes: []hddfix.Take{{
				NameCluster: 0x0100, Content: m16(samples...), CorruptToClusters: 1,
			}},
			Events: []hddfix.Event{{Start: 12, End: 12 + 64, NameCluster: 0x0100, Count: 2, Track: 1, VTrack: 1}},
		}},
	}}}
	r, err := Extract(writeDisk(t, disk), Options{})
	require.NoError(t, err)
	tracks, devs := collectTracks(t, r)

	require.Len(t, tracks, 1, "the v-track is still emitted, not dropped")
	found := false
	for _, d := range devs {
		if d.SpecRef == "§8.3" {
			found = true
			assert.Equal(t, SeverityWarning, d.Severity)
		}
	}
	assert.True(t, found, "a truncated FAT chain is reported as a §8.3 corruption, not silent garbage")
	// The surviving cluster's audio is present; the missing cluster is silence.
	assert.Equal(t, samples[:512], tracks[0].PCM.Samples[:512], "the recoverable half is real audio")
	assert.NotContains(t, tracks[0].PCM.Samples[512:], int32(999999), "the rest is silence-padded")
}

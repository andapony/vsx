package core

import (
	"bytes"
	"testing"

	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The core suite drives the ReaderAt extraction/list entries over fixtures built
// in memory (issue #25): no t.TempDir round-trip, and — where only enumeration
// or timeline behaviour is under test — a fake Decoder in place of the golden
// RDAC codec (ADR-0004). These helpers are that seam; the path-based Extract/
// List keep their own coverage in media_test.go against real corpus media.

// mustExtractBytes drives the ReaderAt extraction entry over an in-memory disc
// image with the production decoder — the diskless equivalent of Extract(path).
func mustExtractBytes(t *testing.T, raw []byte, opts Options) Result {
	t.Helper()
	r, err := extractReader(bytes.NewReader(raw), int64(len(raw)), NewDecoder(), opts)
	require.NoError(t, err)
	return r
}

// mustListBytes is mustExtractBytes for List: it enumerates an in-memory disc
// image's songs. List constructs no decoder, so none is supplied.
func mustListBytes(t *testing.T, raw []byte, opts Options) ([]SongInfo, []Deviation) {
	t.Helper()
	songs, devs, err := listReader(bytes.NewReader(raw), int64(len(raw)), opts)
	require.NoError(t, err)
	return songs, devs
}

// memDiscs turns raw disc dumps and their names into the in-memory disc inputs a
// backup-set extraction consumes, so the whole §5.2/§5.6 assembly (grouping,
// ordering, spanning) runs with no files on disk. Names position discs for the
// order-independence and foreign-file assertions exactly as filenames would.
func memDiscs(raws [][]byte, names ...string) []discInput {
	discs := make([]discInput, len(raws))
	for i, raw := range raws {
		name := ""
		if i < len(names) {
			name = names[i]
		}
		discs[i] = discInput{r: bytes.NewReader(raw), size: int64(len(raw)), name: name}
	}
	return discs
}

// mustExtractSetBytes drives the ReaderAt backup-set extraction over in-memory
// discs with the production decoder.
func mustExtractSetBytes(t *testing.T, discs []discInput, opts Options) Result {
	t.Helper()
	r, err := extractSetReader(discs, NewDecoder(), opts)
	require.NoError(t, err)
	return r
}

// mustListSetBytes drives the ReaderAt backup-set list over in-memory discs.
func mustListSetBytes(t *testing.T, discs []discInput, opts Options) ([]SongInfo, []Deviation) {
	t.Helper()
	songs, devs, err := listSetReader(discs, opts)
	require.NoError(t, err)
	return songs, devs
}

// stampDecoder is a Decoder that ignores its input and returns a fixed sample
// pattern, so an extraction's output can be traced back to the decoder that was
// supplied (never the golden RDAC codec).
type stampDecoder struct{ samples []int32 }

func (d stampDecoder) Decode(Format, []byte, int) (PCM, error) {
	return PCM{Samples: append([]int32(nil), d.samples...), BitDepth: 16}, nil
}

// silentDecoder returns deterministic silence longer than any event span (a real
// take of len(data) bytes decodes to at most ~1.34·len(data) samples), so it
// stands in for the codec in tests that only assert enumeration/timeline
// behaviour without triggering a short-take deviation.
type silentDecoder struct{}

func (silentDecoder) Decode(_ Format, data []byte, _ int) (PCM, error) {
	return PCM{Samples: make([]int32, len(data)*2), BitDepth: 16}, nil
}

// TestReaderAtEntryListsAndExtractsSingleDiscDiskless is the single-disc half of
// acceptance criterion #1: a Source is both listed and extracted straight from
// an in-memory byte source, with no temp file. It drives the same disc through
// both ReaderAt entries and checks the enumeration matches the extracted audio.
func TestReaderAtEntryListsAndExtractsSingleDiscDiskless(t *testing.T) {
	disc := vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{{
			Number: 1, Name: "DISKLESS",
			Takes:  []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: silentMT2(4)}},
			Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1}},
		}},
	}
	raw := disc.BuildRaw()

	songs, ldevs := mustListBytes(t, raw, Options{})
	require.Len(t, songs, 1)
	assert.Equal(t, "DISKLESS", songs[0].Name)
	assert.Equal(t, 1, songs[0].VTracks)
	assert.Empty(t, ldevs)

	tracks, edevs := collectTracks(t, mustExtractBytes(t, raw, Options{}))
	require.Len(t, tracks, 1)
	assert.Equal(t, songs[0].VTracks, len(tracks), "List's v-track count matches Extract's tracks")
	assert.Empty(t, edevs)
}

// TestReaderAtEntryListsAndExtractsMultiDiscDiskless is the multi-disc half of
// acceptance criterion #1: a backup set is both listed and extracted straight
// from in-memory disc bytes, with no temp file. The set spans one take across
// its two discs, so the whole §5.6 assembly runs from bytes, and List and
// Extract are checked to agree on the song it enumerates.
func TestReaderAtEntryListsAndExtractsMultiDiscDiskless(t *testing.T) {
	discs := vsfix.VR9Set{SetID: [4]byte{7, 7, 7, 7}, Songs: spanSongs(), SpanFileID: 0x0100, SpanAvailBlocks: 1}.BuildDiscsRaw()
	require.Len(t, discs, 2)

	// The in-memory readers are stateless (bytes.Reader.ReadAt), so one set of
	// disc inputs drives both entries.
	set := memDiscs(discs, "disc0.bin", "disc1.bin")

	songs, ldevs := mustListSetBytes(t, set, Options{})
	require.Len(t, songs, 1, "the set enumerates as one Source with one song")
	assert.Equal(t, "SPAN", songs[0].Name)
	assert.Equal(t, 1, songs[0].VTracks)
	assert.Empty(t, ldevs, "a complete, well-formed set lists without deviations")

	tracks, edevs := collectTracks(t, mustExtractSetBytes(t, set, Options{}))
	require.Len(t, tracks, 1)
	assert.Equal(t, songs[0].Name, tracks[0].Song.Name, "List and Extract agree on the set's song")
	assert.Equal(t, songs[0].VTracks, len(tracks), "List's v-track count matches Extract's tracks")
	assert.Empty(t, edevs)
}

// TestExtractUsesSuppliedDecoder is acceptance criterion #2: a fake Decoder
// supplied to the ReaderAt entry is the one that produces the audio. The take is
// silent MT2 (the real decoder would yield zeros), so a non-zero stamped result
// can only have come from the injected decoder.
func TestExtractUsesSuppliedDecoder(t *testing.T) {
	// One song, one whole-take event over 4 frames (VR9 origin 12) = 64 samples.
	disc := vsfix.Disc{
		SetID: [4]byte{9, 9, 9, 9},
		Songs: []vsfix.Song{{
			Number: 1, Name: "INJECT",
			Takes:  []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: silentMT2(4)}},
			Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1}},
		}},
	}
	raw := disc.BuildRaw()

	stamp := make([]int32, 64)
	for i := range stamp {
		stamp[i] = int32(i + 1) // distinctive, non-zero
	}
	r, err := extractReader(bytes.NewReader(raw), int64(len(raw)), stampDecoder{samples: stamp}, Options{})
	require.NoError(t, err)
	tracks, _ := collectTracks(t, r)

	require.Len(t, tracks, 1)
	assert.Equal(t, stamp, tracks[0].PCM.Samples,
		"the extracted audio came from the injected decoder, not the golden codec")
}

// TestPathExtractDefaultsToRealDecoder is the other half of criterion #2: the
// path API still decodes through the production codec. The same silent take the
// injection test stamps decodes to real silence here, proving Extract did not
// pick up a fake.
func TestPathExtractDefaultsToRealDecoder(t *testing.T) {
	disc := vsfix.Disc{
		SetID: [4]byte{9, 9, 9, 9},
		Songs: []vsfix.Song{{
			Number: 1, Name: "REAL",
			Takes:  []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: silentMT2(4)}},
			Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1}},
		}},
	}
	tracks, _ := collectTracks(t, mustExtractBytes(t, disc.BuildRaw(), Options{}))
	require.Len(t, tracks, 1)
	assert.False(t, anyNonZero(tracks[0].PCM.Samples),
		"the production decoder renders a silent MT2 take as silence")
}

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andapony/vsx/internal/testutil"
	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// silentMT2Bytes returns n bytes of silent MT2 (n a multiple of 12). Sized above
// 0x8000, its file data occupies two 0x8000 blocks, so a take built from it can
// be split across a disc junction.
func silentMT2Bytes(n int) []byte { return make([]byte, n) }

// spanSongs is the one-song, one-take, one-event archive both the single-disc
// and the multi-disc fixtures below are built from, so their extractions can be
// compared: the take (FileID 0x0100) is two data blocks long and is placed whole
// at T1/V1.
func spanSongs() []vsfix.Song {
	return []vsfix.Song{{
		Number: 1, Name: "SPAN",
		Takes: []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: silentMT2Bytes(0xC000)}},
		Events: []vsfix.Event{
			// 0xC000 MT2 bytes decode to 4095 blocks (§2 page padding) = 65520
			// samples; the event span is sized to match so the reference is clean.
			{Start: 12, End: 12 + 4095, FileID: 0x0100, Track: 1, VTrack: 1},
		},
	}}
}

// writeSet writes each disc dump to dir under the given filename.
func writeSet(t *testing.T, dir string, discs [][]byte, names []string) {
	t.Helper()
	require.Len(t, names, len(discs))
	for i, d := range discs {
		require.NoError(t, os.WriteFile(filepath.Join(dir, names[i]), d, 0o644))
	}
}

// TestSpanningReconstructsByteExact is the headline §5.6 acceptance criterion at
// the pipeline level: a take split across a disc boundary extracts to the exact
// same PCM as the identical take on a single disc. The two-disc set's files are
// named so that sorted filename order is the reverse of disc-index order, which
// also proves the set is ordered by disc index, not by filename or argument
// order.
func TestSpanningReconstructsByteExact(t *testing.T) {
	songs := spanSongs()

	// Single-disc reference: the whole take on one disc, no spanning.
	single := filepath.Join(t.TempDir(), "single.bin")
	require.NoError(t, os.WriteFile(single, vsfix.Disc{SetID: [4]byte{7, 7, 7, 7}, Songs: songs}.BuildRaw(), 0o644))
	refTracks, refDevs := collectTracks(t, mustExtract(t, single, Options{}))
	require.Len(t, refTracks, 1)
	assert.Empty(t, refDevs, "the single-disc reference is spec-clean")
	wantHash := testutil.PCMHash(refTracks[0].PCM.Samples)

	// Two-disc set: the same take split 1 block on disc 0, remainder on disc 1.
	dir := t.TempDir()
	discs := vsfix.VR9Set{SetID: [4]byte{7, 7, 7, 7}, Songs: songs, SpanFileID: 0x0100, SpanAvailBlocks: 1}.BuildDiscsRaw()
	require.Len(t, discs, 2)
	writeSet(t, dir, discs, []string{"z_disc0.bin", "a_disc1.bin"}) // sorted order reversed

	gotTracks, gotDevs := collectTracks(t, mustExtract(t, dir, Options{}))
	require.Len(t, gotTracks, 1, "the set extracts as one Source with one populated v-track")
	assert.Equal(t, 1, gotTracks[0].Song.Number)
	assert.Equal(t, 1, gotTracks[0].Track)
	assert.Equal(t, 1, gotTracks[0].VTrack)
	assert.Equal(t, wantHash, testutil.PCMHash(gotTracks[0].PCM.Samples),
		"the spanned take reconstructs to the byte-exact same PCM as on a single disc")
	assert.Empty(t, gotDevs, "a complete, well-formed set walks without deviations")
}

// TestVR5SpanningReconstructsByteExact is the VS-1880 counterpart of the VR9
// span test: the CD spanning path is machine-agnostic, so a VR5 take split
// across a disc boundary must likewise reconstruct to the byte-exact PCM of the
// same take on a single disc. It exercises the separate VR5 walk (different §5.4
// header offsets, song grouping by name, SONG-file number resolution).
func TestVR5SpanningReconstructsByteExact(t *testing.T) {
	songs := vr5SpanSongs()

	single := filepath.Join(t.TempDir(), "single.bin")
	require.NoError(t, os.WriteFile(single, vsfix.VR5Disc{SetID: [4]byte{5, 5, 5, 5}, Songs: songs}.BuildRaw(), 0o644))
	refTracks, refDevs := collectTracks(t, mustExtract(t, single, Options{}))
	require.Len(t, refTracks, 1)
	assert.Empty(t, refDevs, "the single-disc VR5 reference is spec-clean")
	wantHash := testutil.PCMHash(refTracks[0].PCM.Samples)

	dir := t.TempDir()
	discs := vsfix.VR5Set{SetID: [4]byte{5, 5, 5, 5}, Songs: songs, SpanFileID: 0x9CC7, SpanAvailBlocks: 1}.BuildDiscsRaw()
	require.Len(t, discs, 2)
	writeSet(t, dir, discs, []string{"z_disc0.bin", "a_disc1.bin"})

	gotTracks, gotDevs := collectTracks(t, mustExtract(t, dir, Options{}))
	require.Len(t, gotTracks, 1)
	assert.Equal(t, wantHash, testutil.PCMHash(gotTracks[0].PCM.Samples),
		"the spanned VR5 take reconstructs byte-exactly")
	assert.Empty(t, gotDevs, "a complete, well-formed VR5 set walks without deviations")
}

// vr5SpanSongs is the one-song VR5 archive the VR5 span test builds both its
// single-disc and two-disc fixtures from: take 0x9CC7 is two data blocks long
// (4096 MTP blocks = 65536 samples) and placed whole at T1/V1.
func vr5SpanSongs() []vsfix.VR5Song {
	return []vsfix.VR5Song{{
		Number: 4, Name: "VR5SPAN",
		Takes: []vsfix.VR5Take{{FileID: 0x9CC7, Name: "TAKE9CC7", MTP: make([]byte, 0x10000)}},
		VTracks: []vsfix.VR5VTrack{{
			Track: 1, VTrack: 1,
			Events: []vsfix.VR5Event{{Start: 0, End: 4096, FileID: 0x9CC7}},
		}},
	}}
}

// pokeRawUserData sets one byte of a raw 2352-byte-frame dump, addressed by
// user-data offset (§5.1: udoff = frame×2048 + inframe → phys = frame×2352 + 16
// + inframe). It lets a test corrupt a specific archive field on one disc.
func pokeRawUserData(dump []byte, udoff int, b byte) {
	frame := udoff / 2048
	inframe := udoff % 2048
	dump[frame*2352+16+inframe] = b
}

// TestSpanJunctionHeaderMismatchReported locks the §5.6 verification step: when a
// continuation disc's block-0 header does not repeat the spanning file's header
// (here its FileID is corrupted), the mismatch is reported as a deviation rather
// than silently splicing possibly-wrong bytes into the file. The audio is still
// stitched (best-effort), but the run no longer looks clean.
func TestSpanJunctionHeaderMismatchReported(t *testing.T) {
	songs := spanSongs()
	discs := vsfix.VR9Set{SetID: [4]byte{7, 7, 7, 7}, Songs: songs, SpanFileID: 0x0100, SpanAvailBlocks: 1}.BuildDiscsRaw()
	// Corrupt disc 1's block-0 repeated-header FileID (low byte at offFileID+1),
	// so it no longer matches the spanning file's header on disc 0.
	pokeRawUserData(discs[1], offFileID+1, 0xFF)

	dir := t.TempDir()
	writeSet(t, dir, discs, []string{"disc0.bin", "disc1.bin"})

	_, devs := collectTracks(t, mustExtract(t, dir, Options{}))
	assert.True(t, hasDeviationMentioning(devs, "does not repeat this file's header"),
		"the §5.6 header-repeat mismatch is reported")
}

// TestMissingDiscPartialOutput covers the incomplete-set path: with disc 1 of a
// two-disc set absent, the missing index is named as a deviation and the take
// that spanned into the gap still yields its recoverable (partial) audio.
func TestMissingDiscPartialOutput(t *testing.T) {
	songs := spanSongs()
	discs := vsfix.VR9Set{SetID: [4]byte{7, 7, 7, 7}, Songs: songs, SpanFileID: 0x0100, SpanAvailBlocks: 1}.BuildDiscsRaw()

	dir := t.TempDir()
	writeSet(t, dir, discs[:1], []string{"disc0.bin"}) // only disc 0

	tracks, devs := collectTracks(t, mustExtract(t, dir, Options{}))
	require.Len(t, tracks, 1, "the spanning take still yields partial audio")
	assert.Positive(t, len(tracks[0].PCM.Samples), "partial samples were recovered")

	assert.True(t, hasDeviationMentioning(devs, "disc index 1"),
		"a deviation names the missing disc index (1)")
}

// TestForeignFilesReportedAndSkipped covers the two foreign-file cases: an
// unrelated file that is not a CD archive at all, and a disc from a different
// backup set. Both are reported and skipped, and the primary set still extracts.
func TestForeignFilesReportedAndSkipped(t *testing.T) {
	songs := spanSongs()
	discs := vsfix.VR9Set{SetID: [4]byte{7, 7, 7, 7}, Songs: songs, SpanFileID: 0x0100, SpanAvailBlocks: 1}.BuildDiscsRaw()

	dir := t.TempDir()
	writeSet(t, dir, discs, []string{"disc0.bin", "disc1.bin"})
	// An unrelated file: not CD geometry.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a disc"), 0o644))
	// A disc from a different set.
	foreign := vsfix.Disc{SetID: [4]byte{3, 3, 3, 3}, Songs: []vsfix.Song{{
		Number: 9, Name: "OTHER",
		Takes:  []vsfix.Take{{FileID: 0x0200, Name: "TAKE0200", MT2: silentMT2(2)}},
		Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0200, Track: 1, VTrack: 1}},
	}}}.BuildRaw()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foreign.bin"), foreign, 0o644))

	tracks, devs := collectTracks(t, mustExtract(t, dir, Options{}))
	require.Len(t, tracks, 1, "only the primary set's audio is extracted")
	assert.Equal(t, "SPAN", tracks[0].Song.Name)

	assert.True(t, hasDeviationMentioning(devs, "notes.txt"), "the unrelated file is reported")
	assert.True(t, hasDeviationMentioning(devs, "foreign"), "the different-set disc is reported")
}

// mustExtract opens a Source and fails the test on error.
func mustExtract(t *testing.T, path string, opts Options) Result {
	t.Helper()
	r, err := Extract(path, opts)
	require.NoError(t, err)
	return r
}

// hasDeviationMentioning reports whether any deviation's location or message
// contains sub.
func hasDeviationMentioning(devs []Deviation, sub string) bool {
	for _, d := range devs {
		if strings.Contains(d.Location, sub) || strings.Contains(d.Message, sub) {
			return true
		}
	}
	return false
}

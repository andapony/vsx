package core

import (
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

// TestSpanningReconstructsByteExact is the headline §5.6 acceptance criterion at
// the pipeline level: a take split across a disc boundary extracts to the exact
// same PCM as the identical take on a single disc. The two-disc set is handed to
// the ReaderAt entry in reverse disc-index order, which also proves the set is
// ordered by disc index, not by input order.
func TestSpanningReconstructsByteExact(t *testing.T) {
	songs := spanSongs()

	// Single-disc reference: the whole take on one disc, no spanning.
	ref := vsfix.Disc{SetID: [4]byte{7, 7, 7, 7}, Songs: songs}.BuildRaw()
	refTracks, refDevs := collectTracks(t, mustExtractBytes(t, ref, Options{}))
	require.Len(t, refTracks, 1)
	assert.Empty(t, refDevs, "the single-disc reference is spec-clean")
	wantHash := testutil.PCMHash(refTracks[0].PCM.Samples)

	// Two-disc set: the same take split 1 block on disc 0, remainder on disc 1.
	discs := vsfix.VR9Set{SetID: [4]byte{7, 7, 7, 7}, Songs: songs, SpanFileID: 0x0100, SpanAvailBlocks: 1}.BuildDiscsRaw()
	require.Len(t, discs, 2)
	set := memDiscs([][]byte{discs[1], discs[0]}, "disc1.bin", "disc0.bin") // reverse of index order

	gotTracks, gotDevs := collectTracks(t, mustExtractSetBytes(t, set, Options{}))
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

	ref := vsfix.VR5Disc{SetID: [4]byte{5, 5, 5, 5}, Songs: songs}.BuildRaw()
	refTracks, refDevs := collectTracks(t, mustExtractBytes(t, ref, Options{}))
	require.Len(t, refTracks, 1)
	assert.Empty(t, refDevs, "the single-disc VR5 reference is spec-clean")
	wantHash := testutil.PCMHash(refTracks[0].PCM.Samples)

	discs := vsfix.VR5Set{SetID: [4]byte{5, 5, 5, 5}, Songs: songs, SpanFileID: 0x9CC7, SpanAvailBlocks: 1}.BuildDiscsRaw()
	require.Len(t, discs, 2)
	set := memDiscs([][]byte{discs[1], discs[0]}, "disc1.bin", "disc0.bin") // reverse of index order

	gotTracks, gotDevs := collectTracks(t, mustExtractSetBytes(t, set, Options{}))
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

	set := memDiscs(discs, "disc0.bin", "disc1.bin")

	_, devs := collectTracks(t, mustExtractSetBytes(t, set, Options{}))
	assert.True(t, hasDeviationMentioning(devs, "does not repeat this file's header"),
		"the §5.6 header-repeat mismatch is reported")
}

// TestMissingDiscPartialOutput covers the incomplete-set path: with disc 1 of a
// two-disc set absent, the missing index is named as a deviation and the take
// that spanned into the gap still yields its recoverable (partial) audio.
func TestMissingDiscPartialOutput(t *testing.T) {
	songs := spanSongs()
	discs := vsfix.VR9Set{SetID: [4]byte{7, 7, 7, 7}, Songs: songs, SpanFileID: 0x0100, SpanAvailBlocks: 1}.BuildDiscsRaw()

	set := memDiscs(discs[:1], "disc0.bin") // only disc 0

	tracks, devs := collectTracks(t, mustExtractSetBytes(t, set, Options{}))
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

	// A disc from a different set.
	foreign := vsfix.Disc{SetID: [4]byte{3, 3, 3, 3}, Songs: []vsfix.Song{{
		Number: 9, Name: "OTHER",
		Takes:  []vsfix.Take{{FileID: 0x0200, Name: "TAKE0200", MT2: silentMT2(2)}},
		Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0200, Track: 1, VTrack: 1}},
	}}}.BuildRaw()
	// The set plus an unrelated file (not CD geometry) and the foreign-set disc.
	set := memDiscs([][]byte{discs[0], discs[1], []byte("not a disc"), foreign},
		"disc0.bin", "disc1.bin", "notes.txt", "foreign.bin")

	tracks, devs := collectTracks(t, mustExtractSetBytes(t, set, Options{}))
	require.Len(t, tracks, 1, "only the primary set's audio is extracted")
	assert.Equal(t, "SPAN", tracks[0].Song.Name)

	assert.True(t, hasDeviationMentioning(devs, "notes.txt"), "the unrelated file is reported")
	assert.True(t, hasDeviationMentioning(devs, "foreign"), "the different-set disc is reported")
}

// truncSpanSongs is the multi-song VR9 archive the filler-less-junction tests
// (#31) build both their single-disc reference and their truncated two-disc set
// from. Song "SPAN" holds the take that straddles the junction (0x0100, two data
// blocks) followed by a second take (0x0101, one block) that resumes mid-song on
// disc 1, so the continuation disc's first valid header sits exactly at
// 0x8000 + remainder (§5.6). Songs "SOLO1" and "SOLO2" live entirely on disc 1 —
// they never span, and are precisely the songs the bug made vanish.
func truncSpanSongs() []vsfix.Song {
	return []vsfix.Song{
		{
			Number: 1, Name: "SPAN",
			Takes: []vsfix.Take{
				{FileID: 0x0100, Name: "TAKE0100", MT2: silentMT2Bytes(0xC000)}, // 2 blocks: spans
				{FileID: 0x0101, Name: "TAKE0101", MT2: silentMT2Bytes(0x6000)}, // 1 block: resumes on disc 1
			},
			Events: []vsfix.Event{
				{Start: 12, End: 12 + 4095, FileID: 0x0100, Track: 1, VTrack: 1},
				{Start: 12, End: 12 + 2047, FileID: 0x0101, Track: 2, VTrack: 1},
			},
		},
		{
			Number: 2, Name: "SOLO1",
			Takes:  []vsfix.Take{{FileID: 0x0200, Name: "TAKE0200", MT2: silentMT2Bytes(0x6000)}},
			Events: []vsfix.Event{{Start: 12, End: 12 + 2047, FileID: 0x0200, Track: 1, VTrack: 1}},
		},
		{
			Number: 3, Name: "SOLO2",
			Takes:  []vsfix.Take{{FileID: 0x0300, Name: "TAKE0300", MT2: silentMT2Bytes(0x6000)}},
			Events: []vsfix.Event{{Start: 12, End: 12 + 2047, FileID: 0x0300, Track: 1, VTrack: 1}},
		},
	}
}

// TestTruncatedNonTerminalDiscEnumeratesLaterDiscs is the #31 headline: a two-disc
// set whose index-0 disc lacks a trailing filler run (§10) and whose length is off
// the 0x8000 grid must still enumerate every following disc's songs (Option A), and
// — reconstructing the true junction from the continuation disc (Option B) — must
// reseam the spanning take byte-exactly, dropping the over-count residue (Option
// B). The enumeration is checked by song names; the residue drop is proven by the
// byte-exact PCM match and the absence of a spanned-file-corruption deviation,
// both against the identical archive on a single disc.
func TestTruncatedNonTerminalDiscEnumeratesLaterDiscs(t *testing.T) {
	songs := truncSpanSongs()

	// Reference: the whole archive on one clean disc.
	refTracks, refDevs := collectTracks(t, mustExtractBytes(t, vsfix.Disc{SetID: [4]byte{7, 7, 7, 7}, Songs: songs}.BuildRaw(), Options{}))
	assert.Empty(t, refDevs, "the single-disc reference is spec-clean")
	refByKey := tracksByKey(refTracks)

	// The truncated two-disc set: disc 0 loses its filler and gains an over-count
	// residue (two junk blocks + a partial block) that leaves its length off grid.
	discs := vsfix.VR9Set{
		SetID: [4]byte{7, 7, 7, 7}, Songs: songs, SpanFileID: 0x0100, SpanAvailBlocks: 1,
		Disc0Trunc: &vsfix.Disc0Trunc{Junk: 2, TailFrames: 10},
	}.BuildDiscsRaw()
	require.Len(t, discs, 2)
	set := memDiscs(discs, "disc0.bin", "disc1.bin")

	// Option A: the later disc's independent songs enumerate.
	songInfos, _ := mustListSetBytes(t, set, Options{})
	assert.Equal(t, []string{"SPAN", "SOLO1", "SOLO2"}, songNames(songInfos),
		"every disc's songs enumerate")

	// Option B: the spanning take reconstructs byte-exactly, and the whole set
	// extracts to exactly the reference's tracks.
	gotTracks, gotDevs := collectTracks(t, mustExtractSetBytes(t, set, Options{}))
	assertTracksMatch(t, refByKey, gotTracks)
	assert.False(t, hasDeviationMentioning(gotDevs, "may be corrupt"),
		"the junction is reseamed exactly, so no spanned-file corruption is reported")
	assert.True(t, hasDeviationMentioning(gotDevs, "trailing TDI filler"),
		"the truncated rip is still reported as a §10 deviation")
}

// vr5TruncSpanSongs is the VS-1880 counterpart of truncSpanSongs: song "VR5SPAN"
// holds the spanning take (0x9CC7, two blocks) followed by a mid-song take
// (0x9CC8, one block) resuming on disc 1, and "VR5SOLO1"/"VR5SOLO2" live entirely
// on disc 1.
func vr5TruncSpanSongs() []vsfix.VR5Song {
	return []vsfix.VR5Song{
		{
			Number: 4, Name: "VR5SPAN",
			Takes: []vsfix.VR5Take{
				{FileID: 0x9CC7, Name: "TAKE9CC7", MTP: make([]byte, 0x10000)}, // 2 blocks: spans
				{FileID: 0x9CC8, Name: "TAKE9CC8", MTP: make([]byte, 0x8000)},  // 1 block: resumes on disc 1
			},
			VTracks: []vsfix.VR5VTrack{
				{Track: 1, VTrack: 1, Events: []vsfix.VR5Event{{Start: 0, End: 4096, FileID: 0x9CC7}}},
				{Track: 2, VTrack: 1, Events: []vsfix.VR5Event{{Start: 0, End: 2048, FileID: 0x9CC8}}},
			},
		},
		{
			Number: 5, Name: "VR5SOLO1",
			Takes:   []vsfix.VR5Take{{FileID: 0x9CD0, Name: "TAKE9CD0", MTP: make([]byte, 0x8000)}},
			VTracks: []vsfix.VR5VTrack{{Track: 1, VTrack: 1, Events: []vsfix.VR5Event{{Start: 0, End: 2048, FileID: 0x9CD0}}}},
		},
		{
			Number: 6, Name: "VR5SOLO2",
			Takes:   []vsfix.VR5Take{{FileID: 0x9CD1, Name: "TAKE9CD1", MTP: make([]byte, 0x8000)}},
			VTracks: []vsfix.VR5VTrack{{Track: 1, VTrack: 1, Events: []vsfix.VR5Event{{Start: 0, End: 2048, FileID: 0x9CD1}}}},
		},
	}
}

// TestVR5TruncatedNonTerminalDiscEnumeratesLaterDiscs is the VS-1880 counterpart
// of the #31 headline: the fix is machine-agnostic, so a VR5 set with a
// filler-less, off-grid index-0 disc must likewise enumerate the later disc's
// songs and reseam its spanning take byte-exactly.
func TestVR5TruncatedNonTerminalDiscEnumeratesLaterDiscs(t *testing.T) {
	songs := vr5TruncSpanSongs()

	refTracks, refDevs := collectTracks(t, mustExtractBytes(t, vsfix.VR5Disc{SetID: [4]byte{5, 5, 5, 5}, Songs: songs}.BuildRaw(), Options{}))
	assert.Empty(t, refDevs, "the single-disc VR5 reference is spec-clean")
	refByKey := tracksByKey(refTracks)

	discs := vsfix.VR5Set{
		SetID: [4]byte{5, 5, 5, 5}, Songs: songs, SpanFileID: 0x9CC7, SpanAvailBlocks: 1,
		Disc0Trunc: &vsfix.Disc0Trunc{Junk: 2, TailFrames: 10},
	}.BuildDiscsRaw()
	require.Len(t, discs, 2)
	set := memDiscs(discs, "disc0.bin", "disc1.bin")

	songInfos, _ := mustListSetBytes(t, set, Options{})
	assert.Equal(t, []string{"VR5SPAN", "VR5SOLO1", "VR5SOLO2"}, songNames(songInfos),
		"every disc's songs enumerate, with no spurious residue entry")

	gotTracks, gotDevs := collectTracks(t, mustExtractSetBytes(t, set, Options{}))
	assertTracksMatch(t, refByKey, gotTracks)
	assert.False(t, hasDeviationMentioning(gotDevs, "may be corrupt"),
		"the junction is reseamed exactly, so no spanned-file corruption is reported")
	assert.True(t, hasDeviationMentioning(gotDevs, "trailing TDI filler"),
		"the truncated rip is still reported as a §10 deviation")
}

// trackKey identifies a v-track's audio across the reference and set extractions:
// the song name plus the physical track/v-track it was laid down at.
type trackKey struct {
	song          string
	track, vtrack int
}

// tracksByKey indexes tracks by song/track/v-track and PCM hash, so a set
// extraction can be checked to reproduce a reference extraction exactly.
func tracksByKey(tracks []TrackResult) map[trackKey]string {
	byKey := map[trackKey]string{}
	for _, tr := range tracks {
		byKey[trackKey{tr.Song.Name, tr.Track, tr.VTrack}] = testutil.PCMHash(tr.PCM.Samples)
	}
	return byKey
}

// assertTracksMatch checks the set extraction produced exactly the reference's
// tracks — same set of v-tracks, each byte-identical PCM — the byte-exact §5.6
// reconstruction across a filler-less junction (#31).
func assertTracksMatch(t *testing.T, want map[trackKey]string, got []TrackResult) {
	t.Helper()
	gotByKey := tracksByKey(got)
	assert.Equal(t, len(want), len(gotByKey), "the set extracts exactly the reference's v-tracks")
	for k, wantHash := range want {
		assert.Equal(t, wantHash, gotByKey[k], "v-track %+v reconstructs byte-exactly", k)
	}
}

// songNames returns the enumerated songs' names in order, for asserting a set
// lists every disc's songs.
func songNames(songs []SongInfo) []string {
	names := make([]string, len(songs))
	for i, s := range songs {
		names[i] = s.Name
	}
	return names
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

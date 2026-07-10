package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andapony/vsx/internal/core"
	"github.com/andapony/vsx/internal/hddfix"
	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeDisc writes a synthetic VR9 dump to a temp file and returns its path.
func writeDisc(t *testing.T, d vsfix.Disc) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vr9.bin")
	require.NoError(t, os.WriteFile(path, d.BuildRaw(), 0o644))
	return path
}

// tracerDisc is a one-song VR9 archive with two populated v-tracks, used across
// the CLI tests.
func tracerDisc() vsfix.Disc {
	return vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{{
			Number: 2, Name: "SONG TWO",
			Takes: []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: make([]byte, 12*4)}},
			Events: []vsfix.Event{
				{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1},
				{Start: 12, End: 16, FileID: 0x0100, Track: 3, VTrack: 2},
			},
		}},
	}
}

// twoSongTracerDisc is a two-song VR9 archive, each song carrying one
// populated v-track, used to exercise the --song selection CLI flag. Songs
// are numbered 1 and 2 so their CD keys are "1" and "2" and their folders are
// "01 - …" / "02 - …".
func twoSongTracerDisc() vsfix.Disc {
	return vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{
			{
				Number: 1, Name: "SONG ONE",
				Takes: []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: make([]byte, 12*4)}},
				Events: []vsfix.Event{
					{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1},
				},
			},
			{
				Number: 2, Name: "SONG TWO",
				Takes: []vsfix.Take{{FileID: 0x0200, Name: "TAKE0200", MT2: make([]byte, 12*4)}},
				Events: []vsfix.Event{
					{Start: 12, End: 16, FileID: 0x0200, Track: 1, VTrack: 1},
				},
			},
		},
	}
}

// runCLI invokes run with captured stdout/stderr and returns the exit code
// alongside both streams, so tests can assert on the manifest/diagnostics
// split.
func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errBuf bytes.Buffer
	code = run(args, &out, &errBuf)
	return code, out.String(), errBuf.String()
}

// TestHDDCollidingSongsGetDistinctFolders verifies the multi-partition HDD
// collision fix: two partitions each holding a SONG0000 with the same stored
// number and name must land in distinct output folders (named by SongKey)
// instead of overwriting each other.
func TestHDDCollidingSongsGetDistinctFolders(t *testing.T) {
	// Two partitions, each a SONG0000 named "INIT" with the same stored number —
	// the collision that overwrote output before the key change.
	// Format is pinned to the uncompressed M16 codec (as the other clean HDD
	// fixtures in internal/core/hdd_test.go do) so the all-zero take content
	// decodes with no deviations, and the event covers only one frame (as those
	// fixtures' events do) so the 48-byte take comfortably covers the span —
	// this test is about the folder-collision fix, not about deviation
	// reporting, so the run must come back clean.
	song := hddfix.Song{
		Number: 5, Name: "INIT", Ext: "VR9", Format: byte(core.FormatM16),
		Takes:  []hddfix.Take{{NameCluster: 0x0100, Content: make([]byte, 12*4)}},
		Events: []hddfix.Event{{Start: 12, End: 13, NameCluster: 0x0100, Track: 1, VTrack: 1}},
	}
	disk := hddfix.Disk{Partitions: []hddfix.Partition{
		{Songs: []hddfix.Song{song}}, {Songs: []hddfix.Song{song}},
	}}
	imgPath := filepath.Join(t.TempDir(), "collide.img")
	require.NoError(t, os.WriteFile(imgPath, disk.Build(), 0o644))

	out := t.TempDir()
	code, _, stderr := runCLI("-o", out, imgPath)
	require.Equal(t, exitOK, code, "stderr: %s", stderr)
	assert.DirExists(t, filepath.Join(out, "01.000 - INIT"))
	assert.DirExists(t, filepath.Join(out, "02.000 - INIT"))
	assert.Equal(t, 2, countWavs(t, out), "no overwrite — two distinct v-track files")
}

// TestNoArgsPrintsUsageAndExitsNonZero verifies the acceptance criterion that
// invoking vsx with no arguments prints usage to stderr, writes nothing to the
// stdout manifest, and exits non-zero.
func TestNoArgsPrintsUsageAndExitsNonZero(t *testing.T) {
	code, stdout, stderr := runCLI()
	assert.NotZero(t, code, "no-args invocation must exit non-zero")
	assert.Empty(t, stdout, "no manifest output on a usage error")
	assert.Contains(t, strings.ToLower(stderr), "usage", "usage text goes to stderr")
}

// TestUnknownFlagExitsNonZero verifies that an unrecognized flag is a usage
// error: non-zero exit with diagnostics on stderr and no manifest on stdout.
func TestUnknownFlagExitsNonZero(t *testing.T) {
	code, stdout, stderr := runCLI("--nonsense")
	assert.NotZero(t, code)
	assert.Empty(t, stdout)
	assert.NotEmpty(t, stderr)
}

// TestNonexistentSourceReportsErrorOnStderr verifies that a source path that
// cannot be opened produces a diagnostic on stderr, no manifest on stdout, and
// a non-zero exit code.
func TestNonexistentSourceReportsErrorOnStderr(t *testing.T) {
	code, stdout, stderr := runCLI(filepath.Join(t.TempDir(), "missing.img"))
	assert.NotZero(t, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "vsx:", "error is reported on stderr")
}

// TestUnidentifiableSourceExitsError verifies that a source vsx cannot identify
// is a fatal error on stderr with no manifest — the "genuinely unidentifiable
// input" case (issue #3).
func TestUnidentifiableSourceExitsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "src.img")
	require.NoError(t, os.WriteFile(path, []byte("placeholder"), 0o644))

	code, stdout, stderr := runCLI(path)
	assert.Equal(t, exitError, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "vsx:")
}

// TestExtractsVR9DiscToWavFiles verifies the headline acceptance criterion: a
// VR9 CD dump extracts to "<NN> - <name>/T<track>-V<vtrack>.wav" files, one per
// populated v-track, with the manifest on stdout and a clean exit.
func TestExtractsVR9DiscToWavFiles(t *testing.T) {
	src := writeDisc(t, tracerDisc())
	out := t.TempDir()

	code, stdout, stderr := runCLI("-o", out, src)
	assert.Equal(t, exitOK, code, "clean extraction exits zero; stderr: %s", stderr)

	// Both populated v-tracks were written, under one numbered+named folder.
	t1 := filepath.Join(out, "02 - SONG TWO", "T1-V1.wav")
	t3 := filepath.Join(out, "02 - SONG TWO", "T3-V2.wav")
	assert.FileExists(t, t1)
	assert.FileExists(t, t3)
	assert.Contains(t, stdout, "T1-V1.wav", "manifest lists written files on stdout")
	assert.Contains(t, stdout, "T3-V2.wav")

	// Written bytes are a real WAV (RIFF/WAVE header).
	b, err := os.ReadFile(t1)
	require.NoError(t, err)
	assert.Equal(t, "RIFF", string(b[:4]))
	assert.Equal(t, "WAVE", string(b[8:12]))
}

// TestExtractsMultiDiscSet verifies the CLI end-to-end on a directory of disc
// dumps (issue #6): the discs are grouped into one Source, a take spanning the
// disc boundary is reconstructed, and its v-track is written as a WAV under the
// numbered song folder with a clean exit.
func TestExtractsMultiDiscSet(t *testing.T) {
	songs := []vsfix.Song{{
		Number: 1, Name: "SPANSONG",
		Takes: []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: make([]byte, 0xC000)}},
		Events: []vsfix.Event{
			{Start: 12, End: 12 + 4095, FileID: 0x0100, Track: 1, VTrack: 1},
		},
	}}
	discs := vsfix.VR9Set{SetID: [4]byte{7, 7, 7, 7}, Songs: songs, SpanFileID: 0x0100, SpanAvailBlocks: 1}.BuildDiscsRaw()

	dir := t.TempDir()
	// Filenames sorted opposite to disc-index order: extraction must order by
	// disc index, not by filename.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "z_disc0.bin"), discs[0], 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a_disc1.bin"), discs[1], 0o644))

	out := t.TempDir()
	code, stdout, stderr := runCLI("-o", out, dir)
	assert.Equal(t, exitOK, code, "a complete multi-disc set extracts cleanly; stderr: %s", stderr)
	assert.FileExists(t, filepath.Join(out, "01 - SPANSONG", "T1-V1.wav"))
	assert.Contains(t, stdout, "T1-V1.wav")
}

// TestMultiFileArgsListAndExtractAsSet verifies that passing several disc files
// as separate command-line arguments (rather than staging them in a directory)
// groups them into one multi-disc backup set (§5.6), for both --list and
// extraction.
func TestMultiFileArgsListAndExtractAsSet(t *testing.T) {
	songs := []vsfix.Song{{
		Number: 1, Name: "SPANSONG",
		Takes:  []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: make([]byte, 0xC000)}},
		Events: []vsfix.Event{{Start: 12, End: 12 + 4095, FileID: 0x0100, Track: 1, VTrack: 1}},
	}}
	discs := vsfix.VR9Set{SetID: [4]byte{7, 7, 7, 7}, Songs: songs, SpanFileID: 0x0100, SpanAvailBlocks: 1}.BuildDiscsRaw()
	dir := t.TempDir()
	d0 := filepath.Join(dir, "a_disc1.bin") // deliberately mis-sorted vs disc index
	d1 := filepath.Join(dir, "z_disc0.bin")
	require.NoError(t, os.WriteFile(d0, discs[1], 0o644))
	require.NoError(t, os.WriteFile(d1, discs[0], 0o644))

	// --list over two file args lists the stitched set's songs.
	code, stdout, stderr := runCLI("--list", d0, d1)
	require.Equal(t, exitOK, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "SPANSONG")

	// --song over two file args extracts the selected song (ordered by disc index).
	out := t.TempDir()
	code, stdout, _ = runCLI("--song", "1", "-o", out, d0, d1)
	require.Equal(t, exitOK, code)
	assert.FileExists(t, filepath.Join(out, "01 - SPANSONG", "T1-V1.wav"))
}

// TestMultipleArgsWithDirectoryIsUsageError verifies that mixing a directory
// into a multi-file invocation is rejected: a directory is already one set, so
// combining it with other sources is ambiguous and disallowed rather than
// silently accepted.
func TestMultipleArgsWithDirectoryIsUsageError(t *testing.T) {
	f := writeDisc(t, tracerDisc())
	code, _, stderr := runCLI("--list", f, t.TempDir())
	assert.Equal(t, exitUsage, code)
	assert.Contains(t, stderr, "disc file")
}

// TestAsOverrideForcesVR9 verifies the --as override drives extraction of a dump
// whose signature is unrecognized but whose structure is otherwise VR9.
func TestAsOverrideForcesVR9(t *testing.T) {
	d := tracerDisc()
	raw := d.BuildRaw()
	// Corrupt the signature so autodetection fails; --as must rescue it.
	copy(raw[16:16+11], []byte("XXXXXXXXXXX"))
	src := filepath.Join(t.TempDir(), "nosig.bin")
	require.NoError(t, os.WriteFile(src, raw, 0o644))

	code, _, stderr := runCLI("--as", "vr9", "-o", t.TempDir(), src)
	assert.Equal(t, exitOK, code, "stderr: %s", stderr)

	code2, _, _ := runCLI("-o", t.TempDir(), src)
	assert.Equal(t, exitError, code2, "without --as the corrupt-signature dump is unidentifiable")
}

// TestAsOverrideUnknownIsUsageError verifies an unrecognized --as value is
// rejected at the flag boundary as a usage error (ParseAs), before any Source is
// opened, rather than surfacing later as a fatal extraction failure.
func TestAsOverrideUnknownIsUsageError(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.bin")
	require.NoError(t, os.WriteFile(src, tracerDisc().BuildRaw(), 0o644))

	code, _, stderr := runCLI("--as", "bogus", "-o", t.TempDir(), src)
	assert.Equal(t, exitUsage, code)
	assert.Contains(t, stderr, "unknown --as value")
}

// TestDeviationFlipsExitCodeButStillWrites verifies best-effort mode: a run that
// hits a deviation exits non-zero yet still writes all recoverable output.
func TestDeviationFlipsExitCodeButStillWrites(t *testing.T) {
	d := tracerDisc()
	// Reference a take that is not on the disc (§10 incomplete backup).
	d.Songs[0].Events = append(d.Songs[0].Events,
		vsfix.Event{Start: 12, End: 16, FileID: 0xBEEF, Track: 5, VTrack: 1})
	src := writeDisc(t, d)
	out := t.TempDir()

	code, stdout, stderr := runCLI("-o", out, src)
	assert.Equal(t, exitDeviations, code, "a deviation flips the exit code")
	assert.Contains(t, stderr, "deviation", "deviations are reported on stderr")
	assert.FileExists(t, filepath.Join(out, "02 - SONG TWO", "T1-V1.wav"), "output is still written")
	assert.Contains(t, stdout, "T1-V1.wav")

	// Quiet mode suppresses the deviation and summary chatter on stderr.
	codeQ, _, stderrQ := runCLI("-q", "-o", t.TempDir(), src)
	assert.Equal(t, exitDeviations, codeQ)
	assert.NotContains(t, stderrQ, "deviation")
}

// TestDeviationsStreamInContext verifies issue #28: in best-effort mode a
// deviation is reported on stderr as extraction reaches it, not batched at the
// end of the run. A deviation confined to the first song must appear before the
// second song's per-track output, so someone watching a long run sees each
// problem next to the audio it concerns rather than in a dump after everything.
//
// The ordering is observable because -v logs each extracted v-track to stderr:
// with streaming, song 1's deviation precedes song 2's "extracted …/02 - …"
// line; with the deviations batched at the end it would follow every song.
func TestDeviationsStreamInContext(t *testing.T) {
	d := vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{
			// Song 1 has a clean v-track plus a dangling take reference (§10
			// deviation) — it still yields audio, and the deviation is about it.
			{Number: 1, Name: "AONE", Takes: []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: make([]byte, 12*4)}},
				Events: []vsfix.Event{
					{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1},
					{Start: 12, End: 16, FileID: 0xBEEF, Track: 5, VTrack: 1},
				}},
			// Song 2 is clean.
			{Number: 2, Name: "BTWO", Takes: []vsfix.Take{{FileID: 0x0200, Name: "TAKE0200", MT2: make([]byte, 12*4)}},
				Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0200, Track: 1, VTrack: 1}}},
		},
	}
	src := writeDisc(t, d)

	code, _, stderr := runCLI("-v", "-o", t.TempDir(), src)
	require.Equal(t, exitDeviations, code, "stderr: %s", stderr)

	devAt := strings.Index(stderr, "deviation")
	song2At := strings.Index(stderr, "02 - BTWO")
	require.NotEqual(t, -1, devAt, "the deviation is reported on stderr")
	require.NotEqual(t, -1, song2At, "song 2's v-track is logged on stderr under -v")
	assert.Less(t, devAt, song2At,
		"song 1's deviation must stream before song 2's output, not batch at the end")
}

// stereoPairDisc is a one-song VR9 archive whose tracks 1 and 2 each carry a
// single populated v-track with identical event geometry — a genuine §8.4
// stereo pair — used to exercise the --stereo CLI path.
func stereoPairDisc() vsfix.Disc {
	return vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{{
			Number: 2, Name: "SONG TWO",
			Takes: []vsfix.Take{
				{FileID: 0x0100, Name: "TAKE0100", MT2: make([]byte, 12*4)},
				{FileID: 0x0101, Name: "TAKE0101", MT2: make([]byte, 12*4)},
			},
			Events: []vsfix.Event{
				{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1},
				{Start: 12, End: 16, FileID: 0x0101, Track: 2, VTrack: 1},
			},
		}},
	}
}

// TestStereoFlagPairsAdjacentTracks verifies the issue #8 acceptance criteria at
// the CLI: without --stereo the matched adjacent tracks are two mono WAVs; with
// --stereo they become one interleaved stereo WAV (left = the lower track), the
// two monos are gone, and the formed pair is reported on stderr.
func TestStereoFlagPairsAdjacentTracks(t *testing.T) {
	src := writeDisc(t, stereoPairDisc())

	// Off by default: one mono WAV per v-track.
	outMono := t.TempDir()
	code, _, stderr := runCLI("-o", outMono, src)
	require.Equal(t, exitOK, code, "stderr: %s", stderr)
	assert.FileExists(t, filepath.Join(outMono, "02 - SONG TWO", "T1-V1.wav"))
	assert.FileExists(t, filepath.Join(outMono, "02 - SONG TWO", "T2-V1.wav"))
	assert.Equal(t, 2, countWavs(t, outMono), "mono by default")

	// With --stereo: the pair collapses to a single interleaved stereo file.
	outStereo := t.TempDir()
	code, stdout, stderr := runCLI("--stereo", "-o", outStereo, src)
	require.Equal(t, exitOK, code, "stderr: %s", stderr)
	stereoPath := filepath.Join(outStereo, "02 - SONG TWO", "T1+2-V1.wav")
	assert.FileExists(t, stereoPath, "the pair is one stereo file named for both tracks")
	assert.NoFileExists(t, filepath.Join(outStereo, "02 - SONG TWO", "T1-V1.wav"), "left mono is replaced")
	assert.NoFileExists(t, filepath.Join(outStereo, "02 - SONG TWO", "T2-V1.wav"), "right mono is replaced")
	assert.Equal(t, 1, countWavs(t, outStereo), "one file replaces the two monos")
	assert.Contains(t, stdout, "T1+2-V1.wav", "manifest lists the stereo file")
	assert.Contains(t, strings.ToLower(stderr), "pair", "the formed pair is reported")

	// The written file really is a two-channel WAV.
	b, err := os.ReadFile(stereoPath)
	require.NoError(t, err)
	assert.EqualValues(t, 2, binary.LittleEndian.Uint16(b[22:24]), "stereo: 2 channels")
}

// countWavs returns how many .wav files exist under dir, recursively — used to
// assert that strict mode writes nothing on a deviation.
func countWavs(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	require.NoError(t, filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".wav") {
			n++
		}
		return nil
	}))
	return n
}

// TestStrictCleanImageWritesAndExitsZero verifies the strict conformance gate on
// a spec-clean image: it exits zero and still writes every v-track (issue #7).
func TestStrictCleanImageWritesAndExitsZero(t *testing.T) {
	src := writeDisc(t, tracerDisc())
	out := t.TempDir()

	code, stdout, stderr := runCLI("--strict", "-o", out, src)
	assert.Equal(t, exitOK, code, "a clean image passes the strict gate; stderr: %s", stderr)
	assert.Equal(t, 2, countWavs(t, out), "strict writes the clean output")
	assert.Contains(t, stdout, "T1-V1.wav")
}

// TestStrictAbortsOnDeviationWithNoOutput verifies the core strict guarantee:
// the first deviation aborts the whole run, non-zero, having written nothing.
func TestStrictAbortsOnDeviationWithNoOutput(t *testing.T) {
	d := tracerDisc()
	// A referenced-but-absent take is a §10 deviation.
	d.Songs[0].Events = append(d.Songs[0].Events,
		vsfix.Event{Start: 12, End: 16, FileID: 0xBEEF, Track: 5, VTrack: 1})
	src := writeDisc(t, d)
	out := t.TempDir()

	code, stdout, stderr := runCLI("--strict", "-o", out, src)
	assert.NotEqual(t, exitOK, code, "strict fails on any deviation")
	assert.Empty(t, stdout, "no manifest under a strict abort")
	assert.Equal(t, 0, countWavs(t, out), "strict writes no output on a deviation")
	assert.Contains(t, stderr, "strict")
}

// TestStrictVerdictIndependentOfSongCount verifies that a deviation confined to
// one song of a multi-song set still fails the whole run with no output — the
// verdict does not depend on how many songs are clean (issue #7).
func TestStrictVerdictIndependentOfSongCount(t *testing.T) {
	d := vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{
			{Number: 1, Name: "CLEAN ONE", Takes: []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: make([]byte, 12*4)}},
				Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1}}},
			{Number: 2, Name: "CLEAN TWO", Takes: []vsfix.Take{{FileID: 0x0200, Name: "TAKE0200", MT2: make([]byte, 12*4)}},
				Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0200, Track: 1, VTrack: 1}}},
			// Only the third song deviates (dangling take reference).
			{Number: 3, Name: "BAD THREE", Takes: []vsfix.Take{{FileID: 0x0300, Name: "TAKE0300", MT2: make([]byte, 12*4)}},
				Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0xDEAD, Track: 1, VTrack: 1}}},
		},
	}
	src := writeDisc(t, d)
	out := t.TempDir()

	code, _, _ := runCLI("--strict", "-o", out, src)
	assert.Equal(t, exitDeviations, code)
	assert.Equal(t, 0, countWavs(t, out), "one deviating song fails the whole run; nothing written")
}

// TestCookedRipBestEffortWarnsStrictAborts verifies the §5 cooked/dd-rip
// diagnostic: best-effort warns and still extracts, strict aborts with no
// output (issue #7).
func TestCookedRipBestEffortWarnsStrictAborts(t *testing.T) {
	cooked := tracerDisc().BuildCooked()
	src := filepath.Join(t.TempDir(), "cooked.iso")
	require.NoError(t, os.WriteFile(src, cooked, 0o644))

	// Best-effort: warns, writes output, non-zero exit.
	out := t.TempDir()
	code, stdout, stderr := runCLI("-o", out, src)
	assert.Equal(t, exitDeviations, code)
	assert.Contains(t, stderr, "cooked")
	assert.Contains(t, stdout, "T1-V1.wav", "best-effort still extracts a cooked rip")
	assert.Positive(t, countWavs(t, out))

	// Strict: aborts, no output.
	outS := t.TempDir()
	codeS, stdoutS, _ := runCLI("--strict", "-o", outS, src)
	assert.NotEqual(t, exitOK, codeS)
	assert.Empty(t, stdoutS)
	assert.Equal(t, 0, countWavs(t, outS))
}

// TestTruncatedDumpBestEffortWarnsStrictAborts verifies the §10 truncated-rip
// diagnostic (a raw dump missing its trailing TDI filler): best-effort warns and
// still extracts, strict aborts with no output (issue #7).
func TestTruncatedDumpBestEffortWarnsStrictAborts(t *testing.T) {
	d := tracerDisc()
	d.NoFiller = true // a raw dump with no trailing filler run
	src := writeDisc(t, d)

	out := t.TempDir()
	code, _, stderr := runCLI("-o", out, src)
	assert.Equal(t, exitDeviations, code)
	assert.Contains(t, stderr, "filler")
	assert.Positive(t, countWavs(t, out), "best-effort still extracts a truncated dump")

	outS := t.TempDir()
	codeS, _, _ := runCLI("--strict", "-o", outS, src)
	assert.NotEqual(t, exitOK, codeS)
	assert.Equal(t, 0, countWavs(t, outS))
}

// TestExtractsVR5DiscWithNamedTrack verifies the VS-1880 (VR5) CD path through
// the CLI (issue #4): the disc is auto-detected, the song folder is numbered
// from the SONG file, and a user-assigned track name is appended to the WAV
// filename.
func TestExtractsVR5DiscWithNamedTrack(t *testing.T) {
	disc := vsfix.VR5Disc{
		SetID: [4]byte{5, 5, 5, 5},
		Songs: []vsfix.VR5Song{{
			Number: 7, Name: "MIXDOWN",
			Takes: []vsfix.VR5Take{{FileID: 0x9CC7, Name: "TAKE9CC7", MTP: make([]byte, 16*4)}},
			VTracks: []vsfix.VR5VTrack{{Track: 1, VTrack: 1, Name: "Bass", Events: []vsfix.VR5Event{
				{Start: 0, End: 4, FileID: 0x9CC7},
			}}},
		}},
	}
	src := filepath.Join(t.TempDir(), "vr5.bin")
	require.NoError(t, os.WriteFile(src, disc.BuildRaw(), 0o644))
	out := t.TempDir()

	code, stdout, stderr := runCLI("-o", out, src)
	assert.Equal(t, exitOK, code, "clean VR5 extraction exits zero; stderr: %s", stderr)

	named := filepath.Join(out, "07 - MIXDOWN", "T1-V1 Bass.wav")
	assert.FileExists(t, named, "user track name is appended to the filename")
	assert.Contains(t, stdout, "T1-V1 Bass.wav")
}

// TestSongFlagExtractsOnlySelected verifies that --song restricts extraction
// to the selected song(s) only.
func TestSongFlagExtractsOnlySelected(t *testing.T) {
	src := writeDisc(t, twoSongTracerDisc())
	out := t.TempDir()
	code, stdout, stderr := runCLI("--song", "2", "-o", out, src)
	require.Equal(t, exitOK, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "02 - ") // only song 2's folder
	assert.NotContains(t, stdout, "01 - ")
}

// TestSongFlagUnknownKeyReportsDeviation verifies that a --song key not present
// on the source surfaces as a deviation from the single extraction walk (issue
// #27): nothing matches, so no audio is written, and the deviation flips the
// exit code and prints a hint to use --list. Core reports the unknown key now,
// so the CLI no longer runs a separate enumeration pass to validate keys.
func TestSongFlagUnknownKeyReportsDeviation(t *testing.T) {
	src := writeDisc(t, twoSongTracerDisc())
	out := t.TempDir()
	code, stdout, stderr := runCLI("--song", "9", "-o", out, src)
	assert.Equal(t, exitDeviations, code)
	assert.Empty(t, stdout, "nothing written on an unknown key")
	assert.Equal(t, 0, countWavs(t, out))
	assert.Contains(t, stderr, "no song 9")
	assert.Contains(t, stderr, "--list")
}

// TestSongFlagMalformedKeyIsUsageError verifies that a malformed --song key is
// a usage error, not a fatal crash.
func TestSongFlagMalformedKeyIsUsageError(t *testing.T) {
	src := writeDisc(t, twoSongTracerDisc())
	code, _, _ := runCLI("--song", "x.y", "-o", t.TempDir(), src)
	assert.Equal(t, exitUsage, code)
}

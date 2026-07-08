package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// runCLI invokes run with captured stdout/stderr and returns the exit code
// alongside both streams, so tests can assert on the manifest/diagnostics
// split.
func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errBuf bytes.Buffer
	code = run(args, &out, &errBuf)
	return code, out.String(), errBuf.String()
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

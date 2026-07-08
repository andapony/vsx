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

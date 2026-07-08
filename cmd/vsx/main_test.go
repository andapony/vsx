package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestOpenableSourceSucceedsWithEmptyManifest verifies that a source which can
// be opened exits zero and produces no diagnostics. The format walk is not yet
// implemented, so the stdout manifest is empty in this foundation slice; the
// test locks in the exit-code and stream-split plumbing.
func TestOpenableSourceSucceedsWithEmptyManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "src.img")
	require.NoError(t, os.WriteFile(path, []byte("placeholder"), 0o644))

	code, stdout, stderr := runCLI(path)
	assert.Zero(t, code, "opening a valid source with no deviations exits zero")
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

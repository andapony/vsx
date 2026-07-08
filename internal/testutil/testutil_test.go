package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSkipT records whether Skip was invoked, standing in for *testing.T so
// RequireMedia's skip behavior can be observed without skipping the real test.
type fakeSkipT struct {
	skipped bool
}

func (f *fakeSkipT) Helper()              {}
func (f *fakeSkipT) Skipf(string, ...any) { f.skipped = true }

// TestRequireMediaSkipsWhenUnset verifies that RequireMedia skips the test and
// returns an empty path when VSX_TEST_MEDIA is not set, so media-dependent
// tests do not run (or fail) on a machine without the corpus.
func TestRequireMediaSkipsWhenUnset(t *testing.T) {
	t.Setenv(MediaEnv, "")
	f := &fakeSkipT{}
	got := RequireMedia(f)
	assert.True(t, f.skipped, "should skip when the media env var is unset")
	assert.Empty(t, got)
}

// TestRequireMediaReturnsPathWhenSet verifies that RequireMedia returns the
// configured media directory (and does not skip) when VSX_TEST_MEDIA is set.
func TestRequireMediaReturnsPathWhenSet(t *testing.T) {
	t.Setenv(MediaEnv, "/media/vsx-corpus")
	f := &fakeSkipT{}
	got := RequireMedia(f)
	assert.False(t, f.skipped, "should not skip when the media env var is set")
	assert.Equal(t, "/media/vsx-corpus", got)
}

// TestPCMHashIsStableGoldenVector verifies that PCMHash produces a fixed,
// reproducible digest for a known sample vector. The expected value is
// computed independently (sha256 over the little-endian int32 encoding of the
// samples), so the test pins the hashing contract rather than restating it.
func TestPCMHashIsStableGoldenVector(t *testing.T) {
	samples := []int32{0, 1, -1, 32767, -2147483648}
	const want = "97bc76c43c79a7b396b47e98fa0d13f7b3b57af18211ae27502ea185a7767c9b"
	assert.Equal(t, want, PCMHash(samples))
}

// TestPCMHashIsSensitiveToSamples verifies that changing a single sample
// changes the digest, so a golden-master hash actually detects PCM
// regressions rather than collapsing distinct outputs together.
func TestPCMHashIsSensitiveToSamples(t *testing.T) {
	base := PCMHash([]int32{1, 2, 3})
	require.Len(t, base, 64, "sha256 hex digest length")
	assert.NotEqual(t, base, PCMHash([]int32{1, 2, 4}))
	assert.NotEqual(t, base, PCMHash([]int32{1, 2, 3, 0}), "trailing sample must matter")
}

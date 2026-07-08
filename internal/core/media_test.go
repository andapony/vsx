package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andapony/vsx/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findVR9Dumps returns the media-directory files that Extract identifies and
// extracts as VS-880EX CD archives. It is how the media tests locate real
// corpus discs without hard-coding filenames.
func findVR9Dumps(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, pat := range []string{"*.bin", "*.img", "*.iso"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, m := range matches {
			if _, err := Extract(m, Options{}); err == nil {
				out = append(out, m)
			}
		}
	}
	return out
}

// TestVR9MediaStructuralInvariants runs the extractor against real VS-880EX CD
// media (when VSX_TEST_MEDIA is set) and asserts the §5.5/§7 structural
// invariants hold on genuine discs: every emitted v-track has a code in the VR9
// 16×8 grid, a decoded bit depth, and a native sample rate. A block that slipped
// past §5.5 validation (an archive-header copy or a song-boundary block) would
// surface here as an out-of-range code or a decode failure. The walk terminating
// at the §10 filler and the event-log count bound (§9 remnants) are what keep
// the enumeration honest; both are unit-tested above.
func TestVR9MediaStructuralInvariants(t *testing.T) {
	dir := testutil.RequireMedia(t)
	dumps := findVR9Dumps(t, dir)
	if len(dumps) == 0 {
		t.Skipf("no VS-880EX CD dumps found under %s", dir)
	}
	for _, path := range dumps {
		r, err := Extract(path, Options{})
		require.NoError(t, err)
		n := 0
		for tr, err := range r.Tracks() {
			require.NoError(t, err)
			n++
			assert.GreaterOrEqual(t, tr.Track, 1)
			assert.LessOrEqual(t, tr.Track, 16, "VR9 has 16 physical tracks")
			assert.GreaterOrEqual(t, tr.VTrack, 1)
			assert.LessOrEqual(t, tr.VTrack, 8, "VR9 has 8 v-tracks per track")
			assert.Contains(t, []int{16, 24}, tr.PCM.BitDepth)
			assert.Positive(t, tr.Take.SampleRate)
		}
		t.Logf("%s: %d v-tracks extracted", filepath.Base(path), n)
	}
}

// TestVR9HDDtoCDCrossCheck is the ready, skipped cross-check slot the issue
// calls for: when both an HDD image and a CD backup of the same song exist, the
// two must extract to byte-identical PCM (§5.7). It stays skipped until HDD
// extraction (a later slice) and matching media are both available.
func TestVR9HDDtoCDCrossCheck(t *testing.T) {
	dir := testutil.RequireMedia(t)
	hdd := filepath.Join(dir, "vs-880ex.img")
	if _, err := os.Stat(hdd); err != nil {
		t.Skip("HDD↔CD cross-check pending: no HDD image and matching CD backup in the corpus")
	}
	t.Skip("HDD↔CD cross-check pending HDD extraction support (later slice)")
}

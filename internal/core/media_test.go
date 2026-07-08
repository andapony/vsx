package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andapony/vsx/internal/cd"
	"github.com/andapony/vsx/internal/hdd"
	"github.com/andapony/vsx/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findDumps returns the media-directory files whose archive signature matches
// sig — how the media tests locate real corpus discs of a given machine without
// hard-coding filenames. Signature-matching (not extraction success) is the
// discriminator, since Extract now accepts both machines.
func findDumps(t *testing.T, dir, sig string) []string {
	t.Helper()
	var out []string
	for _, pat := range []string{"*.bin", "*.img", "*.iso"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, m := range matches {
			if dumpSignature(m) == sig {
				out = append(out, m)
			}
		}
	}
	return out
}

// dumpSignature reads a dump's 32-byte archive signature at user-data offset 0
// (§5.2), returning "" if the file is not usable CD geometry.
func dumpSignature(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	img, err := cd.New(f, info.Size())
	if err != nil {
		return ""
	}
	sig, err := img.ReadUserData(0, 32)
	if err != nil {
		return ""
	}
	return string(sig)
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
	dumps := findDumps(t, dir, sigVR9)
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

// TestVR5MediaStructuralInvariants runs the extractor against real VS-1880 CD
// media (when VSX_TEST_MEDIA is set) and asserts the §6.1/§7 structural
// invariants on genuine discs: every emitted v-track sits in the VR5 18×16 grid
// (the 288-entry positional table bound), decodes to a real bit depth (24-bit
// for the MTP default), and carries a native sample rate. A block that slipped
// past §5.5 validation, or a table read that ran past the 288-entry bound into
// §9 remnants, would surface here as an out-of-range track/v-track or a decode
// failure.
func TestVR5MediaStructuralInvariants(t *testing.T) {
	dir := testutil.RequireMedia(t)
	dumps := findDumps(t, dir, sigVR5)
	if len(dumps) == 0 {
		t.Skipf("no VS-1880 CD dumps found under %s", dir)
	}
	for _, path := range dumps {
		r, err := Extract(path, Options{})
		require.NoError(t, err)
		n := 0
		for tr, err := range r.Tracks() {
			require.NoError(t, err)
			n++
			assert.GreaterOrEqual(t, tr.Track, 1)
			assert.LessOrEqual(t, tr.Track, 18, "VR5 has 18 physical tracks")
			assert.GreaterOrEqual(t, tr.VTrack, 1)
			assert.LessOrEqual(t, tr.VTrack, 16, "VR5 has 16 v-tracks per track")
			assert.Contains(t, []int{16, 24}, tr.PCM.BitDepth)
			assert.Positive(t, tr.Take.SampleRate)
			// The take was resolved by a take-reference field in the archive's
			// cluster space (§5.7): a populated v-track carries the first
			// archive cluster its timeline drew from.
			assert.Positive(t, tr.Take.FirstCluster, "populated VR5 take resolves in archive cluster space")
		}
		t.Logf("%s: %d v-tracks extracted", filepath.Base(path), n)
	}
}

// findHDDImages returns the media-directory files that open as Roland VS live
// disks — how the HDD media test locates real corpus images without hard-coding
// filenames. hdd.Open succeeding (a partition BPB carries the "Roland  " OEM ID,
// §4.1) is the discriminator.
func findHDDImages(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, pat := range []string{"*.img", "*.bin", "*.iso"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, m := range matches {
			f, err := os.Open(m)
			if err != nil {
				continue
			}
			info, err := f.Stat()
			if err != nil {
				f.Close()
				continue
			}
			_, herr := hdd.Open(f, info.Size())
			f.Close()
			if herr == nil {
				out = append(out, m)
			}
		}
	}
	return out
}

// TestHDDMediaStructuralInvariants runs the extractor against real Roland VS
// live-disk images (when VSX_TEST_MEDIA is set) and asserts the §4 structural
// invariants on genuine media: the 12-partition MBR (including the extended
// offsets §4.1 warns a 4-entry parser misses) and byte-swapped FAT16 walk yield
// songs; every emitted v-track sits in its machine's track/v-track grid, decodes
// to a real bit depth (implying the §4.2 byte-pair unswap is correct — a wrong
// swap would corrupt the codec stream), carries a native rate, and resolves a
// take by filename (§4.3). A disk hosting both machines' song directories
// extracts each by extension.
func TestHDDMediaStructuralInvariants(t *testing.T) {
	dir := testutil.RequireMedia(t)
	images := findHDDImages(t, dir)
	if len(images) == 0 {
		t.Skipf("no Roland VS HDD images found under %s", dir)
	}
	for _, path := range images {
		r, err := Extract(path, Options{})
		require.NoError(t, err)
		n := 0
		for tr, err := range r.Tracks() {
			require.NoError(t, err)
			n++
			assert.GreaterOrEqual(t, tr.Track, 1)
			assert.LessOrEqual(t, tr.Track, 18, "no machine exceeds 18 physical tracks")
			assert.GreaterOrEqual(t, tr.VTrack, 1)
			assert.LessOrEqual(t, tr.VTrack, 16, "no machine exceeds 16 v-tracks per track")
			assert.Contains(t, []int{16, 24}, tr.PCM.BitDepth)
			assert.Positive(t, tr.Take.SampleRate)
			assert.Positive(t, tr.Take.ClusterSize, "the BPB cluster size reached the decode metadata (§4.2)")
			// A populated v-track resolved a take by filename (§4.3): its first
			// FAT cluster (the event's 0x14) is a real, positive cluster number.
			assert.Positive(t, tr.Take.FirstCluster, "populated HDD take resolves to a FAT cluster")
		}
		assert.Positive(t, n, "a Roland HDD image extracts at least one v-track")
		t.Logf("%s: %d v-tracks extracted", filepath.Base(path), n)
	}
}

// TestVR9HDDtoCDCrossCheck is the ready, skipped cross-check slot the issue
// calls for: when both an HDD image and a CD backup of the same song exist, the
// two must extract to byte-identical PCM (§5.7). HDD extraction now exists (this
// slice); it stays skipped only until matching HDD+CD media of one song are in
// the corpus so the pairing can be named.
func TestVR9HDDtoCDCrossCheck(t *testing.T) {
	dir := testutil.RequireMedia(t)
	hddImg := filepath.Join(dir, "vs-880ex.img")
	if _, err := os.Stat(hddImg); err != nil {
		t.Skip("HDD↔CD cross-check pending: no HDD image and matching CD backup in the corpus")
	}
	t.Skip("HDD↔CD cross-check pending a named HDD+CD song pairing in the corpus")
}

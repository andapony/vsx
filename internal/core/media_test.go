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

// discGroup is one backup set discovered in the corpus: the ordered disc-dump
// paths that share a set ID, with the machine their signature names.
type discGroup struct {
	setID   [4]byte
	machine machine
	paths   []string // one per disc, any order
}

// findMultiDiscSets groups the corpus's CD dumps by §5.2 set ID and returns the
// groups holding more than one disc — the real multi-disc backup sets, without
// hard-coding filenames or a directory convention. A set's discs may be spread
// across the flat media directory, so grouping is by set ID, not by folder.
func findMultiDiscSets(t *testing.T, dir string) []discGroup {
	t.Helper()
	groups := map[[4]byte]*discGroup{}
	for _, pat := range []string{"*.bin", "*.img", "*.iso"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, m := range matches {
			mach, h, ok := readDiscHeader(m)
			if !ok {
				continue
			}
			g := groups[h.SetID]
			if g == nil {
				g = &discGroup{setID: h.SetID, machine: mach}
				groups[h.SetID] = g
			}
			g.paths = append(g.paths, m)
		}
	}
	var out []discGroup
	for _, g := range groups {
		if len(g.paths) > 1 {
			out = append(out, *g)
		}
	}
	return out
}

// readDiscHeader opens a corpus file and reads its machine (from the archive
// signature) and §5.2 set-membership header, reporting ok only for a readable CD
// archive of a known machine.
func readDiscHeader(path string) (machine, cd.ArchiveHeader, bool) {
	f, err := os.Open(path)
	if err != nil {
		return machineUnknown, cd.ArchiveHeader{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return machineUnknown, cd.ArchiveHeader{}, false
	}
	img, err := cd.New(f, info.Size())
	if err != nil {
		return machineUnknown, cd.ArchiveHeader{}, false
	}
	sig, err := img.ReadUserData(0, 32)
	if err != nil {
		return machineUnknown, cd.ArchiveHeader{}, false
	}
	m := machineForSig(string(sig), machineUnknown)
	if m == machineUnknown {
		return machineUnknown, cd.ArchiveHeader{}, false
	}
	h, err := img.ArchiveHeader()
	if err != nil {
		return machineUnknown, cd.ArchiveHeader{}, false
	}
	return m, h, true
}

// linkSetDir symlinks a set's disc dumps into a fresh temp directory so Extract
// can group them as one Source — pointing directly at the flat corpus would mix
// every set together.
func linkSetDir(t *testing.T, g discGroup) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range g.paths {
		abs, err := filepath.Abs(p)
		require.NoError(t, err)
		require.NoError(t, os.Symlink(abs, filepath.Join(dir, filepath.Base(p))))
	}
	return dir
}

// discsMissingFiller and setsWithForeignSpan record the corpus's *known-imperfect*
// rips: dumps that are genuinely truncated or a genuinely foreign/mis-ordered
// continuation disc, so the extractor is behaving correctly (ADR-0002 best-effort)
// when it flags them. The multi-disc test below asserts each listed deviation
// *is* present for exactly its entry and *absent* everywhere else, so a new
// clean-corpus regression still fails the test while a known-bad rip is an
// expected condition rather than a failure (issue #24). Each entry says *why*
// it is imperfect; if a future re-rip makes an entry pristine the "expected
// imperfect" assertion trips, which is the signal to delete it here.
//
// Note on the filename oddities the issue flagged as suspects: vs-cd-6a + vs-cd-6c
// (set c51208e2) and vs-cd-9 + vs-cd-10b (set 76c5075e) are *complete* two-disc
// sets — the discs carry §5.2 indices 0 and 1 regardless of the letters in their
// filenames — so they span cleanly and are deliberately NOT listed here; asserting
// them clean is the correct expectation.

// discsMissingFiller are corpus discs with no trailing §10 TDI filler run:
// truncated/incomplete dumps whose data end the walk correctly falls back to
// end-of-data for (with a warning). Keyed by basename.
var discsMissingFiller = map[string]string{
	"vs-cd-7a.bin": "truncated dump (disc 0 of set c53305e5); no trailing §10 TDI filler run",
	"vs-cd-8.bin":  "truncated dump (disc 0 of set f9b305d5); no trailing §10 TDI filler run",
}

// setsWithForeignSpan are corpus backup sets whose continuation disc's block-0
// header does not repeat a spanned file's header (§5.6): a genuinely foreign or
// mis-ordered continuation disc, exactly the condition §5.6 verification exists
// to flag. Keyed by setIDString(g.setID) output (the "set <hex>" form), so the
// lookup key is the exact string the extractor uses in its deviation messages.
var setsWithForeignSpan = map[string]string{
	"set c53305e5": "continuation disc vs-cd-7b.bin does not repeat the spanned file's header (§5.6)",
	"set f9b305d5": "continuation disc vs-cd-14.bin does not repeat the spanned file's header (§5.6)",
}

// TestMultiDiscMediaStructuralInvariants runs the extractor against real
// multi-disc CD backup sets (when VSX_TEST_MEDIA is set) and asserts the §5.6
// spanning invariants on genuine media. It separates two claims the old test
// conflated (issue #24): *the extractor behaved correctly* and *the corpus is
// pristine*. The structural invariants — every emitted v-track sits in its grid,
// decodes to a real bit depth and rate, and at least one v-track comes out —
// apply to every set unconditionally. The pristineness checks (every disc ends
// in a §10 TDI filler run so junctions measure against burned-data ends; every
// span reconstructs flush with no §5.6 deviation) apply to every set *except*
// the known-imperfect discs and sets above, for which the deviation is asserted
// to be present instead — so a mis-located boundary or dropped junction byte on
// a good rip still surfaces here as a regression.
func TestMultiDiscMediaStructuralInvariants(t *testing.T) {
	dir := testutil.RequireMedia(t)
	sets := findMultiDiscSets(t, dir)
	if len(sets) == 0 {
		t.Skipf("no multi-disc CD backup sets found under %s", dir)
	}
	for _, g := range sets {
		// Filler signature: a finalized disc ends in a detectable §10 run, which
		// is what the junction arithmetic is measured against. A known-truncated
		// dump is expected to lack it; every other disc must have it.
		for _, p := range g.paths {
			base := filepath.Base(p)
			f, err := os.Open(p)
			require.NoError(t, err)
			info, err := f.Stat()
			require.NoError(t, err)
			img, err := cd.New(f, info.Size())
			require.NoError(t, err)
			_, ok := img.FillerStart()
			f.Close()
			if why, imperfect := discsMissingFiller[base]; imperfect {
				assert.False(t, ok, "%s is a known-imperfect rip (%s); if it now ends in a §10 filler run it was re-ripped clean — remove it from discsMissingFiller", base, why)
			} else {
				assert.True(t, ok, "%s: a finalized disc ends with a TDI filler run (§10)", base)
			}
		}

		r, err := Extract(linkSetDir(t, g), Options{})
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
		}
		assert.Positive(t, n, "a multi-disc set extracts at least one v-track")

		// §5.6 spanning: a complete set reconstructs every span flush (no
		// remainder ran off the last disc, no disc index missing, every junction
		// header matched). A known foreign/mis-ordered set is expected to raise
		// exactly this deviation; every other set must not.
		setID := setIDString(g.setID)
		_, foreign := setsWithForeignSpan[setID]
		sawForeignSpan := false
		for _, d := range r.Deviations() {
			if d.SpecRef != "§5.6" {
				continue
			}
			sawForeignSpan = true
			assert.True(t, foreign,
				"%s raised an unexpected §5.6 spanning deviation (a clean set spanned wrong, or a new imperfect rip entered the corpus): %s", setID, d.Message)
		}
		if why, imperfect := setsWithForeignSpan[setID]; imperfect {
			assert.True(t, sawForeignSpan,
				"%s is a known foreign/mis-ordered set (%s) and should still raise a §5.6 deviation; if it now spans cleanly it was re-ripped/re-ordered — remove it from setsWithForeignSpan", setID, why)
		}
		t.Logf("%s (%d discs): %d v-tracks extracted", setID, len(g.paths), n)
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

// TestCorruptSectorMediaDetectsKnownBadFrame verifies the §10 per-frame EDC
// damage detector against real damaged media (when VSX_TEST_MEDIA is set):
// vs-cd-6a.bin has exactly one physically corrupt sector — frame 313043, whose
// stored EDC 1ba752d3 disagrees with the computed 53b3b48f, traced during
// VS-880EX corpus verification (issue #16) — and the detector must name that
// frame and no other.
func TestCorruptSectorMediaDetectsKnownBadFrame(t *testing.T) {
	dir := testutil.RequireMedia(t)
	f, err := os.Open(filepath.Join(dir, "vs-cd-6a.bin"))
	if err != nil {
		t.Skipf("vs-cd-6a.bin not present under %s", dir)
	}
	defer f.Close()
	info, err := f.Stat()
	require.NoError(t, err)
	img, err := cd.New(f, info.Size())
	require.NoError(t, err)

	corrupt, err := img.CorruptFrames()
	require.NoError(t, err)
	assert.Equal(t, []int{313043}, corrupt, "the one known corrupt sector is named, and no other")
}

// TestHDDListKeysAreUnique runs List against real Roland VS HDD images (when
// VSX_TEST_MEDIA is set) and asserts every song's SongKey is distinct. The key
// is partition.enumeration-index (PP.OOO), standing in for the VS device's own
// song number precisely because that number is not unique across a
// multi-partition disk; a regression that let two songs collide on the same
// key would silently overwrite one song's output folder with another's.
func TestHDDListKeysAreUnique(t *testing.T) {
	dir := testutil.RequireMedia(t)
	images := findHDDImages(t, dir)
	if len(images) == 0 {
		t.Skipf("no HDD images under %s", dir)
	}
	for _, path := range images {
		songs, _, err := List(path, Options{})
		require.NoError(t, err)
		seen := map[SongKey]bool{}
		for _, s := range songs {
			assert.False(t, seen[s.Key], "%s: duplicate key %s", filepath.Base(path), s.Key)
			seen[s.Key] = true
		}
		t.Logf("%s: %d songs, all keys unique", filepath.Base(path), len(songs))
	}
}

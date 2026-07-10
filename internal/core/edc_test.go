package core

import (
	"strings"
	"testing"

	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEDCDeviationsNameSingleFrame is the §10 damage detector's reporting
// contract (acceptance criterion 1): one EDC-broken frame becomes exactly one
// error deviation naming that frame.
func TestEDCDeviationsNameSingleFrame(t *testing.T) {
	devs := edcDeviations("disc", []int{313043})
	require.Len(t, devs, 1)
	d := devs[0]
	assert.Equal(t, "disc", d.Location)
	assert.Equal(t, "§10", d.SpecRef)
	assert.Equal(t, SeverityError, d.Severity)
	assert.Contains(t, d.Message, "313043", "the deviation names the corrupt frame")
}

// TestEDCDeviationsNoneWhenClean confirms a clean disc raises nothing.
func TestEDCDeviationsNoneWhenClean(t *testing.T) {
	assert.Nil(t, edcDeviations("disc", nil))
}

// TestEDCDeviationsCoalesceContiguousRun keeps a damaged region from flooding the
// report: a run of adjacent corrupt frames collapses to one deviation spanning
// the range, while a gap starts a new one.
func TestEDCDeviationsCoalesceContiguousRun(t *testing.T) {
	devs := edcDeviations("disc", []int{10, 11, 12, 20})
	require.Len(t, devs, 2, "the 10–12 run coalesces; frame 20 is separate")

	assert.Contains(t, devs[0].Message, "10")
	assert.Contains(t, devs[0].Message, "12", "the range names both ends")
	assert.Contains(t, devs[0].Message, "3", "the range reports its sector count")
	assert.Contains(t, devs[1].Message, "20")
	assert.NotContains(t, devs[1].Message, "–", "a lone frame is not rendered as a range")

	for _, d := range devs {
		assert.Equal(t, "§10", d.SpecRef)
		assert.Equal(t, SeverityError, d.Severity)
		assert.True(t, strings.Contains(d.Message, "EDC"), "each deviation cites EDC")
	}
}

// corruptSectorDevs keeps only the §10 corrupt-raw-dump-sector deviations.
func corruptSectorDevs(devs []Deviation) []Deviation {
	var out []Deviation
	for _, d := range devs {
		if d.SpecRef == "§10" && strings.Contains(d.Message, "corrupt raw-dump sector") {
			out = append(out, d)
		}
	}
	return out
}

// TestExtractReportsCorruptSector is the end-to-end §10 contract: a raw disc with
// one EDC-broken frame extracts best-effort — every track is still produced —
// while surfacing exactly one corrupt-sector deviation naming the damaged frame.
// The break flips the frame's stored EDC only, leaving its user data intact, so
// the walk and decode are untouched and the single deviation is unambiguously the
// EDC check's.
func TestExtractReportsCorruptSector(t *testing.T) {
	disc := vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{{
			Number: 1, Name: "DAMAGED",
			Takes:  []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: silentMT2(4)}},
			Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1}},
		}},
	}
	raw := disc.BuildRaw()

	const badFrame = 7
	raw[badFrame*2352+2064] ^= 0xFF // flip one stored-EDC byte; user data untouched

	tracks, devs := collectTracks(t, mustExtractBytes(t, raw, Options{}))
	require.Len(t, tracks, 1, "best-effort still extracts the populated v-track")

	cs := corruptSectorDevs(devs)
	require.Len(t, cs, 1, "exactly one corrupt-sector deviation")
	assert.Contains(t, cs[0].Message, "7", "the deviation names the damaged frame")
}

// TestExtractCleanDiscHasNoCorruptSectorDeviation guards the detector against
// false positives: a spec-faithful raw disc — whose fixture burns a correct EDC
// per frame — extracts with no §10 corrupt-sector deviation.
func TestExtractCleanDiscHasNoCorruptSectorDeviation(t *testing.T) {
	disc := vsfix.Disc{
		SetID: [4]byte{1, 2, 3, 4},
		Songs: []vsfix.Song{{
			Number: 1, Name: "CLEAN",
			Takes:  []vsfix.Take{{FileID: 0x0100, Name: "TAKE0100", MT2: silentMT2(4)}},
			Events: []vsfix.Event{{Start: 12, End: 16, FileID: 0x0100, Track: 1, VTrack: 1}},
		}},
	}
	_, devs := collectTracks(t, mustExtractBytes(t, disc.BuildRaw(), Options{}))
	assert.Empty(t, corruptSectorDevs(devs), "a clean disc raises no corrupt-sector deviation")
}

package core

import (
	"fmt"

	"github.com/andapony/vsx/internal/cd"
)

// edcDeviations turns a raw disc's corrupt-frame indices (ascending, from
// cd.Image.CorruptFrames) into best-effort §10 deviations: one per contiguous run
// of failed-EDC sectors, so a damaged region collapses to a single reported range
// instead of flooding the manifest one frame at a time. location names the disc.
//
// A corrupt sector is a physically misread frame the codec decodes into noise
// with no other signal (ROLAND-VS-FORMAT-SPEC.md §10), so each is an error: audio
// at that sector may be garbage. best-effort keeps decoding; --strict aborts on
// the first one as usual.
func edcDeviations(location string, corrupt []int) []Deviation {
	var devs []Deviation
	for i := 0; i < len(corrupt); {
		j := i + 1
		for j < len(corrupt) && corrupt[j] == corrupt[j-1]+1 {
			j++
		}
		lo, hi := corrupt[i], corrupt[j-1]
		var msg string
		if lo == hi {
			msg = fmt.Sprintf("corrupt raw-dump sector: frame %d fails its MODE1 EDC (§10); "+
				"the codec decodes it into noise", lo)
		} else {
			msg = fmt.Sprintf("corrupt raw-dump sectors: frames %d–%d (%d sectors) fail their MODE1 EDC (§10); "+
				"the codec decodes them into noise", lo, hi, hi-lo+1)
		}
		devs = append(devs, Deviation{Location: location, SpecRef: "§10", Severity: SeverityError, Message: msg})
		i = j
	}
	return devs
}

// setEDCDeviations runs the §10 per-frame EDC scan across a backup set's discs,
// labelling each disc's corrupt sectors by its basename so a report names the
// physical disc a damaged frame lives on. It is a no-op unless the set's reader
// is a *cd.Set (always so for an assembled set); a cooked disc contributes no EDC
// and so no deviation.
func setEDCDeviations(set *assembledSet) []Deviation {
	cset, ok := set.reader.(*cd.Set)
	if !ok {
		return nil
	}
	perDisc, err := cset.CorruptFrames()
	if err != nil {
		return []Deviation{edcSkippedDeviation("backup set", err)}
	}
	var devs []Deviation
	for i, corrupt := range perDisc {
		devs = append(devs, edcDeviations(discLabel(set.discs, i), corrupt)...)
	}
	return devs
}

// discLabel names disc position i for a deviation: its basename, or "disc i" when
// the input carries no name (an in-memory disc in tests).
func discLabel(discs []discInput, i int) string {
	if i < len(discs) && discs[i].name != "" {
		return discs[i].name
	}
	return fmt.Sprintf("disc %d", i)
}

// edcScanDeviations reports the §10 outcome of one raw disc's EDC scan: the
// corrupt-sector runs when the scan completed, or — when it could not read — a
// single warning that the check was skipped, so a scan I/O failure downgrades to
// "corruption may go undetected" rather than aborting a best-effort extraction.
func edcScanDeviations(location string, corrupt []int, err error) []Deviation {
	if err != nil {
		return []Deviation{edcSkippedDeviation(location, err)}
	}
	return edcDeviations(location, corrupt)
}

// edcSkippedDeviation is the §10 warning raised when a raw disc's EDC scan could
// not read the dump: the check was skipped, so a corrupt sector may pass
// unreported. Both the single-disc and set scan paths raise it identically.
func edcSkippedDeviation(location string, err error) Deviation {
	return Deviation{Location: location, SpecRef: "§10", Severity: SeverityWarning,
		Message: fmt.Sprintf("could not verify per-frame EDC: %v; corrupt sectors may go undetected", err)}
}

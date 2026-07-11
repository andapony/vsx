package core

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/andapony/vsx/internal/cd"
)

// discInput is one candidate disc of a backup set as bytes rather than a path:
// a random-access read surface, its length, a name for deviation messages, and
// a close func (nil for an in-memory source) run once the disc is done with.
// openDev, when non-nil, is a disc the path adapter could not open or stat —
// openBackupSet reports it and moves on, so file-I/O failures stay in the path
// layer while the byte-level assembly (grouping, ordering, spanning) is driven
// entirely from these inputs and so runs diskless in tests.
type discInput struct {
	r       io.ReaderAt
	size    int64
	name    string
	close   func()
	openDev *Deviation
}

// closeInput runs the input's close func if it has one (in-memory inputs do not).
func (in discInput) closeInput() {
	if in.close != nil {
		in.close()
	}
}

// setDisc is one opened, classified disc of a candidate backup set: its byte
// input, its CD image, the machine its signature names, and the §5.2
// set-membership fields it is grouped and ordered by.
type setDisc struct {
	in      discInput
	img     *cd.Image
	machine machine
	setID   [4]byte
	index   uint16
	total   uint16
	cooked  bool
}

// name is the disc's basename, carried on its input for deviation messages.
func (d setDisc) name() string { return d.in.name }

// assembledSet is a directory grouped down to one backup set ready to extract:
// a stitched multi-disc reader (§5.6), the machine to read it as, and the
// deviations found while grouping (foreign files, missing discs). discs are the
// chosen set's byte inputs, kept open for the lazy walk and closed when it ends.
type assembledSet struct {
	reader  cdSource
	machine machine
	cooked  bool
	discs   []discInput
	devs    []Deviation
}

// openBackupSet groups the given disc byte-inputs into a single backup set
// (§5.2/§5.6): it classifies each as a CD archive, keeps those of one set
// (chosen as described in chooseSet), orders them by disc index, and builds a
// stitched reader over the contiguous run from disc 0. Foreign files (a
// different set ID or a non-archive file) and missing discs are reported as
// deviations rather than failing the run; a set with no index-0 anchor cannot
// be read and is a hard error. It opens no files and closes only the inputs it
// discards, so the whole assembly is exercised from in-memory bytes in tests.
func openBackupSet(discs []discInput, opts Options) (*assembledSet, error) {
	var classified []setDisc
	var devs []Deviation
	for _, in := range discs {
		if in.openDev != nil {
			devs = append(devs, *in.openDev)
			continue
		}
		d, dev, ok := classifyDisc(in, opts)
		if dev != nil {
			devs = append(devs, *dev)
		}
		if ok {
			classified = append(classified, d)
		}
	}
	if len(classified) == 0 {
		return nil, fmt.Errorf("core: no CD backup-set discs found in the given source")
	}

	chosen, setID, foreign := chooseSet(classified)
	for _, d := range foreign {
		devs = append(devs, Deviation{Location: d.name(), SpecRef: "§5.2", Severity: SeverityWarning,
			Message: fmt.Sprintf("disc belongs to a different backup set (%s, not the chosen %s); skipped",
				setIDString(d.setID), setIDString(setID))})
		d.in.closeInput()
	}

	ordered, orderDevs := orderDiscs(chosen)
	devs = append(devs, orderDevs...)
	if len(ordered) == 0 {
		closeDiscs(chosen)
		return nil, fmt.Errorf("core: backup set %s has no disc index 0; cannot read it", setIDString(setID))
	}

	imgs := make([]*cd.Image, len(ordered))
	inputs := make([]discInput, len(ordered))
	cooked := false
	for i, d := range ordered {
		imgs[i] = d.img
		inputs[i] = d.in
		cooked = cooked || d.cooked
	}
	set, err := cd.NewSet(imgs)
	if err != nil {
		closeDiscs(ordered)
		return nil, fmt.Errorf("core: %w", err)
	}
	reseamed := reseamTruncatedJunctions(set, imgs, formatFor(ordered[0].machine))
	for _, pos := range set.MissingFiller() {
		switch {
		case reseamed[pos]:
			devs = append(devs, Deviation{Location: ordered[pos].name(), SpecRef: "§10", Severity: SeverityWarning,
				Message: fmt.Sprintf("disc index %d lacks a trailing TDI filler run; its data end was reconstructed from the continuation disc's junction (§5.6), so later discs and the spanning file are recovered", ordered[pos].index)})
		case pos < len(ordered)-1:
			devs = append(devs, Deviation{Location: ordered[pos].name(), SpecRef: "§10", Severity: SeverityError,
				Message: fmt.Sprintf("disc index %d lacks a trailing TDI filler run; its data end was estimated to a block boundary so later discs still enumerate, but the file spanning the junction may be corrupt", ordered[pos].index)})
		default:
			devs = append(devs, Deviation{Location: ordered[pos].name(), SpecRef: "§10", Severity: SeverityError,
				Message: fmt.Sprintf("disc index %d lacks a trailing TDI filler run; its data end was estimated, which may corrupt a spanned file", ordered[pos].index)})
		}
	}

	return &assembledSet{
		reader:  set,
		machine: ordered[0].machine,
		cooked:  cooked,
		discs:   inputs,
		devs:    devs,
	}, nil
}

// discInputsFromPaths opens each path into a discInput — the path adapter's half
// of the ReaderAt seam. A file that cannot be opened or statted becomes a
// discInput carrying only its openDev deviation, so openBackupSet reports the
// file-I/O failure (the sole path-specific deviation) in source order without
// core needing to touch the filesystem.
func discInputsFromPaths(paths []string) []discInput {
	out := make([]discInput, 0, len(paths))
	for _, p := range paths {
		name := filepath.Base(p)
		f, err := os.Open(p)
		if err != nil {
			out = append(out, discInput{name: name, openDev: &Deviation{Location: name, SpecRef: "§5", Severity: SeverityWarning,
				Message: fmt.Sprintf("could not open file: %v; skipped", err)}})
			continue
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			out = append(out, discInput{name: name, openDev: &Deviation{Location: name, SpecRef: "§5", Severity: SeverityWarning,
				Message: fmt.Sprintf("could not stat file: %v; skipped", err)}})
			continue
		}
		out = append(out, discInput{r: f, size: info.Size(), name: name, close: func() { f.Close() }})
	}
	return out
}

// discPathsInDir lists a directory's non-directory entries as full paths, in
// the order os.ReadDir yields them (lexical by name) — the disc-file path list
// the directory form of a backup set consumes.
func discPathsInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	return paths, nil
}

// classifyDisc decides whether one disc input is a CD archive that can join a
// set. A file that is not CD geometry or carries no known archive signature is a
// foreign file (reported, skipped, its input closed); a readable archive disc is
// returned with its §5.2 fields. The returned deviation, when non-nil, is
// reported whether or not the disc is usable.
func classifyDisc(in discInput, opts Options) (setDisc, *Deviation, bool) {
	img, err := cd.New(in.r, in.size)
	if err != nil {
		in.closeInput()
		return setDisc{}, &Deviation{Location: in.name, SpecRef: "§5.1", Severity: SeverityWarning,
			Message: "not a CD dump (unusable frame/sector geometry); skipped as an unrelated file"}, false
	}
	sig, err := img.ReadUserData(0, 32)
	if err != nil {
		in.closeInput()
		return setDisc{}, &Deviation{Location: in.name, SpecRef: "§5.2", Severity: SeverityWarning,
			Message: fmt.Sprintf("could not read archive signature: %v; skipped", err)}, false
	}
	m := machineForSig(string(sig), opts.As.machine)
	if m == machineUnknown {
		in.closeInput()
		return setDisc{}, &Deviation{Location: in.name, SpecRef: "§5.2", Severity: SeverityWarning,
			Message: "no known archive signature; skipped as an unrelated file"}, false
	}
	h, err := img.ArchiveHeader()
	if err != nil {
		in.closeInput()
		return setDisc{}, &Deviation{Location: in.name, SpecRef: "§5.2", Severity: SeverityWarning,
			Message: fmt.Sprintf("could not read archive header: %v; skipped", err)}, false
	}
	return setDisc{in: in, img: img, machine: m, setID: h.SetID, index: h.DiscIndex, total: h.TotalDiscs, cooked: img.Cooked()}, nil, true
}

// chooseSet partitions discs by set ID and picks the primary set: the group is
// chosen to be independent of directory/filename order, preferring a group that
// has an index-0 disc (the only anchor a set can be read from), then the largest
// group, then the numerically smallest set ID. Discs outside the chosen group
// are returned as foreign.
func chooseSet(discs []setDisc) (chosen []setDisc, setID [4]byte, foreign []setDisc) {
	groups := map[[4]byte][]setDisc{}
	var order [][4]byte
	for _, d := range discs {
		if _, ok := groups[d.setID]; !ok {
			order = append(order, d.setID)
		}
		groups[d.setID] = append(groups[d.setID], d)
	}

	best := order[0]
	for _, id := range order[1:] {
		if betterSet(groups[id], groups[best]) {
			best = id
		}
	}

	for _, id := range order {
		if id == best {
			chosen = groups[id]
		} else {
			foreign = append(foreign, groups[id]...)
		}
	}
	return chosen, best, foreign
}

// betterSet reports whether group a is a better primary-set candidate than b:
// having an index-0 disc wins, then the larger group, then the smaller set ID.
func betterSet(a, b []setDisc) bool {
	ai, bi := hasIndexZero(a), hasIndexZero(b)
	if ai != bi {
		return ai
	}
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return setIDLess(a[0].setID, b[0].setID)
}

func hasIndexZero(g []setDisc) bool {
	for _, d := range g {
		if d.index == 0 {
			return true
		}
	}
	return false
}

func setIDLess(a, b [4]byte) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// orderDiscs sorts a set's discs by disc index and returns the contiguous run
// starting at index 0. A duplicate index keeps the first disc; a gap ends the
// run, and every missing index below the set's declared total — plus every disc
// stranded past the gap — is reported as a deviation. Discs that will not be
// read (duplicates, stranded) are closed here.
func orderDiscs(discs []setDisc) ([]setDisc, []Deviation) {
	sort.SliceStable(discs, func(i, j int) bool { return discs[i].index < discs[j].index })

	var devs []Deviation
	var run []setDisc
	next := uint16(0)
	total := declaredTotal(discs)
	for _, d := range discs {
		switch {
		case d.index < next: // duplicate index
			devs = append(devs, Deviation{Location: d.name(), SpecRef: "§5.2", Severity: SeverityWarning,
				Message: fmt.Sprintf("duplicate disc index %d; keeping the first, skipping this one", d.index)})
			d.in.closeInput()
		case d.index == next:
			run = append(run, d)
			next++
		default: // d.index > next: a gap
			// Report the missing indices up to this stranded disc, then strand it.
			for missing := next; missing < d.index; missing++ {
				devs = append(devs, missingDiscDeviation(missing, total))
			}
			devs = append(devs, Deviation{Location: d.name(), SpecRef: "§5.6", Severity: SeverityError,
				Message: fmt.Sprintf("disc index %d present but unreachable because an earlier disc is missing; skipped", d.index)})
			d.in.closeInput()
		}
	}
	// Report any remaining missing indices below the declared total.
	for missing := next; missing < total; missing++ {
		devs = append(devs, missingDiscDeviation(missing, total))
	}
	return run, devs
}

// declaredTotal is the set's expected disc count: the total-discs field of the
// index-0 disc when present, else the largest field seen (best effort). It is at
// least the number of discs on hand so a stale/zero field never hides a gap.
func declaredTotal(discs []setDisc) uint16 {
	var total uint16
	for _, d := range discs {
		if d.index == 0 && d.total > 0 {
			total = d.total
		}
	}
	for _, d := range discs {
		if d.total > total {
			total = d.total
		}
		if d.index+1 > total {
			total = d.index + 1
		}
	}
	return total
}

func missingDiscDeviation(index, total uint16) Deviation {
	return Deviation{Location: "backup set", SpecRef: "§5.6", Severity: SeverityError,
		Message: fmt.Sprintf("disc index %d of %d is missing; a file spanning into it is recovered only partially", index, total)}
}

// setIDString renders a 4-byte set ID as hex for deviation messages.
func setIDString(id [4]byte) string {
	return fmt.Sprintf("set %02x%02x%02x%02x", id[0], id[1], id[2], id[3])
}

// closeDiscs closes every classified disc's input — used on an error path where
// no Result will take ownership of them.
func closeDiscs(discs []setDisc) {
	for _, d := range discs {
		d.in.closeInput()
	}
}

// closeInputs closes every disc input — the teardown for a chosen set on an
// error path, and (via streamClosingInputs) once the lazy walk ends.
func closeInputs(discs []discInput) {
	for _, in := range discs {
		in.closeInput()
	}
}

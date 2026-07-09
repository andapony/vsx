package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/andapony/vsx/internal/cd"
)

// setDisc is one opened, classified disc of a candidate backup set: its file
// handle, its CD image, the machine its signature names, and the §5.2
// set-membership fields it is grouped and ordered by.
type setDisc struct {
	f       *os.File
	img     *cd.Image
	machine machine
	setID   [4]byte
	index   uint16
	total   uint16
	name    string // basename, for deviation messages
	cooked  bool
}

// assembledSet is a directory grouped down to one backup set ready to extract:
// a stitched multi-disc reader (§5.6), the machine to read it as, and the
// deviations found while grouping (foreign files, missing discs). files are the
// chosen set's disc handles, kept open for the lazy walk and closed when it ends.
type assembledSet struct {
	reader  cdSource
	machine machine
	cooked  bool
	files   []*os.File
	devs    []Deviation
}

// openBackupSet groups the given CD dump files into a single backup set
// (§5.2/§5.6): it opens every path, keeps those that are CD archives of one
// set (chosen as described in chooseSet), orders them by disc index, and builds
// a stitched reader over the contiguous run from disc 0. Foreign files (a
// different set ID or a non-archive file) and missing discs are reported as
// deviations rather than failing the run; a set with no index-0 anchor cannot
// be read and is a hard error.
func openBackupSet(paths []string, opts Options) (*assembledSet, error) {
	var discs []setDisc
	var devs []Deviation
	for _, p := range paths {
		d, dev, ok := classifyDisc(p, filepath.Base(p), opts)
		if dev != nil {
			devs = append(devs, *dev)
		}
		if ok {
			discs = append(discs, d)
		}
	}
	if len(discs) == 0 {
		return nil, fmt.Errorf("core: no CD backup-set discs found in the given source")
	}

	chosen, setID, foreign := chooseSet(discs)
	for _, d := range foreign {
		devs = append(devs, Deviation{Location: d.name, SpecRef: "§5.2", Severity: SeverityWarning,
			Message: fmt.Sprintf("disc belongs to a different backup set (%s, not the chosen %s); skipped",
				setIDString(d.setID), setIDString(setID))})
		d.f.Close()
	}

	ordered, orderDevs := orderDiscs(chosen)
	devs = append(devs, orderDevs...)
	if len(ordered) == 0 {
		closeDiscs(chosen)
		return nil, fmt.Errorf("core: backup set %s has no disc index 0; cannot read it", setIDString(setID))
	}

	imgs := make([]*cd.Image, len(ordered))
	files := make([]*os.File, len(ordered))
	cooked := false
	for i, d := range ordered {
		imgs[i] = d.img
		files[i] = d.f
		cooked = cooked || d.cooked
	}
	set, err := cd.NewSet(imgs)
	if err != nil {
		closeDiscs(ordered)
		return nil, fmt.Errorf("core: %w", err)
	}
	for _, pos := range set.MissingFiller() {
		devs = append(devs, Deviation{Location: ordered[pos].name, SpecRef: "§10", Severity: SeverityError,
			Message: fmt.Sprintf("disc index %d lacks a trailing TDI filler run; its data end was estimated, which may corrupt a spanned file", ordered[pos].index)})
	}

	return &assembledSet{
		reader:  set,
		machine: ordered[0].machine,
		cooked:  cooked,
		files:   files,
		devs:    devs,
	}, nil
}

// discPathsInDir lists a directory's non-directory entries as full paths, in
// the order os.ReadDir yields them (lexical by name) — the disc-file path list
// openBackupSet consumes for the directory form of a backup set.
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

// classifyDisc opens one directory entry and decides whether it is a CD archive
// disc that can join a set. A file that is not CD geometry or carries no known
// archive signature is a foreign file (reported, skipped); a readable archive
// disc is returned with its §5.2 fields. The returned deviation, when non-nil,
// is reported whether or not the disc is usable.
func classifyDisc(path, name string, opts Options) (setDisc, *Deviation, bool) {
	f, err := os.Open(path)
	if err != nil {
		return setDisc{}, &Deviation{Location: name, SpecRef: "§5", Severity: SeverityWarning,
			Message: fmt.Sprintf("could not open file: %v; skipped", err)}, false
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return setDisc{}, &Deviation{Location: name, SpecRef: "§5", Severity: SeverityWarning,
			Message: fmt.Sprintf("could not stat file: %v; skipped", err)}, false
	}
	img, err := cd.New(f, info.Size())
	if err != nil {
		f.Close()
		return setDisc{}, &Deviation{Location: name, SpecRef: "§5.1", Severity: SeverityWarning,
			Message: "not a CD dump (unusable frame/sector geometry); skipped as an unrelated file"}, false
	}
	sig, err := img.ReadUserData(0, 32)
	if err != nil {
		f.Close()
		return setDisc{}, &Deviation{Location: name, SpecRef: "§5.2", Severity: SeverityWarning,
			Message: fmt.Sprintf("could not read archive signature: %v; skipped", err)}, false
	}
	m := machineForSig(string(sig), opts.As.machine)
	if m == machineUnknown {
		f.Close()
		return setDisc{}, &Deviation{Location: name, SpecRef: "§5.2", Severity: SeverityWarning,
			Message: "no known archive signature; skipped as an unrelated file"}, false
	}
	h, err := img.ArchiveHeader()
	if err != nil {
		f.Close()
		return setDisc{}, &Deviation{Location: name, SpecRef: "§5.2", Severity: SeverityWarning,
			Message: fmt.Sprintf("could not read archive header: %v; skipped", err)}, false
	}
	return setDisc{f: f, img: img, machine: m, setID: h.SetID, index: h.DiscIndex, total: h.TotalDiscs, name: name, cooked: img.Cooked()}, nil, true
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
			devs = append(devs, Deviation{Location: d.name, SpecRef: "§5.2", Severity: SeverityWarning,
				Message: fmt.Sprintf("duplicate disc index %d; keeping the first, skipping this one", d.index)})
			d.f.Close()
		case d.index == next:
			run = append(run, d)
			next++
		default: // d.index > next: a gap
			// Report the missing indices up to this stranded disc, then strand it.
			for missing := next; missing < d.index; missing++ {
				devs = append(devs, missingDiscDeviation(missing, total))
			}
			devs = append(devs, Deviation{Location: d.name, SpecRef: "§5.6", Severity: SeverityError,
				Message: fmt.Sprintf("disc index %d present but unreachable because an earlier disc is missing; skipped", d.index)})
			d.f.Close()
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

// closeDiscs closes every disc's file handle — used on an error path where no
// Result will take ownership of them.
func closeDiscs(discs []setDisc) {
	for _, d := range discs {
		d.f.Close()
	}
}

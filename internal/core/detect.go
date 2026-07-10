package core

import (
	"fmt"
	"strings"

	"github.com/andapony/vsx/internal/cd"
)

// sourceKind is the physical shape of a Source: a CD "Song Copy Archive" dump
// or an HDD live-disk image. §3 fixes one Source per run.
type sourceKind int

const (
	kindUnknown sourceKind = iota
	kindCD
	kindHDD
)

// machine is the recorder family a Source was written by (§1). It selects the
// field layouts and event-record size used to read the archive.
type machine int

const (
	machineUnknown machine = iota
	machineVR5             // VS-1880
	machineVR9             // VS-880EX
)

// profile is the identified (source kind, machine) pair that selects an
// extractor.
type profile struct {
	kind    sourceKind
	machine machine
}

// SourceOverride is the typed --as override (Options.As): which aspects of
// byte-level autodetection the caller has forced. Its zero value autodetects
// everything. It is opaque — built only by ParseAs, read only inside core — so
// the invalid combinations a bare string allowed (an unknown machine name, a
// "-cd" alias) cannot be constructed.
type SourceOverride struct {
	kind    sourceKind // kindHDD/kindCD force the Source type; kindUnknown autodetects
	machine machine    // machineVR5/machineVR9 force the CD machine; machineUnknown autodetects
}

// ParseAs converts an --as override string into a typed SourceOverride, or
// returns a usage error for an unrecognized value. "" autodetects everything;
// "hdd" forces the HDD live-disk path (§4); "cd" forces the CD archive path and
// autodetects the machine by signature; "vr9"/"vr5" force the CD path as that
// machine when no archive signature is present (§5.2). It is the single place an
// --as string becomes an override, so the accepted values live in one switch.
func ParseAs(s string) (SourceOverride, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return SourceOverride{}, nil
	case "hdd":
		return SourceOverride{kind: kindHDD}, nil
	case "cd":
		return SourceOverride{kind: kindCD}, nil
	case "vr9":
		return SourceOverride{kind: kindCD, machine: machineVR9}, nil
	case "vr5":
		return SourceOverride{kind: kindCD, machine: machineVR5}, nil
	default:
		return SourceOverride{}, fmt.Errorf("core: unknown --as value %q (want hdd, cd, vr9, or vr5)", s)
	}
}

// Archive signatures at CD user-data offset 0 (§1/§5.2), 32 bytes each.
const (
	sigVR9 = "VS-8EXECR02 Song Copy Archives  "
	sigVR5 = "VS1880EXR06 Song Copy Archives  "
)

// detect identifies a Source from its bytes (§5.2). It reads the archive
// signature at user-data offset 0; a match fixes the machine. When the bytes
// are unrecognized, override (machineVR9/machineVR5, from --as) forces the
// machine; with neither a signature nor an override, detection is a hard error —
// the "genuinely unidentifiable input" case the issue calls out.
func detect(img *cd.Image, override machine) (profile, error) {
	sig, err := img.ReadUserData(0, 32)
	if err != nil {
		return profile{}, fmt.Errorf("core: reading archive signature: %w", err)
	}
	if m := machineForSig(string(sig), override); m != machineUnknown {
		return profile{kind: kindCD, machine: m}, nil
	}
	return profile{}, fmt.Errorf("core: unidentifiable source: no known archive signature at user-data offset 0 (pass --as to override)")
}

// machineForSig maps a 32-byte archive signature to a machine (§5.2), falling
// back to the override machine for a disc whose signature is absent or damaged
// (machineUnknown when neither applies). It is the single source of the
// signature→machine table, shared by detect (single disc) and the backup-set
// grouping (a directory).
func machineForSig(sig string, override machine) machine {
	switch sig {
	case sigVR9:
		return machineVR9
	case sigVR5:
		return machineVR5
	}
	return override
}

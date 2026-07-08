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

// Archive signatures at CD user-data offset 0 (§1/§5.2), 32 bytes each.
const (
	sigVR9 = "VS-8EXECR02 Song Copy Archives  "
	sigVR5 = "VS1880EXR06 Song Copy Archives  "
)

// detect identifies a Source from its bytes (§5.2). It reads the archive
// signature at user-data offset 0; a match fixes the machine. When the bytes
// are unrecognized, override ("vr9"/"vr5", the --as value) forces the profile;
// with neither a signature nor an override, detection is a hard error — the
// "genuinely unidentifiable input" case the issue calls out.
func detect(img *cd.Image, override string) (profile, error) {
	sig, err := img.ReadUserData(0, 32)
	if err != nil {
		return profile{}, fmt.Errorf("core: reading archive signature: %w", err)
	}
	if m := machineForSig(string(sig), override); m != machineUnknown {
		return profile{kind: kindCD, machine: m}, nil
	}
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "":
		return profile{}, fmt.Errorf("core: unidentifiable source: no known archive signature at user-data offset 0 (pass --as to override)")
	default:
		return profile{}, fmt.Errorf("core: unknown --as value %q (want vr9 or vr5)", override)
	}
}

// machineForSig maps a 32-byte archive signature to a machine (§5.2), falling
// back to the --as override for a disc whose signature is absent or damaged. An
// unrecognized, un-overridden signature yields machineUnknown — the single
// source of the signature→machine and --as-alias table, shared by detect (single
// disc) and the backup-set grouping (a directory).
func machineForSig(sig, override string) machine {
	switch sig {
	case sigVR9:
		return machineVR9
	case sigVR5:
		return machineVR5
	}
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "vr9", "vr9-cd":
		return machineVR9
	case "vr5", "vr5-cd":
		return machineVR5
	}
	return machineUnknown
}

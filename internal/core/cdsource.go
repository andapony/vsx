package core

import (
	"bytes"
	"fmt"
)

// cdSource is the read surface the CD chain walk and take reads need: random
// access to the archive's concatenated user-data stream, that stream's length,
// and where the disc's file data ends (the §10 TDI filler start). Both a single
// *cd.Image and a multi-disc *cd.Set satisfy it, so the machine-specific
// enumeration and replay run over a single disc or a whole backup set unchanged.
type cdSource interface {
	ReadUserData(udoff int64, n int) ([]byte, error)
	UserDataLen() int64
	FillerStart() (int64, bool)
}

// spanningSource is the capability a multi-disc set adds over a single disc:
// exposing the continuation discs' block-0 headers so a spanned file's junctions
// can be verified (§5.6). A single *cd.Image does not implement it, so the walk
// simply skips junction checks on a one-disc Source.
type spanningSource interface {
	ContinuationHeaders(dataStart, dataLen int64, n int) ([][]byte, error)
}

// verifyJunction applies the §5.6 verification step to a file whose data spans a
// disc boundary: each continuation disc's block-0 header must repeat the file's
// own header in the identity fields (filename, FileID, size). A mismatch means
// the set is mis-ordered or a continuation disc is foreign — the stitched stream
// would then splice the wrong bytes into the file — so it is reported rather
// than silently corrupting the audio. fields are the [lo,hi) header byte ranges
// that must match; headerLen is how many bytes of each header to read. On a
// single-disc Source (no spanningSource) this is a no-op.
func verifyJunction(src cdSource, hdr []byte, dataStart, size int64, headerLen int, fields [][2]int, loc string) []Deviation {
	ss, ok := src.(spanningSource)
	if !ok {
		return nil
	}
	heads, err := ss.ContinuationHeaders(dataStart, size, headerLen)
	if err != nil {
		return []Deviation{{Location: loc, SpecRef: "§5.6", Severity: SeverityError,
			Message: fmt.Sprintf("could not read a continuation disc's repeated header: %v", err)}}
	}
	var devs []Deviation
	for _, ch := range heads {
		if !headerFieldsMatch(hdr, ch, fields) {
			devs = append(devs, Deviation{Location: loc, SpecRef: "§5.6", Severity: SeverityError,
				Message: "a continuation disc's block-0 header does not repeat this file's header (name/FileID/size mismatch); the set may be mis-ordered or a disc is foreign — spanned audio may be corrupt"})
		}
	}
	return devs
}

// headerFieldsMatch reports whether two header blocks agree on every identity
// field range.
func headerFieldsMatch(a, b []byte, fields [][2]int) bool {
	for _, f := range fields {
		if f[1] > len(a) || f[1] > len(b) || !bytes.Equal(a[f[0]:f[1]], b[f[0]:f[1]]) {
			return false
		}
	}
	return true
}

// readFileData reads a file's stored bytes, clamping the read to the source's
// data end. On a complete Source the full file is returned. On an incomplete
// multi-disc set, a file whose data runs past the last available disc (§5.6:
// it spanned into a missing disc) yields the recoverable prefix plus a
// deviation, so partial audio is still emitted rather than the read failing. A
// nil result means nothing was recovered; the caller skips the take.
func readFileData(src cdSource, off, size int64, loc string, id uint16) ([]byte, []Deviation) {
	end := src.UserDataLen()
	if off >= end {
		return nil, []Deviation{{Location: loc, SpecRef: "§5.6", Severity: SeverityError,
			Message: fmt.Sprintf("take %#04x begins past the last available disc; no data recovered", id)}}
	}
	avail := size
	var devs []Deviation
	if off+size > end {
		avail = end - off
		devs = append(devs, Deviation{Location: loc, SpecRef: "§5.6", Severity: SeverityError,
			Message: fmt.Sprintf("take %#04x runs %d byte(s) past the last available disc; recovering the %d present",
				id, off+size-end, avail)})
	}
	raw, err := src.ReadUserData(off, int(avail))
	if err != nil {
		return nil, append(devs, Deviation{Location: loc, SpecRef: "§5.4", Severity: SeverityError,
			Message: fmt.Sprintf("reading take %#04x: %v", id, err)})
	}
	return raw, devs
}

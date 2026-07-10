package cd

import "fmt"

// MODE1 EDC (ROLAND-VS-FORMAT-SPEC.md §10): each raw 2352-byte frame carries a
// 4-byte Error Detection Code over its first 2064 bytes (12 B sync + 4 B header +
// 2048 B user data), stored little-endian at [2064,2068). A degraded or misread
// sector fails that check, so verifying it is a cheap, direct damage detector on
// raw dumps — one the frame geometry the rest of this package skips over makes
// possible here and nowhere else.

const (
	edcCovered = 2064 // bytes the EDC is computed over: sync + header + user data
	edcOffset  = 2064 // where the EDC is stored within the frame
	edcSize    = 4    // the EDC is a 4-byte little-endian value
)

// edcPoly is the CD-ROM EDC generator in reflected form (§10). It is a standard
// reflected CRC-32 with init 0 and no final XOR, so appending a frame's own EDC
// makes the CRC over [0,2068) fold back to zero.
const edcPoly = 0xD8018001

// edcTable is the 256-entry lookup for the reflected EDC CRC, built once.
var edcTable = makeEDCTable()

func makeEDCTable() [256]uint32 {
	var t [256]uint32
	for i := range t {
		c := uint32(i)
		for b := 0; b < 8; b++ {
			if c&1 != 0 {
				c = (c >> 1) ^ edcPoly
			} else {
				c >>= 1
			}
		}
		t[i] = c
	}
	return t
}

// computeEDC returns the MODE1 EDC over b: the reflected CRC-32 (poly edcPoly,
// init 0, no final XOR). A clean frame stores this value little-endian at
// [2064,2068).
func computeEDC(b []byte) uint32 {
	var crc uint32
	for _, v := range b {
		crc = (crc >> 8) ^ edcTable[byte(crc)^v]
	}
	return crc
}

// CorruptFrames scans a raw dump for MODE1 frames whose stored EDC (§10) does not
// match the EDC recomputed over the frame's first 2064 bytes, returning their
// 0-based frame indices in ascending order. Such a frame is a physically damaged
// or misread sector: the codec would decode it into noise with no other warning,
// so callers surface each as a best-effort §10 deviation.
//
// A cooked (dd) dump carries no per-frame EDC (§5) — the frame wrapper is gone —
// so this returns nil for one; that data-integrity risk is reported as a §5
// deviation instead. The scan reads frames in batches to keep a full-disc pass to
// a handful of large reads rather than one per frame.
func (im *Image) CorruptFrames() ([]int, error) {
	if im.cooked {
		return nil, nil
	}
	frames := int(im.size / frameSize)
	const batch = 512 // frames per read: ~1.2 MB, trading memory for far fewer syscalls
	buf := make([]byte, batch*frameSize)

	var corrupt []int
	for base := 0; base < frames; base += batch {
		n := batch
		if base+n > frames {
			n = frames - base
		}
		b := buf[:n*frameSize]
		if _, err := im.src.ReadAt(b, int64(base)*frameSize); err != nil {
			return nil, fmt.Errorf("cd: reading frames [%d,%d) for EDC check: %w", base, base+n, err)
		}
		for j := 0; j < n; j++ {
			f := b[j*frameSize : j*frameSize+edcOffset+edcSize]
			stored := uint32(f[edcOffset]) | uint32(f[edcOffset+1])<<8 |
				uint32(f[edcOffset+2])<<16 | uint32(f[edcOffset+3])<<24
			if computeEDC(f[:edcCovered]) != stored {
				corrupt = append(corrupt, base+j)
			}
		}
	}
	return corrupt, nil
}

// CorruptFrames scans every disc of the set for EDC-broken MODE1 frames (§10),
// returning a slice parallel to the set's discs — element i is disc i's corrupt
// frame indices (nil when that disc is clean or a cooked rip). Frame indices are
// per-disc, since EDC is a physical-frame property of each dump, not of the
// stitched logical stream. On the first disc that cannot be read the scan stops
// and returns the read error.
func (s *Set) CorruptFrames() ([][]int, error) {
	out := make([][]int, len(s.discs))
	for i, im := range s.discs {
		c, err := im.CorruptFrames()
		if err != nil {
			return nil, fmt.Errorf("cd: EDC scan of disc %d: %w", i, err)
		}
		out[i] = c
	}
	return out, nil
}

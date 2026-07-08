// Package cd turns a raw Roland VS "Song Copy Archive" CD dump into the
// continuous user-data byte stream that every archive structure lives in
// (ROLAND-VS-FORMAT-SPEC.md §5.1). It hides the physical MODE1 frame geometry —
// the 16-byte header and 288-byte EDC/ECC wrapping each 2048-byte payload —
// behind user-data-offset addressing, and detects the two dump shapes vsx
// accepts: a raw 2352-byte-frame rip and a "cooked" 2048-byte-sector rip.
//
// It is machine-agnostic: identifying the archive (VR5 vs VR9) and walking its
// header blocks belong to callers, which read named fields through ReadUserData.
package cd

import (
	"fmt"
	"io"
)

const (
	frameSize    = 2352 // one raw MODE1 frame
	userDataSize = 2048 // payload bytes per frame
	frameHeader  = 16   // 12 B sync + 3 B MSF + 1 B mode, before the payload
)

// Image is a random-access view of a CD dump's user-data stream. Reads are
// translated to physical offsets on demand, so opening a large dump does not
// pull it into memory.
type Image struct {
	src    io.ReaderAt
	size   int64 // physical dump length in bytes
	cooked bool  // true = 2048-byte-sector dump with no frame wrapper
}

// New builds an Image over a dump of the given physical size. A size that is a
// whole number of 2352-byte frames is a raw rip; one that is a whole number of
// 2048-byte sectors but not of frames is a cooked (dd) rip, read as a
// contiguous user-data stream. Any other length is unusable geometry.
func New(src io.ReaderAt, size int64) (*Image, error) {
	switch {
	case size > 0 && size%frameSize == 0:
		return &Image{src: src, size: size, cooked: false}, nil
	case size > 0 && size%userDataSize == 0:
		return &Image{src: src, size: size, cooked: true}, nil
	default:
		return nil, fmt.Errorf("cd: dump length %d is neither a 2352-byte-frame nor a 2048-byte-sector multiple", size)
	}
}

// Cooked reports whether the dump is a cooked (2048-byte-sector) rip. §5 treats
// a cooked dump as a data-integrity risk (dd rips of these discs are frequently
// truncated), so callers surface it as a deviation while still reading it.
func (im *Image) Cooked() bool { return im.cooked }

// UserDataLen returns the length of the concatenated user-data stream in bytes.
func (im *Image) UserDataLen() int64 {
	if im.cooked {
		return im.size
	}
	return im.size / frameSize * userDataSize
}

// fillerSig is the 13-byte TDI filler-frame signature (§10); a filler frame's
// 2048-byte payload is this signature followed by zeros.
var fillerSig = []byte{0x54, 0x44, 0x49, 0x01, 0x50, 0x01, 0x01, 0x01, 0x01, 0x80, 0xFF, 0xFF, 0xFF}

// blockSize is the archive allocation and MT2 page unit — 0x8000 bytes (§5.4).
const blockSize = 0x8000

// FillerStart returns the user-data offset where the disc's trailing TDI filler
// run begins (§10) — the true end of burned file data, which chain-walking and
// spanning arithmetic are measured against, not the end of the dump. It scans
// 0x8000-aligned offsets for the first filler frame. The second result is false
// when no filler run is present, which §10 flags as a truncated/incomplete rip.
func (im *Image) FillerStart() (int64, bool) {
	end := im.UserDataLen()
	for udoff := int64(0); udoff+userDataSize <= end; udoff += blockSize {
		frame, err := im.ReadUserData(udoff, userDataSize)
		if err != nil {
			return 0, false
		}
		if isFillerPayload(frame) {
			return udoff, true
		}
	}
	return 0, false
}

// isFillerPayload reports whether a 2048-byte user-data payload is a TDI filler
// frame: the signature followed by zeros to the end of the payload.
func isFillerPayload(ud []byte) bool {
	if len(ud) < len(fillerSig) {
		return false
	}
	for i, b := range fillerSig {
		if ud[i] != b {
			return false
		}
	}
	for _, b := range ud[len(fillerSig):] {
		if b != 0 {
			return false
		}
	}
	return true
}

// ReadUserData returns n bytes of the user-data stream starting at user-data
// offset udoff (§5.1: frame N, byte K → udoff = N×2048 + K). Reads may straddle
// frame boundaries; the physical ECC/header bytes between payloads are skipped.
// A read that runs past the end of the stream is an error.
func (im *Image) ReadUserData(udoff int64, n int) ([]byte, error) {
	if udoff < 0 || n < 0 {
		return nil, fmt.Errorf("cd: invalid read udoff=%d n=%d", udoff, n)
	}
	if udoff+int64(n) > im.UserDataLen() {
		return nil, fmt.Errorf("cd: read [%d,%d) runs past user-data end %d", udoff, udoff+int64(n), im.UserDataLen())
	}
	out := make([]byte, n)
	if im.cooked {
		if _, err := im.src.ReadAt(out, udoff); err != nil {
			return nil, fmt.Errorf("cd: reading cooked dump at %d: %w", udoff, err)
		}
		return out, nil
	}
	// Raw: walk frame by frame, copying the slice of each frame's payload that
	// falls in [udoff, udoff+n).
	filled := 0
	for filled < n {
		cur := udoff + int64(filled)
		frame := cur / userDataSize
		inFrame := cur % userDataSize
		want := userDataSize - int(inFrame)
		if want > n-filled {
			want = n - filled
		}
		phys := frame*frameSize + frameHeader + inFrame
		if _, err := im.src.ReadAt(out[filled:filled+want], phys); err != nil {
			return nil, fmt.Errorf("cd: reading raw frame at phys %d: %w", phys, err)
		}
		filled += want
	}
	return out, nil
}

package core

import (
	"fmt"

	"github.com/andapony/vsx/internal/rdac"
)

// Format is the RDAC audio format code stored in SONG.VRx byte 19 and copied
// into CD headers (ROLAND-VS-FORMAT-SPEC.md §2).
type Format int

const (
	FormatMT1 Format = 0 // 16-byte blocks, 16-bit, predictive
	FormatMT2 Format = 1 // 12-byte blocks, 16-bit, predictive, cluster-padded
	FormatM16 Format = 3 // uncompressed little-endian 16-bit PCM
	FormatMTP Format = 5 // 16-byte blocks, 24-bit, predictive (VS-1880 default)
	FormatM24 Format = 8 // uncompressed little-endian 24-bit PCM
)

// PCM is one channel of decoded mono audio. Samples holds one sample per
// element, sign-extended into int32; BitDepth is the bits each sample
// occupies (16 or 24) and is fixed by the source Format.
type PCM struct {
	Samples  []int32
	BitDepth int

	// UnknownBlockOffsets holds the 0-based block index of each RDAC block in
	// this take that selected an unimplemented ("never occurs") codec pattern
	// and was rendered silent (Appendix A / §12); block i covers samples
	// [i*16, i*16+16). The timeline uses these to warn only when such silence is
	// actually copied into output audio — a take's unused tail routinely decodes
	// to these patterns (padding / §9 remnants) and must not be reported. Empty
	// for the uncompressed formats and MTP.
	UnknownBlockOffsets []int
}

// Decoder turns a take's raw codec bytes into mono PCM. It is the narrow seam
// (ADR-0004) behind which the vendored golden rdac codec sits, invisible to
// the rest of the pipeline. clusterSize is the storage cluster size in bytes,
// needed only for MT2 page-padding; other formats ignore it.
type Decoder interface {
	Decode(format Format, data []byte, clusterSize int) (PCM, error)
}

// NewDecoder returns the production Decoder, backed by the vendored rdac
// codec for the predictive formats and by direct little-endian unpacking for
// the uncompressed formats.
func NewDecoder() Decoder { return rdacDecoder{} }

type rdacDecoder struct{}

func (rdacDecoder) Decode(format Format, data []byte, clusterSize int) (PCM, error) {
	switch format {
	case FormatMTP:
		s, err := rdac.DecodeMTP(data, 0)
		if err != nil {
			return PCM{}, err
		}
		return PCM{Samples: s, BitDepth: 24}, nil
	case FormatMT1:
		s, stats, err := rdac.DecodeMT1CorrectStats(data)
		if err != nil {
			return PCM{}, err
		}
		return PCM{Samples: widen16(s), BitDepth: 16, UnknownBlockOffsets: stats.UnknownBlockOffsets}, nil
	case FormatMT2:
		s, stats, err := rdac.DecodeMT2ClusterStats(data, clusterSize)
		if err != nil {
			return PCM{}, err
		}
		return PCM{Samples: widen16(s), BitDepth: 16, UnknownBlockOffsets: stats.UnknownBlockOffsets}, nil
	case FormatM16:
		return decodeM16(data)
	case FormatM24:
		return decodeM24(data)
	default:
		return PCM{}, fmt.Errorf("core: unknown RDAC format code %d", int(format))
	}
}

// widen16 sign-extends 16-bit samples into the common int32 representation.
func widen16(s []int16) []int32 {
	out := make([]int32, len(s))
	for i, v := range s {
		out[i] = int32(v)
	}
	return out
}

// decodeM16 unpacks uncompressed little-endian signed 16-bit PCM.
func decodeM16(data []byte) (PCM, error) {
	if len(data)%2 != 0 {
		return PCM{}, fmt.Errorf("core: M16 data length %d is not a multiple of 2", len(data))
	}
	out := make([]int32, len(data)/2)
	for i := range out {
		out[i] = int32(int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8))
	}
	return PCM{Samples: out, BitDepth: 16}, nil
}

// decodeM24 unpacks uncompressed little-endian signed 24-bit PCM.
func decodeM24(data []byte) (PCM, error) {
	if len(data)%3 != 0 {
		return PCM{}, fmt.Errorf("core: M24 data length %d is not a multiple of 3", len(data))
	}
	out := make([]int32, len(data)/3)
	for i := range out {
		v := int32(data[i*3]) | int32(data[i*3+1])<<8 | int32(data[i*3+2])<<16
		if v&0x800000 != 0 {
			v |= ^int32(0xffffff) // sign-extend bit 23
		}
		out[i] = v
	}
	return PCM{Samples: out, BitDepth: 24}, nil
}

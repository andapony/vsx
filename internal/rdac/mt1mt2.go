// MT1/MT2 RDAC block decoders, ported from Randy Gordon's rdac
// (https://github.com/randygordon/rdac; structure from the Go reference
// vs2reaper/decode/decode.go, constants and validation against the C
// reference rdac2wav/src/{main.c,decode.c}). LGPL-3.0-or-later — same
// provenance and license as decoder.go; modifications by Rob Duncan,
// 2026-07-05.
//
// Key facts (verified against the reference implementation and real
// VS-880EX/VS-1880 media, 2026-07-05):
//   - MT1 blocks are 16 bytes -> 16 samples (16-bit), own pattern switch
//     (patterns A/B/B3/C/D/E/F), NOT the same as MT2 or MTP.
//   - MT2 blocks are 12 bytes -> 16 samples (16-bit), pattern switch over
//     the 12-byte patterns (pattern12A..12G).
//   - MT2 streams carry cluster-page padding: blocksPerPage = pageSize/12
//     whole blocks per page, then (pageSize - blocksPerPage*12) pad bytes
//     (32 KB page: 2730 blocks + 8 pad; 64 KB page: 5460 blocks + 16 pad).
//     MT1/MTP (16-byte blocks) divide pages evenly and have no padding.
//
// The previous DecodeMT2/DecodeMT1 in decoder.go used a 16-byte stride and a
// hybrid switch and produced garbage for all MT2/MT1 material.
package rdac

import "fmt"

// DecodeStats reports decode anomalies a caller should surface as deviations
// rather than the codec printing them. UnknownPatternBlocks counts RDAC blocks
// whose pattern index selected a dispatch case the reference decoder leaves
// unimplemented ("never occurs", §12 / Appendix A): vsx renders those blocks as
// silence — the same output the reference produces — and records the count here
// so the extractor can report it (ADR-0002/0004) instead of writing to stdout.
type DecodeStats struct {
	// UnknownBlockOffsets holds the 0-based block index of each such block, in
	// decode order (its length is the count). Block i covers decoded samples
	// [i*16, i*16+16); the caller maps that into the take's timeline to judge
	// whether the silence lands in audio the output actually uses.
	UnknownBlockOffsets []int
}

// DecodeMT2Cluster decodes an MT2 stream that was stored with the given
// cluster (page) size. Use 32768 for VS-880EX disks (32 KB clusters).
func DecodeMT2Cluster(compressed []byte, clusterSize int) ([]int16, error) {
	s, _, err := DecodeMT2ClusterStats(compressed, clusterSize)
	return s, err
}

// DecodeMT2ClusterStats is DecodeMT2Cluster plus the DecodeStats anomaly
// report; the stats-free wrapper delegates here so existing callers are
// unchanged.
func DecodeMT2ClusterStats(compressed []byte, clusterSize int) ([]int16, DecodeStats, error) {
	var stats DecodeStats
	if clusterSize <= 0 {
		return nil, stats, fmt.Errorf("invalid clusterSize %d", clusterSize)
	}
	blocksPerPage := clusterSize / 12
	padBytes := clusterSize - blocksPerPage*12

	numPages := len(compressed) / clusterSize
	leftOver := len(compressed) % clusterSize
	numBlocks := numPages*blocksPerPage + leftOver/12

	samples := make([]int16, 0, numBlocks*16)
	out := make([]int, 16)
	in := make([]uint8, 12)
	d0 := 0
	pos := 0
	for i := 0; i < numBlocks; i++ {
		copy(in, compressed[pos:pos+12])
		pos += 12
		if decodeMT2Frame12(d0, in, out) {
			stats.UnknownBlockOffsets = append(stats.UnknownBlockOffsets, i)
		}
		for j := 0; j < 16; j++ {
			samples = append(samples, int16(limit16(out[j])))
		}
		d0 = out[15]
		if (i+1)%blocksPerPage == 0 {
			pos += padBytes // eat page padding
		}
	}
	return samples, stats, nil
}

// DecodeMT1Correct decodes an MT1 stream (16-byte blocks, no page padding).
func DecodeMT1Correct(compressed []byte) ([]int16, error) {
	s, _, err := DecodeMT1CorrectStats(compressed)
	return s, err
}

// DecodeMT1CorrectStats is DecodeMT1Correct plus the DecodeStats anomaly report.
func DecodeMT1CorrectStats(compressed []byte) ([]int16, DecodeStats, error) {
	var stats DecodeStats
	numBlocks := len(compressed) / 16
	samples := make([]int16, 0, numBlocks*16)
	out := make([]int, 16)
	in := make([]uint8, 16)
	d0 := 0
	for i := 0; i < numBlocks; i++ {
		copy(in, compressed[i*16:(i+1)*16])
		if decodeMT1Frame(d0, in, out) {
			stats.UnknownBlockOffsets = append(stats.UnknownBlockOffsets, i)
		}
		for j := 0; j < 16; j++ {
			samples = append(samples, int16(limit16(out[j])))
		}
		d0 = out[15]
	}
	return samples, stats, nil
}

// decodeMT1Frame decodes one 16-byte MT1 block into out. It returns true when
// the block's pattern index selects an unimplemented ("never occurs") case:
// out is then left silent (as the reference decoder does) and the caller counts
// it as a DecodeStats anomaly instead of the decoder printing to stdout.
func decodeMT1Frame(d0 int, in []uint8, out []int) (unknown bool) {

	// Decodes a 16-byte MT1 RDAC block into 16-bit samples.

	patternIndex := (in[0] & 0xf0) | ((in[2] & 0xf0) >> 4)

	pattern := patterns[patternIndex]

	for i := 0; i < 16; i++ {
		out[i] = 0
	}
	//fmt.Printf("pattern = %d\n", pattern)
	switch pattern {

	// Pattern B

	case 0:
		/* Unknown - never occurs? */
		unknown = true
	case 1:
		/* Unknown - never occurs? */
		unknown = true
	case 2:
		patternStr := patternB
		applyPattern(in, out, patternStr)
		// 2 linear
		interpolate2(d0, out)
	case 3:
		patternStr := patternB
		applyPattern(in, out, patternStr)
		shiftRound(out, 1)
		// 2 linear
		interpolate2(d0, out)
	case 4:
		patternStr := patternB
		applyPattern(in, out, patternStr)
		shiftRound(out, 2)
		// 2 linear
		interpolate2(d0, out)
	case 5:
		patternStr := patternB
		applyPattern(in, out, patternStr)
		shiftRound(out, 3)
		// 2 linear
		interpolate2(d0, out)

		// Pattern D

	case 6:
		patternStr := patternD
		applyPattern(in, out, patternStr)
		shiftRound(out, 2)
		// 4 linear
		interpolate4(d0, out)
	case 7:
		patternStr := patternD
		applyPattern(in, out, patternStr)
		shiftRound(out, 3)
		// 4 linear
		interpolate4(d0, out)
	case 8:
		patternStr := patternD
		applyPattern(in, out, patternStr)
		shiftRound(out, 4)
		// 4 linear
		interpolate4(d0, out)
	case 9:
		patternStr := patternD
		applyPattern(in, out, patternStr)
		shiftRound(out, 5)
		// 4 linear
		interpolate4(d0, out)
	case 10:
		patternStr := patternD
		applyPattern(in, out, patternStr)
		shiftRound(out, 6)
		// 4 linear
		interpolate4(d0, out)
	case 11:
		patternStr := patternD
		applyPattern(in, out, patternStr)
		shiftRound(out, 7)
		// 4 linear
		interpolate4(d0, out)

		// Pattern A

	case 12:
		/* Unknown - never occurs? */
		unknown = true
	case 13:
		/* Unknown - never occurs? */
		unknown = true
	case 14:
		/* Unknown - never occurs? */
		unknown = true
	case 15:
		patternStr := patternA
		applyPattern(in, out, patternStr)
		// 2 linear
		interpolate2(d0, out)
	case 16:
		patternStr := patternA
		applyPattern(in, out, patternStr)
		shiftRound(out, 1)
		// 2 linear
		interpolate2(d0, out)
	case 17:
		patternStr := patternA
		applyPattern(in, out, patternStr)
		shiftRound(out, 2)
		// 2 linear
		interpolate2(d0, out)

		// Pattern B3

	case 18:
		patternStr := patternB3
		applyPattern(in, out, patternStr)
		shiftRound(out, 4)
		// 2 linear
		interpolate2(d0, out)

		// Pattern C

	case 19:
		patternStr := patternC
		applyPattern(in, out, patternStr)
		// 2 linear
		interpolate2(d0, out)
	case 20:
		patternStr := patternC
		applyPattern(in, out, patternStr)
		shiftRound(out, 1)
		// 2 linear
		interpolate2(d0, out)
	case 21:
		patternStr := patternC
		applyPattern(in, out, patternStr)
		shiftRound(out, 2)
		// 2 linear
		interpolate2(d0, out)
	case 22:
		patternStr := patternC
		applyPattern(in, out, patternStr)
		shiftRound(out, 3)
		// 2 linear
		interpolate2(d0, out)
	case 23:
		patternStr := patternC
		applyPattern(in, out, patternStr)
		shiftRound(out, 4)
		// 2 linear
		interpolate2(d0, out)
	case 24:
		patternStr := patternC
		applyPattern(in, out, patternStr)
		shiftRound(out, 5)
		// 2 linear
		interpolate2(d0, out)

		// Pattern F

	case 25:
		patternStr := patternF
		applyPattern(in, out, patternStr)
		shiftRound(out, 4)
		// 8 linear
		interpolate8(d0, out)
	case 26:
		patternStr := patternF
		applyPattern(in, out, patternStr)
		shiftRound(out, 5)
		// 8 linear
		interpolate8(d0, out)
	case 27:
		patternStr := patternF
		applyPattern(in, out, patternStr)
		shiftRound(out, 6)
		// 8 linear
		interpolate8(d0, out)
	case 28:
		patternStr := patternF
		applyPattern(in, out, patternStr)
		shiftRound(out, 7)
		// 8 linear
		interpolate8(d0, out)
	case 29:
		patternStr := patternF
		applyPattern(in, out, patternStr)
		shiftRound(out, 8)
		// 8 linear
		interpolate8(d0, out)
	case 30:
		patternStr := patternF
		applyPattern(in, out, patternStr)
		shiftRound(out, 8)
		// 16 linear - but odd samples are doubled!
		doubleOdds(out)

		// Pattern E

	case 31:
		patternStr := patternE
		applyPattern(in, out, patternStr)
		shiftRound(out, 6)
		// 4 linear
		interpolate4(d0, out)

		// Pattern B4

	case 32:
		/* Unknown - never occurs? */
		unknown = true
	case 33:
		/* Unknown - never occurs? */
		unknown = true
	case 34:
		/* Unknown - never occurs? */
		unknown = true
	case 35:
		/* Unknown - never occurs? */
		unknown = true
	case 36:
		/* Unknown - never occurs? */
		unknown = true
	default:
	}

	preventOverflow16(out)
	return
}

//*****************************************************************************

// decodeMT2Frame12 decodes one 12-byte MT2 block into out. It returns true when
// the block's pattern index selects an unimplemented ("never occurs") case:
// out is then left silent (as the reference decoder does) and the caller counts
// it as a DecodeStats anomaly instead of the decoder printing to stdout.
func decodeMT2Frame12(d0 int, in []uint8, out []int) (unknown bool) {

	// Decodes a 12-byte MT2 RDAC block into 16 16-bit samples.
	// (The upstream comment said "16-byte" but the MT2 block is 12 bytes —
	// see convertMT2 in the reference rdac2wav/src/main.c.)

	patternIndex := (in[0] & 0xf0) | ((in[2] & 0xf0) >> 4)

	pattern := patterns[patternIndex]

	for i := 0; i < 16; i++ {
		out[i] = 0
	}

	switch pattern {

	// Pattern 12A

	case 0:
		patternStr := pattern12A
		applyPattern12(in, out, patternStr)
		// 2 linear
		interpolate2(d0, out)
	case 1:
		patternStr := pattern12A
		applyPattern12(in, out, patternStr)
		shiftRound(out, 1)
		// 2 linear
		interpolate2(d0, out)
	case 2:
		patternStr := pattern12A
		applyPattern12(in, out, patternStr)
		shiftRound(out, 2)
		// 2 linear
		interpolate2(d0, out)
	case 3:
		patternStr := pattern12A
		applyPattern12(in, out, patternStr)
		shiftRound(out, 3)
		// 2 linear
		interpolate2(d0, out)
	case 4:
		patternStr := pattern12A
		applyPattern12(in, out, patternStr)
		shiftRound(out, 4)
		// 2 linear
		interpolate2(d0, out)
	case 5:
		patternStr := pattern12A
		applyPattern12(in, out, patternStr)
		shiftRound(out, 5)
		// 2 linear
		interpolate2(d0, out)

		// Pattern 12B

	case 6:
		patternStr := pattern12B
		applyPattern12(in, out, patternStr)
		shiftRound(out, 4)
		// 4 linear
		interpolate4(d0, out)
	case 7:
		patternStr := pattern12B
		applyPattern12(in, out, patternStr)
		shiftRound(out, 5)
		// 4 linear
		interpolate4(d0, out)
	case 8:
		patternStr := pattern12B
		applyPattern12(in, out, patternStr)
		shiftRound(out, 6)
		// 4 linear
		interpolate4(d0, out)
	case 9:
		patternStr := pattern12B
		applyPattern12(in, out, patternStr)
		shiftRound(out, 7)
		// 4 linear
		interpolate4(d0, out)
	case 10:
		patternStr := pattern12B
		applyPattern12(in, out, patternStr)
		shiftRound(out, 8)
		// 4 linear
		interpolate4(d0, out)
	case 11:
		patternStr := pattern12B
		applyPattern12(in, out, patternStr)
		shiftRound(out, 9)
		// 4 linear
		interpolate4(d0, out)

		// Pattern 12F

	case 12:
		/* Unknown - never occurs? */
		unknown = true
	case 13:
		patternStr := pattern12F
		applyPattern12(in, out, patternStr)
		// 2 linear
		interpolate2(d0, out)
	case 14:
		patternStr := pattern12F
		applyPattern12(in, out, patternStr)
		shiftRound(out, 1)
		// 2 linear
		interpolate2(d0, out)
	case 15:
		patternStr := pattern12F
		applyPattern12(in, out, patternStr)
		shiftRound(out, 2)
		// 2 linear
		interpolate2(d0, out)
	case 16:
		patternStr := pattern12F
		applyPattern12(in, out, patternStr)
		shiftRound(out, 3)
		// 2 linear
		interpolate2(d0, out)
	case 17:
		patternStr := pattern12F
		applyPattern12(in, out, patternStr)
		shiftRound(out, 4)
		// 2 linear
		interpolate2(d0, out)

		// Pattern 12G

	case 18:
		patternStr := pattern12G
		applyPattern12(in, out, patternStr)
		shiftRound(out, 6)
		// 2 linear
		interpolate2(d0, out)

		// Pattern 12E

	case 19:
		patternStr := pattern12E
		applyPattern12(in, out, patternStr)
		shiftRound(out, 2)
		// 2 linear
		interpolate2(d0, out)
	case 20:
		patternStr := pattern12E
		applyPattern12(in, out, patternStr)
		shiftRound(out, 3)
		// 2 linear
		interpolate2(d0, out)
	case 21:
		patternStr := pattern12E
		applyPattern12(in, out, patternStr)
		shiftRound(out, 4)
		// 2 linear
		interpolate2(d0, out)
	case 22:
		patternStr := pattern12E
		applyPattern12(in, out, patternStr)
		shiftRound(out, 5)
		// 2 linear
		interpolate2(d0, out)
	case 23:
		patternStr := pattern12E
		applyPattern12(in, out, patternStr)
		shiftRound(out, 6)
		// 2 linear
		interpolate2(d0, out)
	case 24:
		patternStr := pattern12E
		applyPattern12(in, out, patternStr)
		shiftRound(out, 7)
		// 2 linear
		interpolate2(d0, out)

		// Pattern 12C

	case 25:
		patternStr := pattern12C
		applyPattern12(in, out, patternStr)
		shiftRound(out, 6)
		// 8 linear
		interpolate8(d0, out)
	case 26:
		patternStr := pattern12C
		applyPattern12(in, out, patternStr)
		shiftRound(out, 7)
		// 8 linear
		interpolate8(d0, out)
	case 27:
		patternStr := pattern12C
		applyPattern12(in, out, patternStr)
		shiftRound(out, 8)
		// 8 linear
		interpolate8(d0, out)
	case 28:
		patternStr := pattern12C
		applyPattern12(in, out, patternStr)
		shiftRound(out, 9)
		// 8 linear
		interpolate8(d0, out)
	case 29:
		patternStr := pattern12C
		applyPattern12(in, out, patternStr)
		shiftRound(out, 10)
		// 8 linear
		interpolate8(d0, out)
	case 30:
		patternStr := pattern12C
		applyPattern12(in, out, patternStr)
		shiftRound(out, 10)
		// 16 linear - but odd samples are doubled!
		doubleOdds(out)

		// Pattern 12D

	case 31:
		patternStr := pattern12D
		applyPattern12(in, out, patternStr)
		shiftRound(out, 8)
		// 4 linear
		interpolate4(d0, out)

		// Pattern ?

	case 32:
		/* Unknown - never occurs? */
		unknown = true
	case 33:
		/* Unknown - never occurs? */
		unknown = true
	case 34:
		/* Unknown - never occurs? */
		unknown = true
	case 35:
		/* Unknown - never occurs? */
		unknown = true
	case 36:
		/* Unknown - never occurs? */
		unknown = true
	default:
	}

	preventOverflow16(out)
	return
}

//*****************************************************************************

// Package rdac decodes RDAC (Roland's proprietary differential audio
// compression) as used by Roland VS-series digital studio workstations:
// the MTP (24-bit), MT1, and MT2 (16-bit) formats.
//
// Provenance: the pattern lookup table, bit-layout strings, and
// reconstruction code in this file are a Go-to-Go port of Randy Gordon's
// reference decoder, vs2reaper/decode/decode.go in
// https://github.com/randygordon/rdac. The port has been validated
// sample-exact against the compiled companion C implementation
// (rdac2wav) on real VS-880EX and VS-1880 media (2026-07-05).
//
// Copyright of the reference decoder:
//
//	Copyright (c) Randy Gordon (randy@integrand.com) —
//	rdac2wav (C) 2006, vs2reaper (Go) 2017.
//	Licensed LGPL-3.0-or-later.
//
// This file is a derivative work of the reference decoder and is likewise
// licensed under LGPL-3.0-or-later. See COPYING.LESSER and COPYING
// distributed alongside this package.
//
// Modifications from the reference (Rob Duncan, 2025–2026):
//   - restructured as an importable, I/O-free library (the reference is a
//     drive-reading CLI); decode functions take byte slices, return samples
//   - MT1/MT2 entry points delegate to the corrected implementations in
//     mt1mt2.go (2026-07-05); see that file's header
package rdac

import (
	"fmt"
)

// Pattern lookup table - maps 8-bit pattern indices (0-255) to pattern numbers (0-36)
var patterns = [256]int{
	0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3,
	0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3,
	0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3,
	0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3,
	4, 4, 4, 4, 5, 5, 5, 5, 6, 6, 6, 6, 7, 7, 7, 7,
	4, 4, 4, 4, 5, 5, 5, 5, 6, 6, 6, 6, 7, 7, 7, 7,
	4, 4, 4, 4, 5, 5, 5, 5, 6, 6, 6, 6, 7, 7, 7, 7,
	4, 4, 4, 4, 5, 5, 5, 5, 6, 6, 6, 6, 7, 7, 7, 7,
	8, 8, 8, 8, 9, 9, 9, 9, 10, 10, 10, 10, 11, 11, 11, 11,
	8, 8, 8, 8, 9, 9, 9, 9, 10, 10, 10, 10, 11, 11, 11, 11,
	8, 8, 8, 8, 9, 9, 9, 9, 10, 10, 10, 10, 11, 11, 11, 11,
	8, 8, 8, 8, 9, 9, 9, 9, 10, 10, 10, 10, 11, 11, 11, 11,
	12, 12, 13, 13, 14, 14, 15, 15, 16, 16, 17, 17, 18, 18, 19, 19,
	12, 12, 13, 13, 14, 14, 15, 15, 16, 16, 17, 17, 18, 18, 19, 19,
	20, 20, 21, 21, 22, 22, 23, 23, 24, 24, 25, 26, 27, 28, 29, 30,
	20, 20, 21, 21, 22, 22, 23, 23, 24, 24, 31, 32, 33, 34, 35, 36,
}

// Symbol index maps pattern characters to sample indices
var symbolIndex = map[byte]int{
	'p': -1, // padding (ignored)
	'1': 0, '2': 1, '3': 2, '4': 3,
	'5': 4, '6': 5, '7': 6, '8': 7,
	'9': 8, 'a': 9, 'b': 10, 'c': 11,
	'd': 12, 'e': 13, 'f': 14, 'g': 15,
}

// Decorated patterns define bit-to-sample mappings for MTP decoding
var decoratedPatternA = "ppp88888 88888888 pppggggg gggggggg 87777776 66666655 gffffffe eeeeeedd 55554444 44444333 ddddcccc cccccbbb 33322222 22111111 bbbaaaaa aa999999"
var decoratedPatternB = "pp888888 88888887 ppgggggg gggggggf 77777666 66666555 fffffeee eeeeeddd 55544444 44443333 dddccccc ccccbbbb 33222222 22111111 bbaaaaaa aa999999"
var decoratedPatternB3 = "ppp88888 88888887 pppggggg gggggggf 77777666 66666555 fffffeee eeeeeddd 55544444 44443333 dddccccc ccccbbbb 33222222 22111111 bbaaaaaa aa999999"
var decoratedPatternB4 = "pppp8888 88888887 ppppgggg gggggggf 77777766 66666555 ffffffee eeeeeddd 55554444 44433333 ddddcccc cccbbbbb 33222222 21111111 bbaaaaaa a9999999"
var decoratedPatternC = "ppp88888 88888877 pppggggg ggggggff 77776666 66665555 ffffeeee eeeedddd 55444444 44443333 ddcccccc ccccbbbb 33222222 22111111 bbaaaaaa aa999999"
var decoratedPatternD = "pp888888 88877777 ppgggggg gggfffff 77666666 66555555 ffeeeeee eedddddd 54444444 44333333 dccccccc ccbbbbbb 32222222 21111111 baaaaaaa a9999999"
var decoratedPatternE = "pppp8888 88888877 ppppgggg ggggggff 77776666 66665555 ffffeeee eeeedddd 55444444 44443333 ddcccccc ccccbbbb 33222222 22111111 bbaaaaaa aa999999"
var decoratedPatternF = "pppp8888 88887777 ppppgggg ggggffff 77766666 66655555 fffeeeee eeeddddd 55444444 44333333 ddcccccc ccbbbbbb 32222222 21111111 baaaaaaa a9999999"

// Decorated patterns for MT1/MT2 (12-bit) decoding
var decoratedPattern12A = "pp888888 88888777 ppgggggg gggggfff 76666665 55544444 feeeeeed dddccccc 44333322 22221111 ccbbbbaa aaaa9999"
var decoratedPattern12B = "pp888888 87777766 ppgggggg gfffffee 66665555 54444444 eeeedddd dccccccc 33333222 22211111 bbbbbaaa aaa99999"
var decoratedPattern12C = "pppp8888 88777776 ppppgggg ggfffffe 66666555 55444444 eeeeeddd ddcccccc 33333222 22211111 bbbbbaaa aaa99999"
var decoratedPattern12D = "pppp8888 88887777 ppppgggg ggggffff 66666655 55444444 eeeeeedd ddcccccc 44333322 22221111 ccbbbbaa aaaa9999"
var decoratedPattern12E = "ppp88888 88887777 pppggggg ggggffff 66666655 55444444 eeeeeedd ddcccccc 44333322 22221111 ccbbbbaa aaaa9999"
var decoratedPattern12F = "ppp88888 88888887 pppggggg gggggggf 77766666 55554444 fffeeeee ddddcccc 44433332 22221111 cccbbbba aaaa9999"
var decoratedPattern12G = "ppp88888 88888777 pppggggg gggggfff 76666665 55544444 feeeeeed dddccccc 44333322 22221111 ccbbbbaa aaaa9999"

// Strip spaces from decorated patterns
func stripSpaces(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' {
			result = append(result, s[i])
		}
	}
	return string(result)
}

// Pattern strings with spaces removed
var patternA = stripSpaces(decoratedPatternA)
var patternB = stripSpaces(decoratedPatternB)
var patternB3 = stripSpaces(decoratedPatternB3)
var patternB4 = stripSpaces(decoratedPatternB4)
var patternC = stripSpaces(decoratedPatternC)
var patternD = stripSpaces(decoratedPatternD)
var patternE = stripSpaces(decoratedPatternE)
var patternF = stripSpaces(decoratedPatternF)

var pattern12A = stripSpaces(decoratedPattern12A)
var pattern12B = stripSpaces(decoratedPattern12B)
var pattern12C = stripSpaces(decoratedPattern12C)
var pattern12D = stripSpaces(decoratedPattern12D)
var pattern12E = stripSpaces(decoratedPattern12E)
var pattern12F = stripSpaces(decoratedPattern12F)
var pattern12G = stripSpaces(decoratedPattern12G)

// DecodeMTP decodes MTP-compressed audio to 24-bit PCM samples
func DecodeMTP(compressed []byte, sampleRate int) ([]int32, error) {
	if len(compressed)%16 != 0 {
		return nil, fmt.Errorf("compressed data must be multiple of 16 bytes (frames)")
	}

	frameCount := len(compressed) / 16
	samples := make([]int32, frameCount*16) // 16 samples per frame

	var d0 int // Previous frame's last sample (for inter-frame continuity)
	out := make([]int, 16)
	in := make([]uint8, 16)

	for i := 0; i < frameCount; i++ {
		copy(in, compressed[i*16:(i+1)*16])

		decodeMTPFrame(d0, in, out)

		// Copy to output
		for j := 0; j < 16; j++ {
			samples[i*16+j] = int32(out[j])
		}

		d0 = out[15]
	}

	return samples, nil
}

// DecodeMT2 decodes MT2-compressed audio to 16-bit PCM samples
// MT2 blocks are 12 bytes (not 16!) and streams stored on 32 KB-cluster
// disks carry 8 pad bytes per 32 KB page — see mt1mt2.go. This wrapper
// assumes 32 KB source clusters (VS-880EX default); callers that know a
// different cluster size should call DecodeMT2Cluster directly.
// (The previous implementation here used a 16-byte stride with a hybrid
// pattern switch and produced garbage for all MT2 material.)
func DecodeMT2(compressed []byte, sampleRate int) ([]int16, error) {
	return DecodeMT2Cluster(compressed, 32768)
}

// DecodeMT1 decodes MT1-compressed audio to 16-bit PCM samples.
// MT1 uses 16-byte blocks with its own pattern switch (NOT the same as MT2).
func DecodeMT1(compressed []byte, sampleRate int) ([]int16, error) {
	return DecodeMT1Correct(compressed)
}

// decodeMTPFrame decodes a single 16-byte MTP RDAC frame
func decodeMTPFrame(d0 int, in []uint8, out []int) {
	patternIndex := (in[0] & 0xf0) | ((in[2] & 0xf0) >> 4)
	pattern := patterns[patternIndex]

	for i := 0; i < 16; i++ {
		out[i] = 0
	}

	switch pattern {
	case 0:
		applyPattern(in, out, patternB)
		shiftRound(out, 6)
		interpolate2(d0, out)
	case 1:
		applyPattern(in, out, patternB)
		shiftRound(out, 7)
		interpolate2(d0, out)
	case 2:
		applyPattern(in, out, patternB)
		shiftRound(out, 8)
		interpolate2(d0, out)
	case 3:
		applyPattern(in, out, patternB)
		shiftRound(out, 9)
		interpolate2(d0, out)
	case 4:
		applyPattern(in, out, patternB)
		shiftRound(out, 10)
		interpolate2(d0, out)
	case 5:
		applyPattern(in, out, patternB)
		shiftRound(out, 11)
		interpolate2(d0, out)
	case 6:
		applyPattern(in, out, patternD)
		shiftRound(out, 10)
		interpolate4(d0, out)
	case 7:
		applyPattern(in, out, patternD)
		shiftRound(out, 11)
		interpolate4(d0, out)
	case 8:
		applyPattern(in, out, patternD)
		shiftRound(out, 12)
		interpolate4(d0, out)
	case 9:
		applyPattern(in, out, patternD)
		shiftRound(out, 13)
		interpolate4(d0, out)
	case 10:
		applyPattern(in, out, patternD)
		shiftRound(out, 14)
		interpolate4(d0, out)
	case 11:
		applyPattern(in, out, patternD)
		shiftRound(out, 15)
		interpolate4(d0, out)
	case 12:
		applyPattern(in, out, patternA)
		shiftRound(out, 5)
		interpolate2(d0, out)
	case 13:
		applyPattern(in, out, patternA)
		shiftRound(out, 6)
		interpolate2(d0, out)
	case 14:
		applyPattern(in, out, patternA)
		shiftRound(out, 7)
		interpolate2(d0, out)
	case 15:
		applyPattern(in, out, patternA)
		shiftRound(out, 8)
		interpolate2(d0, out)
	case 16:
		applyPattern(in, out, patternA)
		shiftRound(out, 9)
		interpolate2(d0, out)
	case 17:
		applyPattern(in, out, patternA)
		shiftRound(out, 10)
		interpolate2(d0, out)
	case 18:
		applyPattern(in, out, patternB3)
		shiftRound(out, 12)
		interpolate2(d0, out)
	case 19:
		applyPattern(in, out, patternC)
		shiftRound(out, 8)
		interpolate2(d0, out)
	case 20:
		applyPattern(in, out, patternC)
		shiftRound(out, 9)
		interpolate2(d0, out)
	case 21:
		applyPattern(in, out, patternC)
		shiftRound(out, 10)
		interpolate2(d0, out)
	case 22:
		applyPattern(in, out, patternC)
		shiftRound(out, 11)
		interpolate2(d0, out)
	case 23:
		applyPattern(in, out, patternC)
		shiftRound(out, 12)
		interpolate2(d0, out)
	case 24:
		applyPattern(in, out, patternC)
		shiftRound(out, 13)
		interpolate2(d0, out)
	case 25:
		applyPattern(in, out, patternF)
		shiftRound(out, 12)
		interpolate8(d0, out)
	case 26:
		applyPattern(in, out, patternF)
		shiftRound(out, 13)
		interpolate8(d0, out)
	case 27:
		applyPattern(in, out, patternF)
		shiftRound(out, 14)
		interpolate8(d0, out)
	case 28:
		applyPattern(in, out, patternF)
		shiftRound(out, 15)
		interpolate8(d0, out)
	case 29:
		applyPattern(in, out, patternF)
		shiftRound(out, 16)
		interpolate8(d0, out)
	case 30:
		applyPattern(in, out, patternF)
		shiftRound(out, 16)
		doubleOdds(out)
	case 31:
		applyPattern(in, out, patternE)
		shiftRound(out, 14)
		interpolate4(d0, out)
	case 32:
		applyPattern(in, out, patternB4)
		shiftRound(out, 4)
		interpolate2(d0, out)
	case 33:
		applyPattern(in, out, patternB4)
		shiftRound(out, 5)
		interpolate2(d0, out)
	case 34:
		applyPattern(in, out, patternB4)
		// No shift/round
		interpolate2(d0, out)
	case 35:
		applyPattern(in, out, patternB4)
		shiftRound(out, 2)
		interpolate2(d0, out)
	case 36:
		applyPattern(in, out, patternB4)
		shiftRound(out, 3)
		interpolate2(d0, out)
	}

	preventOverflow24(out)
}

func applyPattern(in []uint8, out []int, pattern string) {
	outPos := make([]uint, 16)

	// Process bytes 15 down to 0 (reverse order)
	for inPosition := 15; inPosition >= 0; inPosition-- {
		bytePatternIndex := inPosition * 8
		bytePatternStr := pattern[bytePatternIndex : bytePatternIndex+8]

		// Process bits 0-7 within the byte
		for bitPosition := 0; bitPosition <= 7; bitPosition++ {
			symbol := bytePatternStr[7-bitPosition]
			outIndex := symbolIndex[symbol]

			if outIndex == -1 {
				continue // Skip padding bits
			}

			hasBit := ((in[inPosition] >> uint(bitPosition)) & 0x01) == 0x01
			if hasBit {
				out[outIndex] |= 0x01 << outPos[outIndex]
			}
			outPos[outIndex]++
		}
	}

	// Sign-extend each differential value
	for i := 0; i < 16; i++ {
		out[i] = signExtend(out[i], outPos[i]-1)
	}
}

// applyPattern12 is like applyPattern but for 12-bit patterns (MT1/MT2)
func applyPattern12(in []uint8, out []int, pattern string) {
	outPos := make([]uint, 16)

	// Process bytes 11 down to 0 (reverse order)
	for inPosition := 11; inPosition >= 0; inPosition-- {
		bytePatternIndex := inPosition * 8
		bytePatternStr := pattern[bytePatternIndex : bytePatternIndex+8]

		for bitPosition := 0; bitPosition <= 7; bitPosition++ {
			symbol := bytePatternStr[7-bitPosition]
			outIndex := symbolIndex[symbol]

			if outIndex == -1 {
				continue
			}

			hasBit := ((in[inPosition] >> uint(bitPosition)) & 0x01) == 0x01
			if hasBit {
				out[outIndex] |= 0x01 << outPos[outIndex]
			}
			outPos[outIndex]++
		}
	}

	// Sign-extend
	for i := 0; i < 16; i++ {
		out[i] = signExtend(out[i], outPos[i]-1)
	}
}

// signExtend performs sign extension at the specified bit position
func signExtend(xx int, signPos uint) int {
	return -(xx & (0x01 << signPos)) | xx
}

// shiftRound applies left-shift with rounding
func shiftRound(out []int, pos uint) {
	if pos == 0 {
		return
	}
	for i := 0; i < 16; i++ {
		out[i] = out[i]<<pos | 0x01<<(pos-1)
	}
}

// interpolate computes the average of two samples with proper rounding
func interpolate(aa int, bb int) int {
	if (aa + bb) < 0 {
		return (aa + bb - 1) / 2
	}
	return (aa + bb) / 2
}

// interpolate2 performs 2x linear interpolation (decodes 8 samples, fills 8 gaps)
func interpolate2(d0 int, out []int) {
	out[3] += interpolate(d0, out[7])
	out[1] += interpolate(d0, out[3])
	out[5] += interpolate(out[3], out[7])
	out[11] += interpolate(out[7], out[15])
	out[9] += interpolate(out[7], out[11])
	out[13] += interpolate(out[11], out[15])
	out[0] += interpolate(d0, out[1])
	out[2] += interpolate(out[1], out[3])
	out[4] += interpolate(out[3], out[5])
	out[6] += interpolate(out[5], out[7])
	out[8] += interpolate(out[7], out[9])
	out[10] += interpolate(out[9], out[11])
	out[12] += interpolate(out[11], out[13])
	out[14] += interpolate(out[13], out[15])
}

// interpolate4 performs 4x linear interpolation (decodes 4 samples, fills 12 gaps)
func interpolate4(d0 int, out []int) {
	out[1] += interpolate(d0, out[3])
	out[5] += interpolate(out[3], out[7])
	out[9] += interpolate(out[7], out[11])
	out[13] += interpolate(out[11], out[15])
	out[0] += interpolate(d0, out[1])
	out[2] += interpolate(out[1], out[3])
	out[4] += interpolate(out[3], out[5])
	out[6] += interpolate(out[5], out[7])
	out[8] += interpolate(out[7], out[9])
	out[10] += interpolate(out[9], out[11])
	out[12] += interpolate(out[11], out[13])
	out[14] += interpolate(out[13], out[15])
}

// interpolate8 performs 8x linear interpolation (decodes 2 samples, fills 14 gaps)
func interpolate8(d0 int, out []int) {
	out[0] += interpolate(d0, out[1])
	out[2] += interpolate(out[1], out[3])
	out[4] += interpolate(out[3], out[5])
	out[6] += interpolate(out[5], out[7])
	out[8] += interpolate(out[7], out[9])
	out[10] += interpolate(out[9], out[11])
	out[12] += interpolate(out[11], out[13])
	out[14] += interpolate(out[13], out[15])
}

// doubleOdds doubles even-indexed samples (special case for pattern 30)
func doubleOdds(out []int) {
	for i := 0; i < 16; i += 2 {
		out[i] <<= 1
	}
}

// limit16 clamps values to 16-bit signed range
func limit16(xx int) int {
	if xx < -32768 {
		return -32768
	} else if xx > 32767 {
		return 32767
	}
	return xx
}

// limit24 clamps values to 24-bit signed range
func limit24(xx int) int {
	if xx < -8388608 {
		return -8388608
	} else if xx > 8388607 {
		return 8388607
	}
	return xx
}

// preventOverflow16 clamps all output samples to 16-bit range
func preventOverflow16(out []int) {
	for i := 0; i < 16; i++ {
		out[i] = limit16(out[i])
	}
}

// preventOverflow24 clamps all output samples to 24-bit range
func preventOverflow24(out []int) {
	for i := 0; i < 16; i++ {
		out[i] = limit24(out[i])
	}
}

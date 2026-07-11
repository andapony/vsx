// Package wav encodes decoded PCM samples into canonical RIFF/WAVE container
// bytes. It performs no resampling, dithering, or channel mixing: each input
// sample is written verbatim as a little-endian integer of the requested bit
// depth, so the output is a faithful copy of the decoded audio. Mono v-tracks
// use Encode; a §8.4 stereo pair uses EncodeStereo, which interleaves its two
// channels into one file.
package wav

import (
	"encoding/binary"
	"fmt"
)

// Encode returns RIFF/WAVE bytes for a single mono channel of PCM.
//
// samples holds one mono sample per element, sign-extended into int32. Only
// the low bitDepth bits of each sample are written; callers are responsible
// for clamping to the sample range (the RDAC decoders already do). bitDepth
// must be 16 or 24; sampleRate is the song's native rate (e.g. 44100). No
// resampling or dithering is applied.
func Encode(samples []int32, sampleRate, bitDepth int) ([]byte, error) {
	return encode([][]int32{samples}, sampleRate, bitDepth)
}

// EncodeStereo returns RIFF/WAVE bytes for an interleaved two-channel file:
// left is written first in each sample frame, right second (§8.4: left is the
// lower-numbered physical track). Channels of unequal length are padded with
// trailing silence to the longer one, though a genuine pair's channels are
// equal-length by construction. Sample-value and bitDepth handling match
// Encode.
func EncodeStereo(left, right []int32, sampleRate, bitDepth int) ([]byte, error) {
	return encode([][]int32{left, right}, sampleRate, bitDepth)
}

// encode writes one PCM data chunk interleaving the given channels frame by
// frame. Frame count is the longest channel; shorter channels contribute
// silence past their end, so the file is always fully framed.
func encode(channels [][]int32, sampleRate, bitDepth int) ([]byte, error) {
	if bitDepth != 16 && bitDepth != 24 {
		return nil, fmt.Errorf("wav: unsupported bit depth %d (want 16 or 24)", bitDepth)
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("wav: invalid sample rate %d", sampleRate)
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("wav: no channels to encode")
	}

	numChannels := len(channels)
	frames := 0
	for _, ch := range channels {
		if len(ch) > frames {
			frames = len(ch)
		}
	}
	bytesPerSample := bitDepth / 8
	blockAlign := numChannels * bytesPerSample
	byteRate := sampleRate * blockAlign
	dataSize := frames * blockAlign
	// RIFF requires a trailing zero pad byte after any chunk whose payload is
	// odd-length (here only mono 24-bit audio with an odd frame count — every
	// other channel/depth combination gives an even blockAlign). The pad is
	// counted in the RIFF chunk size but not in the data subchunk size.
	pad := dataSize % 2
	riffSize := 36 + dataSize + pad // everything after the first 8 bytes

	buf := make([]byte, 0, 44+dataSize+pad)
	buf = append(buf, "RIFF"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(riffSize))
	buf = append(buf, "WAVE"...)

	// fmt subchunk (PCM, 16 bytes).
	buf = append(buf, "fmt "...)
	buf = binary.LittleEndian.AppendUint32(buf, 16)
	buf = binary.LittleEndian.AppendUint16(buf, 1) // AudioFormat = PCM
	buf = binary.LittleEndian.AppendUint16(buf, uint16(numChannels))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(sampleRate))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(byteRate))
	buf = binary.LittleEndian.AppendUint16(buf, uint16(blockAlign))
	buf = binary.LittleEndian.AppendUint16(buf, uint16(bitDepth))

	// data subchunk: frames written channel-interleaved, low channel first. A
	// channel that has run out contributes a zero (silence) sample.
	buf = append(buf, "data"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(dataSize))
	for i := 0; i < frames; i++ {
		for _, ch := range channels {
			var s int32
			if i < len(ch) {
				s = ch[i]
			}
			u := uint32(s)
			switch bitDepth {
			case 16:
				buf = append(buf, byte(u), byte(u>>8))
			case 24:
				buf = append(buf, byte(u), byte(u>>8), byte(u>>16))
			}
		}
	}
	if pad == 1 {
		buf = append(buf, 0)
	}
	return buf, nil
}

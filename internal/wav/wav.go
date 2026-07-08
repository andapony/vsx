// Package wav encodes mono PCM samples into canonical RIFF/WAVE container
// bytes. It performs no resampling, dithering, or channel mixing: each input
// sample is written verbatim as a little-endian integer of the requested bit
// depth, so the output is a faithful copy of the decoded audio.
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
	if bitDepth != 16 && bitDepth != 24 {
		return nil, fmt.Errorf("wav: unsupported bit depth %d (want 16 or 24)", bitDepth)
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("wav: invalid sample rate %d", sampleRate)
	}

	const numChannels = 1
	bytesPerSample := bitDepth / 8
	blockAlign := numChannels * bytesPerSample
	byteRate := sampleRate * blockAlign
	dataSize := len(samples) * bytesPerSample
	// RIFF requires a trailing zero pad byte after any chunk whose payload is
	// odd-length (here, 24-bit data with an odd sample count). The pad is
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
	buf = binary.LittleEndian.AppendUint16(buf, numChannels)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(sampleRate))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(byteRate))
	buf = binary.LittleEndian.AppendUint16(buf, uint16(blockAlign))
	buf = binary.LittleEndian.AppendUint16(buf, uint16(bitDepth))

	// data subchunk. The bit-depth branch is hoisted out of the per-sample
	// loop since it is invariant across the buffer.
	buf = append(buf, "data"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(dataSize))
	switch bitDepth {
	case 16:
		for _, s := range samples {
			u := uint32(s)
			buf = append(buf, byte(u), byte(u>>8))
		}
	case 24:
		for _, s := range samples {
			u := uint32(s)
			buf = append(buf, byte(u), byte(u>>8), byte(u>>16))
		}
	}
	if pad == 1 {
		buf = append(buf, 0)
	}
	return buf, nil
}

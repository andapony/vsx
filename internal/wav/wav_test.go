package wav

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parsedWAV is the subset of a canonical RIFF/WAVE PCM file this test cares
// about. It is parsed here independently of the Encode implementation so the
// assertions come from the WAV spec, not from Encode's own arithmetic.
type parsedWAV struct {
	audioFormat   uint16
	numChannels   uint16
	sampleRate    uint32
	byteRate      uint32
	blockAlign    uint16
	bitsPerSample uint16
	data          []byte
}

func parseWAV(t *testing.T, b []byte) parsedWAV {
	t.Helper()
	require.GreaterOrEqual(t, len(b), 44, "file shorter than a WAV header")
	require.Equal(t, "RIFF", string(b[0:4]), "RIFF tag")
	require.EqualValues(t, len(b)-8, binary.LittleEndian.Uint32(b[4:8]), "RIFF chunk size")
	require.Equal(t, "WAVE", string(b[8:12]), "WAVE tag")
	require.Equal(t, "fmt ", string(b[12:16]), "fmt subchunk tag")
	require.EqualValues(t, 16, binary.LittleEndian.Uint32(b[16:20]), "fmt subchunk size")

	var p parsedWAV
	p.audioFormat = binary.LittleEndian.Uint16(b[20:22])
	p.numChannels = binary.LittleEndian.Uint16(b[22:24])
	p.sampleRate = binary.LittleEndian.Uint32(b[24:28])
	p.byteRate = binary.LittleEndian.Uint32(b[28:32])
	p.blockAlign = binary.LittleEndian.Uint16(b[32:34])
	p.bitsPerSample = binary.LittleEndian.Uint16(b[34:36])
	require.Equal(t, "data", string(b[36:40]), "data subchunk tag")
	dataSize := binary.LittleEndian.Uint32(b[40:44])
	// The file holds dataSize bytes of samples, plus a RIFF pad byte when
	// dataSize is odd.
	wantLen := 44 + int(dataSize) + int(dataSize%2)
	require.Equal(t, wantLen, len(b), "file length accounts for data plus any RIFF pad byte")
	p.data = b[44 : 44+dataSize]
	return p
}

// TestEncode16BitHeader verifies that encoding 16-bit mono PCM produces a
// canonical RIFF/WAVE header (PCM format, 1 channel, given sample rate,
// derived byte rate and block align) and that each sample round-trips as a
// signed little-endian int16, including the 16-bit range extremes.
func TestEncode16BitHeader(t *testing.T) {
	samples := []int32{0, 1, -1, 32767, -32768}
	out, err := Encode(samples, 44100, 16)
	require.NoError(t, err)
	p := parseWAV(t, out)

	assert.EqualValues(t, 1, p.audioFormat, "audioFormat should be PCM")
	assert.EqualValues(t, 1, p.numChannels, "mono")
	assert.EqualValues(t, 44100, p.sampleRate)
	assert.EqualValues(t, 16, p.bitsPerSample)
	assert.EqualValues(t, 2, p.blockAlign)
	assert.EqualValues(t, 44100*2, p.byteRate)
	require.Len(t, p.data, len(samples)*2)

	want := []int16{0, 1, -1, 32767, -32768}
	for i, w := range want {
		got := int16(binary.LittleEndian.Uint16(p.data[i*2 : i*2+2]))
		assert.Equalf(t, w, got, "sample %d", i)
	}
}

// TestEncode24BitSamplesRoundTrip verifies that encoding 24-bit mono PCM
// declares 24 bits per sample with the matching block align and byte rate,
// and that each sample round-trips through three-byte little-endian packing —
// exercising the 24-bit range extremes and a negative value to confirm sign
// handling.
func TestEncode24BitSamplesRoundTrip(t *testing.T) {
	samples := []int32{0, 1, -1, 8388607, -8388608, 0x123456, -0x123456}
	out, err := Encode(samples, 48000, 24)
	require.NoError(t, err)
	p := parseWAV(t, out)

	assert.EqualValues(t, 24, p.bitsPerSample)
	assert.EqualValues(t, 3, p.blockAlign)
	assert.EqualValues(t, 48000*3, p.byteRate)
	require.Len(t, p.data, len(samples)*3)

	for i, w := range samples {
		b := p.data[i*3 : i*3+3]
		got := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
		if got&0x800000 != 0 {
			got |= ^int32(0xffffff) // sign-extend bit 23
		}
		assert.Equalf(t, w, got, "sample %d", i)
	}
}

// TestEncode24BitOddLengthEmitsPadByte verifies that a 24-bit buffer with an
// odd sample count (odd data-chunk length) is padded to an even boundary with
// a trailing zero byte, so the file stays RIFF-conformant. The data subchunk
// size still reports the true, unpadded byte count.
func TestEncode24BitOddLengthEmitsPadByte(t *testing.T) {
	samples := []int32{1, 2, 3} // 3 × 3 bytes = 9 data bytes (odd)
	out, err := Encode(samples, 44100, 24)
	require.NoError(t, err)

	assert.Equal(t, 0, len(out)%2, "total file length must be even")
	dataSize := binary.LittleEndian.Uint32(out[40:44])
	assert.EqualValues(t, 9, dataSize, "data size reports the true unpadded length")
	assert.Zero(t, out[len(out)-1], "trailing pad byte is zero")
	// parseWAV re-validates the length/pad relationship and header fields.
	p := parseWAV(t, out)
	assert.Len(t, p.data, 9)
}

// TestEncodeStereoInterleaves verifies that EncodeStereo writes a canonical
// two-channel RIFF/WAVE: the header declares 2 channels with the matching
// block align and byte rate, and the data chunk holds one L,R sample frame per
// index — left channel first — so a §8.4 stereo pair round-trips interleaved.
func TestEncodeStereoInterleaves(t *testing.T) {
	left := []int32{0, 100, -100, 32767}
	right := []int32{1, -1, 200, -32768}
	out, err := EncodeStereo(left, right, 44100, 16)
	require.NoError(t, err)
	p := parseWAV(t, out)

	assert.EqualValues(t, 1, p.audioFormat, "audioFormat should be PCM")
	assert.EqualValues(t, 2, p.numChannels, "stereo")
	assert.EqualValues(t, 44100, p.sampleRate)
	assert.EqualValues(t, 16, p.bitsPerSample)
	assert.EqualValues(t, 4, p.blockAlign, "2 channels × 2 bytes")
	assert.EqualValues(t, 44100*4, p.byteRate)
	require.Len(t, p.data, len(left)*4, "one L,R frame of 4 bytes per index")

	for i := range left {
		gotL := int16(binary.LittleEndian.Uint16(p.data[i*4 : i*4+2]))
		gotR := int16(binary.LittleEndian.Uint16(p.data[i*4+2 : i*4+4]))
		assert.EqualValues(t, left[i], gotL, "left sample %d", i)
		assert.EqualValues(t, right[i], gotR, "right sample %d", i)
	}
}

// TestEncodeStereo24Bit verifies the 24-bit interleaved path: 3-byte L,R frames
// with the declared depth, block align, and byte rate, exercising the range
// extremes to confirm sign handling in both channels.
func TestEncodeStereo24Bit(t *testing.T) {
	left := []int32{8388607, -8388608}
	right := []int32{-1, 0x123456}
	out, err := EncodeStereo(left, right, 48000, 24)
	require.NoError(t, err)
	p := parseWAV(t, out)

	assert.EqualValues(t, 2, p.numChannels)
	assert.EqualValues(t, 24, p.bitsPerSample)
	assert.EqualValues(t, 6, p.blockAlign, "2 channels × 3 bytes")
	assert.EqualValues(t, 48000*6, p.byteRate)
	require.Len(t, p.data, len(left)*6)

	sample24 := func(b []byte) int32 {
		v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
		if v&0x800000 != 0 {
			v |= ^int32(0xffffff)
		}
		return v
	}
	for i := range left {
		assert.EqualValues(t, left[i], sample24(p.data[i*6:i*6+3]), "left %d", i)
		assert.EqualValues(t, right[i], sample24(p.data[i*6+3:i*6+6]), "right %d", i)
	}
}

// TestEncodeStereoPadsShorterChannel verifies that channels of unequal length
// are padded with trailing silence to the longer one, so interleaving never
// runs off the end of the shorter channel (the two channels of a genuine §8.4
// pair are equal-length by construction, but the encoder is defensive).
func TestEncodeStereoPadsShorterChannel(t *testing.T) {
	left := []int32{5, 6, 7}
	right := []int32{9}
	out, err := EncodeStereo(left, right, 44100, 16)
	require.NoError(t, err)
	p := parseWAV(t, out)

	require.Len(t, p.data, 3*4, "framed to the longer channel")
	// Frame 0: (5, 9); frames 1 and 2: right padded to silence.
	assert.EqualValues(t, 5, int16(binary.LittleEndian.Uint16(p.data[0:2])))
	assert.EqualValues(t, 9, int16(binary.LittleEndian.Uint16(p.data[2:4])))
	assert.EqualValues(t, 6, int16(binary.LittleEndian.Uint16(p.data[4:6])))
	assert.EqualValues(t, 0, int16(binary.LittleEndian.Uint16(p.data[6:8])), "right padded")
	assert.EqualValues(t, 7, int16(binary.LittleEndian.Uint16(p.data[8:10])))
	assert.EqualValues(t, 0, int16(binary.LittleEndian.Uint16(p.data[10:12])), "right padded")
}

// TestEncodeSampleRates verifies that every supported sample rate (32/44.1/48
// kHz) and bit depth (16/24) is written faithfully into the header, with the
// byte rate consistent with the declared rate and depth.
func TestEncodeSampleRates(t *testing.T) {
	for _, rate := range []int{32000, 44100, 48000} {
		for _, depth := range []int{16, 24} {
			out, err := Encode([]int32{0, 100, -100}, rate, depth)
			require.NoErrorf(t, err, "rate=%d depth=%d", rate, depth)
			p := parseWAV(t, out)
			assert.EqualValuesf(t, rate, p.sampleRate, "rate=%d depth=%d", rate, depth)
			assert.EqualValuesf(t, depth, p.bitsPerSample, "rate=%d depth=%d", rate, depth)
			assert.EqualValuesf(t, rate*depth/8, p.byteRate, "rate=%d depth=%d", rate, depth)
		}
	}
}

// TestEncodeRejectsBadBitDepth verifies that an unsupported bit depth is
// rejected with an error rather than producing a malformed file.
func TestEncodeRejectsBadBitDepth(t *testing.T) {
	_, err := Encode([]int32{0}, 44100, 12)
	require.Error(t, err)
}

// TestEncodeRejectsBadSampleRate verifies that a non-positive sample rate is
// rejected with an error.
func TestEncodeRejectsBadSampleRate(t *testing.T) {
	_, err := Encode([]int32{0}, 0, 16)
	require.Error(t, err)
}

package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The codec itself is golden by provenance (ADR-0004); these are
// characterization tests that guard the wiring behind the Decoder seam —
// format dispatch, the cluster-size parameter, and the uncompressed
// passthrough — not the per-pattern decode math.

// TestDecodeSilentBlockIsSilence verifies that for the formats whose pattern-0
// decode is a true no-op (MT1 and MT2), an all-zero block decodes to exactly
// 16 bit-exact-silent 16-bit samples, confirming the seam is wired to the real
// per-block decoders (and, for MT2, that the cluster-size parameter is
// accepted).
func TestDecodeSilentBlockIsSilence(t *testing.T) {
	dec := NewDecoder()
	const cluster = 32768

	cases := []struct {
		name   string
		format Format
		block  []byte // one silent block
	}{
		{"MT1", FormatMT1, make([]byte, 16)},
		{"MT2", FormatMT2, make([]byte, 12)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pcm, err := dec.Decode(tc.format, tc.block, cluster)
			require.NoError(t, err)
			require.Len(t, pcm.Samples, 16, "one block decodes to 16 samples")
			assert.Equal(t, 16, pcm.BitDepth)
			for i, s := range pcm.Samples {
				assert.Zerof(t, s, "sample %d should be silence", i)
			}
		})
	}
}

// TestDecodeMTPSilentBlockIsNearSilence pins a genuine quirk of the golden
// codec (verified against the original reference DecodeMTP): MTP's pattern-0
// path applies a shift-round term, so an all-zero block decodes to
// near-silence — |sample| well under 1024, i.e. below −78 dBFS of the 24-bit
// full scale ±8388607 — rather than bit-exact zero. This is the codec's
// authoritative behavior (ADR-0004); the test guards the wiring, not the
// decode math.
func TestDecodeMTPSilentBlockIsNearSilence(t *testing.T) {
	pcm, err := NewDecoder().Decode(FormatMTP, make([]byte, 16), 32768)
	require.NoError(t, err)
	require.Len(t, pcm.Samples, 16, "one block decodes to 16 samples")
	assert.Equal(t, 24, pcm.BitDepth)
	for i, s := range pcm.Samples {
		assert.Truef(t, s >= -1024 && s <= 1024, "sample %d = %d, want near-silence", i, s)
	}
}

// TestDecodeM16Passthrough verifies that the M16 format is decoded as raw
// uncompressed little-endian signed 16-bit PCM, sample-for-sample, including a
// negative extreme.
func TestDecodeM16Passthrough(t *testing.T) {
	data := []byte{0x00, 0x00, 0x01, 0x00, 0xff, 0xff, 0x00, 0x80}
	want := []int32{0, 1, -1, -32768}
	pcm, err := NewDecoder().Decode(FormatM16, data, 0)
	require.NoError(t, err)
	assert.Equal(t, 16, pcm.BitDepth)
	assert.Equal(t, want, pcm.Samples)
}

// TestDecodeM24Passthrough verifies that the M24 format is decoded as raw
// uncompressed little-endian signed 24-bit PCM, sample-for-sample, including a
// negative extreme that exercises 24-bit sign extension.
func TestDecodeM24Passthrough(t *testing.T) {
	data := []byte{
		0x00, 0x00, 0x00, // 0
		0x01, 0x00, 0x00, // 1
		0xff, 0xff, 0xff, // -1
		0x00, 0x00, 0x80, // -8388608
	}
	want := []int32{0, 1, -1, -8388608}
	pcm, err := NewDecoder().Decode(FormatM24, data, 0)
	require.NoError(t, err)
	assert.Equal(t, 24, pcm.BitDepth)
	assert.Equal(t, want, pcm.Samples)
}

// TestDecodeUnknownFormatErrors verifies that an unrecognized format code is
// rejected with an error rather than silently producing garbage.
func TestDecodeUnknownFormatErrors(t *testing.T) {
	_, err := NewDecoder().Decode(Format(99), nil, 32768)
	require.Error(t, err)
}

// TestDecodeM16RejectsOddLength verifies that M16 input whose length is not a
// whole number of 16-bit samples is rejected.
func TestDecodeM16RejectsOddLength(t *testing.T) {
	_, err := NewDecoder().Decode(FormatM16, []byte{0x00}, 0)
	require.Error(t, err)
}

// TestDecodeM24RejectsMisalignedLength verifies that M24 input whose length is
// not a whole number of 24-bit samples is rejected.
func TestDecodeM24RejectsMisalignedLength(t *testing.T) {
	_, err := NewDecoder().Decode(FormatM24, []byte{0x00, 0x00}, 0)
	require.Error(t, err)
}

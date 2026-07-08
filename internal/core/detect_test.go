package core

import (
	"bytes"
	"testing"

	"github.com/andapony/vsx/internal/cd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// imageOf wraps a raw dump in a cd.Image for detection tests.
func imageOf(t *testing.T, raw []byte) *cd.Image {
	t.Helper()
	img, err := cd.New(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	return img
}

// vr9Header returns a minimal raw dump whose user-data offset 0 carries the
// VS-880EX archive signature — enough for detection, not a full archive.
func vr9Header(sig []byte) []byte {
	ud := make([]byte, 2048)
	copy(ud, sig)
	// wrap one frame
	f := make([]byte, 2352)
	f[15] = 0x01
	copy(f[16:], ud)
	return f
}

// TestDetectVR9CD verifies the §5.2 detection claim: the VS-880EX signature at
// user-data offset 0 identifies a CD source as machine VR9, with no override.
func TestDetectVR9CD(t *testing.T) {
	p, err := detect(imageOf(t, vr9Header([]byte("VS-8EXECR02 Song Copy Archives  "))), "")
	require.NoError(t, err)
	assert.Equal(t, kindCD, p.kind)
	assert.Equal(t, machineVR9, p.machine)
}

// TestDetectVR5CD verifies the VS-1880 signature is recognized as CD/VR5 (so
// the pipeline can report it as an unsupported machine rather than mis-reading
// it as VR9).
func TestDetectVR5CD(t *testing.T) {
	p, err := detect(imageOf(t, vr9Header([]byte("VS1880EXR06 Song Copy Archives  "))), "")
	require.NoError(t, err)
	assert.Equal(t, machineVR5, p.machine)
}

// TestDetectUnidentifiableErrors verifies that input carrying no known archive
// signature and no override is a hard error, not a silent empty success.
func TestDetectUnidentifiableErrors(t *testing.T) {
	_, err := detect(imageOf(t, vr9Header([]byte("not a roland disc"))), "")
	assert.Error(t, err)
}

// TestDetectOverrideForcesVR9 verifies the --as override: unrecognized bytes are
// forced to VR9 CD when the user asserts it.
func TestDetectOverrideForcesVR9(t *testing.T) {
	p, err := detect(imageOf(t, vr9Header([]byte("garbage"))), "vr9")
	require.NoError(t, err)
	assert.Equal(t, kindCD, p.kind)
	assert.Equal(t, machineVR9, p.machine)
}

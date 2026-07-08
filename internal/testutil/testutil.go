// Package testutil holds shared test helpers for vsx: gating media-dependent
// tests on the presence of the out-of-repo corpus, and hashing decoded PCM for
// golden-master comparisons (ADR-0005). It is imported only from _test.go
// files.
package testutil

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
)

// MediaEnv is the environment variable naming the directory that holds the
// real VS-series test media. Media lives outside the repo (ADR-0005); tests
// that need it call RequireMedia and skip when it is absent.
const MediaEnv = "VSX_TEST_MEDIA"

// skipT is the slice of *testing.T that RequireMedia needs. Taking an
// interface (rather than *testing.T) lets the skip behavior itself be tested.
type skipT interface {
	Helper()
	Skipf(format string, args ...any)
}

// RequireMedia returns the media directory from MediaEnv, or skips the calling
// test when the variable is unset or empty. This is how media-dependent tests
// stay green on machines (and CI) without the corpus.
func RequireMedia(t skipT) string {
	t.Helper()
	dir := os.Getenv(MediaEnv)
	if dir == "" {
		t.Skipf("%s is not set; skipping media-dependent test", MediaEnv)
		return ""
	}
	return dir
}

// PCMHash returns a stable hex SHA-256 digest of a decoded mono PCM buffer,
// hashing each sample as a little-endian int32. Two buffers hash equal iff
// they hold the same samples in the same order, so a committed digest guards a
// verified extraction against regression without storing the audio itself.
func PCMHash(samples []int32) string {
	h := sha256.New()
	var buf [4]byte
	for _, s := range samples {
		binary.LittleEndian.PutUint32(buf[:], uint32(s))
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

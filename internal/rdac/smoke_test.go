package rdac

import (
	"bytes"
	"testing"
)

// These are shape/invariant tests, not golden-output tests. For byte-exact
// validation, compare against compiled rdac2wav output on real media.

func TestDecodeMTPShape(t *testing.T) {
	data := make([]byte, 16*100) // 100 silent-ish blocks
	samples, err := DecodeMTP(data, 44100)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 100*16 {
		t.Fatalf("got %d samples, want %d", len(samples), 100*16)
	}
	if _, err := DecodeMTP(make([]byte, 17), 44100); err == nil {
		t.Fatal("expected error for non-multiple-of-16 input")
	}
}

func TestDecodeMT2ClusterPagePadding(t *testing.T) {
	// Two full 32 KB pages: 2×2730 blocks decoded, 2×8 pad bytes consumed.
	data := make([]byte, 2*32768)
	samples, err := DecodeMT2Cluster(data, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if want := 2 * 2730 * 16; len(samples) != want {
		t.Fatalf("got %d samples, want %d", len(samples), want)
	}

	// Non-zero pad bytes must not affect output (they are write-buffer
	// garbage on real media, never audio).
	dirty := make([]byte, 2*32768)
	for page := 0; page < 2; page++ {
		for i := 0; i < 8; i++ {
			dirty[page*32768+2730*12+i] = 0xA5
		}
	}
	dirtySamples, err := DecodeMT2Cluster(dirty, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if !int16SlicesEqual(samples, dirtySamples) {
		t.Fatal("pad byte content changed decoded output")
	}
}

func TestDecodeMT2ClusterPartialTail(t *testing.T) {
	// One full page plus 10 loose blocks.
	data := make([]byte, 32768+10*12)
	samples, err := DecodeMT2Cluster(data, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if want := (2730 + 10) * 16; len(samples) != want {
		t.Fatalf("got %d samples, want %d", len(samples), want)
	}
}

func TestDeterminism(t *testing.T) {
	data := bytes.Repeat([]byte{0x3f, 0xf8, 0x3f, 0xf8, 0x82, 0x20, 0x82, 0x20, 0x44, 0x11, 0x44, 0x11}, 64)
	a, err := DecodeMT2Cluster(data, 32768)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecodeMT2Cluster(data, 32768)
	if err != nil {
		t.Fatal(err)
	}
	if !int16SlicesEqual(a, b) {
		t.Fatal("decode is not deterministic")
	}
}

func int16SlicesEqual(a, b []int16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

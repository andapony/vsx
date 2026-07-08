package rdac

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// TestDecodeMT2ReportsUnknownPatternsViaStats verifies that MT2 blocks whose
// pattern index selects an unimplemented ("never occurs") dispatch case are
// reported through the returned DecodeStats — not printed to stdout — and are
// rendered as silence, matching the reference decoder. An all-0xFF cluster maps
// every 12-byte block to patterns[0xFF] = 36, an unimplemented case.
func TestDecodeMT2ReportsUnknownPatternsViaStats(t *testing.T) {
	const clusterSize = 32768
	data := bytes.Repeat([]byte{0xFF}, clusterSize) // one full page of 0xFF blocks
	blocksPerPage := clusterSize / 12               // 2730 whole 12-byte blocks per page

	// Capture stdout so a stray debug print would fail the test: the codec must
	// stay silent on stdout (the clean-manifest contract, issue #29).
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	samples, stats, err := DecodeMT2ClusterStats(data, clusterSize)

	w.Close()
	os.Stdout = orig
	printed, _ := io.ReadAll(r)

	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(printed) != 0 {
		t.Errorf("decoder wrote %d bytes to stdout; must report via DecodeStats instead: %q", len(printed), printed)
	}
	if len(stats.UnknownBlockOffsets) != blocksPerPage {
		t.Errorf("UnknownBlockOffsets len = %d, want %d (every 0xFF block is an unimplemented pattern)",
			len(stats.UnknownBlockOffsets), blocksPerPage)
	}
	// Unimplemented patterns leave the block silent.
	for i, s := range samples {
		if s != 0 {
			t.Fatalf("sample %d = %d, want silence for an unimplemented pattern", i, s)
			break
		}
	}
}

// TestDecodeMT2ClusterStillDelegates verifies the original stats-free entry
// point is unchanged — it returns the same samples DecodeMT2ClusterStats does,
// so existing callers keep working.
func TestDecodeMT2ClusterStillDelegates(t *testing.T) {
	data := bytes.Repeat([]byte{0xFF}, 32768)
	a, err := DecodeMT2Cluster(data, 32768)
	if err != nil {
		t.Fatalf("DecodeMT2Cluster: %v", err)
	}
	b, _, err := DecodeMT2ClusterStats(data, 32768)
	if err != nil {
		t.Fatalf("DecodeMT2ClusterStats: %v", err)
	}
	if !bytes.Equal(int16sToBytes(a), int16sToBytes(b)) {
		t.Errorf("DecodeMT2Cluster and DecodeMT2ClusterStats disagree on samples")
	}
}

func int16sToBytes(s []int16) []byte {
	out := make([]byte, len(s)*2)
	for i, v := range s {
		out[i*2] = byte(v)
		out[i*2+1] = byte(v >> 8)
	}
	return out
}

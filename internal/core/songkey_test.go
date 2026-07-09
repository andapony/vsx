package core

import "testing"

func TestSongKeyString(t *testing.T) {
	cases := []struct {
		k    SongKey
		want string
	}{
		{SongKey{Partition: 2, Ordinal: 7}, "2.7"},   // HDD: partition.ordinal
		{SongKey{Partition: 0, Ordinal: 7}, "7"},     // CD: bare number
		{SongKey{Partition: 10, Ordinal: 3}, "10.3"}, // friendly, unpadded
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("SongKey%+v.String() = %q, want %q", c.k, got, c.want)
		}
	}
}

func TestSongKeyFolderName(t *testing.T) {
	// HDD folders zero-pad (2.3) so a file browser sorts them correctly.
	if got := (SongKey{Partition: 2, Ordinal: 7}).FolderName(); got != "02.007" {
		t.Errorf("HDD FolderName = %q, want %q", got, "02.007")
	}
	if got := (SongKey{Partition: 10, Ordinal: 3}).FolderName(); got != "10.003" {
		t.Errorf("HDD FolderName = %q, want %q", got, "10.003")
	}
	// CD keeps the historical %02d form (output unchanged).
	if got := (SongKey{Partition: 0, Ordinal: 7}).FolderName(); got != "07" {
		t.Errorf("CD FolderName = %q, want %q", got, "07")
	}
}

func TestParseSongKey(t *testing.T) {
	ok := map[string]SongKey{
		"2.7":    {Partition: 2, Ordinal: 7},
		"02.007": {Partition: 2, Ordinal: 7}, // padded form also accepted
		"7":      {Partition: 0, Ordinal: 7},
	}
	for in, want := range ok {
		got, err := ParseSongKey(in)
		if err != nil || got != want {
			t.Errorf("ParseSongKey(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a", "2.", ".7", "2.7.1", "-1.2", "2.x"} {
		if _, err := ParseSongKey(bad); err == nil {
			t.Errorf("ParseSongKey(%q) should error", bad)
		}
	}
}

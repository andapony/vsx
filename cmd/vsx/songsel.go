package main

import (
	"strings"

	"github.com/andapony/vsx/internal/core"
)

// songSel is a repeatable --song flag: each occurrence may carry a comma list of
// keys, all accumulated and parsed via core.ParseSongKey.
type songSel struct{ keys []core.SongKey }

func (s *songSel) String() string { return "" }

func (s *songSel) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, err := core.ParseSongKey(part)
		if err != nil {
			return err
		}
		s.keys = append(s.keys, k)
	}
	return nil
}

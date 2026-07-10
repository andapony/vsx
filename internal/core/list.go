package core

import (
	"fmt"
	"os"

	"github.com/andapony/vsx/internal/hdd"
)

// SongInfo is one catalog entry from List — everything derivable without
// decoding audio.
type SongInfo struct {
	Key          SongKey
	StoredNumber int // SONG.VRx +0x04, what the VS display shows
	Name         string
	Machine      string // "VR5" / "VR9"
	VTracks      int    // populated v-track count
	Frames       int    // timeline length in frames
	SampleRate   int    // for rendering Frames as m:ss
}

// List enumerates a Source's songs without decoding audio. It mirrors
// Extract's identify/dispatch (sharing identifySource and openBackupSet so the
// two paths cannot drift), then summarises each song straight from its event
// list — the populated v-track count and timeline length are both derivable
// from the events alone (through the same vtrackStats rule buildVTrack uses,
// §8), so no take is ever read or decoded and no Decoder is ever constructed.
// This makes listing a multi-gigabyte HDD image a matter of seconds.
func List(sourcePath string, opts Options) ([]SongInfo, []Deviation, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("core: stat source: %w", err)
	}
	if info.IsDir() {
		paths, err := discPathsInDir(sourcePath)
		if err != nil {
			return nil, nil, fmt.Errorf("core: reading directory %q: %w", sourcePath, err)
		}
		return listSet(paths, opts)
	}

	h, err := identifySource(sourcePath, info, opts)
	if err != nil {
		return nil, nil, err
	}
	defer h.f.Close()

	if h.isHDD {
		songs, devs := listHDD(h.vol)
		return songs, devs, nil
	}

	var devs []Deviation
	if h.cooked {
		devs = append(devs, cookedRipDeviation())
	}
	mf := formatFor(h.machine)
	if mf == nil {
		return nil, nil, fmt.Errorf("core: source identified but not yet supported by this build; machine=%v", h.machine)
	}
	songs, sdevs := listCD(h.img, mf)
	devs = append(devs, sdevs...)
	return songs, devs, nil
}

// ListSet treats the given disc-image files as one multi-disc CD backup set
// (§5.6) — the same grouping a directory of those files gets — and enumerates
// its songs. Use it when the discs are passed as separate paths rather than a
// folder.
func ListSet(paths []string, opts Options) ([]SongInfo, []Deviation, error) {
	return listSet(paths, opts)
}

// listSet enumerates a list of CD dump files as one multi-disc backup set
// (§5.6), grouping them exactly as extractSet does, then summarises each song
// over the stitched reader without decoding any take.
func listSet(paths []string, opts Options) ([]SongInfo, []Deviation, error) {
	if opts.As.kind == kindHDD {
		return nil, nil, fmt.Errorf("core: --as=hdd is not valid for a multi-disc CD backup set (an HDD source is a single image)")
	}

	set, err := openBackupSet(paths, opts)
	if err != nil {
		return nil, nil, err
	}
	defer closeAll(set.files)

	devs := append([]Deviation{}, set.devs...)
	if set.cooked {
		devs = append(devs, cookedRipDeviation())
	}
	mf := formatFor(set.machine)
	if mf == nil {
		return nil, nil, fmt.Errorf("core: backup set machine not supported by this build; machine=%v", set.machine)
	}
	songs, sdevs := listCD(set.reader, mf)
	devs = append(devs, sdevs...)
	return songs, devs, nil
}

// summarizeVTracks computes a song's populated v-track count and overall
// timeline length in frames from its parsed songTimeline — the same neutral
// timeline the extractor's buildTracks consumes, and through the same vtrackStats
// rule buildVTrack applies, so List and Extract agree by construction rather than
// by two loops kept in sync. It reports frames rather than samples (the caller
// renders a duration as frames*samplesPerFrame/sampleRate) and never touches a
// take's audio, so no Decoder is needed to answer either question.
func summarizeVTracks(st songTimeline) (vtracks, frames int) {
	for _, g := range st.groups {
		hasAudio, endFrame := vtrackStats(g, st.origin)
		if hasAudio {
			vtracks++
			if endFrame > frames {
				frames = endFrame
			}
		}
	}
	return vtracks, frames
}

// listHDD enumerates a Roland VS live disk's songs and summarises each from
// its event list, reusing hdd.Volume.Songs and the SONG-file/EVENTLST reads
// extractHDDSong performs but never resolving or decoding a take.
func listHDD(vol *hdd.Volume) ([]SongInfo, []Deviation) {
	songs, err := vol.Songs()
	if err != nil {
		return nil, []Deviation{{Location: "disk", SpecRef: "§4", Severity: SeverityError,
			Message: fmt.Sprintf("enumerating HDD songs: %v", err)}}
	}
	var out []SongInfo
	var devs []Deviation
	for _, s := range songs {
		info, sdevs := summarizeHDDSong(s)
		devs = append(devs, sdevs...)
		out = append(out, info)
	}
	return out, devs
}

// summarizeHDDSong reduces one HDD song to a catalog entry through the same
// parseHDDSong prologue extractHDDSong runs, then summarises the neutral timeline
// — so the two report identical prologue deviations and agree on the v-track
// count and length by construction. It decodes no take and constructs no Decoder.
func summarizeHDDSong(song hdd.Song) (SongInfo, []Deviation) {
	ps, _, devs := parseHDDSong(song)
	return ps.songInfo(), devs
}

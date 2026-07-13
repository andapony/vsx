package core

import (
	"fmt"
	"io"
	"os"
	"time"

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
	SampleRate   int    // native sample rate in Hz, the divisor Duration uses

	// Created and Saved are the SONG.VR5 header timestamps (§4.4): when the song
	// was first created and last saved. They are the durable cross-media identity
	// key for VR5 songs (§4.3). VR9 songs carry no timestamps anywhere (§4.4), so
	// both are the zero Time there — rendered as the absent placeholder.
	Created time.Time
	Saved   time.Time
	// Modified is the latest event-record timestamp across the song's timeline
	// (§7): when the timeline was last actually edited, which can predate Saved
	// (a re-save without edits bumps Saved but creates no event). Zero on VR9 and
	// on a song with no stamped events.
	Modified time.Time
}

// Duration is the song's timeline length as a wall-clock duration, applying the
// private samples-per-frame framing (§3) so the command layer never has to know
// that constant to render a song length (issue #27, seam 2). A zero or negative
// sample rate yields a zero duration rather than a divide-by-zero.
func (s SongInfo) Duration() time.Duration {
	if s.SampleRate <= 0 {
		return 0
	}
	samples := time.Duration(s.Frames) * samplesPerFrame
	return samples * time.Second / time.Duration(s.SampleRate)
}

// List enumerates a Source's songs without decoding audio. It is the thin path
// adapter over enumerateSource, reducing each parsed song to a catalog entry: the
// populated v-track count and timeline length are both derivable from the events
// alone (through the same vtrackStats rule buildVTrack uses, §8), so no take is
// ever read or decoded and no Decoder is ever constructed. This makes listing a
// multi-gigabyte HDD image a matter of seconds.
func List(sourcePath string, opts Options) ([]SongInfo, []Deviation, error) {
	return enumerateSource(sourcePath, opts, parsedSong.songInfo)
}

// Detail enumerates a Source's songs into the verbose per-song view (#36): one
// row per populated v-track, with its event count, length, and — on VR5 — the
// first/last event timestamps. It runs the identical prologue List does (the same
// parsed timeline, so its v-track set and lengths agree with List and Extract by
// construction) and, like List, decodes no take. Callers narrow the result to the
// songs they want by key; core enumerates them all.
func Detail(sourcePath string, opts Options) ([]SongDetail, []Deviation, error) {
	return enumerateSource(sourcePath, opts, parsedSong.detail)
}

// enumerateSource is the path adapter both List and Detail sit on: it opens the
// file (or, for a directory, the disc-file set) and reduces every parsed song
// with reduce — parsedSong.songInfo for the catalog, parsedSong.detail for the
// verbose view — so the two views share one identify/dispatch and cannot drift.
// It mirrors Extract's identify/dispatch (sharing identifyReader and
// openBackupSet), and never constructs a Decoder.
func enumerateSource[T any](sourcePath string, opts Options, reduce func(parsedSong) T) ([]T, []Deviation, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("core: stat source: %w", err)
	}
	if info.IsDir() {
		paths, err := discPathsInDir(sourcePath)
		if err != nil {
			return nil, nil, fmt.Errorf("core: reading directory %q: %w", sourcePath, err)
		}
		return enumerateSetReader(discInputsFromPaths(paths), opts, reduce)
	}

	f, err := os.Open(sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("core: opening source: %w", err)
	}
	defer f.Close()
	return enumerateReader(f, info.Size(), opts, reduce)
}

// ListSet treats the given disc-image files as one multi-disc CD backup set
// (§5.6) — the same grouping a directory of those files gets — and enumerates
// its songs. Use it when the discs are passed as separate paths rather than a
// folder.
func ListSet(paths []string, opts Options) ([]SongInfo, []Deviation, error) {
	return enumerateSetReader(discInputsFromPaths(paths), opts, parsedSong.songInfo)
}

// DetailSet is Detail's multi-disc counterpart: the verbose per-song view over a
// backup set given as separate disc files (§5.6).
func DetailSet(paths []string, opts Options) ([]SongDetail, []Deviation, error) {
	return enumerateSetReader(discInputsFromPaths(paths), opts, parsedSong.detail)
}

// listReader is the single-Source ReaderAt entry the List reduction sits on; it
// is retained as a thin wrapper so the test harness can list an in-memory image.
func listReader(r io.ReaderAt, size int64, opts Options) ([]SongInfo, []Deviation, error) {
	return enumerateReader(r, size, opts, parsedSong.songInfo)
}

// listSetReader is the backup-set counterpart to listReader, retained for the
// test harness.
func listSetReader(discs []discInput, opts Options) ([]SongInfo, []Deviation, error) {
	return enumerateSetReader(discs, opts, parsedSong.songInfo)
}

// enumerateReader identifies a single Source from its bytes and reduces each of
// its songs with reduce, without reading or decoding a take — so no Decoder is
// ever needed. It reads but does not own r.
func enumerateReader[T any](r io.ReaderAt, size int64, opts Options, reduce func(parsedSong) T) ([]T, []Deviation, error) {
	id, err := identifyReader(r, size, opts)
	if err != nil {
		return nil, nil, err
	}

	if id.isHDD {
		songs, devs := mapHDDSongs(id.vol, reduce)
		return songs, devs, nil
	}

	var devs []Deviation
	if id.cooked {
		devs = append(devs, cookedRipDeviation())
	}
	mf := formatFor(id.machine)
	if mf == nil {
		return nil, nil, fmt.Errorf("core: source identified but not yet supported by this build; machine=%v", id.machine)
	}
	songs, sdevs := mapCDSongs(id.img, mf, reduce)
	devs = append(devs, sdevs...)
	return songs, devs, nil
}

// enumerateSetReader reduces the songs of a multi-disc backup set (§5.6),
// grouping the disc byte-inputs exactly as extractSetReader does, over the
// stitched reader and without decoding any take.
func enumerateSetReader[T any](discs []discInput, opts Options, reduce func(parsedSong) T) ([]T, []Deviation, error) {
	if opts.As.kind == kindHDD {
		closeInputs(discs)
		return nil, nil, errHDDBackupSet()
	}

	set, err := openBackupSet(discs, opts)
	if err != nil {
		return nil, nil, err
	}
	defer closeInputs(set.discs)

	devs := append([]Deviation{}, set.devs...)
	if set.cooked {
		devs = append(devs, cookedRipDeviation())
	}
	mf := formatFor(set.machine)
	if mf == nil {
		return nil, nil, fmt.Errorf("core: backup set machine not supported by this build; machine=%v", set.machine)
	}
	songs, sdevs := mapCDSongs(set.reader, mf, reduce)
	devs = append(devs, sdevs...)
	return songs, devs, nil
}

// summarizeVTracks computes a song's populated v-track count and overall
// timeline length in frames from its parsed songTimeline — the same neutral
// timeline the extractor's buildTracks consumes, and through the same vtrackStats
// rule buildVTrack applies, so List and Extract agree by construction rather than
// by two loops kept in sync. It reports frames rather than samples (SongInfo.Duration
// applies the samples-per-frame framing) and never touches a take's audio, so no
// Decoder is needed to answer either question.
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

// mapHDDSongs enumerates a Roland VS live disk's songs and reduces each with
// reduce, reusing hdd.Volume.Songs and the same parseHDDSong prologue
// extractHDDSong runs — so List and Extract report identical prologue deviations
// and agree on the v-track count and length by construction — but never resolving
// or decoding a take, and constructing no Decoder.
func mapHDDSongs[T any](vol *hdd.Volume, reduce func(parsedSong) T) ([]T, []Deviation) {
	songs, err := vol.Songs()
	if err != nil {
		return nil, []Deviation{{Location: "disk", SpecRef: "§4", Severity: SeverityError,
			Message: fmt.Sprintf("enumerating HDD songs: %v", err)}}
	}
	var out []T
	var devs []Deviation
	for _, s := range songs {
		ps, _, sdevs := parseHDDSong(s)
		devs = append(devs, sdevs...)
		out = append(out, reduce(ps))
	}
	return out, devs
}

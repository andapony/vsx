package core

import (
	"fmt"
	"os"
	"strings"

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
// from the events alone (mirroring buildVTrack's hasAudio/length, §8), so no
// take is ever read or decoded and no Decoder is ever constructed. This makes
// listing a multi-gigabyte HDD image a matter of seconds.
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
	var songs []SongInfo
	var sdevs []Deviation
	switch h.machine {
	case machineVR9:
		songs, sdevs = listVR9(h.img)
	case machineVR5:
		songs, sdevs = listVR5(h.img)
	default:
		return nil, nil, fmt.Errorf("core: source identified but not yet supported by this build; machine=%v", h.machine)
	}
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
	if strings.EqualFold(strings.TrimSpace(opts.As), "hdd") {
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
	var songs []SongInfo
	var sdevs []Deviation
	switch set.machine {
	case machineVR9:
		songs, sdevs = listVR9(set.reader)
	case machineVR5:
		songs, sdevs = listVR5(set.reader)
	default:
		return nil, nil, fmt.Errorf("core: backup set machine not supported by this build; machine=%v", set.machine)
	}
	devs = append(devs, sdevs...)
	return songs, devs, nil
}

// summarizeVTracks computes a song's populated v-track count and overall
// timeline length in frames from its parsed songTimeline — the same neutral
// timeline the extractor's buildTracks consumes, so List and Extract agree by
// construction. It mirrors buildVTrack's hasAudio/length computation (§8) — a
// v-track is populated iff it has at least one take-bearing event (fileID != 0),
// and its length is the maximum origin-relative end frame across every event in
// the group (audio or erase) — but reports frames rather than samples (the
// caller renders a duration as frames*samplesPerFrame/sampleRate) and never
// touches a take's audio, so no Decoder is needed to answer either question.
func summarizeVTracks(st songTimeline) (vtracks, frames int) {
	for _, g := range st.groups {
		hasAudio := false
		length := 0
		for _, e := range g.events {
			if e.fileID != 0 {
				hasAudio = true
			}
			if end := int(e.end) - st.origin; end > length {
				length = end
			}
		}
		if hasAudio {
			vtracks++
			if length > frames {
				frames = length
			}
		}
	}
	return vtracks, frames
}

// listVR9 enumerates a VS-880EX CD archive's songs and summarises each from
// its event list, reusing the same chain walk and grouping the extractor uses
// but never resolving or decoding a take.
func listVR9(img cdSource) ([]SongInfo, []Deviation) {
	files, devs, err := walkVR9(img)
	if err != nil {
		devs = append(devs, Deviation{Location: "disc", SpecRef: "§5.4", Severity: SeverityError,
			Message: fmt.Sprintf("walking archive: %v", err)})
		return nil, devs
	}
	var songs []SongInfo
	for _, g := range groupSongs(files) {
		key := cdSongKey(int(g.number))
		info, sdevs := summarizeVR9Song(img, g, key)
		devs = append(devs, sdevs...)
		songs = append(songs, info)
	}
	return songs, devs
}

// summarizeVR9Song reads and parses one VS-880EX song's event list (exactly as
// extractSong does up to the point takes would be resolved) and reduces it to
// a catalog entry.
func summarizeVR9Song(img cdSource, g songGroup, key SongKey) (SongInfo, []Deviation) {
	loc := fmt.Sprintf("song %d", g.number)
	base := SongInfo{Key: key, StoredNumber: int(g.number), Name: g.name, Machine: "VR9"}

	elst, ok := findEventList(g.files)
	if !ok {
		return base, []Deviation{{Location: loc, SpecRef: "§5.4", Severity: SeverityError,
			Message: "no EVENTLST file found for song; nothing to extract"}}
	}
	data, err := img.ReadUserData(elst.dataOff, int(elst.size))
	if err != nil {
		return base, []Deviation{{Location: loc, SpecRef: "§6.2", Severity: SeverityError,
			Message: fmt.Sprintf("reading event list: %v", err)}}
	}
	st, devs := vr9{}.parseTimeline(data)

	sampleRate, rateDev := rateFromByte(elst.rateByte, loc)
	if rateDev != nil {
		devs = append(devs, *rateDev)
	}

	base.VTracks, base.Frames = summarizeVTracks(st)
	base.SampleRate = sampleRate
	return base, devs
}

// listVR5 enumerates a VS-1880 CD archive's songs and summarises each from its
// V-track table, reusing the same chain walk and grouping the extractor uses
// but never resolving or decoding a take.
func listVR5(img cdSource) ([]SongInfo, []Deviation) {
	files, devs, err := walkVR5(img)
	if err != nil {
		devs = append(devs, Deviation{Location: "disc", SpecRef: "§5.4", Severity: SeverityError,
			Message: fmt.Sprintf("walking archive: %v", err)})
		return nil, devs
	}
	var songs []SongInfo
	for i, g := range groupVR5Songs(files) {
		number, ndevs := vr5SongNumber(img, g.files, i)
		devs = append(devs, ndevs...)
		key := cdSongKey(number)
		info, sdevs := summarizeVR5Song(img, g, number, key)
		devs = append(devs, sdevs...)
		songs = append(songs, info)
	}
	return songs, devs
}

// summarizeVR5Song reads and parses one VS-1880 song's V-track table (exactly
// as extractVR5Song does up to the point takes would be resolved) and reduces
// it to a catalog entry.
func summarizeVR5Song(img cdSource, g songGroup, number int, key SongKey) (SongInfo, []Deviation) {
	loc := fmt.Sprintf("song %d", number)
	base := SongInfo{Key: key, StoredNumber: number, Name: g.name, Machine: "VR5"}

	elst, ok := findEventList(g.files)
	if !ok {
		return base, []Deviation{{Location: loc, SpecRef: "§5.4", Severity: SeverityError,
			Message: "no EVENTLST file found for song; nothing to extract"}}
	}
	data, err := img.ReadUserData(elst.dataOff, int(elst.size))
	if err != nil {
		return base, []Deviation{{Location: loc, SpecRef: "§6.1", Severity: SeverityError,
			Message: fmt.Sprintf("reading event list: %v", err)}}
	}
	st, devs := vr5{}.parseTimeline(data)

	sampleRate, rateDev := rateFromByte(elst.rateByte, loc)
	if rateDev != nil {
		devs = append(devs, *rateDev)
	}

	base.VTracks, base.Frames = summarizeVTracks(st)
	base.SampleRate = sampleRate
	return base, devs
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

// summarizeHDDSong reads one HDD song directory's SONG file (number, name,
// rate, machine extension, §4.4) and EVENTLST (§4.5/§4.6), exactly as
// extractHDDSong does up to the point takes would be resolved, and reduces it
// to a catalog entry.
func summarizeHDDSong(song hdd.Song) (SongInfo, []Deviation) {
	key := hddSongKey(song.Partition, song.Index)

	files, err := song.Files()
	if err != nil {
		return SongInfo{Key: key}, []Deviation{{Location: song.Name, SpecRef: "§4.3", Severity: SeverityError,
			Message: fmt.Sprintf("reading song directory: %v", err)}}
	}

	songEntry, ok := findHDDFile(files, "SONG")
	if !ok {
		return SongInfo{Key: key}, []Deviation{{Location: song.Name, SpecRef: "§4.4", Severity: SeverityError,
			Message: "no SONG file in directory; cannot determine format or rate"}}
	}
	sdata, _, err := songEntry.Read()
	if err != nil {
		return SongInfo{Key: key}, []Deviation{{Location: song.Name, SpecRef: "§4.4", Severity: SeverityError,
			Message: fmt.Sprintf("reading SONG file: %v", err)}}
	}
	number, name, rateByte, _, ok := parseSongFile(sdata)
	if !ok {
		return SongInfo{Key: key}, []Deviation{{Location: song.Name, SpecRef: "§4.4", Severity: SeverityError,
			Message: "SONG file shorter than its 20-byte header; cannot determine format or rate"}}
	}

	loc := fmt.Sprintf("song %d", number)
	base := SongInfo{Key: key, StoredNumber: number, Name: name, Machine: song.Ext}
	var devs []Deviation
	sampleRate, rateDev := rateFromByte(rateByte, loc)
	if rateDev != nil {
		devs = append(devs, *rateDev)
	}
	base.SampleRate = sampleRate

	elEntry, ok := findHDDFile(files, "EVENTLST")
	if !ok {
		return base, append(devs, Deviation{Location: loc, SpecRef: "§4.3", Severity: SeverityError,
			Message: "no EVENTLST file for song; nothing to extract"})
	}
	eldata, _, err := elEntry.Read()
	if err != nil {
		return base, append(devs, Deviation{Location: loc, SpecRef: "§4.5", Severity: SeverityError,
			Message: fmt.Sprintf("reading event list: %v", err)})
	}

	mf, extDev := hddFormat(song.Ext, loc)
	if extDev != nil {
		return base, append(devs, *extDev)
	}
	st, edevs := mf.parseTimeline(eldata)
	devs = append(devs, edevs...)
	base.VTracks, base.Frames = summarizeVTracks(st)
	return base, devs
}

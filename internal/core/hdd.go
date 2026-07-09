package core

import (
	"encoding/binary"
	"fmt"
	"iter"
	"strings"

	"github.com/andapony/vsx/internal/hdd"
)

// extractHDD enumerates a Roland VS live disk (§4) and returns a lazy iterator
// over its per-v-track results, one song directory at a time so a multi-GB image
// is never fully materialized. Replay deviations for a song are appended to devs
// as that song is consumed — the same accumulation contract as the CD path.
//
// The machine is resolved per song directory by its extension (§4.3): a disk
// hosting both VS-1880 (VR5) and VS-880EX (VR9) songs extracts each correctly.
// Take resolution (by filename, §4.3) and the §8.3 FAT-integrity check are the
// only steps that differ from CD; event-list parsing and timeline replay are the
// shared §6/§8 kernels, since the HDD EVENTLST formats (§4.5/§4.6) are
// byte-identical to their CD counterparts.
func extractHDD(vol *hdd.Volume, dec Decoder, devs *[]Deviation, stereo bool, report func(Progress)) (iter.Seq2[TrackResult, error], error) {
	songs, err := vol.Songs()
	if err != nil {
		return nil, fmt.Errorf("core: enumerating HDD songs: %w", err)
	}
	return func(yield func(TrackResult, error) bool) {
		for i, s := range songs {
			report(Progress{Phase: ProgressExtracting, Song: i + 1, TotalSongs: len(songs), SongName: s.Name})
			tracks, sdevs := extractHDDSong(dec, s, stereo)
			*devs = append(*devs, sdevs...)
			for _, tr := range tracks {
				if !yield(tr, nil) {
					return
				}
			}
		}
		report(Progress{Phase: ProgressDone})
	}, nil
}

// extractHDDSong replays one song directory into per-v-track results: read
// SONG.VRx for the number/name/rate/format (§4.4), parse EVENTLST.VRx in the
// machine's form (§4.5 positional table / §4.6 flat log), resolve and integrity-
// check the referenced takes (§4.3/§8.3), and build the timeline with the shared
// kernel using the partition's BPB cluster size (§4.2, for MT2 page-padding).
func extractHDDSong(dec Decoder, song hdd.Song, stereo bool) ([]TrackResult, []Deviation) {
	files, err := song.Files()
	if err != nil {
		return nil, []Deviation{{Location: song.Name, SpecRef: "§4.3", Severity: SeverityError,
			Message: fmt.Sprintf("reading song directory: %v", err)}}
	}

	songEntry, ok := findHDDFile(files, "SONG")
	if !ok {
		return nil, []Deviation{{Location: song.Name, SpecRef: "§4.4", Severity: SeverityError,
			Message: "no SONG file in directory; cannot determine format or rate"}}
	}
	sdata, _, err := songEntry.Read()
	if err != nil {
		return nil, []Deviation{{Location: song.Name, SpecRef: "§4.4", Severity: SeverityError,
			Message: fmt.Sprintf("reading SONG file: %v", err)}}
	}
	number, name, rateByte, formatCode, ok := parseSongFile(sdata)
	if !ok {
		return nil, []Deviation{{Location: song.Name, SpecRef: "§4.4", Severity: SeverityError,
			Message: "SONG file shorter than its 20-byte header; cannot determine format or rate"}}
	}
	loc := fmt.Sprintf("song %d", number)
	ref := SongRef{Number: number, Name: name}
	format := Format(formatCode)
	sampleRate, rateDev := rateFromByte(rateByte, loc)
	aud := audioSpec{sampleRate: sampleRate, format: format, clusterSize: song.ClusterSize()}

	elEntry, ok := findHDDFile(files, "EVENTLST")
	if !ok {
		return nil, []Deviation{{Location: loc, SpecRef: "§4.3", Severity: SeverityError,
			Message: "no EVENTLST file for song; nothing to extract"}}
	}
	eldata, _, err := elEntry.Read()
	if err != nil {
		return nil, []Deviation{{Location: loc, SpecRef: "§4.5", Severity: SeverityError,
			Message: fmt.Sprintf("reading event list: %v", err)}}
	}

	var devs []Deviation
	if rateDev != nil {
		devs = append(devs, *rateDev)
	}

	switch song.Ext {
	case "VR5":
		entries, edevs := parseVR5EventList(eldata)
		devs = append(devs, edevs...)
		refs, counts := gatherVR5Refs(entries)
		takes, tdevs := decodeHDDTakes(files, dec, refs, counts, format, loc)
		devs = append(devs, tdevs...)
		tracks, tldevs := buildVR5Tracks(entries, takes, ref, aud, stereo)
		return tracks, append(devs, tldevs...)
	case "VR9":
		events, edevs := parseVR9Log(eldata)
		devs = append(devs, edevs...)
		refs, counts := gatherVR9Refs(events)
		takes, tdevs := decodeHDDTakes(files, dec, refs, counts, format, loc)
		devs = append(devs, tdevs...)
		tracks, tldevs := buildVR9Tracks(events, takes, ref, aud, stereo)
		return tracks, append(devs, tldevs...)
	default:
		return nil, append(devs, Deviation{Location: loc, SpecRef: "§4.3", Severity: SeverityError,
			Message: fmt.Sprintf("unsupported machine extension %q", song.Ext)})
	}
}

// parseSongFile reads the §4.4 SONG.VRx fields both machines share: the source
// folder number at 0x04, the name at 0x06, and the rate/format bytes at
// 0x12/0x13. VR9 is 20 bytes and VR5 38; only the shared 0x00–0x13 prefix is
// needed. ok is false when the file is too short to hold that prefix.
func parseSongFile(data []byte) (number int, name string, rateByte, formatCode byte, ok bool) {
	if len(data) < 0x14 {
		return 0, "", 0, 0, false
	}
	number = int(binary.BigEndian.Uint16(data[0x04:]))
	name = trimName(data[0x06:0x12])
	return number, name, data[0x12], data[0x13], true
}

// findHDDFile returns the entry with the given fixed base name (§4.3: "SONG",
// "EVENTLST").
func findHDDFile(files []hdd.Entry, base string) (hdd.Entry, bool) {
	for _, f := range files {
		if f.Name == base {
			return f, true
		}
	}
	return hdd.Entry{}, false
}

// gatherVR5Refs collects the take FileIDs a VR5 timeline references, in
// first-seen order, together with the highest 0x18 cluster count claimed for
// each (for the §8.3 integrity check).
func gatherVR5Refs(entries []vr5Entry) ([]uint16, map[uint16]int) {
	var refs []uint16
	counts := map[uint16]int{}
	seen := map[uint16]bool{}
	for _, ent := range entries {
		for _, e := range ent.events {
			addRef(&refs, counts, seen, e.fileID, e.clusterCount)
		}
	}
	return refs, counts
}

// gatherVR9Refs is gatherVR5Refs for a VR9 flat log.
func gatherVR9Refs(events []vr9Event) ([]uint16, map[uint16]int) {
	var refs []uint16
	counts := map[uint16]int{}
	seen := map[uint16]bool{}
	for _, e := range events {
		addRef(&refs, counts, seen, e.fileID, e.clusterCount)
	}
	return refs, counts
}

// addRef records a take reference (skipping erases, FileID 0) and tracks the
// largest cluster count claimed for it.
func addRef(refs *[]uint16, counts map[uint16]int, seen map[uint16]bool, id, clusterCount uint16) {
	if id == 0 {
		return
	}
	if !seen[id] {
		seen[id] = true
		*refs = append(*refs, id)
	}
	if c := int(clusterCount); c > counts[id] {
		counts[id] = c
	}
}

// decodeHDDTakes resolves each referenced take by filename (§4.3) — format the
// FileID as TAKE%04X, find that directory entry, and read from the entry's own
// first cluster, never by chaining from the event's cluster value — runs the
// §8.3 integrity check (the FAT chain must yield at least the event's claimed
// cluster count), and decodes through the seam with the take's partition cluster
// size (§4.2). A referenced take with no file on disk is left absent for the
// timeline builder to report (§10); a truncated chain is warned but its partial
// audio is still laid down (§8/§8.3).
func decodeHDDTakes(files []hdd.Entry, dec Decoder, refs []uint16, counts map[uint16]int, format Format, loc string) (map[uint16]PCM, []Deviation) {
	byName := map[string]hdd.Entry{}
	for _, f := range files {
		if strings.HasPrefix(f.Name, "TAKE") {
			byName[f.Name] = f
		}
	}
	takes := map[uint16]PCM{}
	var devs []Deviation
	for _, id := range refs {
		if _, done := takes[id]; done {
			continue
		}
		name := fmt.Sprintf("TAKE%04X", id)
		entry, ok := byName[name]
		if !ok {
			continue // reported by the timeline builder as a missing take
		}
		data, clusters, err := entry.Read()
		if err != nil {
			devs = append(devs, Deviation{Location: loc, SpecRef: "§4.3", Severity: SeverityError,
				Message: fmt.Sprintf("reading take %s: %v", name, err)})
			continue
		}
		if expect := counts[id]; expect > 0 && clusters < expect {
			devs = append(devs, Deviation{Location: loc, SpecRef: "§8.3", Severity: SeverityWarning,
				Message: fmt.Sprintf("take %s FAT chain yields %d cluster(s) but the event claims %d; take is truncated/corrupt", name, clusters, expect)})
		}
		pcm, err := dec.Decode(format, data, entry.ClusterSize())
		if err != nil {
			devs = append(devs, Deviation{Location: loc, SpecRef: "§2", Severity: SeverityError,
				Message: fmt.Sprintf("decoding take %s: %v", name, err)})
			continue
		}
		takes[id] = pcm
	}
	return takes, devs
}

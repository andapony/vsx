package core

import (
	"encoding/binary"
	"fmt"
	"iter"
	"strings"
	"time"

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
func extractHDD(vol *hdd.Volume, ctx extractCtx) (iter.Seq2[TrackResult, error], error) {
	songs, err := vol.Songs()
	if err != nil {
		return nil, fmt.Errorf("core: enumerating HDD songs: %w", err)
	}
	return func(yield func(TrackResult, error) bool) {
		present := make(map[SongKey]bool, len(songs))
		for i, s := range songs {
			key := hddSongKey(s.Partition, s.Index)
			present[key] = true
			if !ctx.selected(key) {
				continue
			}
			ctx.report(Progress{Phase: ProgressExtracting, Song: i + 1, TotalSongs: len(songs), SongName: s.Name})
			tracks, sdevs := extractHDDSong(ctx.dec, s, ctx.stereo)
			*ctx.devs = append(*ctx.devs, sdevs...)
			for _, tr := range tracks {
				if !yield(tr, nil) {
					return
				}
			}
		}
		*ctx.devs = append(*ctx.devs, ctx.unmatchedSongDeviations(present)...)
		ctx.report(Progress{Phase: ProgressDone})
	}, nil
}

// parseHDDSong runs one song directory's prologue: read SONG.VRx for the
// number/name/rate/format (§4.4), then parse EVENTLST.VRx in the machine's form
// (§4.5 positional table / §4.6 flat log) into the machine-neutral parsedSong
// both List and Extract consume. It also returns the song's directory entries so
// Extract can resolve takes without re-reading the directory (List discards
// them). Deviations are reported in one canonical order — SONG-header, then
// sample-rate, then event-list — for every point the prologue reaches; a failure
// leaves the timeline empty with the reason in the deviations. The partition's
// BPB cluster size (§4.2) rides in the audio spec for MT2 page-padding.
func parseHDDSong(song hdd.Song) (parsedSong, []hdd.Entry, []Deviation) {
	key := hddSongKey(song.Partition, song.Index)
	ps := parsedSong{ref: SongRef{Key: key}, machine: song.Ext}

	files, err := song.Files()
	if err != nil {
		return ps, nil, []Deviation{{Location: song.Name, SpecRef: "§4.3", Severity: SeverityError,
			Message: fmt.Sprintf("reading song directory: %v", err)}}
	}

	songEntry, ok := findHDDFile(files, "SONG")
	if !ok {
		return ps, files, []Deviation{{Location: song.Name, SpecRef: "§4.4", Severity: SeverityError,
			Message: "no SONG file in directory; cannot determine format or rate"}}
	}
	sdata, _, err := songEntry.Read()
	if err != nil {
		return ps, files, []Deviation{{Location: song.Name, SpecRef: "§4.4", Severity: SeverityError,
			Message: fmt.Sprintf("reading SONG file: %v", err)}}
	}
	hdr, ok := parseSongFile(sdata)
	if !ok {
		return ps, files, []Deviation{{Location: song.Name, SpecRef: "§4.4", Severity: SeverityError,
			Message: "SONG file shorter than its 20-byte header; cannot determine format or rate"}}
	}

	loc := fmt.Sprintf("song %d", hdr.number)
	ps.ref = SongRef{Key: key, Number: hdr.number, Name: hdr.name}
	ps.created, ps.saved = hdr.created, hdr.saved
	sampleRate, rateDev := rateFromByte(hdr.rateByte, loc)
	ps.aud = audioSpec{sampleRate: sampleRate, format: Format(hdr.formatCode), clusterSize: song.ClusterSize()}

	var devs []Deviation
	if rateDev != nil {
		devs = append(devs, *rateDev)
	}

	elEntry, ok := findHDDFile(files, "EVENTLST")
	if !ok {
		return ps, files, append(devs, Deviation{Location: loc, SpecRef: "§4.3", Severity: SeverityError,
			Message: "no EVENTLST file for song; nothing to extract"})
	}
	eldata, _, err := elEntry.Read()
	if err != nil {
		return ps, files, append(devs, Deviation{Location: loc, SpecRef: "§4.5", Severity: SeverityError,
			Message: fmt.Sprintf("reading event list: %v", err)})
	}
	mf, extDev := hddFormat(song.Ext, loc)
	if extDev != nil {
		return ps, files, append(devs, *extDev)
	}
	st, edevs := mf.parseTimeline(eldata)
	ps.st = st
	return ps, files, append(devs, edevs...)
}

// extractHDDSong replays one song directory into per-v-track results: it runs the
// shared parseHDDSong prologue, then resolves and integrity-checks the referenced
// takes (§4.3/§8.3) and builds the timeline with the shared kernel. On a prologue
// that produced no timeline the reference gathering is empty, so no take is read
// and no track is built — the prologue's deviations are the whole result.
func extractHDDSong(dec Decoder, song hdd.Song, stereo bool) ([]TrackResult, []Deviation) {
	ps, files, devs := parseHDDSong(song)
	loc := fmt.Sprintf("song %d", ps.ref.Number)
	refs, counts := gatherRefs(ps.st)
	takes, tdevs := decodeHDDTakes(files, dec, refs, counts, ps.aud.format, loc)
	devs = append(devs, tdevs...)
	tracks, tldevs := buildTracks(ps.st, takes, ps.ref, ps.aud, stereo)
	return tracks, append(devs, tldevs...)
}

// songHeader is the parsed SONG.VRx header (§4.4). The number/name/rate/format
// fields are shared by both machines; created/saved are the VR5-only timestamps
// at 0x14/0x1C, left zero on the shorter VR9 header (which stamps nothing).
type songHeader struct {
	number         int
	name           string
	rateByte       byte
	formatCode     byte
	created, saved time.Time
}

// parseSongFile reads the §4.4 SONG.VRx fields: the source folder number at
// 0x04, the name at 0x06, and the rate/format bytes at 0x12/0x13 — the shared
// 0x00–0x13 prefix both machines carry — plus, when the header is long enough to
// be a VR5 one (38 bytes), the created and last-saved timestamps at 0x14 and
// 0x1C. VR9's 20-byte header is too short to hold them, so they stay zero. ok is
// false when the file is too short to hold even the shared prefix.
func parseSongFile(data []byte) (songHeader, bool) {
	if len(data) < 0x14 {
		return songHeader{}, false
	}
	h := songHeader{
		number:     int(binary.BigEndian.Uint16(data[0x04:])),
		name:       trimName(data[0x06:0x12]),
		rateByte:   data[0x12],
		formatCode: data[0x13],
	}
	// VR5 (38-byte) headers carry the two timestamps; decodeStamp bounds-checks,
	// so a header truncated between the two fields yields the earlier one and a
	// zero later one.
	h.created = decodeStamp(headerStamp(data, 0x14))
	h.saved = decodeStamp(headerStamp(data, 0x1C))
	return h, true
}

// headerStamp returns the 8-byte timestamp slice at off, or nil when the header
// is too short to contain it (decodeStamp maps nil to the absent zero Time).
func headerStamp(data []byte, off int) []byte {
	if len(data) < off+8 {
		return nil
	}
	return data[off : off+8]
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

// hddFormat resolves an HDD song directory's machine extension (§4.3) to its
// event-list adapter, or returns the "unsupported machine extension" deviation
// when the extension is not one this build handles — so extract and list share
// one home for that §4.3 message.
func hddFormat(ext, loc string) (machineFormat, *Deviation) {
	mf := formatFor(machineForExt(ext))
	if mf == nil {
		return nil, &Deviation{Location: loc, SpecRef: "§4.3", Severity: SeverityError,
			Message: fmt.Sprintf("unsupported machine extension %q", ext)}
	}
	return mf, nil
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

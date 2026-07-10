package core

import (
	"encoding/binary"
	"testing"

	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mtpBytes returns n MTP blocks (16 bytes each) that decode to 16n 24-bit
// samples (§2). Zero bytes decode to near-silence, which is enough to make a
// v-track populated (a real, non-zero FileID is what marks audio, not the sample
// values).
func mtpBytes(nBlocks int) []byte { return make([]byte, 16*nBlocks) }

// vr5EventListBytes hand-builds a §6.1 VS-1880 event list: the magic, a
// registry of `registry` skipped 64-byte records, 288 positional entries (only
// those in `events`/`names` populated), and a trailing "V.T"-shaped remnant.
func vr5EventListBytes(registry int, events map[int][]vsfix.VR5Event, names map[int]string) []byte {
	out := make([]byte, 18)
	copy(out, "TAKE EVENT LIST ")
	binary.BigEndian.PutUint16(out[0x10:], uint16(registry))
	for i := 0; i < registry; i++ {
		out = append(out, make([]byte, 64)...) // historical registry — must be skipped
	}
	for i := 0; i < 288; i++ {
		entry := make([]byte, 18)
		name := names[i]
		if name == "" {
			name = "V.T"
		}
		copy(entry[:16], []byte(name))
		evs := events[i]
		binary.BigEndian.PutUint16(entry[16:], uint16(len(evs)))
		out = append(out, entry...)
		for _, e := range evs {
			r := make([]byte, 64)
			binary.BigEndian.PutUint32(r[0x00:], e.Start)
			binary.BigEndian.PutUint32(r[0x04:], e.End)
			binary.BigEndian.PutUint32(r[0x08:], e.Trimmed)
			binary.BigEndian.PutUint16(r[0x14:], e.FileID)
			out = append(out, r...)
		}
	}
	// Optimize remnant past the 288th entry — a positional parser ignores it.
	rem := make([]byte, 18+64)
	copy(rem, "V.T remnant")
	binary.BigEndian.PutUint16(rem[16:], 1)
	return append(out, rem...)
}

// TestParseVR5EventListPositional locks the §6.1 rules: the historical registry
// is skipped, exactly 288 entries are parsed positionally (track = i/16+1,
// v-track = i%16+1), user names survive without any "V.T" gating, and the
// trailing remnant past entry 288 is never parsed as a 289th entry.
func TestParseVR5EventListPositional(t *testing.T) {
	data := vr5EventListBytes(
		2, // two registry records to skip
		map[int][]vsfix.VR5Event{
			0:  {{Start: 0, End: 4, FileID: 0x0100}},  // T1/V1
			17: {{Start: 8, End: 12, FileID: 0x0200}}, // T2/V2
		},
		map[int]string{0: "Bass"},
	)

	entries, devs := parseVR5EventList(data)
	assert.Empty(t, devs)
	require.Len(t, entries, 288, "exactly 288 positional entries, remnant ignored")

	assert.Equal(t, 1, entries[0].track)
	assert.Equal(t, 1, entries[0].vtrack)
	assert.Equal(t, "Bass", entries[0].name, "user names are kept without V.T gating")
	require.Len(t, entries[0].events, 1)
	assert.EqualValues(t, 0x0100, entries[0].events[0].fileID, "registry skip lands the table at the right offset")

	assert.Equal(t, 2, entries[17].track)
	assert.Equal(t, 2, entries[17].vtrack)
	require.Len(t, entries[17].events, 1)
	assert.EqualValues(t, 0x0200, entries[17].events[0].fileID)

	// An unpopulated position carries no events.
	assert.Empty(t, entries[5].events)
}

// TestUserTrackName locks the §6.1/§7 default-vs-user name rule: both default
// forms ("V.T " + single digit, and the space-less two-digit "V.T10-…") and
// blank names yield no filename suffix, while a genuine user name — including
// one that merely starts with "V.T" followed by a letter — is kept.
func TestUserTrackName(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"V.T  1- 1", ""},          // default, single-digit track
		{"V.T10- 1", ""},           // default, two-digit track (no space after V.T)
		{"V.T", ""},                // bare default
		{"   ", ""},                // all-spaces name
		{"Bass", "Bass"},           // plain user name
		{"V.Trumpet", "V.Trumpet"}, // user name that starts with "V.T" + a letter
	} {
		assert.Equal(t, tc.want, userTrackName(tc.in), "userTrackName(%q)", tc.in)
	}
}

// TestWalkVR5EnumeratesFiles verifies the §5.4 VR5 chain walk: every file is
// found via its own 0x8000 header block, in source order, and the archive
// header, its second copy, and the song-boundary block (§5.5, caught by the
// magic and +0x8000 checks since VR5 has no marker flag) are all skipped.
func TestWalkVR5EnumeratesFiles(t *testing.T) {
	disc := vsfix.VR5Disc{
		SetID: [4]byte{5, 5, 5, 5},
		Songs: []vsfix.VR5Song{
			{Number: 12, Name: "SONG A", Takes: []vsfix.VR5Take{
				{FileID: 0x9CC7, Name: "TAKE9CC7", MTP: mtpBytes(4)},
			}},
			{Number: 13, Name: "SONG B", Takes: []vsfix.VR5Take{
				{FileID: 0x9CD0, Name: "TAKE9CD0", MTP: mtpBytes(2)},
			}},
		},
	}
	img := imageOf(t, disc.BuildRaw())

	files, devs, err := walkCD(img, vr5{})
	require.NoError(t, err)
	assert.Empty(t, devs, "a well-formed disc walks without deviations")

	// Each song: SONG + EVENTLST + 1 take = 3 files; two songs = 6.
	require.Len(t, files, 6)
	assert.Equal(t, "SONG    VR5", files[0].filename)
	assert.Equal(t, "EVENTLSTVR5", files[1].filename)
	assert.Equal(t, "TAKE9CC7VR5", files[2].filename)
	assert.EqualValues(t, 0x9CC7, files[2].fileID)

	// Song B's files followed the boundary block without desync.
	assert.Equal(t, "SONG B", files[5].songName)
	assert.Equal(t, "TAKE9CD0VR5", files[5].filename)
}

// TestExtractVR5EndToEnd drives a synthetic single-disc VR5 archive through the
// whole pipeline — VR5 detection, header-block walk, 288-entry V-track table,
// MTP take decode, timeline build — and verifies the per-v-track results emerge
// with the right identity, a 24-bit depth, the song number resolved from the
// SONG file, and the user-assigned track name carried through.
func TestExtractVR5EndToEnd(t *testing.T) {
	disc := vsfix.VR5Disc{
		SetID: [4]byte{5, 5, 5, 5},
		Songs: []vsfix.VR5Song{{
			Number: 12, Name: "MIXDOWN",
			Takes: []vsfix.VR5Take{
				{FileID: 0x9CC7, Name: "TAKE9CC7", MTP: mtpBytes(4)}, // 64 samples
			},
			VTracks: []vsfix.VR5VTrack{
				{Track: 1, VTrack: 1, Name: "Bass", Events: []vsfix.VR5Event{
					{Start: 0, End: 4, FileID: 0x9CC7},
				}},
				{Track: 3, VTrack: 16, Name: "V.T  3-16", Events: []vsfix.VR5Event{
					{Start: 0, End: 4, FileID: 0x9CC7},
				}},
			},
		}},
	}
	tracks, devs := collectTracks(t, mustExtractBytes(t, disc.BuildRaw(), Options{}))
	assert.Empty(t, devs, "a well-formed VR5 disc extracts cleanly")

	require.Len(t, tracks, 2)

	bass := tracks[0]
	assert.Equal(t, 12, bass.Song.Number, "song number resolved from the SONG file")
	assert.Equal(t, "MIXDOWN", bass.Song.Name)
	assert.Equal(t, 1, bass.Track)
	assert.Equal(t, 1, bass.VTrack)
	assert.Equal(t, "Bass", bass.Name, "user track name is carried into the result")
	assert.Equal(t, 24, bass.PCM.BitDepth, "MTP decodes to 24-bit")
	assert.Len(t, bass.PCM.Samples, 64)
	assert.EqualValues(t, 44100, bass.Take.SampleRate)

	// The default-named v-track (position 3/16) is populated but carries no
	// user name, so nothing is appended to its filename.
	def := tracks[1]
	assert.Equal(t, 3, def.Track)
	assert.Equal(t, 16, def.VTrack)
	assert.Empty(t, def.Name, "a default V.T name is not treated as user-assigned")
}

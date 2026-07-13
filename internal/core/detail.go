package core

import "time"

// VTrackDetail is one populated v-track's row in the per-song verbose view (#36):
// its physical track and v-track, its user-assigned name ("" for the default or
// blank name, §6.1), the number of timeline events that place takes on it, its
// length in frames, and — on VR5 — the first and last event-record timestamps
// (§7) that bracket its recording history. First/Last are the zero Time on VR9
// (no timestamps anywhere) and on a v-track whose events carry no stamp.
type VTrackDetail struct {
	Track, VTrack int
	Name          string
	Events        int
	Frames        int
	First, Last   time.Time
}

// Duration renders this v-track's length as a wall-clock duration, applying the
// private samples-per-frame framing (§3) with the song's sample rate — the same
// conversion SongInfo.Duration does for the whole song, so the command layer
// never has to know that constant. A zero or negative rate yields zero.
func (v VTrackDetail) Duration(sampleRate int) time.Duration {
	if sampleRate <= 0 {
		return 0
	}
	samples := time.Duration(v.Frames) * samplesPerFrame
	return samples * time.Second / time.Duration(sampleRate)
}

// SongDetail is one song's verbose view: its catalog summary plus a row per
// populated v-track, derived from the same parsed timeline List and Extract
// consume — so the v-track set and lengths agree with both by construction.
type SongDetail struct {
	Info   SongInfo
	Tracks []VTrackDetail
}

// detail reduces a parsed song to its verbose view, keeping only populated
// v-tracks (through the same vtrackStats "has a take-bearing event" rule List's
// count and Extract's build use, so all three agree on which v-tracks exist and
// how long they are). Each row's length is that v-track's own end frame, and its
// First/Last are the earliest and latest event stamps on it.
func (ps parsedSong) detail() SongDetail {
	d := SongDetail{Info: ps.songInfo()}
	for _, g := range ps.st.groups {
		hasAudio, endFrame := vtrackStats(g, ps.st.origin)
		if !hasAudio {
			continue
		}
		first, last := eventStampSpan(g.events)
		d.Tracks = append(d.Tracks, VTrackDetail{
			Track:  g.track,
			VTrack: g.vtrack,
			Name:   g.name,
			Events: len(g.events),
			Frames: endFrame,
			First:  first,
			Last:   last,
		})
	}
	return d
}

// eventStampSpan returns the earliest and latest non-zero event-record timestamp
// (§7) among a v-track's events. Events with no stamp (VR9, or an unstamped VR5
// record) are skipped; when none carries a stamp, both results are the zero Time.
func eventStampSpan(evs []timelineEvent) (first, last time.Time) {
	for _, e := range evs {
		if e.stamp.IsZero() {
			continue
		}
		if first.IsZero() || e.stamp.Before(first) {
			first = e.stamp
		}
		if e.stamp.After(last) {
			last = e.stamp
		}
	}
	return first, last
}

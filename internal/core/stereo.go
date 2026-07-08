package core

import "sort"

// builtTrack couples a populated v-track's decoded result with the timeline
// events that produced it, so the §8.4 stereo heuristic can compare adjacent
// tracks' event geometry before the events are discarded. It is the common
// shape both the VR5 and VR9 per-song builders reduce to before pairing.
type builtTrack struct {
	result TrackResult
	events []timelineEvent
}

// pairTracks turns a song's built v-tracks into the results the caller emits,
// applying the conservative §8.4 stereo heuristic when stereo is set.
//
// When stereo is false it is a passthrough: the results in their original order.
// When stereo is true it walks physical tracks in ascending order and pairs two
// adjacent tracks (t and t+1) only when each has exactly one populated v-track
// and their events match (identical counts and Start/End frames, matchEvents).
// A formed pair becomes one interleaved stereo result (left = the lower track);
// every other v-track — ambiguous tracks with several populated v-tracks,
// mismatched or non-adjacent tracks — stays mono. Output is in ascending track
// order (v-tracks of an unpaired multi-v-track track keep their built order).
func pairTracks(built []builtTrack, stereo bool) []TrackResult {
	if !stereo {
		out := make([]TrackResult, len(built))
		for i, b := range built {
			out[i] = b.result
		}
		return out
	}

	// Group the populated v-tracks by physical track. built holds only populated
	// v-tracks, so a track's group length is its populated-v-track count.
	byTrack := map[int][]builtTrack{}
	for _, b := range built {
		t := b.result.Track
		byTrack[t] = append(byTrack[t], b)
	}
	tracks := make([]int, 0, len(byTrack))
	for t := range byTrack {
		tracks = append(tracks, t)
	}
	sort.Ints(tracks)

	var out []TrackResult
	paired := map[int]bool{}
	for i, t := range tracks {
		if paired[t] {
			continue // already emitted as the right channel of an earlier pair
		}
		if len(byTrack[t]) == 1 && i+1 < len(tracks) && tracks[i+1] == t+1 && len(byTrack[t+1]) == 1 {
			left, right := byTrack[t][0], byTrack[t+1][0]
			if matchEvents(left.events, right.events) {
				out = append(out, makeStereo(left.result, right.result))
				paired[t+1] = true
				continue
			}
		}
		for _, b := range byTrack[t] {
			out = append(out, b.result)
		}
	}
	return out
}

// matchEvents reports whether two v-tracks' timelines are a §8.4 stereo match:
// identical event counts and, position for position, the same Start and End
// frames. Only the frame geometry is compared — a real pair is recorded in
// lockstep — not the take a record draws from or its trim.
func matchEvents(a, b []timelineEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].start != b[i].start || a[i].end != b[i].end {
			return false
		}
	}
	return true
}

// makeStereo folds two matched mono results into one interleaved stereo result:
// the lower track's result becomes the frame, carrying its own Track/VTrack/
// Name/PCM as the left channel, with the higher track's PCM as the right
// channel and its track index in PairTrack. Both channels come from the same
// song and therefore share sample rate and bit depth (audioSpec is per-song, §8),
// so the left result's Take drives the encoded file for both.
func makeStereo(left, right TrackResult) TrackResult {
	r := left
	rp := right.PCM
	r.Right = &rp
	r.PairTrack = right.Track
	return r
}

package core

import "fmt"

// samplesPerFrame is the RDAC framing constant: one frame is 16 samples (§3).
const samplesPerFrame = 16

// timelineEvent is a machine-neutral take placement on one v-track's timeline
// (§8): the [start,end) frame range it covers, how many frames of the take to
// skip (trim), and the take it draws from (FileID 0 is an erase — write
// silence). It is the common shape the VR5 V-track table (§6.1) and the VR9
// event log (§6.2) each reduce to before the shared replay.
type timelineEvent struct {
	start, end, trimmed uint32
	fileID              uint16
	clusterCount        uint16 // event 0x18: take cluster count, for the §8.3 HDD integrity check
}

// audioSpec is the per-song audio parameters that decode a take and describe its
// output: the native sample rate (§3), the RDAC format code (§2), and the
// storage cluster size in bytes (§4.2/§5.4, for MT2 page-padding). They travel
// together from a song's header into every one of its v-tracks.
type audioSpec struct {
	sampleRate  int
	format      Format
	clusterSize int
}

// buildVTrack replays one v-track's events into a single PCM buffer (§8): events
// apply in stored order, each writing its [start,end) range over whatever is
// there (later wins); gaps stay silent; an erase (fileID 0) writes silence. The
// origin (VR9 = 12, VR5 = 0, §3) is subtracted so samples land at zero. A
// v-track with no take-bearing event is not populated and yields ok=false.
//
// name is the user-assigned track name to carry into the result ("" for the
// default/blank name, §6.1). track/vtrack are the 1-based indices this v-track
// occupies; loc is the human-readable location prefix for deviations.
func buildVTrack(evs []timelineEvent, takes map[uint16]PCM, origin int, song SongRef, track, vtrack int, name string, aud audioSpec) (TrackResult, bool, []Deviation) {
	loc := fmt.Sprintf("song %d / track %d / v-track %d", song.Number, track, vtrack)

	hasAudio := false
	length := 0
	for _, e := range evs {
		if e.fileID != 0 {
			hasAudio = true
		}
		if end := (int(e.end) - origin) * samplesPerFrame; end > length {
			length = end
		}
	}
	if !hasAudio {
		return TrackResult{}, false, nil // empty v-track: no file
	}

	var devs []Deviation
	buf := make([]int32, length)
	var firstCluster, firstClusterCount uint16
	// Track unimplemented-codec-pattern blocks (§12 / Appendix A) that are
	// actually copied into this v-track's output. The decoder decodes a take in
	// full, so such blocks routinely occur in a take's unused tail (padding /
	// §9 Optimize remnants past the recorded audio); those never reach the WAV
	// and must not be reported. Only silence that lands in output audio is a
	// real deviation, and the earliest one gives its timeline position.
	unknownInOutput := 0
	firstUnknownAt := -1
	for _, e := range evs {
		if e.end <= e.start {
			devs = append(devs, Deviation{Location: loc, SpecRef: "§8", Severity: SeverityWarning,
				Message: fmt.Sprintf("degenerate event (EndFrame %d ≤ StartFrame %d); skipped", e.end, e.start)})
			continue
		}
		at := (int(e.start) - origin) * samplesPerFrame
		span := (int(e.end) - int(e.start)) * samplesPerFrame
		// later-wins: clear the range first so a short/erase write does not
		// leave a previous take's tail showing through.
		clearRange(buf, at, span)
		if e.fileID == 0 {
			continue // erase (§8.2): the cleared range is the result
		}
		if firstCluster == 0 {
			firstCluster = e.fileID
			firstClusterCount = e.clusterCount
		}
		take, ok := takes[e.fileID]
		if !ok {
			devs = append(devs, Deviation{Location: loc, SpecRef: "§10", Severity: SeverityError,
				Message: fmt.Sprintf("event references take %#04x with no take file; span filled with silence", e.fileID)})
			continue
		}
		trim := int(e.trimmed) * samplesPerFrame
		copied := overlay(buf, at, span, take.Samples, trim)
		if copied < span {
			devs = append(devs, Deviation{Location: loc, SpecRef: "§10", Severity: SeverityWarning,
				Message: fmt.Sprintf("take %#04x shorter than event span; %d samples padded with silence", e.fileID, span-copied)})
		}
		// Count only the unimplemented-pattern blocks whose samples this event
		// actually copied. overlay copies copied contiguous take-samples starting
		// at cLo (= trim, or trim-at when the event starts before the origin and
		// the head is clipped), mapping take-sample s to output sample at+(s-trim).
		cLo := trim - min(at, 0)
		for _, b := range take.UnknownBlockOffsets {
			s0 := b * samplesPerFrame
			if s0 >= cLo && s0 < cLo+copied {
				unknownInOutput++
				if out := at + (s0 - trim); firstUnknownAt < 0 || out < firstUnknownAt {
					firstUnknownAt = out
				}
			}
		}
	}
	if unknownInOutput > 0 {
		devs = append(devs, Deviation{Location: loc, SpecRef: "§2", Severity: SeverityWarning,
			Message: fmt.Sprintf("%d RDAC block(s) used an unimplemented codec pattern (Appendix A) within "+
				"output audio (first at ~%.3fs); rendered silent", unknownInOutput, float64(firstUnknownAt)/float64(aud.sampleRate))})
	}

	return TrackResult{
		Song:   song,
		Track:  track,
		VTrack: vtrack,
		Name:   name,
		PCM:    PCM{Samples: buf, BitDepth: bitDepthForFormat(aud.format)},
		Take: Take{
			FirstCluster: int(firstCluster),
			ClusterCount: int(firstClusterCount),
			ClusterSize:  aud.clusterSize,
			Format:       aud.format,
			SampleRate:   aud.sampleRate,
		},
	}, true, devs
}

// clearRange zeroes buf over [at, at+span), clamped to the buffer, so a
// later-winning record's silence overwrites earlier audio.
func clearRange(buf []int32, at, span int) {
	lo, hi := clamp(len(buf), at, span)
	for i := lo; i < hi; i++ {
		buf[i] = 0
	}
}

// overlay copies up to span samples from src[srcOff:] into buf at position at,
// clamped to both buffer and source. It returns the number of destination
// samples that received real audio (fewer than span means the source ran out —
// a truncated take, §10).
func overlay(buf []int32, at, span int, src []int32, srcOff int) int {
	lo, hi := clamp(len(buf), at, span)
	copied := 0
	for i := lo; i < hi; i++ {
		si := srcOff + (i - at)
		if si < 0 || si >= len(src) {
			continue
		}
		buf[i] = src[si]
		copied++
	}
	return copied
}

// clamp returns the [lo,hi) intersection of [at,at+span) with [0,length).
func clamp(length, at, span int) (int, int) {
	lo, hi := at, at+span
	if lo < 0 {
		lo = 0
	}
	if hi > length {
		hi = length
	}
	if lo > hi {
		lo = hi
	}
	return lo, hi
}

// bitDepthForFormat returns the PCM bit depth a decoded format yields (§2).
func bitDepthForFormat(f Format) int {
	switch f {
	case FormatMTP, FormatM24:
		return 24
	default:
		return 16
	}
}

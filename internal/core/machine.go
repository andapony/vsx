package core

// machineFormat is the seam behind which a recorder family's event-list format
// sits (ADR-0003). Its one job is the event-list → timeline reduction: turning a
// song's raw event-list bytes into a machine-neutral songTimeline that the
// neutral build and summarize paths consume unchanged. VR5 (VS-1880) and VR9
// (VS-880EX) are the two adapters; a third machine slots in here as a third
// adapter without editing a switch.
type machineFormat interface {
	parseTimeline(data []byte) (songTimeline, []Deviation)
}

// vtrackGroup is one v-track's placement on the timeline: its 1-based track and
// v-track, its user-assigned name ("" when the name is the default or blank),
// and its current-timeline events in replay order. Both machines reduce to this
// shape.
type vtrackGroup struct {
	track, vtrack int
	name          string
	events        []timelineEvent
}

// songTimeline is a song's parsed timeline, machine-neutral: the per-v-track
// groups plus the origin frame the events are measured from (§3 — VR9 subtracts
// 12, VR5 subtracts 0). Every machine-specific rule — the event-record layout,
// the VR9 code→track/v-track mapping, the VR5 positional table and name
// defaulting, the origin — is resolved by the adapter before this type is
// formed, so the consumers below need no machine knowledge.
type songTimeline struct {
	origin int
	groups []vtrackGroup
}

// formatFor resolves a detected machine identity to its behavior adapter — the
// single dispatch point replacing the per-call switch. It returns nil for an
// unidentified machine, so callers surface the "unsupported machine" deviation
// rather than a switch default.
func formatFor(m machine) machineFormat {
	switch m {
	case machineVR5:
		return vr5{}
	case machineVR9:
		return vr9{}
	default:
		return nil
	}
}

// machineForExt maps an HDD song directory's extension (§4.3: "SONG    VR5" /
// "…VR9") to a machine identity, so the HDD path resolves its adapter through
// formatFor exactly as the CD path resolves one from the archive signature.
func machineForExt(ext string) machine {
	switch ext {
	case "VR5":
		return machineVR5
	case "VR9":
		return machineVR9
	default:
		return machineUnknown
	}
}

// buildTracks replays a parsed songTimeline against its decoded takes into one
// TrackResult per populated v-track (§8), then pairs adjacent mono v-tracks into
// stereo when requested. It is the single neutral build path both machines and
// both source kinds share: every machine difference is already resolved into the
// songTimeline, so this needs no machine knowledge. A v-track with no
// take-bearing event yields no TrackResult.
func buildTracks(st songTimeline, takes map[uint16]PCM, song SongRef, aud audioSpec, stereo bool) ([]TrackResult, []Deviation) {
	var built []builtTrack
	var devs []Deviation
	for _, g := range st.groups {
		tr, ok, d := buildVTrack(g.events, takes, st.origin, song, g.track, g.vtrack, g.name, aud)
		devs = append(devs, d...)
		if ok {
			built = append(built, builtTrack{result: tr, events: g.events})
		}
	}
	return pairTracks(built, stereo), devs
}

// gatherRefs collects the take FileIDs a parsed timeline references, in
// first-seen order, together with the highest 0x18 cluster count claimed for
// each (for the §8.3 HDD integrity check). Erase records (FileID 0) reference no
// take and are skipped. It is the neutral replacement for the per-machine ref
// gatherers — the songTimeline already holds every event.
func gatherRefs(st songTimeline) ([]uint16, map[uint16]int) {
	var refs []uint16
	counts := map[uint16]int{}
	seen := map[uint16]bool{}
	for _, g := range st.groups {
		for _, e := range g.events {
			addRef(&refs, counts, seen, e.fileID, e.clusterCount)
		}
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

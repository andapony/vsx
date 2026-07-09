package core

import (
	"encoding/binary"
	"testing"

	"github.com/andapony/vsx/internal/vsfix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vr9LogBytes hand-builds a §6.2 VS-880EX event log: a u16 BE live count then
// one 48-byte record per event, each carrying its v-track code at 0x20.
func vr9LogBytes(events []vr9Event) []byte {
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, uint16(len(events)))
	for _, e := range events {
		r := make([]byte, 48)
		binary.BigEndian.PutUint32(r[0x00:], e.start)
		binary.BigEndian.PutUint32(r[0x04:], e.end)
		binary.BigEndian.PutUint32(r[0x08:], e.trimmed)
		binary.BigEndian.PutUint16(r[0x14:], e.fileID)
		binary.BigEndian.PutUint16(r[0x18:], e.clusterCount)
		r[0x20] = byte(e.code)
		out = append(out, r...)
	}
	return out
}

// TestVR9ParseTimeline locks the VS-880EX adapter's event-list → timeline
// reduction: origin 12, records grouped by v-track code in first-seen order, the
// code→track/v-track mapping (code/8+1, code%8+1), no per-track name, and events
// carried through faithfully.
func TestVR9ParseTimeline(t *testing.T) {
	data := vr9LogBytes([]vr9Event{
		{start: 12, end: 16, fileID: 0x0100, code: 0}, // T1/V1
		{start: 20, end: 24, fileID: 0x0200, code: 9}, // T2/V2
		{start: 24, end: 28, fileID: 0x0300, code: 0}, // second record on T1/V1
	})

	st, devs := vr9{}.parseTimeline(data)
	assert.Empty(t, devs)
	assert.Equal(t, vr9OriginFrames, st.origin, "VR9 origin is 12")

	require.Len(t, st.groups, 2, "two distinct codes → two groups, in first-seen order")

	assert.Equal(t, 1, st.groups[0].track)
	assert.Equal(t, 1, st.groups[0].vtrack)
	assert.Equal(t, "", st.groups[0].name, "the VR9 log carries no per-track name")
	require.Len(t, st.groups[0].events, 2, "both records for code 0 land in one group, in log order")
	assert.EqualValues(t, 0x0100, st.groups[0].events[0].fileID)
	assert.EqualValues(t, 0x0300, st.groups[0].events[1].fileID)

	assert.Equal(t, 2, st.groups[1].track, "code 9 → track 9/8+1 = 2")
	assert.Equal(t, 2, st.groups[1].vtrack, "code 9 → v-track 9%8+1 = 2")
	assert.EqualValues(t, 0x0200, st.groups[1].events[0].fileID)
}

// TestVR5ParseTimeline locks the VS-1880 adapter's reduction: origin 0, exactly
// 288 positional groups, track/v-track from table position, and the §6.1/§7
// name rule applied so a user name survives while a "V.T" default becomes "".
func TestVR5ParseTimeline(t *testing.T) {
	data := vr5EventListBytes(
		1, // one registry record to skip
		map[int][]vsfix.VR5Event{
			0:  {{Start: 0, End: 4, FileID: 0x0100}},  // T1/V1
			17: {{Start: 8, End: 12, FileID: 0x0200}}, // T2/V2
		},
		map[int]string{0: "Bass"}, // position 17 left as the default "V.T" name
	)

	st, devs := vr5{}.parseTimeline(data)
	assert.Empty(t, devs)
	assert.Equal(t, vr5Origin, st.origin, "VR5 origin is 0")
	require.Len(t, st.groups, 288, "exactly 288 positional groups")

	assert.Equal(t, 1, st.groups[0].track)
	assert.Equal(t, 1, st.groups[0].vtrack)
	assert.Equal(t, "Bass", st.groups[0].name, "a user-assigned name survives into the group")
	require.Len(t, st.groups[0].events, 1)
	assert.EqualValues(t, 0x0100, st.groups[0].events[0].fileID)

	assert.Equal(t, 2, st.groups[17].track)
	assert.Equal(t, 2, st.groups[17].vtrack)
	assert.Equal(t, "", st.groups[17].name, "a default \"V.T\" name is normalized to empty")
	require.Len(t, st.groups[17].events, 1)
	assert.EqualValues(t, 0x0200, st.groups[17].events[0].fileID)

	assert.Empty(t, st.groups[5].events, "an unpopulated position carries no events")
}

// TestFormatFor locks the resolver: each known machine identity maps to its
// adapter, and an unknown identity yields nil so callers surface a deviation.
func TestFormatFor(t *testing.T) {
	assert.IsType(t, vr5{}, formatFor(machineVR5))
	assert.IsType(t, vr9{}, formatFor(machineVR9))
	assert.Nil(t, formatFor(machineUnknown), "an unidentified machine has no adapter")
}

// TestMachineForExt locks the HDD extension → machine mapping (§4.3), including
// the unknown case that drives the "unsupported machine extension" deviation.
func TestMachineForExt(t *testing.T) {
	assert.Equal(t, machineVR5, machineForExt("VR5"))
	assert.Equal(t, machineVR9, machineForExt("VR9"))
	assert.Equal(t, machineUnknown, machineForExt("VR6"))
}

// TestGatherRefs locks the neutral ref gatherer: FileIDs collected in first-seen
// order across all groups, erases (FileID 0) skipped, duplicates deduped, and
// the highest claimed cluster count retained per take (§8.3).
func TestGatherRefs(t *testing.T) {
	st := songTimeline{groups: []vtrackGroup{
		{events: []timelineEvent{{fileID: 0x10, clusterCount: 3}, {fileID: 0}}},
		{events: []timelineEvent{{fileID: 0x20, clusterCount: 1}, {fileID: 0x10, clusterCount: 7}}},
	}}

	refs, counts := gatherRefs(st)
	assert.Equal(t, []uint16{0x10, 0x20}, refs, "first-seen order, erase skipped, dedup")
	assert.Equal(t, 7, counts[0x10], "the largest claimed cluster count wins")
	assert.Equal(t, 1, counts[0x20])
}

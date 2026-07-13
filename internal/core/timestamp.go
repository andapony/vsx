package core

import (
	"encoding/binary"
	"time"
)

// decodeStamp decodes the 8-byte Roland timestamp layout (§4.4), shared by the
// SONG.VR5 created/last-saved fields and the VR5 event record (§7):
//
//	[ss, mm, hh, dow, dd, MM, yyyy(u16 BE)]
//
// The day-of-week byte at offset 3 is redundant (1 = Saturday … 7 = Friday) and
// plays no part in the instant, so it is ignored. A field whose year is zero —
// an all-zero slot on media that never stamped it — decodes to the zero Time,
// which the display layer renders as the absent placeholder rather than a year-0
// date; a field too short to hold the layout is likewise absent. The instant is
// assembled in UTC because the media carries zone-less wall-clock values, so the
// decode (and therefore the rendering) is deterministic and locale-independent.
func decodeStamp(b []byte) time.Time {
	if len(b) < 8 {
		return time.Time{}
	}
	year := int(binary.BigEndian.Uint16(b[6:8]))
	if year == 0 {
		return time.Time{}
	}
	sec, minute, hour := int(b[0]), int(b[1]), int(b[2])
	day, month := int(b[4]), int(b[5])
	return time.Date(year, time.Month(month), day, hour, minute, sec, 0, time.UTC)
}

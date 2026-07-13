package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestDecodeStamp checks the §4.4 timestamp layout decode: the 8-byte
// [ss,mm,hh,dow,dd,MM,yyyy(u16 BE)] field, with the day-of-week byte ignored and
// an all-zero (year 0) field treated as absent.
func TestDecodeStamp(t *testing.T) {
	// 2001-02-27 13:45:09, day-of-week byte deliberately garbage (0x7F) to prove
	// it plays no part in the decoded instant.
	b := []byte{0x09, 0x2D, 0x0D, 0x7F, 0x1B, 0x02, 0x07, 0xD1}
	got := decodeStamp(b)
	assert.Equal(t, time.Date(2001, 2, 27, 13, 45, 9, 0, time.UTC), got)

	// A different day-of-week byte over the same fields decodes identically.
	b2 := append([]byte(nil), b...)
	b2[3] = 0x01
	assert.Equal(t, got, decodeStamp(b2), "day-of-week byte must not affect the decoded time")
}

// TestDecodeStampAbsent maps the unstamped cases to the zero Time, which the
// display layer renders as the placeholder rather than a year-0 date.
func TestDecodeStampAbsent(t *testing.T) {
	assert.True(t, decodeStamp(make([]byte, 8)).IsZero(), "all-zero field (year 0) is absent")
	assert.True(t, decodeStamp([]byte{1, 2, 3}).IsZero(), "a field too short to hold the layout is absent")
	assert.True(t, decodeStamp(nil).IsZero(), "no bytes is absent")
}

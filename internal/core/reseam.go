package core

import "github.com/andapony/vsx/internal/cd"

// reseamTruncatedJunctions reconstructs the exact disc-junction of every
// non-terminal disc of a set whose dump lacks a trailing TDI filler run (§10), so
// the byte-exact data end the missing filler left unknown is recovered from the
// continuation disc (§5.6, issue #31 Option B). Without it a filler-less disc
// falls back to a block-aligned length (Option A): later discs still enumerate,
// but the file spanning the junction is spliced with the disc's over-count residue
// and surfaces as a corrupt, spurious entry.
//
// For each such disc it walks the disc to find the spanning file (its last file,
// whose data runs off the disc), reads the spanning remainder off the continuation
// disc — whose block 0 repeats that file's header and whose first valid header
// then sits at 0x8000 + remainder — and sets the disc's data end to
// dataStart + (fileBlocks − remainderBlocks), the boundary that seams the file
// flush. It returns the set of disc positions it reseamed exactly; a disc it
// cannot resolve (no continuation disc, a header/identity mismatch, or an
// out-of-range result) keeps the block-aligned fallback. It reads no take and
// mutates only the set's segment boundaries, so a failure never corrupts a walk.
func reseamTruncatedJunctions(set *cd.Set, imgs []*cd.Image, lay cdLayout) map[int]bool {
	reseamed := map[int]bool{}
	if lay == nil {
		return reseamed
	}
	for _, p := range set.MissingFiller() {
		if p+1 >= len(imgs) {
			continue // terminal disc: no continuation disc to reconstruct from
		}
		if end, ok := spanJunctionEnd(imgs[p], imgs[p+1], lay); ok {
			set.SetDataEnd(p, end)
			reseamed[p] = true
		}
	}
	return reseamed
}

// spanJunctionEnd computes the physical user-data offset where disc prev's file
// data truly ends, from the continuation disc next (§5.6). It returns false when
// the junction cannot be reconstructed — the caller then keeps the block-aligned
// fallback rather than trusting a guess.
func spanJunctionEnd(prev, next *cd.Image, lay cdLayout) (int64, bool) {
	// The spanning file is prev's last file: its data runs off the disc onto next.
	fe, hdr, ok := lastFileHeader(prev, lay)
	if !ok {
		return 0, false
	}
	// next's block 0 must repeat that header (§5.6); otherwise the set is
	// mis-ordered or a disc is foreign and any recovered remainder is meaningless.
	head0, err := next.ReadUserData(0, lay.headerSpan())
	if err != nil || !headerFieldsMatch(hdr, head0, lay.identityFields()) {
		return 0, false
	}
	// The spanning file's data resumes at next's 0x8000; the next file's header
	// then sits at 0x8000 + remainder (§5.6), so the first valid header past block 0
	// pins the remainder — the whole 0x8000 blocks of the file burned on next.
	firstHdr, ok := firstFileHeaderAfter(next, lay, blockSize)
	if !ok {
		return 0, false
	}
	remainder := firstHdr - blockSize
	physSize := lay.blocks(hdr, fe) * blockSize
	avail := physSize - remainder
	if remainder <= 0 || avail <= 0 || avail >= physSize {
		return 0, false // not a genuine split with data on both discs
	}
	dataEnd := fe.dataOff + avail
	if dataEnd%blockSize != 0 || dataEnd > prev.UserDataLen() {
		return 0, false
	}
	return dataEnd, true
}

// lastFileHeader walks one disc image and returns the last valid file header it
// enumerates, with the header block bytes. On a filler-less disc that last file is
// the one that spans onto the next disc (§5.6): its data start and block count are
// where the reconstruction anchors the disc's true data end.
func lastFileHeader(img cdSource, lay cdLayout) (fileEntry, []byte, bool) {
	files, _, err := walkCD(img, lay)
	if err != nil || len(files) == 0 {
		return fileEntry{}, nil, false
	}
	fe := files[len(files)-1]
	hdr, err := img.ReadUserData(fe.dataOff-blockSize, lay.headerSpan())
	if err != nil {
		return fileEntry{}, nil, false
	}
	return fe, hdr, true
}

// firstFileHeaderAfter scans a disc image for the first valid file header at or
// after user-data offset start, stepping one 0x8000 slot at a time (§5.4), and
// returns its offset. Started at 0x8000 on a continuation disc — past the repeated
// spanning-file header at block 0 — it lands on the next file's header at
// 0x8000 + remainder, the spanning remainder the missing filler left unknown (§5.6,
// #31).
func firstFileHeaderAfter(img cdSource, lay cdLayout, start int64) (int64, bool) {
	end, ok := img.FillerStart()
	if !ok {
		end = img.UserDataLen()
	}
	for udoff := start; udoff+blockSize <= end; udoff += blockSize {
		hdr, err := img.ReadUserData(udoff, lay.headerSpan())
		if err != nil {
			return 0, false
		}
		if validCDHeader(img, lay, hdr, udoff, end) {
			return udoff, true
		}
	}
	return 0, false
}

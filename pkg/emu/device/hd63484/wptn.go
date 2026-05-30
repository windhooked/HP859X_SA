package hd63484

// feedGlyph drives the per-word states of a WPTN-with-count=10 packet,
// which the 8593 firmware uses as its text-glyph blit primitive. Layout
// (matches the live Rev L stream — see cmd/displayprobe):
//
//	0x1800 0x000A                ← WPTN header (parsed before this fn)
//	fg, bg                       ← 2 colour selector words
//	row0..row7                   ← 8 × 16-bit bitmap rows (LSB-first pixels)
//	0x0805, 0x0000, 0xD000, 0x0907   ← 4-word trailer
//
// The bitmap occupies the 8×16-pixel cell at (penX, penY) to (penX+15,
// penY+7) — the pen is positioned at the TOP-LEFT of the character cell
// (so penY=0 corresponds to the top row of the screen, matching the
// firmware's annunciator MOVE coordinates of (0, 0), (8, 0), (16, 0)…).
//
// Rows are sent BOTTOM-TO-TOP within the cell: row 0 is the bottom of the
// glyph (lands at penY+7), row 7 is the top (lands at penY+0). This is
// the standard 1970s/80s character-bitmap convention. Bit 0 of each row is
// the leftmost pixel.
//
// FG / BG colour words: per the HD63484 glyph-blit semantics, FG is applied
// to bits set in the row bitmap and BG to bits clear in the row bitmap.
// For the 8593's monochrome amber display we collapse to a binary rule:
// non-zero ⇒ lit pixel, zero ⇒ dark pixel. The common case (FG=0xFFFF,
// BG=0x0000) therefore lights the glyph bits AND erases the rest of the
// cell — which gives the firmware's annunciator-redraw the per-cell clear
// it needs to overwrite the previous frame's text.
func (dec *decoder) feedGlyph(c *Chip, w uint16) {
	switch dec.st {
	case stGlyphFG:
		c.glyphFG = w
		dec.st = stGlyphBG
	case stGlyphBG:
		c.glyphBG = w
		dec.rowIdx = 0
		dec.st = stGlyphRows
		if c.glyphLog != nil {
			c.glyphLog.RecordColours(c.glyphFG, c.glyphBG)
		}
	case stGlyphRows:
		dec.rows[dec.rowIdx] = w
		dec.rowIdx++
		if dec.rowIdx >= glyphRows {
			c.blitGlyph(dec.rows, c.glyphFG, c.glyphBG)
			c.Glyphs++
			if c.glyphLog != nil {
				c.glyphLog.Record(c.penX, c.penY, dec.rows)
			}
			dec.trailIdx = 0
			dec.st = stGlyphTrailer
		}
	case stGlyphTrailer:
		dec.trailIdx++
		if dec.trailIdx >= glyphTrailLen {
			dec.st = stCmd
		}
	}
}

// blitGlyph paints an 8-row × 16-column bitmap into the cell whose top-left
// corner is the pen. Rows are stored bottom-up in the input array (row 0 =
// glyph bottom), so row i lands at penY + (glyphRows-1-i). Bit 0 of each
// row is the leftmost pixel.
//
// FG / BG are pen/palette indices, not RGB. The 8593 firmware emits FG=0
// BG=0 for every glyph (observed via the glyph logger), which on a real
// HD63484 selects pen 0 — and pen 0 holds the chip's "default lit colour"
// for both foreground AND background. We model that as: glyph row bit set
// → lit pixel (always). Background pixels are only forced lit when BG is
// explicitly non-zero (no firmware path observed so far); BG = 0 is
// transparent (don't touch the existing pixel). This matches the firmware's
// behaviour where the screen accumulates glyphs over time — the per-frame
// clear must come from a separate mechanism (likely partial raster bursts
// at MAR addresses other than 0x4000/0x0000, which we don't model yet).
func (c *Chip) blitGlyph(rows [glyphRows]uint16, fg, bg uint16) {
	bgLit := bg != 0
	for i := 0; i < glyphRows; i++ {
		row := rows[i]
		y := c.penY + (glyphRows - 1 - i)
		for b := 0; b < 16; b++ {
			x := c.penX + b
			switch {
			case row&(1<<uint(b)) != 0 || bgLit:
				c.setVRAMPixel(x, y)
			default:
				// OPAQUE glyph: clear the non-lit pixels of the cell in the
				// foreground plane so a re-blitted glyph (e.g. a blinking
				// annunciator redrawn at the same cell) overwrites the previous
				// one instead of accumulating. The dim background dots (bgVram)
				// still show through the cleared pixels at render time.
				c.clearVRAMPixel(x, y)
			}
		}
	}
	_ = fg // capture only; chip's pen 0 ⇒ always lit on row-bit-set
}

// feedRaster drives the per-word states of either:
//
//   1. A bulk raster-write into video RAM (entered via the WPR MAR-pair the
//      8593 firmware uses to clear / paint regions). 16,384 data words
//      pour in; we wrap memPos when it would overflow.
//
//   2. A WPTN with non-glyph count (i.e. count != 0x000A), which writes
//      pattern data into the chip's internal pattern RAM. Less common in
//      the 8593 firmware (which uses pattern RAM for blink/cursor) but
//      modelled here so the parser stays in sync.
//
// We disambiguate via dec.wptnCount: if non-zero we're in the pattern-RAM
// path; otherwise we're in the vram raster path.
func (dec *decoder) feedRaster(c *Chip, w uint16) {
	if dec.wptnCount > 0 {
		// Pattern-RAM write path.
		if dec.wptnPos < len(c.pattern) {
			c.pattern[dec.wptnPos] = w
		}
		dec.wptnPos++
		dec.wptnCount--
		if dec.wptnCount == 0 {
			dec.st = stCmd
		}
		return
	}
	// VRAM raster-write path. Little-endian within the word — bit 0 = leftmost
	// pixel of the 16-pixel run. Bulk raster bursts are the firmware's faint
	// background dot texture (the 0x4400 fill), so they land in the BACKGROUND
	// plane (bgVram), rendered dim under the bright foreground (see render.go).
	if c.memPos+1 < len(c.bgVram) {
		c.bgVram[c.memPos] = byte(w & 0xFF)
		c.bgVram[c.memPos+1] = byte(w >> 8)
	}
	c.memPos += 2
	c.PaintWords++
	// Each WPR-triggered raster burst is exactly 16384 words (see the 8593
	// firmware's parameter words 0x003F=63 / 0x00FF=255 → 64×256 cells).
	// After a burst we exit raster mode and wait for the next command.
	const burstWords = 16384
	if c.PaintWords%burstWords == 0 {
		dec.st = stCmd
		// Wrap memPos when vram fills (firmware paints 2 bursts per
		// frame and the chip's auto-increment carries position across
		// them — we extend by chunking modulo vram).
		if c.memPos >= len(c.vram) {
			c.memPos = 0
		}
	}
}

// handleWPRSideEffect catches WPR-completed events that need parser-state
// changes. The HD63484 family auto-enters raster-write mode when an
// MAR pair (parameter regs 0x0C MARLow + 0x0D MARHigh) is followed by data-
// port writes — there's no separate "begin write" command on this chip. To
// avoid splattering VRAM when the firmware sets MAR for unrelated reasons
// (read positioning, register access), we keep the original empirical gate:
// only the canonical screen-fill MAR pair (low=0x4000, high=0x0000) arms
// raster mode. Other MAR pairs are stored but don't transition the parser.
//
// Partial-region screen updates (annunciator clears via small raster
// bursts at other MAR addresses) remain unmodelled — that's a follow-up
// once we have a reliable way to distinguish write-arming MAR sets from
// other uses in the command stream. The per-cell BG-clear in blitGlyph
// covers the dominant accumulation case the firmware exhibits at boot.
func (dec *decoder) handleWPRSideEffect(c *Chip, reg, value uint16) {
	switch reg {
	case PRMARLow:
		c.marLow = value
	case PRMARHigh:
		c.marHigh = value
		if c.marLow == 0x4000 && c.marHigh == 0x0000 {
			c.memPos = 0
			c.Paints++
			dec.st = stRasterData
			dec.wptnCount = 0
		}
	}
}

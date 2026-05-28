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
// the standard 1970s/80s character-bitmap convention. Verifying against
// the second model-banner glyph in the live stream
//
//	rows = {0x0022, 0x0022, 0x0022, 0x002A, 0x002A, 0x0036, 0x0022, 0x0000}
//
// gives an upper-case "M" / "W" / similar character: two parallel
// verticals at cols 1 + 5 with the V-convergence near rows 3–5 (mid-cell).
// Bit 0 of each row is the leftmost pixel.
func (dec *decoder) feedGlyph(c *Chip, w uint16) {
	switch dec.st {
	case stGlyphFG:
		// Foreground selector — palette/pen index, not literal RGB. We
		// render glyph foreground in a fixed amber via fgColor.
		dec.st = stGlyphBG
	case stGlyphBG:
		// Background selector — typically 0 (transparent / no overwrite).
		dec.rowIdx = 0
		dec.st = stGlyphRows
	case stGlyphRows:
		dec.rows[dec.rowIdx] = w
		dec.rowIdx++
		if dec.rowIdx >= glyphRows {
			c.blitGlyph(dec.rows)
			c.Glyphs++
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
func (c *Chip) blitGlyph(rows [glyphRows]uint16) {
	for i := 0; i < glyphRows; i++ {
		row := rows[i]
		if row == 0 {
			continue
		}
		y := c.penY + (glyphRows - 1 - i)
		for b := 0; b < 16; b++ {
			if row&(1<<uint(b)) == 0 {
				continue
			}
			c.setPixel(c.penX+b, y, fgColor)
		}
	}
}

// feedRaster drives the per-word states of either:
//
//   1. A bulk raster-write into video RAM (entered via the WPR 0x0C =
//      0x4000 + WPR 0x0D = 0x0000 pair the 8593 firmware uses to clear /
//      paint the screen background). 16,384 data words pour in; we wrap
//      vramPos when it would overflow.
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
	// VRAM raster-write path. Little-endian within the word — LSB = leftmost
	// pixel of the 16-pixel run (matches the glyph row encoding above).
	if c.memPos*2+1 < len(c.vram) {
		c.vram[c.memPos*2] = byte(w & 0xFF)
		c.vram[c.memPos*2+1] = byte(w >> 8)
	}
	c.memPos++
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
		if c.memPos*2 >= len(c.vram) {
			c.memPos = 0
		}
	}
}

// handleWPRSideEffect catches the WPR-completed events the chip semantics
// need to react to. The 8593-specific raster-mode trigger is here: writing
// WPR PRMARLow = 0x4000 followed by WPR PRMARHigh = 0x0000 arms a 16384-
// word video-RAM burst.
func (dec *decoder) handleWPRSideEffect(c *Chip, reg, value uint16) {
	switch reg {
	case PRMARHigh:
		mlo := c.regs[PRMARLow]
		mhi := value
		// Empirical trigger: 0x4000 / 0x0000 enters raster mode. Other
		// MAR values would prime memory access for COPY / DMR / DMW
		// (which we don't model yet).
		if mlo == 0x4000 && mhi == 0x0000 {
			c.Paints++
			dec.st = stRasterData
			dec.wptnCount = 0 // ensure VRAM path, not pattern-RAM path
		}
	}
}

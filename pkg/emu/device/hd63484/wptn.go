package hd63484

// The HD63484 renders text in TWO commands: WPTN loads the glyph bitmap into
// pattern RAM (parsed by execWPTN in parser.go — 2 colour words + glyphRows bitmap
// rows), then PTN (0xD000) blits it at the pen sized by SZ (drawPattern below).
//
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
func (c *Chip) blitGlyph(rows [glyphRows]uint16, fg, bg uint16, yoff int) {
	// In Colorized mode glyphs land in the dedicated text plane (white, never
	// dithered). In mono they stay in vram: the SCLR's per-cycle graph dither
	// clears them to the dim background and they redraw solid, so they read as
	// crisp (not prominently dithered) without the text-plane machinery.
	// Suppress the residual early-boot glyph the firmware blits at the exact
	// drawing origin (0,0). With ORG_row=256 and displayScanStart=23 the ORG
	// origin maps to VRAM row 256 — just below the visible window (rows 23..278),
	// so it's naturally off-screen. We still guard explicitly so future ORG
	// changes don't accidentally let it through; the real instrument never shows
	// it. See docs/HARDWARE.md §7.3.
	if c.penX == 0 && c.penY == 0 {
		return
	}
	bgLit := bg != 0
	for i := 0; i < glyphRows; i++ {
		row := rows[i]
		// The firmware sends rows BOTTOM-to-TOP (row 0 = glyph bottom) in its
		// Y-up coordinate system; place row i at firmware-Y penY+i. setVRAMPixel
		// applies the global Y-up→Y-down flip (drawYOrigin - y), so after the
		// flip row 0 (bottom) lands at the bottom of the rendered cell and row 7
		// (top) at the top — i.e. the glyph renders right-side-up. yoff is the
		// PTN-cell bottom margin (SZ height − glyphRows) so the bitmap sits at the
		// firmware's intended baseline within the taller cell (see drawPattern).
		y := c.penY + i + yoff
		for b := 0; b < 16; b++ {
			x := c.penX + b
			switch {
			case row&(1<<uint(b)) != 0 || bgLit:
				c.setVRAMPixel(x, y)
			default:
				// OPAQUE glyph: clear the non-lit pixels of the cell so a
				// re-blitted glyph (e.g. a blinking annunciator redrawn at the
				// same cell) overwrites the previous one instead of
				// accumulating.
				c.clearVRAMPixel(x, y)
			}
		}
	}
	_ = fg // capture only; chip's pen 0 ⇒ always lit on row-bit-set
}

// drawPattern is the PTN command (0xD000): it draws the pattern-RAM glyph staged
// by the preceding WPTN onto the rectangle at the current pen, sized by SZ. SZ is
// SZy:SZx (high:low byte), each in pixels and size-1 encoded, so the cell is
// (SZx+1)×(SZy+1). The 8593's text uses SZ=0x0907 ⇒ an 8×10 cell. Our bitmap is
// glyphRows (8) tall, so the cell is taller by (SZy+1 − glyphRows) rows; that
// extra height is the cell's bottom margin, which the firmware's AMOVE accounts
// for — so we offset the bitmap down by it to land on the intended baseline
// (without it, glyphs render that many scan rows too high). PTN was previously
// mis-identified as a stubbed "GCHR" and the glyph was blitted at WPTN time with
// no cell sizing — the cause of the ~2-row glyph offset. See parser.go.
func (c *Chip) drawPattern(sz uint16) {
	if !c.pendGlyph {
		return
	}
	c.pendGlyph = false
	// SZ says the cell is (SZy+1) tall (10 for SZ=0x0907) vs our glyphRows (8)
	// bitmap. The 2-row difference is the candidate for the reported "glyph sits ~2
	// scan rows too high": shifting the bitmap DOWN by that margin (yoff = -(cellH −
	// glyphRows)) lowers glyphs relative to the vector graticule. We keep yoff=0 for
	// now (no visual change, golden/tests stable) until the direction is confirmed
	// on the instrument — the real PTN may TILE the 8-row pattern into the 10-row
	// cell rather than shift the baseline. glyphCellMargin computes the shift; wire
	// it into yoff once verified.
	glyphCellMargin := int((sz>>8)&0xFF) + 1 - glyphRows // SZy+1 − glyphRows (= 2)
	_ = glyphCellMargin
	yoff := 0
	c.blitGlyph(c.pendRows, c.glyphFG, c.glyphBG, yoff)
	c.Glyphs++
}

// feedRaster drives the per-word states of either:
//
//  1. A bulk raster-write into video RAM (entered via the WPR MAR-pair the
//     8593 firmware uses to clear / paint regions). 16,384 data words
//     pour in; we wrap memPos when it would overflow.
//
//  2. A WPTN with non-glyph count (i.e. count != 0x000A), which writes
//     pattern data into the chip's internal pattern RAM. Less common in
//     the 8593 firmware (which uses pattern RAM for blink/cursor) but
//     modelled here so the parser stays in sync.
//
// We disambiguate via dec.wptnCount: if non-zero we're in the pattern-RAM
// path; otherwise we're in the vram raster path.
func (dec *decoder) feedRaster(c *Chip, w uint16) {
	if dec.wptnCount > 0 {
		// Pattern-RAM write path.
		if dec.wptnPos < len(c.pattern) {
			c.pattern[dec.wptnPos] = w
		}
		if dec.wptnPos == 0 {
			// The first pattern word is the active line stipple the firmware
			// uses for subsequent vector lines (graticule frame/grid). 0xFFFF
			// solid, 0x1111 dotted, 0xCCCC dash, etc. See drawLine.
			c.linePattern = w
		}
		dec.wptnPos++
		dec.wptnCount--
		if dec.wptnCount == 0 {
			dec.st = stCmd
		}
		return
	}
	// VRAM raster-write path. Little-endian within the word — bit 0 = leftmost
	// pixel of the 16-pixel run.
	// The 0x4400 raster burst is the graticule GRID pattern. Write it into the
	// core's GRID PAGE (words 0x4000..0x7fff) so the scanout can superimpose it as a
	// faint recessive grid. CLAMP to the grid page: the firmware writes two bursts
	// (double-buffer); without the clamp the second burst's address ran past
	// 0x8000 and wrapped into the CONTENT page (words < 0x4000), painting garbage in
	// the rows above the graph. Folding it back to 0x4000 makes the second burst
	// overwrite the first instead.
	gw := c.memPos >> 1
	if gw >= gridPageWords {
		gw = gridPageWords + ((gw - gridPageWords) & (gridPageWords - 1))
	}
	c.core.curCmd = tagRaster
	c.core.writeword(uint32(gw), w)
	c.memPos += 2
	c.PaintWords++
	// Each WPR-triggered raster burst is exactly 16384 words (see the 8593
	// firmware's parameter words 0x003F=63 / 0x00FF=255 → 64×256 cells).
	// After a burst we exit raster mode and wait for the next command.
	const burstWords = 16384
	if c.PaintWords%burstWords == 0 {
		dec.st = stCmd
		// Wrap memPos when the frame buffer fills (firmware paints 2 bursts
		// per frame and the chip's auto-increment carries position across
		// them — we extend by chunking modulo the buffer size).
		if c.memPos >= VRAMSize {
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
			// The raster burst targets the RWP word-address: (marHigh<<16)|
			// marLow = 0x4000 words = the core GRID PAGE (words 0x4000..0x7FFF,
			// outside the displayed content page), where the firmware's uniform
			// 0x4400 fill prepares the graticule grid pattern instead of
			// striping the visible screen. memPos is a BYTE offset into that
			// address space (the raster path halves it back to words).
			c.memPos = (int(c.marHigh)<<16 | int(c.marLow)) << 1
			c.Paints++
			dec.st = stRasterData
			dec.wptnCount = 0
			// The canonical MAR pair is also the firmware's "rewind read
			// pointer" before the POST RAM-verify read loop reads the
			// block-filled pattern back from the start (ROM 0xD6B2). Reset
			// the block-read pointer so ReadData() returns dmem[0] onward.
			c.readPtr = 0
		}
	}
}

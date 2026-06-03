package hd63484

// area_ops.go is a faithful port of the HD63484's RWP-addressed memory engine
// from MAME (src/devices/video/hd63484.cpp) — specifically the CLR (0x5800) and
// SCLR (0x5C00) area-fill commands. Unlike the pen-based line/rect/glyph
// primitives (which walk firmware X/Y coordinates), these commands address the
// frame buffer by the 20-bit Read/Write Pointer (RWP) in WORD units, MWR words
// per raster line, and apply a pattern word through a logical operation + bit
// mask. The cal-data display (CAL DISP;) repaints the whole screen with SCLR, so
// without this the table overlays the previous (graticule/trace) screen.
//
// Register inputs (set by the WPR handler, see registers.go):
//   rwp[rwpDn] — current write pointer (word offset into the bitmap)
//   maskReg    — WPR 0x04, which bits of each word a logical op may change
//   dispMWR    — MWR1, words per raster line (fallback 64 = our 128-byte rows)
//
// The command word itself (cr) carries the mode: bit 10 set ⇒ apply the logical
// operation selected by cr&3 (0 replace / 1 OR / 2 AND / 3 EOR) under maskReg;
// bit 10 clear ⇒ plain replace with the pattern word.

// areaMWR returns the active memory-width (words per raster line), defaulting to
// our 64-word (128-byte) row when the firmware hasn't programmed MWR1.
func (c *Chip) areaMWR() int {
	if c.dispMWR > 0 {
		return int(c.dispMWR)
	}
	return PaintRowBytes / 2 // 64 words/row
}

// wordByteAddr maps a bitmap WORD offset to the byte address of its low byte in
// our VRAM, or -1 if it falls outside the frame buffer. A word covers 16
// horizontal pixels; the low byte holds the left 8 (bit 0 = leftmost), matching
// setVRAMPixel and the 0x4400 raster path.
func (c *Chip) wordByteAddr(off, mwr int) int {
	if off < 0 || mwr <= 0 {
		return -1
	}
	row := off / mwr
	col := off % mwr
	if row < 0 || row >= PaintHeight || col < 0 || col >= PaintRowBytes/2 {
		return -1
	}
	return row*PaintRowBytes + col*2
}

// readPlaneWord returns the 16-bit word at bitmap word offset off in plane
// (0 if off-frame).
func readPlaneWord(plane []byte, a int) uint16 {
	if a < 0 || a+1 >= len(plane) {
		return 0
	}
	return uint16(plane[a]) | uint16(plane[a+1])<<8
}

// writePlaneWord stores a 16-bit word at byte address a in plane (no-op off-frame).
func writePlaneWord(plane []byte, a int, val uint16) {
	if a < 0 || a+1 >= len(plane) {
		return
	}
	plane[a] = byte(val)
	plane[a+1] = byte(val >> 8)
}

// execClear is MAME's command_clr_exec: fill the (|ax|+1)×(|ay|+1) word region
// anchored at the RWP — d0 steps along the raster (+1 word), d1 steps up one
// raster line (−MWR words) — writing the pattern word d through the logical op.
// CLR (cr bit10=0) replaces; SCLR (cr bit10=1) applies cr&3 under maskReg. After
// the fill the RWP advances up by (ay+1) raster lines, exactly as the chip does,
// so a sequence of region fills tiles the screen. cr is the command word.
func (c *Chip) execClear(cr, pattern uint16, ax, ay int16) {
	mwr := c.areaMWR()
	mm := cr & 0x03
	logical := cr&0x0400 != 0 // BIT(cr,10)

	// Plane routing: a REPLACE fill (bit10=0) is a foreground clear/fill — it
	// wipes the bright graticule/text plane (vram). A logical-op fill (bit10=1,
	// e.g. the cal/boot AND-0x5555 dither) is BACKGROUND texture — route it to the
	// dim bgVram plane so it never fades foreground text (matches the 0x4400
	// background-fill architecture; render.go composites bgVram dim under vram).
	plane := c.vram[:]
	if logical {
		plane = c.bgVram[:]
	}

	d0inc := int16(1)
	if ax < 0 {
		d0inc = -1
	}
	d1inc := int16(1)
	if ay < 0 {
		d1inc = -1
	}
	base := int(c.rwp[c.rwpDn])
	for d1 := int16(0); d1 != ay+d1inc; d1 += d1inc {
		for d0 := int16(0); d0 != ax+d0inc; d0 += d0inc {
			off := base - int(d1)*mwr + int(d0)
			a := c.wordByteAddr(off, mwr)
			var res uint16
			if logical {
				data := readPlaneWord(plane, a)
				switch mm {
				case 0: // replace
					res = (data &^ c.maskReg) | (pattern & c.maskReg)
				case 1: // OR
					res = (data &^ c.maskReg) | ((data | pattern) & c.maskReg)
				case 2: // AND
					res = (data &^ c.maskReg) | ((data & pattern) & c.maskReg)
				case 3: // EOR
					res = (data &^ c.maskReg) | ((data ^ pattern) & c.maskReg)
				}
			} else {
				res = pattern
			}
			writePlaneWord(plane, a, res)
		}
	}
	c.rwp[c.rwpDn] = uint32((int(c.rwp[c.rwpDn]) - int(ay+d1inc)*mwr) & 0xfffff)
	c.AreaClears++
}

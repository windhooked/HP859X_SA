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
	// The firmware's RWP addresses the EFFECTIVE display position; the pen stores
	// content at ORG_row (displayScanStart rows lower in vram). Shift the SCLR's
	// target down by displayScanStart so it lands on the stored content rather
	// than 23 rows above it (the "partially clears / misaligned" bug).
	row := off/mwr + displayScanStart
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

	// === NO DITHERING ===
	// On the real instrument the display is 1-BIT: every pixel is on or off, one
	// colour. The firmware DITHERS — ANDs the graph with a 0x5555/0xAAAA
	// checkerboard (SCLR, bit10=1) — purely to work around that 1-bit tube: a 50%
	// checkerboard time-averages on the phosphor into "half bright" (the recessive
	// graticule) and the complementary 0x5555/0xAAAA pair fades old content away
	// (its "erase to grey", since a 1-bit tube has no erase-to-dim primitive).
	//
	// We render into a true-colour RGBA framebuffer and have NONE of those
	// constraints, so we do NOT reproduce the dither. Replicating it pixel-for-
	// pixel froze one checkerboard phase at full brightness — that was the "trace
	// never clears" ghost. Instead we read the firmware's INTENT through the
	// command and render it cleanly:
	//   • SCLR (logical AND-checkerboard) over the graph  → the intent is CLEAR
	//     → clean foreground erase (no dots).
	//   • CLR (REPLACE, bit10=0)                          → a genuine pattern fill
	//     → write the pattern (real content / explicit erase).
	// The bgVram "dim background" plane the dither used to feed is therefore no
	// longer written here (it survives only for the off-screen 0x4400 raster
	// prepare in wptn.go). The logical-op math (OR/EOR/masked-REPLACE) is kept as
	// labelled, intentionally-empty handlers below: this firmware only ever uses
	// the AND-checkerboard on the graph, but the cases are documented so the
	// handler is present if a future path needs one.

	// AREA-DEFINITION clip rect (WPR 0x08-0x0b). The cal/operating display sets it
	// to the graph (0,0)-(400,209) before its SCLR, so the clear is confined to the
	// graph and never touches the static annunciators outside it.
	xmin := int(int16(c.regs[0x08]))
	ymin := int(int16(c.regs[0x09]))
	xmax := int(int16(c.regs[0x0a]))
	ymax := int(int16(c.regs[0x0b]))
	clip := logical && xmax > xmin && ymax > ymin

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
			if clip {
				// Map the word's byte address back to firmware drawing coords
				// (inverse of setVRAMPixel) and skip words outside the graph rect.
				if a < 0 {
					continue
				}
				vramRow := a / PaintRowBytes
				vramXbase := (a % PaintRowBytes) * 8
				yfw := c.orgRow - vramRow
				xfw := vramXbase - c.orgCol
				if yfw < ymin || yfw > ymax || xfw+15 < xmin || xfw > xmax {
					continue
				}
			}
			switch {
			case !logical:
				// CLR REPLACE — a genuine fill: write the pattern straight to the
				// bright foreground (cal-table background, CLR-with-data, or data=0
				// explicit erase).
				writePlaneWord(c.vram[:], a, pattern)
			case clip:
				// SCLR over the graph = the firmware's graph CLEAR (AND-checkerboard
				// fade on real HW). Render the intent: clean foreground erase, no
				// dither dots. The pattern word (0x5555/0xAAAA) is the checkerboard
				// and is intentionally ignored — we clear unconditionally.
				switch mm {
				case 2: // AND — the only op this firmware uses on the graph: CLEAR.
					writePlaneWord(c.vram[:], a, 0)
				case 0: // masked REPLACE — unused as a graph SCLR; intentionally empty.
				case 1: // OR  — cursor/blink overlay on real HW; unused; intentionally empty.
				case 3: // EOR — XOR cursor on real HW; unused; intentionally empty.
				}
			default:
				// Logical SCLR with NO area-def (e.g. boot's full-screen pass). On
				// real HW this is the background dither fill; we don't dither and have
				// no clip marking it a content clear, so we leave the foreground
				// untouched (clean black background). Intentionally empty — was: wrote
				// the 0x5555 dots to bgVram.
			}
		}
	}
	c.rwp[c.rwpDn] = uint32((int(c.rwp[c.rwpDn]) - int(ay+d1inc)*mwr) & 0xfffff)

	// Faithful core (Phase 3): mirror the area op into the unified buffer at the
	// REAL physical address (no displayScanStart/+23 hack — the core's RWP is the
	// firmware's actual RWP). Keep the shipped intent=clear semantics: a REPLACE
	// fill (CLR) writes the pattern; a logical SCLR (the firmware's AND-checkerboard
	// fade) is read as a CLEAN CLEAR (replace with 0, no dither). core.clrExec
	// advances core.rwp by the same (ay+1) lines as the legacy advance above, so the
	// two pointers stay in lockstep.
	if logical {
		c.core.clrExec(0x0000, 0x0000, ax, ay) // clean clear, no dither
	} else {
		c.core.clrExec(cr, pattern, ax, ay) // faithful REPLACE fill
	}
	c.AreaClears++
}

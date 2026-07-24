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

// graphTopMargin is the firmware Y above which the graph clear's region holds
// only top chrome (the REF/atten readout), not trace/grid — the graticule grid is
// drawn up to y≈196 and the box top is ~y200, while the readout sits at ~y205. The
// clean-clear preserves glyph pixels above this line (the readout text) but still
// clears glyph pixels below it (stale boot-time messages in the graph body).
const graphTopMargin = 200

// areaMWR returns the active memory-width (words per raster line), defaulting to
// our 64-word (128-byte) row when the firmware hasn't programmed MWR1.
func (c *Chip) areaMWR() int {
	if c.dispMWR > 0 {
		return int(c.dispMWR)
	}
	return PaintRowBytes / 2 // 64 words/row
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

	// === PHASE-MULTIPLEX MODEL (2026-07-24) ===
	// The display is phase-multiplexed by pixel parity (see draw.go
	// penPhaseLit): content is drawn at a CL1-selected phase and the SCLR
	// AND-0x5555/0xAAAA ops erase EXACTLY the matching phase. The faithful
	// logical op below IS the erase mechanism — it must run for every logical
	// SCLR (per MAME command_clr_exec there is NO area-def dependency; the
	// former "clip required" gate silently skipped the boot's 696 two-phase
	// full-screen erases and left the boot text as even-phase ghosts). The
	// area-def, when valid, still trims the op at the graph edges (verified:
	// clearing boundary words whole wiped softkey-label leading chars).

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
			// FAITHFUL core address = the RWP-relative word DIRECTLY (the firmware's
			// actual pointer), which is the SAME address space as the pen's
			// calcOffset — so the clear ALIGNS with the pen draws (trace/box) instead
			// of diverging through the legacy wordByteAddr mapping. Firmware coords for
			// the area-def clip are the exact inverse of calcOffset about orgDPA.
			coreOff := uint32(off) & acrtcRAMMask
			orow := int(c.core.orgDPA) / mwr
			ocol := int(c.core.orgDPA) % mwr
			yfw := orow - off/mwr
			xfw := (off%mwr - ocol) * 16
			if clip && (yfw < ymin || yfw > ymax || xfw+15 < xmin || xfw > xmax) {
				continue
			}
			// Per-word X area-mask: which of this word's 16 px fall within
			// [xmin,xmax]. At the area-def's right/left edge a word straddles the
			// boundary; clearing the whole 16-px word there wiped the FIRST letter of
			// the right-side softkey labels — their leading char sits in the boundary
			// word (firmware x=400..415, one past xmax=400). Restrict the op to the
			// in-bounds pixels so the boundary clear stops exactly at xmax.
			am := uint16(0xFFFF)
			if clip {
				lo, hi := xmin-xfw, xmax-xfw
				if lo < 0 {
					lo = 0
				}
				if hi > 15 {
					hi = 15
				}
				am = 0
				for b := lo; b <= hi; b++ {
					am |= 1 << uint(b)
				}
			}
			if c.GraticuleToUpper && clip {
				coreOff = (coreOff + 0x4000) & acrtcRAMMask // experiment hook
			}
			switch {
			case !logical:
				// CLR REPLACE — a genuine fill: write the pattern straight to the
				// frame buffer at its real address.
				c.core.writeword(coreOff, pattern)
			case clip && c.CleanClear && (mm == 2 || mm == 3):
				// OPTION B — CLEAN CLEAR. The firmware's AND/EOR dither over the graph
				// (cr&3 == 2/3) is, by intent, an ERASE: it clears the previous sweep's
				// trace and any stale content so the new sweep can be drawn fresh. The
				// faithful `data AND 0x5555` is idempotent, so it only thins old content
				// to a 50% ghost that never goes away. Here we honour the intent and
				// ZERO the masked bits — no residue, no ghost. The graticule grid and
				// trace then come from the firmware's per-frame redraws, not from a
				// dither residue.
				data := c.core.readword(coreOff)
				m := c.core.mask & am
				// PRESERVE GLYPH (text) PIXELS in the TOP MARGIN. The status readout
				// (REF .0 dBm  AT 10 dB) is drawn at a firmware y just above the box top
				// but inside the graph clear's y-range; the clean-clear would erase its
				// lower rows. Text is glyph-tagged and is repainted by the firmware, not
				// part of the per-sweep trace, so keep glyph pixels HERE. Scoped to
				// yfw>graphTopMargin (above the box, where only chrome lives) so stale
				// boot-time glyphs IN the graph body still clear (no ghosts).
				if data != 0 && yfw > graphTopMargin {
					base := coreOff << 4
					var gm uint16
					for b := 0; b < 16; b++ {
						if c.core.cmdTag[base|uint32(b)] == tagGlyph {
							gm |= 1 << uint(b)
						}
					}
					m &^= gm
				}
				if res := data &^ m; res != data {
					c.core.writeword(coreOff, res)
				}
			case clip:
				if c.ClearColLogOn && d0 == 0 && d1 == 0 && len(c.ClearColLog) < 4096 {
					c.ClearColLog = append(c.ClearColLog, [3]uint32{c.rwp[c.rwpDn], uint32(c.core.mask), uint32(pattern)})
				}
				if c.AAAARectLog != nil && pattern == 0xAAAA && d0 == 0 && d1 == 0 && len(*c.AAAARectLog) < 4096 {
					*c.AAAARectLog = append(*c.AAAARectLog, [3]int{int(c.rwp[c.rwpDn]), int(ax), int(ay)})
				}
				// FAITHFUL SCLR. Execute the chip's real logical op (cr&3: 0 REPLACE /
				// 1 OR / 2 AND / 3 EOR) under the mask register — NOT a "clean clear".
				// So the firmware's 0x5555/0xAAAA AND-dither leaves the graticule's dot
				// texture, and partial trace clears come out exactly as drawn. Clipped
				// to the area-def (the chip clips area ops in area mode), so it never
				// spills past the graph into the annunciators/softkeys.
				data := c.core.readword(coreOff)
				m := c.core.mask & am
				var res uint16
				switch mm {
				case 0:
					res = (data &^ m) | (pattern & m)
				case 1:
					res = (data &^ m) | ((data | pattern) & m)
				case 2:
					res = (data &^ m) | ((data & pattern) & m)
				case 3:
					res = (data &^ m) | ((data ^ pattern) & m)
				}
				if res != data {
					c.core.writeword(coreOff, res)
				}
			default:
				// FAITHFUL logical SCLR with NO valid area-def (the boot's
				// two-phase full-screen erase passes): same chip op, full-word
				// mask. Formerly a silent no-op — that skip left the boot text
				// as ghost bands (SCLRNoAreaDef counts these for diagnostics).
				c.SCLRNoAreaDef++
				c.SCLRNoAreaLast = [4]int{int(c.rwp[c.rwpDn]), int(ax), int(ay), int(pattern)}
				if c.ClearColLogOn && len(c.SCLRSkipLog) < 2048 {
					c.SCLRSkipLog = append(c.SCLRSkipLog, [4]int{int(c.rwp[c.rwpDn]), int(ax), int(ay), int(pattern)})
				}
				data := c.core.readword(coreOff)
				m := c.core.mask
				var res uint16
				switch mm {
				case 0:
					res = (data &^ m) | (pattern & m)
				case 1:
					res = (data &^ m) | ((data | pattern) & m)
				case 2:
					res = (data &^ m) | ((data & pattern) & m)
				case 3:
					res = (data &^ m) | ((data ^ pattern) & m)
				}
				if res != data {
					c.core.writeword(coreOff, res)
				}
			}
		}
	}
	c.rwp[c.rwpDn] = uint32((int(c.rwp[c.rwpDn]) - int(ay+d1inc)*mwr) & 0xfffff)
	c.AreaClears++
}

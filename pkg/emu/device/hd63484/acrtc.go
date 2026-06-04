package hd63484

// acrtc is the faithful HD63484 core being rebuilt per docs/HD63484_REBUILD_PROMPT.md:
// a single FLAT, word-addressed frame buffer with logical→physical address
// translation (ORG/MWR/bit-depth) and RWP-addressed area ops — a direct port of
// MAME src/devices/video/hd63484.cpp (calc_offset, command_clr_exec,
// draw_graphics_line). It replaces the old vram/bgVram split + the orgRow=256 /
// displayScanStart / +209 coordinate hacks: every operation (pen draw, SCLR/CLR,
// raster fill, display scanout) indexes THIS one buffer, so alignment falls out of
// honoring the firmware's real ORG/SAR/MWR instead of being patched.
//
// Phase 1 (this file): the buffer + addressing + area ops, unit-tested in isolation
// (acrtc_test.go) before the decoder/scanout are migrated onto it in later phases.

// acrtcRAMWords is the installed video RAM in 16-bit words. The A16 board has
// U305/U306 = 2 × 256-Kbit SRAM = 64 KB = 0x8000 words (HARDWARE.md). Must be a
// power of two so acrtcRAMMask can fold out-of-range addresses (MAME masks to the
// configured size; the chip's pointers are modular).
const (
	acrtcRAMWords = 0x8000
	acrtcRAMMask  = acrtcRAMWords - 1
)

type acrtc struct {
	ram [acrtcRAMWords]uint16 // the single flat word-addressed frame buffer

	// Layer registers (banks 0..3 = Base/Upper/Lower/Window). The 8593 uses only
	// bank 1, but the arrays model the real chip so layers/scanout extend cleanly.
	mwr [4]uint16 // MWR0-3 — memory width (words per raster line) per layer
	sar [4]uint32 // SAR0-3 — display start word address per layer
	rwp [4]uint32 // RWP    — read/write (drawing/area) pointer per layer

	// ORG (set-drawing-origin) decoded state — see setORG.
	orgDPA uint32 // origin physical word address (logical 0,0 maps here)
	orgDPD uint16 // origin dot offset within the first word
	orgDN  int    // origin display/layer number (which MWR/SAR bank drawing uses)

	rwpDN int // RWP layer select for area ops

	ccr  uint16 // control reg; GBM (graphic bit mode) in bits 8-10 selects bpp
	mask uint16 // WPR 0x04 write-mask: which bits a logical op may change

	// drawOffset is added to every setDot word address — an experiment hook to
	// redirect specific draws (e.g. APLL polylines) into another memory region.
	drawOffset uint32

	// writeHist buckets every writeword by 0x200-word (1 KB) page — a histogram of
	// which controller memory regions the firmware actually populates.
	writeHist [acrtcRAMWords >> 9]int

	// curCmd tags which command class is currently writing; cmdTag records, per
	// word, the last command that wrote it — for the by-command colour render.
	curCmd uint8
	cmdTag [acrtcRAMWords]uint8

	// stable / stableTag are a snapshot of ram / cmdTag taken at each frame
	// boundary (snapshot()), so the scanout shows a COMPLETE, flicker-free frame
	// regardless of where execution pauses mid-repaint. Under clean-clear the live
	// buffer is momentarily blank between a graph clear and the redraw; the snapshot
	// is the last fully-painted frame. haveStable gates it (false until the first
	// frame boundary, so direct-draw unit tests read the live buffer).
	stable     [acrtcRAMWords]uint16
	stableTag  [acrtcRAMWords]uint8
	haveStable bool
}

// snapshot captures the current frame buffer (+ command tags) as the stable,
// flicker-free image the scanout renders. Called at each frame boundary.
func (a *acrtc) snapshot() {
	a.stable = a.ram
	a.stableTag = a.cmdTag
	a.haveStable = true
}

// scanWord / scanTag return the rendered word / command-tag at off — from the
// stable snapshot if one exists, else live ram.
func (a *acrtc) scanWord(off uint32) uint16 {
	off &= acrtcRAMMask
	if a.haveStable {
		return a.stable[off]
	}
	return a.ram[off]
}

func (a *acrtc) scanTag(off uint32) uint8 {
	off &= acrtcRAMMask
	if a.haveStable {
		return a.stableTag[off]
	}
	return a.cmdTag[off]
}

// Command-class tags (cmdTag values).
const (
	tagNone uint8 = iota
	tagPoly        // APLL / RPLL polyline
	tagSCLR        // SCLR area op
	tagCLR         // CLR area op
	tagRect        // ARCT / RRCT / AFRCT / RFRCT
	tagLine        // ALINE / RLINE
	tagDot         // DOT
	tagGlyph       // WPTN glyph
	tagRaster      // raster pattern fill (0x4400)
	tagOther
)

// getBpp returns bits-per-logical-pixel from the GBM field (CCR bits 8-10):
// 000→1, 001→2, 010→4, 011→8, 100→16. The 8593 runs 1bpp (GBM=000).
func (a *acrtc) getBpp() int {
	gbm := (a.ccr >> 8) & 0x07
	return 1 << gbm
}

// setORG decodes the ORG command's two parameter words (MAME COMMAND_ORG):
//
//	org_dn  = pr0[15:14]                    — display/layer number
//	org_dpa = pr0[7:0]<<12 | pr1[15:4]>>4   — origin word address
//	org_dpd = pr1[3:0]                      — origin dot offset
//
// The 8593 firmware (ROM 0xA95E) issues ORG 0x4003,0xa450 ⇒ dn=1, dpa=0x3a45,
// dpd=0 — i.e. logical (0,0) is physical word 0x3a45 in layer 1.
func (a *acrtc) setORG(pr0, pr1 uint16) {
	a.orgDN = int((pr0 & 0xc000) >> 14)
	a.orgDPA = (uint32(pr0&0x00ff) << 12) | (uint32(pr1&0xfff0) >> 4)
	a.orgDPD = pr1 & 0x000f
}

// calcOffset converts a logical drawing coordinate (x,y) to a physical word offset
// + bit position — a direct port of MAME hd63484_device::calc_offset. Y is
// SUBTRACTED (Y-up): increasing y moves UP in screen space = toward lower
// addresses. All arithmetic is modular uint32 (the chip's pointers wrap); the
// final offset is folded to the installed RAM size on access (readword/writeword).
func (a *acrtc) calcOffset(x, y int16) (offset uint32, bitPos uint8) {
	bpp := a.getBpp()
	ppw := 16 / bpp // pixels per 16-bit word
	gbm := (a.ccr >> 8) & 0x07

	xi := int(x) + int(a.orgDPD>>gbm)
	var off, bp int
	if xi >= 0 {
		off = xi / ppw
		bp = xi % ppw
	} else {
		off = xi / ppw
		bp = (-xi) % ppw
		if bp != 0 {
			off--
			bp = ppw - bp
		}
	}
	// uint32(off) wraps for negative off, matching the C++ uint32_t arithmetic.
	o := uint32(int32(off)) + a.orgDPA - uint32(int32(y)*int32(a.mwr[a.orgDN]))
	return o, uint8(bp * bpp)
}

func (a *acrtc) readword(off uint32) uint16 { return a.ram[off&acrtcRAMMask] }
func (a *acrtc) writeword(off uint32, v uint16) {
	a.ram[off&acrtcRAMMask] = v
	a.writeHist[(off&acrtcRAMMask)>>9]++ // 0x200-word (1 KB) page buckets
	a.cmdTag[off&acrtcRAMMask] = a.curCmd
}

// getDot reads the logical pixel value at (x,y) (MAME get_dot).
func (a *acrtc) getDot(x, y int16) uint16 {
	bpp := a.getBpp()
	off, bitPos := a.calcOffset(x, y)
	return (a.readword(off) >> bitPos) & ((1 << bpp) - 1)
}

// setDot writes logical pixel value `color` at (x,y) (read-modify-write the bits
// the pixel occupies). For 1bpp this sets/clears a single bit.
func (a *acrtc) setDot(x, y int16, color uint16) {
	bpp := a.getBpp()
	off, bitPos := a.calcOffset(x, y)
	off += a.drawOffset // experiment hook: redirect draws to another region
	pmask := uint16((1<<bpp)-1) << bitPos
	w := a.readword(off)
	w = (w &^ pmask) | ((color << bitPos) & pmask)
	a.writeword(off, w)
}

// clrExec is the CLR/SCLR area op — a direct port of MAME command_clr_exec. It
// fills the (|ax|+1)×(|ay|+1) word region anchored at the RWP: d0 steps along the
// raster (+1 word), d1 steps up one line (−MWR words). cr bit10 set ⇒ apply the
// logical op cr&3 (0 replace / 1 OR / 2 AND / 3 EOR) under the write-mask; clear ⇒
// plain replace with d. Afterwards the RWP advances up by (ay+1) lines so a
// sequence tiles the buffer. Operates on the flat buffer — no plane split.
func (a *acrtc) clrExec(cr, d uint16, ax, ay int16) {
	mm := cr & 0x03
	d0inc := int16(1)
	if ax < 0 {
		d0inc = -1
	}
	d1inc := int16(1)
	if ay < 0 {
		d1inc = -1
	}
	mwr := uint32(a.mwr[a.rwpDN])
	base := a.rwp[a.rwpDN]
	for d1 := int16(0); d1 != ay+d1inc; d1 += d1inc {
		for d0 := int16(0); d0 != ax+d0inc; d0 += d0inc {
			off := base - uint32(int32(d1))*mwr + uint32(int32(d0))
			data := a.readword(off)
			var res uint16
			if cr&0x0400 != 0 { // BIT(cr,10)
				switch mm {
				case 0:
					res = (data &^ a.mask) | (d & a.mask)
				case 1:
					res = (data &^ a.mask) | ((data | d) & a.mask)
				case 2:
					res = (data &^ a.mask) | ((data & d) & a.mask)
				case 3:
					res = (data &^ a.mask) | ((data ^ d) & a.mask)
				}
			} else {
				res = d
			}
			a.writeword(off, res)
		}
	}
	a.rwp[a.rwpDN] = (a.rwp[a.rwpDN] - uint32(int32(ay+d1inc))*mwr) & 0xfffff
}

package hd63484

import "testing"

// This file is a behavioural "test screen" for the HD63484 CRT controller: it
// drives a known command sequence through the chip and asserts the rendered
// VRAM matches what the firmware expects. The focus is the DRAW → CLEAR →
// REDRAW pipeline, because that is what the cal-data display (CAL DISP;) relies
// on — it wipes the graticule/trace, then paints its table. If a clear is
// dropped, the new content OVERLAYS the old screen (the observed bug). These
// tests verify each clear primitive actually clears, and that a clear-then-
// redraw leaves only the new content.
//
// All coordinates are in firmware drawing space; setVRAMPixel/clearVRAMPixel/
// isVRAMPixelLit share the same ORG transform (default origin (48,256) with a
// Y-up→Y-down flip), so assertions in firmware space are self-consistent.

// fillRect paints a solid filled rectangle from (x0,y0) to (x1,y1) via AMOVE +
// AFRCT — the controller's area-fill primitive.
func fillRect(c *Chip, x0, y0, x1, y1 uint16) {
	feedWords(c, cmdAMOVE, x0, y0)
	feedWords(c, cmdAFRCT, x1, y1)
}

// drawSolidGlyph blits a solid 16×8 glyph cell at firmware pen (x,y) via the
// WPTN glyph packet (fg=0xFFFF lights every bit).
func drawSolidGlyph(c *Chip, x, y uint16) {
	feedWords(c, cmdAMOVE, x, y)
	feedWords(c, cmdWPTN, glyphWPTNCount, 0xFFFF, 0x0000) // fg, bg
	for i := 0; i < glyphRows; i++ {
		feedWords(c, 0xFFFF) // all 16 columns lit
	}
	feedWords(c, cmdPTN, 0x0907) // PTN: blit the staged glyph (SZ = 8×10 cell)
}

// setRWP sets the Read/Write Pointer to word offset `off` via WPR 0x0C/0x0D,
// using the chip's exact encoding (0x0C = bank+high byte, 0x0D = low 12 bits).
func setRWP(c *Chip, off uint32) {
	hi := uint16((off >> 12) & 0xff) // bank 0
	lo := uint16((off & 0xfff) << 4)
	feedWords(c, 0x0800|PRMARLow, hi)
	feedWords(c, 0x0800|PRMARHigh, lo)
}

// setAreaDef programs the area-definition clip rect (WPR 0x08-0x0B).
func setAreaDef(c *Chip, xmin, ymin, xmax, ymax uint16) {
	feedWords(c, 0x0800|0x08, xmin)
	feedWords(c, 0x0800|0x09, ymin)
	feedWords(c, 0x0800|0x0a, xmax)
	feedWords(c, 0x0800|0x0b, ymax)
}

// TestCRTSclrClearsGlyph is the core alignment test the user asked for: draw a
// single glyph, then issue the firmware's SCLR (0x5C02 = AND with a 0x5555
// checkerboard) over its region, and assert the glyph is CLEANLY CLEARED — gone
// from the foreground with nothing left in the background plane. We don't
// reproduce the 1-bit dither (see area_ops.go "NO DITHERING"); the SCLR is read
// as "clear this region". This pins the SCLR-clears-content alignment with
// pixel-level assertions, in isolation from the full-boot render. The SCLR is
// addressed by the Read/Write Pointer, so the RWP must map to the SAME vram words
// the pen drew the glyph into; if the RWP↔pen mapping is off, the glyph won't be
// touched and this fails.
func TestCRTSclrClearsGlyph(t *testing.T) {
	c := New()
	const gx, gy = 64, 100 // firmware pen position, inside a typical graph
	drawSolidGlyph(c, gx, gy)

	// Sanity: the solid glyph lit the cell.
	if !isLit(c, gx, gy) || !isLit(c, gx+1, gy) {
		t.Fatalf("solid glyph not drawn at (%d,%d)", gx, gy)
	}

	// Address the SCLR by the RWP using the SAME core addressing the pen used to
	// draw the glyph (calcOffset = orgDPA + x/16 − y·mwr), so the clear lands on the
	// stored content. (The legacy orgRow/orgCol/displayScanStart formula diverged
	// from the core address at cell edges.)
	off, _ := c.core.calcOffset(int16(gx), int16(gy))
	setAreaDef(c, 0, 0, 400, 209)
	feedWords(c, 0x0800|PRMemWidth, 0xFFFF) // mask = all bits
	setRWP(c, off)
	feedWords(c, 0x5C02, 0x5555, 0x0001, 0x0008) // SCLR AND 0x5555, ax=1, ay=8

	// The glyph is cleanly cleared from the foreground; nothing lingers in the
	// background plane (no dither dots).
	for dx := 0; dx < 2; dx++ {
		if isLit(c, gx+dx, gy) {
			t.Errorf("foreground (%d,%d) should be cleanly cleared", gx+dx, gy)
		}
		if isLitBg(c, gx+dx, gy) {
			t.Errorf("background (%d,%d) should be empty — we don't dither", gx+dx, gy)
		}
	}
}

// TestCRTAreaFillLitThenScreenClear: a filled rectangle lights its interior,
// and a full SCLR (screen clear, fill word 0) returns the whole screen to dark.
func TestCRTAreaFillLitThenScreenClear(t *testing.T) {
	c := New()
	fillRect(c, 0, 0, 300, 150)

	// Interior + corners of the fill must be lit.
	for _, p := range [][2]int{{1, 1}, {150, 75}, {299, 149}} {
		if !isLit(c, p[0], p[1]) {
			t.Fatalf("after AFRCT, (%d,%d) should be lit", p[0], p[1])
		}
	}

	// SCLR 0x0000 — clear the entire screen to dark.
	feedWords(c, cmdSCLR, 0x0000)
	for _, p := range [][2]int{{1, 1}, {150, 75}, {299, 149}} {
		if isLit(c, p[0], p[1]) {
			t.Errorf("after SCLR 0, (%d,%d) should be dark", p[0], p[1])
		}
	}
}

// TestCRTAreaClearWindowPreservesOutside: CLR (0xF000) with fill word 0 clears
// exactly the addressed window and nothing outside it — the per-region clear the
// firmware uses to wipe a sub-area of the screen.
func TestCRTAreaClearWindowPreservesOutside(t *testing.T) {
	c := New()
	fillRect(c, 0, 0, 300, 150) // whole "old screen" lit

	// CLR a 100×50 window whose top-left is (100,50): clears (100,50)..(200,100).
	feedWords(c, cmdAMOVE, 100, 50)
	feedWords(c, cmdCLR, 0x0000, 100, 50) // data=0 (clear), dx=100, dy=50

	// Inside the window → cleared.
	for _, p := range [][2]int{{100, 50}, {150, 75}, {200, 100}} {
		if isLit(c, p[0], p[1]) {
			t.Errorf("inside CLR window (%d,%d) should be dark", p[0], p[1])
		}
	}
	// Outside the window → still lit.
	for _, p := range [][2]int{{50, 25}, {250, 125}, {99, 75}, {201, 75}} {
		if !isLit(c, p[0], p[1]) {
			t.Errorf("outside CLR window (%d,%d) should still be lit", p[0], p[1])
		}
	}
}

// TestCRTClearThenRedrawNoOverlay models the cal-display scenario directly:
// draw an "old screen", clear its content region, then paint "new content" into
// the cleared region. The cleared region must show ONLY the new content — the
// old content must not bleed through (the overlay bug).
func TestCRTClearThenRedrawNoOverlay(t *testing.T) {
	c := New()

	// 1. Old screen: a filled background block (stand-in for graticule + trace).
	fillRect(c, 0, 0, 200, 120)
	if !isLit(c, 100, 60) {
		t.Fatal("old content should be lit before clear")
	}

	// 2. Clear the content window (80,40)..(160,90).
	feedWords(c, cmdAMOVE, 80, 40)
	feedWords(c, cmdCLR, 0x0000, 80, 50)
	if isLit(c, 100, 60) {
		t.Fatal("content window should be dark after CLR")
	}

	// 3. New content: a single dot at (120,65) inside the cleared window.
	feedWords(c, cmdAMOVE, 120, 65)
	feedWords(c, cmdDOT)

	// Only the new dot is lit inside the window; the rest stays dark (no overlay).
	if !isLit(c, 120, 65) {
		t.Error("new content dot should be lit")
	}
	if isLit(c, 100, 60) || isLit(c, 140, 70) {
		t.Error("old content bled through the clear (overlay) — should be dark")
	}
}

// TestCRTAreaFillThenLitFill: CLR with a NON-zero fill word lights the region
// (the "fill lit" half of the CLR contract), complementing the clear-to-dark
// case above.
func TestCRTAreaFillThenLitFill(t *testing.T) {
	c := New()
	feedWords(c, cmdSCLR, 0x0000) // start from a known-dark screen
	feedWords(c, cmdAMOVE, 40, 40)
	feedWords(c, cmdCLR, 0xFFFF, 30, 20) // data!=0 → fill lit, (40,40)..(70,60)
	if !isLit(c, 55, 50) {
		t.Error("CLR with non-zero fill should light the region")
	}
	if isLit(c, 10, 10) {
		t.Error("area outside the filled region should remain dark")
	}
}

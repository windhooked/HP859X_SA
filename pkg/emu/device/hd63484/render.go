package hd63484

import "image"

// RenderFrame materialises the chip's VRAM into an RGBA framebuffer for
// inspection / display. Every lit bit in the visible 640×480 sub-window of
// the paint area renders as fgColor; everything else renders as opaque
// black.
//
// VRAM is now the SINGLE source of truth — drawing commands (lines, dots,
// rectangles, glyph blits, raster bursts, SCLR/CLR) all manipulate vram
// bits directly. RenderFrame is a pure read of that state, so calls are
// idempotent and reflect exactly what the firmware has painted as of the
// most recent command word.
// displayStartRow returns the VRAM row the display scans from. Per MAME's
// draw_graphics_line the base screen layer is scanned from the Start Address
// Register SAR[1] (AR 0xCC:0xCE) at MWR1 words/line: srcWord = SAR[1] +
// line*MWR1, so the first displayed row = SAR[1] / MWR1. This is the page-flip /
// mode-switch selector — re-pointing SAR[1] swaps which buffer is shown (the
// mechanism the cal-data display uses). Falls back to the legacy RAR1 (AR 0xC8)
// when the firmware hasn't programmed SAR[1], so the boot (which leaves SAR[1]=0)
// is unchanged. Wrapped to PaintHeight.
func (c *Chip) displayStartRow() int {
	wpr := int(c.dispMWR)
	if wpr <= 0 {
		wpr = PaintRowBytes / 2 // 64 words/line (1024 bits ÷ 16) default
	}
	src := int(c.sar[1])
	if src == 0 {
		src = int(c.dispRAR) // legacy fallback when SAR1 unprogrammed
	}
	row := (src / wpr) % PaintHeight
	if row < 0 {
		row += PaintHeight
	}
	return row
}

func (c *Chip) RenderFrame() *image.RGBA {
	// CUTOVER (Phase 4): the display is now scanned from the faithful unified core
	// buffer (scanout.go RenderCore) — content page OR grid page at +0x4000, framed
	// by the firmware's real ORG — with NO coordinate hacks (orgRow=256 /
	// displayScanStart / +209 / bgVram). The legacy plane-based scanout it replaced
	// is gone; the legacy vram/textPlane/bgVram planes remain only as the dual-write
	// source and are slated for removal once the decoder writes the core directly.
	c.img = c.RenderCore()
	return c.img
}

// RenderCropped returns the sub-image covering the chip's drawn pixels,
// plus a 1-pixel margin. Returns the full frame if nothing has been drawn.
// Useful for tests that want to focus on a specific blit's output.
func (c *Chip) RenderCropped() image.Image {
	c.RenderFrame()
	if c.maxX < c.minX || c.maxY < c.minY {
		return c.img
	}
	// Bounds are in VRAM/drawing space; the output image is vertically stretched
	// (VisibleHeight→DisplayHeight). X maps 1:1; Y scales by DisplayHeight/
	// VisibleHeight.
	x0 := c.minX - 1
	x1 := c.maxX + 2
	y0 := c.minY*DisplayHeight/VisibleHeight - 1
	y1 := (c.maxY+1)*DisplayHeight/VisibleHeight + 1
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > DisplayWidth {
		x1 = DisplayWidth
	}
	if y1 > DisplayHeight {
		y1 = DisplayHeight
	}
	return c.img.SubImage(image.Rect(x0, y0, x1, y1))
}

// Image returns the most recently rendered RGBA framebuffer, materialising
// one if none exists yet. Test helpers use this to inspect drawing results.
func (c *Chip) Image() *image.RGBA {
	if c.img == nil {
		c.RenderFrame()
	}
	return c.img
}

// VRAM returns a read-only view of the chip's video RAM (for inspection by
// tests / probes). Live buffer; copy if you need to retain it across
// further writes.
func (c *Chip) VRAM() []byte { return c.vram[:] }

// BgVRAM returns a read-only view of the background plane (the 0x4400 raster
// fill). For diagnostics that inspect the background dot/stripe texture.
func (c *Chip) BgVRAM() []byte { return c.bgVram[:] }

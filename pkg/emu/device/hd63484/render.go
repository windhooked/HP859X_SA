package hd63484

import "image"

// displayStartRow returns the frame-buffer row the display scans from. Per
// MAME's draw_graphics_line the base screen layer is scanned from the Start
// Address Register SAR[1] (AR 0xCC:0xCE) at MWR1 words/line: srcWord = SAR[1] +
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

// RenderFrame materialises the display into an RGBA framebuffer for
// inspection / headless tools. The unified core buffer (acrtc.go) is the
// SINGLE source of truth — drawing commands (lines, dots, rectangles, glyph
// blits, raster bursts, SCLR/CLR, WT) all manipulate core words directly, and
// the scanout (RenderCore) is a pure read of that state, so calls are
// idempotent and reflect exactly what the firmware has painted as of the most
// recent command word.
func (c *Chip) RenderFrame() *image.RGBA {
	c.img = c.RenderCore()
	return c.img
}

// Image returns the most recently rendered RGBA framebuffer, materialising
// one if none exists yet. Test helpers use this to inspect drawing results.
func (c *Chip) Image() *image.RGBA {
	if c.img == nil {
		c.RenderFrame()
	}
	return c.img
}

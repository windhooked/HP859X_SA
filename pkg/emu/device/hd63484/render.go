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
func (c *Chip) RenderFrame() *image.RGBA {
	if c.img == nil {
		c.img = image.NewRGBA(image.Rect(0, 0, DisplayWidth, DisplayHeight))
	}
	pix := c.img.Pix
	stride := c.img.Stride
	for y := 0; y < DisplayHeight; y++ {
		// Output row y samples VRAM row (y * VisibleHeight / DisplayHeight) —
		// the analog CRT's vertical stretch of the 256-line raster onto the 4:3
		// tube (×1.5). Horizontal is 1:1 over the 512-px visible width; VRAM
		// rows ≥ VisibleHeight (the firmware's off-screen back frame) are never
		// sampled, so the off-screen second graticule frame is not displayed.
		srcY := y * VisibleHeight / DisplayHeight
		rowBase := srcY * PaintRowBytes
		dstBase := y * stride
		for x := 0; x < DisplayWidth; x++ {
			off := dstBase + x*4
			mask := byte(1 << uint(x&7))
			idx := rowBase + (x >> 3)
			switch {
			case c.vram[idx]&mask != 0:
				// Bright foreground: graticule, trace, glyphs, dots.
				pix[off] = fgColor.R
				pix[off+1] = fgColor.G
				pix[off+2] = fgColor.B
			case c.bgVram[idx]&mask != 0:
				// Dim background: the firmware's faint dot texture.
				pix[off] = bgPaintColor.R
				pix[off+1] = bgPaintColor.G
				pix[off+2] = bgPaintColor.B
			default:
				pix[off] = 0
				pix[off+1] = 0
				pix[off+2] = 0
			}
			pix[off+3] = 0xFF
		}
	}
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

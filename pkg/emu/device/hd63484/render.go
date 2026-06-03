package hd63484

import (
	"image"
	"image/color"
)

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
	if c.img == nil {
		c.img = image.NewRGBA(image.Rect(0, 0, DisplayWidth, DisplayHeight))
	}
	pix := c.img.Pix
	stride := c.img.Stride
	// The display scans VRAM starting from:
	//   displayStartRow() (from RAR1/MWR1) + displayScanStart (CRT vertical offset)
	// displayScanStart=23 is the number of VRAM rows before the visible CRT window
	// begins, derived from the ORG_row=256 geometry: to fit the firmware's full
	// content span fwY∈[-22,220] (VRAM rows 36..278) within the 256-row window,
	// the scan must start at row 23 (keeping row 36 ≥ 23 at the top and row 278 =
	// 23+255 at the bottom). See the defaultOrgRow / displayScanStart comments.
	start := c.displayStartRow() + displayScanStart
	for y := 0; y < DisplayHeight; y++ {
		// Output row y samples VRAM row start + (y * VisibleHeight / DisplayHeight),
		// wrapped — the analog CRT's vertical stretch of the 256-line raster onto
		// the 4:3 tube (×1.5). Horizontal is 1:1 over the 512-px visible width.
		srcY := y * VisibleHeight / DisplayHeight
		vramRow := (start + srcY) % PaintHeight
		rowBase := vramRow * PaintRowBytes
		dstBase := y * stride
		// renderXStart = 0: the display scans from VRAM column 0.
		// DisplayWidth (see chip.go) is set to HWR_active + HWR_blank = 512 + 32 = 544
		// so the full content span (VRAM cols 8..527) fits without clipping either side.
		const renderXStart = 0
		for x := 0; x < DisplayWidth; x++ {
			off := dstBase + x*4
			vx := renderXStart + x
			if vx < 0 || vx >= PaintRowPixels {
				pix[off] = 0
				pix[off+1] = 0
				pix[off+2] = 0
				pix[off+3] = 0xFF
				continue
			}
			mask := byte(1 << uint(vx&7))
			idx := rowBase + (vx >> 3)
			// Plane priority (front to back): text > trace > graticule(vram) >
			// dither(bgVram) > black. In mono the foreground planes all render
			// bright amber and bgVram dim amber (so the dither is the faint
			// background it is on the real CRT); Colorized assigns distinct colours.
			var col color.RGBA
			fg, grat, bg := fgColor, fgColor, bgPaintColor
			tr := fgColor
			if c.Colorized {
				fg, tr, grat, bg = textColor, traceColor, graticuleColor, ditherColor
			}
			// Graticule GRID. A full-sweep geometry scan finds NO grid polylines —
			// the dotted vertical grid is the firmware's 0x4400 raster PATTERN fill,
			// written into the back page at MAR=0x4000 (= vram row 256, one graph-
			// height below the graph). Map that page UP so it lands in the graph, and
			// confine it to the graph rect (area-def 0,0-400,209 ⇒ vram cols orgCol..
			// orgCol+400, rows orgRow-209..orgRow). It renders dim/recessive; the
			// bright trace + box (vram/tracePlane) composite ON TOP via the cases
			// above. Vertical lines are Y-invariant so the exact row offset only needs
			// to land the visible graph rows inside the filled page (256..511).
			const graphH = 209 // area-def ymax
			const gridPageBase = 256
			graphTop := c.orgRow - graphH
			gridRow := (vramRow - graphTop + gridPageBase) % PaintHeight
			gridIdx := gridRow*PaintRowBytes + (vx >> 3)
			inGraph := vramRow >= graphTop && vramRow <= c.orgRow && vx >= c.orgCol && vx <= c.orgCol+400
			switch {
			case c.textPlane[idx]&mask != 0:
				col = fg
			case c.tracePlane[idx]&mask != 0:
				col = tr
			case c.vram[idx]&mask != 0:
				col = grat
			case inGraph && c.bgVram[gridIdx]&mask != 0:
				col = bg
			}
			pix[off] = col.R
			pix[off+1] = col.G
			pix[off+2] = col.B
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

// BgVRAM returns a read-only view of the background plane (the 0x4400 raster
// fill). For diagnostics that inspect the background dot/stripe texture.
func (c *Chip) BgVRAM() []byte { return c.bgVram[:] }

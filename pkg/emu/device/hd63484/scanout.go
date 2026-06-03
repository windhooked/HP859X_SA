package hd63484

import (
	"image"
	"image/color"
)

// scanout.go is the Phase-4 faithful display readout: it builds the output frame
// by scanning the unified core buffer (acrtc.go) — a port of the essential
// behaviour of MAME hd63484_device::draw_graphics_line, plus the 8593's
// SUPERIMPOSED access mode (OMR bit 3). The firmware draws the graph content
// (box/trace/text) at the ORG page and fills the vertical grid pattern into a
// second page exactly +0x4000 words away (MAR 0x4000); with OMR bit 3 set the
// display OVERLAYS the two pages. So display pixel = content | grid(+0x4000) —
// the real hardware mechanism, replacing the legacy +209 render offset.
//
// This renders directly from c.core with NO orgRow=256 / displayScanStart / +209
// coordinate hacks. It is validated against the legacy render before RenderFrame
// is switched onto it (then bgVram + the hacks get deleted).

const (
	// gridPageWords is the fixed offset between the content page and the grid page
	// (MAR 0x4000) that the firmware fills the 0x4400 vertical pattern into.
	gridPageWords = 0x4000

	// Graph extent in CORE coordinates, derived from the firmware's ORG
	// (calcOffset(0,0)=word 0x3a45=row 233 px 80; calcOffset(400,209)=row 24 px 480).
	// The superimposed grid only shows inside the graph; outside it (labels,
	// softkeys) only the content page shows.
	graphCoreRowTop = 24
	graphCoreRowBot = 233
	graphCorePxL    = 80
	graphCorePxR    = 480

	// coreXOrigin is the core pixel that maps to display column 0. The firmware's
	// ORG puts logical x at core pixel 80+x (dpa column = 5 words = px 80); the
	// screen's left edge is the leftmost content (the hp logo / REF labels at
	// firmware x≈−48 → core px 32). Scanning from here frames the logo at the left
	// and brings the right-side softkeys (firmware x≈408-495 → core px 488-575)
	// back inside the 544-px output.
	coreXOrigin = 32
)

// coreBit reads a single 1bpp pixel from the core at (coreRow, px) — row-major,
// MWR1 words per line.
func (c *Chip) coreBit(coreRow, px int) bool {
	mwr := int(c.core.mwr[1])
	if mwr <= 0 {
		mwr = 64
	}
	w := c.core.readword(uint32(coreRow*mwr + px/16))
	return w&(1<<uint(px&15)) != 0
}

// RenderCore produces the output frame from the unified core buffer via the
// faithful superimposed scanout. Same output geometry as RenderFrame
// (DisplayWidth×DisplayHeight, CRT vertical stretch) so it can be diffed against
// the legacy render.
func (c *Chip) RenderCore() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, DisplayWidth, DisplayHeight))
	pix := img.Pix
	stride := img.Stride
	for y := 0; y < DisplayHeight; y++ {
		// Map output row to a core content row (CRT stretch: VisibleHeight→DisplayHeight).
		coreRow := y * VisibleHeight / DisplayHeight
		dstBase := y * stride
		gridRowOff := gridPageWords / int(coreMWR(c)) // +0x4000 words = +256 rows
		for x := 0; x < DisplayWidth; x++ {
			off := dstBase + x*4
			corePx := coreXOrigin + x
			var col color.RGBA
			content := c.coreBit(coreRow, corePx)
			inGraph := coreRow >= graphCoreRowTop && coreRow <= graphCoreRowBot &&
				corePx >= graphCorePxL && corePx <= graphCorePxR
			switch {
			case content:
				col = fgColor // bright foreground (box/trace/text)
			case inGraph && c.coreBit(coreRow+gridRowOff, corePx):
				col = bgPaintColor // dim recessive grid (superimposed page)
			default:
				col = color.RGBA{0, 0, 0, 0xFF}
			}
			pix[off] = col.R
			pix[off+1] = col.G
			pix[off+2] = col.B
			pix[off+3] = 0xFF
		}
	}
	return img
}

func coreMWR(c *Chip) uint16 {
	if c.core.mwr[1] > 0 {
		return c.core.mwr[1]
	}
	return 64
}

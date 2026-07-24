package hd63484

import (
	"image"
	"image/color"
)

// scanout.go — display readouts over the unified core buffer (acrtc.go), the
// SINGLE frame buffer every drawing command writes (the legacy vram/bgVram/
// textPlane split is deleted).
//
// Two views exist:
//
//   - RenderScanout / RenderScanoutByCmd — the CANONICAL, register-derived
//     scanout (SP1 lines × MWR1 words from SAR1; a port of MAME
//     draw_graphics_line). No coordinate constants. The GUI, goldens and
//     machine tests use these.
//
//   - RenderCore (behind RenderFrame) — a legacy PRESENTATION view for the
//     older cmd/ diagnostic tools and RenderDebug: CRT vertical stretch,
//     fixed graph-window framing (graphCoreRow*/coreXOrigin below) and a
//     synthesized faint dotted rendering of the +0x4000 grid page. Its
//     constants shape only this view, never the buffer content. New code
//     should use RenderScanout.

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

// coreBit reads a single PIXEL (any plane lit) from the core at (coreRow, px)
// — row-major, MWR1 words per line, bpp-aware (2bpp: 8 px/word).
func (c *Chip) coreBit(coreRow, px int) bool {
	mwr := int(c.core.mwr[1])
	if mwr <= 0 {
		mwr = 64
	}
	bpp := c.core.getBpp()
	ppw := 16 / bpp
	w := c.core.readword(uint32(coreRow*mwr + px/ppw))
	return (w>>(uint(px%ppw)*uint(bpp)))&(uint16(1<<uint(bpp))-1) != 0
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
		gridRow := coreRow + gridPageWords/64 // grid page is +0x4000 words = +256 rows
		for x := 0; x < DisplayWidth; x++ {
			off := dstBase + x*4
			corePx := coreXOrigin + x
			var col color.RGBA
			inGraph := coreRow >= graphCoreRowTop && coreRow <= graphCoreRowBot &&
				corePx >= graphCorePxL && corePx <= graphCorePxR
			switch {
			case c.coreBit(coreRow, corePx):
				col = fgColor // bright foreground (box / trace / text / ticks)
			case inGraph && c.coreBit(gridRow, corePx) && (coreRow&3) == 0:
				// Graticule grid: the 0x4400 page superimposed as a FAINT, DOTTED,
				// recessive grid — dotted via the row-stride gate (only every 4th
				// scanline), faint via gridColor, so it reads as the real CRT's dim
				// dotted graticule rather than hard bright stripes.
				col = gridDimColor
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

// RenderScanout produces the display raster DERIVED ENTIRELY from the firmware's
// timing/screen registers — no magic offsets. Vertical extent = SP1 (Base split
// width) rasters; horizontal extent = MWR1 words/line (16 px each); the frame
// buffer address per displayed raster = SAR1 + line*MWR1 (with wraparound). This
// is the chip's actual scan (draw_graphics_line) for the Base screen; the output
// is the literal Base-screen image (1024 × SP1), to be mapped to the CRT output
// and superimposed with the Window screen in later steps.
func (c *Chip) RenderScanout() *image.RGBA {
	lines := int(c.sp[1])
	if lines == 0 {
		lines = VisibleHeight
	}
	mwr := int(c.core.mwr[1])
	if mwr == 0 {
		mwr = 64
	}
	bpp := c.core.getBpp()
	ppw := 16 / bpp
	pmask := uint16(1<<uint(bpp)) - 1
	sar := c.core.sar[1]
	w := mwr * ppw
	img := image.NewRGBA(image.Rect(0, 0, w, lines))
	for dl := 0; dl < lines; dl++ {
		base := (sar + uint32(dl*mwr)) & acrtcRAMMask
		for word := 0; word < mwr; word++ {
			v := c.core.scanWord(base + uint32(word))
			for b := 0; b < ppw; b++ {
				col := color.RGBA{0, 0, 0, 0xFF}
				if (v>>(uint(b)*uint(bpp)))&pmask != 0 {
					col = fgColor
				}
				img.SetRGBA(word*ppw+b, dl, col)
			}
		}
	}
	return img
}

// SetRenderLive makes the scanout read the LIVE frame buffer instead of the
// stable snapshot — so EVERY firmware screen update (CAL DISP, command echo,
// menus) refreshes immediately. Interactive callers (the GUI) set this; tests
// leave it off for a deterministic complete-frame render. See acrtc.renderLive.
func (c *Chip) SetRenderLive(b bool) { c.core.renderLive = b }

// RenderScanoutByCmd renders the same register-derived scanout window as
// RenderScanout, but colours each lit pixel BY THE COMMAND that drew it
// (cmdTagColors) and appends a legend strip at the bottom. This is the canonical
// view for tests/diagnostics: it shows the real display AND attributes every
// pixel to its drawing command (trace vs graticule vs glyph vs clear), so a
// rendering regression is immediately traceable to a command class. Reads the
// stable frame snapshot, so it shows a complete, flicker-free frame.
func (c *Chip) RenderScanoutByCmd() *image.RGBA {
	lines := int(c.sp[1])
	if lines == 0 {
		lines = VisibleHeight
	}
	mwr := int(c.core.mwr[1])
	if mwr == 0 {
		mwr = 64
	}
	bpp := c.core.getBpp()
	ppw := 16 / bpp
	pmask := uint16(1<<uint(bpp)) - 1
	sar := c.core.sar[1]
	w := mwr * ppw
	const legendH = 20
	img := image.NewRGBA(image.Rect(0, 0, w, lines+legendH))
	for dl := 0; dl < lines; dl++ {
		base := (sar + uint32(dl*mwr)) & acrtcRAMMask
		for word := 0; word < mwr; word++ {
			off := base + uint32(word)
			v := c.core.scanWord(off)
			for b := 0; b < ppw; b++ {
				col := color.RGBA{0, 0, 0, 0xFF}
				if (v>>(uint(b)*uint(bpp)))&pmask != 0 {
					// tag of whichever plane bit is lit (prefer the low bit)
					bit := b * bpp
					if v&(1<<uint(bit)) == 0 {
						bit++
					}
					cc := cmdTagColors[c.core.scanTagBit(off, bit)]
					if cc.A == 0 {
						cc = fgColor // lit but untagged ⇒ default foreground
					}
					col = cc
				}
				img.SetRGBA(word*ppw+b, dl, col)
			}
		}
	}
	drawCmdLegend(img, lines)
	return img
}

// ScanoutUnion ORs the LIVE register-derived scanout into dst (one byte per
// pixel, 1 = lit), reusing dst when it is already the right size; returns
// (dst, width, lines). It always reads the live core — never the stable
// snapshot — because its purpose is CRT BEAM INTEGRATION: callers sample it
// several times per displayed frame and union the results, which is what the
// real CRT's continuously-scanning beam does. A single point-sample of the
// live buffer misses content that is mid-erase/redraw (the graticule grid
// spends much of the sweep cycle cleared); the union across the frame's
// samples restores it, faithfully.
func (c *Chip) ScanoutUnion(dst []uint8) ([]uint8, int, int) {
	lines := int(c.sp[1])
	if lines == 0 {
		lines = VisibleHeight
	}
	mwr := int(c.core.mwr[1])
	if mwr == 0 {
		mwr = 64
	}
	bpp := c.core.getBpp()
	ppw := 16 / bpp
	pmask := uint16(1<<uint(bpp)) - 1
	sar := c.core.sar[1]
	w := mwr * ppw
	n := w * lines
	if len(dst) != n {
		dst = make([]uint8, n)
	}
	for dl := 0; dl < lines; dl++ {
		base := (sar + uint32(dl*mwr)) & acrtcRAMMask
		row := dl * w
		for word := 0; word < mwr; word++ {
			v := c.core.ram[(base+uint32(word))&acrtcRAMMask]
			if v == 0 {
				continue
			}
			px := row + word*ppw
			for b := 0; b < ppw; b++ {
				if (v>>(uint(b)*uint(bpp)))&pmask != 0 {
					dst[px+b] = 1
				}
			}
		}
	}
	return dst, w, lines
}

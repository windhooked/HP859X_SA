package hd63484

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// === Display regions ===
// The 8593 display is composed of FIVE regions in the ACRTC frame buffer: a
// CENTER region (the graticule + spectrum trace + CAL DISP, = the firmware's
// area-def rect 0,0-400,209) surrounded by FOUR glyph BORDER regions (TOP / BOTTOM
// / LEFT / RIGHT) that hold the annotation text (status line, freq/BW readouts,
// ref-level & active-function labels, softkey menu). All in firmware logical
// coordinates (Y-up; the graph occupies y 0..209, x 0..400).
const (
	regionCenter = iota // graticule + spectrum + CAL DISP
	regionTop           // above the graph (y>209): status / title line
	regionBottom        // below the graph (y<0): frequency / BW readouts
	regionLeft          // left of the graph (x<0): ref-level / function labels
	regionRight         // right of the graph (x>400): softkey menu
	numRegions
)

var regionNames = [numRegions]string{"CENTER", "TOP", "BOTTOM", "LEFT", "RIGHT"}

// Graph (center) extent in firmware logical coords = the area-def rect.
const (
	graphXMin, graphXMax = 0, 400
	graphYMin, graphYMax = 0, 209
)

// regionOf classifies a firmware logical coordinate into one of the 5 regions.
// Corners are assigned to TOP/BOTTOM first (the status/readout strips span full
// width), then LEFT/RIGHT, else CENTER.
func regionOf(x, y int) int {
	switch {
	case y > graphYMax:
		return regionTop
	case y < graphYMin:
		return regionBottom
	case x < graphXMin:
		return regionLeft
	case x > graphXMax:
		return regionRight
	default:
		return regionCenter
	}
}

// RenderDebug composes a side-by-side diagnostic: the MAIN rendered view on the
// left, and a tiled view of the unified core memory on the right — the whole flat
// buffer laid out as MWR1-word rasters (1024 px × 512 rows), scaled to fit, with
// the displayed window and the content/grid page boundary marked. This makes it
// obvious where each element actually lives in the frame buffer (why garbage
// above the graph, where the 0x4400 grid page sits, whether content wraps, etc.).
//
// Layout:
//
//	[ main view 544×384 ] | [ core memory 256×512→scaled, rows 0..511 ]
//
// The right panel's full height maps core rows 0..511; a brighter band marks the
// displayed window (rows 0..VisibleHeight) and a divider marks row 256 (the grid
// page base, MAR 0x4000).
func (c *Chip) RenderDebug() *image.RGBA {
	main := c.RenderFrame()

	const memW = 256 // core 1024 px → /4
	const gap = 8
	totalW := DisplayWidth + gap + memW
	out := image.NewRGBA(image.Rect(0, 0, totalW, DisplayHeight))

	// Left: blit the main view.
	for y := 0; y < DisplayHeight; y++ {
		copy(out.Pix[y*out.Stride:y*out.Stride+DisplayWidth*4],
			main.Pix[y*main.Stride:y*main.Stride+DisplayWidth*4])
	}

	// Right: the core buffer. Core is 64 words/row × 512 rows = 1024×512 px. Map the
	// panel's `DisplayHeight` rows across core rows 0..511 and `memW` cols across
	// core px 0..1023.
	x0 := DisplayWidth + gap
	dim := color.RGBA{0x20, 0x14, 0x00, 0xFF}
	bright := color.RGBA{0xFF, 0xB0, 0x00, 0xFF}
	win := color.RGBA{0x00, 0x18, 0x28, 0xFF} // tint for the displayed window band
	for py := 0; py < DisplayHeight; py++ {
		coreRow := py * PaintHeight / DisplayHeight // 0..511
		inWindow := coreRow < VisibleHeight
		for px := 0; px < memW; px++ {
			corePx := px * PaintRowPixels / memW // 0..1023
			lit := c.coreBit(coreRow, corePx)
			var col color.RGBA
			switch {
			case lit:
				col = bright
			case inWindow:
				col = win
			default:
				col = dim
			}
			o := py*out.Stride + (x0+px)*4
			out.Pix[o] = col.R
			out.Pix[o+1] = col.G
			out.Pix[o+2] = col.B
			out.Pix[o+3] = 0xFF
		}
	}
	// Divider line at the content/grid page boundary (core row 256 = MAR 0x4000).
	dy := VisibleHeight * DisplayHeight / PaintHeight
	for px := 0; px < memW; px++ {
		o := dy*out.Stride + (x0+px)*4
		out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = 0x00, 0x80, 0xFF, 0xFF
	}
	return out
}

// regionRects returns each region's extent in firmware logical coords
// {x0,x1,y0,y1} (inclusive), spanning the displayed window.
func regionRects() [numRegions][4]int {
	const wxl, wxr = -48, 495 // window x span
	const wyt, wyb = 233, -22 // window y span (Y-up: top=233, bottom=-22)
	var r [numRegions][4]int
	r[regionCenter] = [4]int{graphXMin, graphXMax, graphYMin, graphYMax}
	r[regionTop] = [4]int{wxl, wxr, graphYMax + 1, wyt}
	r[regionBottom] = [4]int{wxl, wxr, wyb, graphYMin - 1}
	r[regionLeft] = [4]int{wxl, graphXMin - 1, graphYMin, graphYMax}
	r[regionRight] = [4]int{graphXMax + 1, wxr, graphYMin, graphYMax}
	return r
}

var regionTints = [numRegions]color.RGBA{
	{0x00, 0x10, 0x20, 0xFF}, // CENTER dark blue
	{0x30, 0x08, 0x08, 0xFF}, // TOP    dark red
	{0x08, 0x22, 0x08, 0xFF}, // BOTTOM dark green
	{0x24, 0x08, 0x24, 0xFF}, // LEFT   dark purple
	{0x30, 0x1E, 0x00, 0xFF}, // RIGHT  dark amber
}

// renderRegionTile draws one region from the core (firmware rect, Y-up flipped to
// screen-down) onto dst at (ox,oy), lit pixels bright over the region tint.
func (c *Chip) renderRegionTile(dst *image.RGBA, ox, oy, x0, x1, y0, y1 int, tint color.RGBA) (w, h int) {
	w, h = x1-x0+1, y1-y0+1
	lit := color.RGBA{0xFF, 0xB0, 0x00, 0xFF}
	for ty := 0; ty < h; ty++ {
		fy := y1 - ty // Y-up: top row = highest y
		for tx := 0; tx < w; tx++ {
			fx := x0 + tx
			col := tint
			if c.core.getDot(int16(fx), int16(fy)) != 0 {
				col = lit
			}
			dst.SetRGBA(ox+tx, oy+ty, col)
		}
	}
	return
}

// RenderRegionCollage explodes the frame buffer into its 5 named regions, laid out
// in their true spatial arrangement (TOP strip on top, BOTTOM on bottom, LEFT /
// CENTER / RIGHT in the middle row) separated by gutters and each tinted a distinct
// colour — so each region's content can be inspected in isolation (is the right
// content in the right region; is the TOP band garbage; etc.). See regionNames /
// regionTints for the colour↔name legend.
func (c *Chip) RenderRegionCollage() *image.RGBA {
	const G = 6 // gutter
	r := regionRects()
	wTop := r[regionTop][1] - r[regionTop][0] + 1
	hTop := r[regionTop][3] - r[regionTop][2] + 1
	hBot := r[regionBottom][3] - r[regionBottom][2] + 1
	wL := r[regionLeft][1] - r[regionLeft][0] + 1
	wC := r[regionCenter][1] - r[regionCenter][0] + 1
	wR := r[regionRight][1] - r[regionRight][0] + 1
	hMid := r[regionCenter][3] - r[regionCenter][2] + 1

	totalW := wL + G + wC + G + wR
	if wTop > totalW {
		totalW = wTop
	}
	totalH := hTop + G + hMid + G + hBot
	out := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	// dark separator background
	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = 0x08, 0x08, 0x08, 0xFF
	}
	// TOP
	c.renderRegionTile(out, 0, 0, r[regionTop][0], r[regionTop][1], r[regionTop][2], r[regionTop][3], regionTints[regionTop])
	// middle row: LEFT | CENTER | RIGHT
	midY := hTop + G
	c.renderRegionTile(out, 0, midY, r[regionLeft][0], r[regionLeft][1], r[regionLeft][2], r[regionLeft][3], regionTints[regionLeft])
	c.renderRegionTile(out, wL+G, midY, r[regionCenter][0], r[regionCenter][1], r[regionCenter][2], r[regionCenter][3], regionTints[regionCenter])
	c.renderRegionTile(out, wL+G+wC+G, midY, r[regionRight][0], r[regionRight][1], r[regionRight][2], r[regionRight][3], regionTints[regionRight])
	// BOTTOM
	c.renderRegionTile(out, 0, hTop+G+hMid+G, r[regionBottom][0], r[regionBottom][1], r[regionBottom][2], r[regionBottom][3], regionTints[regionBottom])
	return out
}

// CoreWriteHist returns the per-0x200-word-page write histogram of the unified
// core buffer (which controller memory regions the firmware populates).
func (c *Chip) CoreWriteHist() []int {
	h := make([]int, len(c.core.writeHist))
	copy(h, c.core.writeHist[:])
	return h
}

// RenderMemoryAreasCollage renders the FOUR ACRTC logical-screen memory areas —
// SAR0 Upper, SAR1 Base, SAR2 Lower, SAR3 Window — each scanned straight from the
// frame buffer the way the display controller reads it: word = SAR + line*MWR +
// col, 64 words (1024 px) per line × 256 lines, ×1/2. 2×2 grid, coloured header
// per area. Naming: top-left Upper(SAR0), top-right Base(SAR1), bottom-left
// Lower(SAR2), bottom-right Window(SAR3).
func (c *Chip) RenderMemoryAreasCollage() *image.RGBA {
	hdrs := [4]color.RGBA{
		{0xFF, 0x30, 0x30, 0xFF}, // SAR0 Upper  red
		{0x20, 0x60, 0xFF, 0xFF}, // SAR1 Base   blue
		{0x20, 0xC0, 0x40, 0xFF}, // SAR2 Lower  green
		{0xFF, 0xB0, 0x00, 0xFF}, // SAR3 Window amber
	}
	mwr := uint32(c.core.mwr[1])
	if mwr == 0 {
		mwr = 64
	}
	const tileW, tileH = 512, 128 // 1024×256 source at ×1/2
	const hdr, gap = 6, 10
	out := image.NewRGBA(image.Rect(0, 0, tileW*2+gap, (tileH+hdr)*2+gap))
	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = 0x08, 0x08, 0x08, 0xFF
	}
	for s := 0; s < 4; s++ {
		ox := (s & 1) * (tileW + gap)
		oy0 := (s >> 1) * (tileH + hdr + gap)
		for y := 0; y < hdr; y++ {
			for x := 0; x < tileW; x++ {
				out.SetRGBA(ox+x, oy0+y, hdrs[s])
			}
		}
		for oy := 0; oy < tileH; oy++ {
			line := uint32(oy * 2)
			for ox2 := 0; ox2 < tileW; ox2++ {
				col := uint32(ox2 * 2)
				word := (c.core.sar[s] + line*mwr + col/16) & acrtcRAMMask
				cc := color.RGBA{0x00, 0x00, 0x00, 0xFF}
				if c.core.ram[word]&(1<<uint(col&15)) != 0 {
					cc = color.RGBA{0xFF, 0xB0, 0x00, 0xFF}
				}
				out.SetRGBA(ox+ox2, oy0+hdr+oy, cc)
			}
		}
	}
	return out
}

// SP returns split-screen width n (0=Upper,1=Base,2=Lower). CoreMWR1 returns MWR1.
func (c *Chip) SP(n int) uint16 {
	if n < 0 || n >= len(c.sp) {
		return 0
	}
	return c.sp[n]
}
func (c *Chip) CoreMWR1() uint16 { return c.core.mwr[1] }

// cmdTagOf classifies a command opcode into a cmdTag value (tagNone for
// non-drawing commands so curCmd keeps the last drawing class).
func cmdTagOf(w uint16) uint8 {
	// Opcodes follow the manual's sequential map: polylines AND polygons sit at
	// 0x9800–0xA7FF; the filled rectangles AFRCT/RFRCT are 0xC0xx/0xC4xx (the old
	// table had 0xA000/0xA400 mis-tagged as rectangles — they are APLG/RPLG).
	switch w & 0xFC00 {
	case 0x9800, 0x9C00, 0xA000, 0xA400: // APLL/RPLL/APLG/RPLG
		return tagPoly
	case 0x9000, 0x9400, 0xC000, 0xC400: // ARCT/RRCT + AFRCT/RFRCT
		return tagRect
	case 0x8800, 0x8C00: // ALINE/RLINE
		return tagLine
	case 0xCC00: // DOT
		return tagDot
	}
	switch {
	case w&0xFFFC == 0x5C00:
		return tagSCLR
	case w&0xFFFE == 0xF000:
		return tagCLR
	case w&0xFFFE == 0x1800:
		return tagGlyph
	}
	return tagNone
}

// cmdTagColors maps each command class to a render colour.
var cmdTagColors = [tagOther + 1]color.RGBA{
	tagNone:   {0x10, 0x10, 0x10, 0xFF}, // grey  — untouched
	tagPoly:   {0xFF, 0xB0, 0x00, 0xFF}, // amber — APLL/RPLL (trace + vector lines)
	tagSCLR:   {0xFF, 0x20, 0x20, 0xFF}, // red   — SCLR area op
	tagCLR:    {0xC0, 0x00, 0x60, 0xFF}, // magenta — CLR
	tagRect:   {0x20, 0x80, 0xFF, 0xFF}, // blue  — ARCT/RRCT box+rects
	tagLine:   {0x00, 0xFF, 0xFF, 0xFF}, // cyan  — ALINE/RLINE
	tagDot:    {0xFF, 0xFF, 0x00, 0xFF}, // yellow— DOT
	tagGlyph:  {0xFF, 0xFF, 0xFF, 0xFF}, // white — glyphs
	tagRaster: {0x20, 0xC0, 0x40, 0xFF}, // green — 0x4400 raster pattern
	tagOther:  {0x80, 0x80, 0x00, 0xFF},
}

// RenderUpperByCmd renders the UPPER memory region (core rows 256..511, the
// 0x4000 page) coloured BY THE COMMAND that wrote each pixel (see cmdTagColors /
// the legend in regionNames-style comments). Lit pixels take their command's
// colour; unlit-but-written words show a faint tint of the writing command.
func (c *Chip) RenderByCmd(startRow int) *image.RGBA {
	const rows = 256
	const legendH = 20
	img := image.NewRGBA(image.Rect(0, 0, PaintRowPixels, rows+legendH))
	mwr := PaintRowBytes / 2 // 64 words/line
	for row := 0; row < rows; row++ {
		coreRow := startRow + row
		for px := 0; px < PaintRowPixels; px++ {
			word := uint32(coreRow*mwr + px/16)
			bit := px & 15
			tag := c.core.cmdTag[(word&acrtcRAMMask)<<4|uint32(bit)]
			lit := c.core.ram[word&acrtcRAMMask]&(1<<uint(bit)) != 0
			cc := cmdTagColors[tag]
			if !lit {
				// unlit: dim the command colour to a faint tint (shows where each
				// command cleared/wrote 0 without lighting a pixel)
				cc = color.RGBA{cc.R / 6, cc.G / 6, cc.B / 6, 0xFF}
			}
			img.SetRGBA(px, row, cc)
		}
	}
	drawCmdLegend(img, rows)
	return img
}

var cmdTagLabels = [tagOther + 1]string{
	tagNone: "none", tagPoly: "APLL/RPLL", tagSCLR: "SCLR", tagCLR: "CLR",
	tagRect: "ARCT", tagLine: "ALINE/RLINE", tagDot: "DOT", tagGlyph: "glyph",
	tagRaster: "raster", tagOther: "other",
}

func drawCmdLegend(img *image.RGBA, y0 int) {
	x := 4
	for tag := tagPoly; tag <= tagRaster; tag++ {
		col := cmdTagColors[tag]
		for dy := 4; dy < 16; dy++ {
			for dx := 0; dx < 12; dx++ {
				img.SetRGBA(x+dx, y0+dy, col)
			}
		}
		label := cmdTagLabels[tag]
		d := &font.Drawer{Dst: img, Src: image.NewUniform(col), Face: basicfont.Face7x13, Dot: fixed.P(x+16, y0+15)}
		d.DrawString(label)
		x += 16 + len(label)*7 + 14
	}
}

package hd63484

import (
	"image"
	"image/color"
)

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

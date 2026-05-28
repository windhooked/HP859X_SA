package hd63484

import "image"

// paint-area geometry. The 8593 firmware emits cell-extent parameter words
// 0x003F=63 and 0x00FF=255 before each PAINT burst, meaning each burst
// covers 64×256 = 16384 word-cells. Each cell is one 16-bit word ⇒ 16
// horizontal pixels at 1bpp ⇒ a 1024×256 region per burst. Two bursts per
// frame (top half + bottom half via auto-increment) ⇒ 1024×512 paint area
// total. We clip to the visible 640×480 CRT viewport at render time.
const (
	paintRowWords  = 64
	paintRowPixels = paintRowWords * 16 // 1024
	paintHeight    = 512                // 2 × 16384 / 64
)

// RenderFrame composites the chip's external video RAM (filled by PAINT /
// raster-write bursts — the background dot pattern, the trace bitmap when
// the firmware draws it) underneath the drawing overlay (glyphs / lines /
// dots painted directly into d.img by the drawing-command handlers) and
// returns the composited image.
//
// Each VRAM word is 16 horizontal pixels at 1bpp; bit 0 = leftmost pixel
// (little-endian within the word, matching the firmware's word writes).
// Lit VRAM bits render in bgPaintColor (dim amber) so they don't overpower
// the bright-amber drawing overlay sitting on top.
//
// The composite is idempotent: subsequent calls re-light the same pixels
// (the condition "img pixel is currently black" gates the write), so callers
// can invoke RenderFrame freely.
func (c *Chip) RenderFrame() *image.RGBA {
	for i := 0; i < paintHeight*paintRowWords; i++ {
		if 2*i+1 >= len(c.vram) {
			break
		}
		w := uint16(c.vram[2*i]) | uint16(c.vram[2*i+1])<<8
		if w == 0 {
			continue
		}
		x0 := (i * 16) % paintRowPixels
		y := (i * 16) / paintRowPixels
		if y >= paintHeight {
			break
		}
		for b := 0; b < 16; b++ {
			if w&(1<<uint(b)) == 0 {
				continue
			}
			x := x0 + b
			if x >= DisplayWidth || y >= DisplayHeight {
				continue
			}
			off := y*c.img.Stride + x*4
			if c.img.Pix[off]|c.img.Pix[off+1]|c.img.Pix[off+2] == 0 {
				c.img.Pix[off] = bgPaintColor.R
				c.img.Pix[off+1] = bgPaintColor.G
				c.img.Pix[off+2] = bgPaintColor.B
				c.img.Pix[off+3] = 0xFF
			}
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
	x0 := c.minX - 1
	y0 := c.minY - 1
	x1 := c.maxX + 2
	y1 := c.maxY + 2
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

// VRAM returns a read-only view of the chip's video RAM (for inspection by
// tests / probes). Live buffer; copy if you need to retain it across
// further writes.
func (c *Chip) VRAM() []byte { return c.vram[:] }

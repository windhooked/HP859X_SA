package hd63484

import "image/color"

// lineIterCap caps Bresenham's per-line iteration count to keep an
// erroneous off-screen endpoint from spinning the renderer.
const lineIterCap = 4096

// setPixel writes one pixel into the chip's compositor framebuffer if it
// lies inside the visible 640×480 region. Also updates the drawn-content
// bounding box so RenderCropped knows what to crop to.
func (c *Chip) setPixel(x, y int, col color.RGBA) {
	if x < 0 || y < 0 || x >= DisplayWidth || y >= DisplayHeight {
		return
	}
	off := y*c.img.Stride + x*4
	c.img.Pix[off] = col.R
	c.img.Pix[off+1] = col.G
	c.img.Pix[off+2] = col.B
	c.img.Pix[off+3] = 0xFF
	if x < c.minX {
		c.minX = x
	}
	if y < c.minY {
		c.minY = y
	}
	if x > c.maxX {
		c.maxX = x
	}
	if y > c.maxY {
		c.maxY = y
	}
}

// drawLine rasterises a line from (x0,y0) to (x1,y1) using Bresenham. The
// HD63484 hardware actually walks the line via a coordinate ALU with
// per-step pattern matching for dotted/dashed styles — but the firmware
// uses solid-line style for the graticule, so we render solid.
func (c *Chip) drawLine(x0, y0, x1, y1 int, col color.RGBA) {
	dx, dy := x1-x0, y1-y0
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy
	for i := 0; i < lineIterCap; i++ {
		c.setPixel(x0, y0, col)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

// drawRect draws the outline of the rectangle spanned by (x0,y0)..(x1,y1).
// Used for ARCT / RRCT. (Note: ARCT moves the pen to the second corner;
// the parser updates pen position after calling this.)
func (c *Chip) drawRect(x0, y0, x1, y1 int, col color.RGBA) {
	c.drawLine(x0, y0, x1, y0, col) // top
	c.drawLine(x1, y0, x1, y1, col) // right
	c.drawLine(x1, y1, x0, y1, col) // bottom
	c.drawLine(x0, y1, x0, y0, col) // left
}

// drawFilledRect rasterises a solid rectangle. Used for AFRCT / RFRCT.
func (c *Chip) drawFilledRect(x0, y0, x1, y1 int, col color.RGBA) {
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			c.setPixel(x, y, col)
		}
	}
}

// drawCircle rasterises a circle of |radius| at (cx, cy) using the midpoint-
// circle algorithm. Used for CRCL.
func (c *Chip) drawCircle(cx, cy, radius int, col color.RGBA) {
	if radius < 0 {
		radius = -radius
	}
	x, y := radius, 0
	err := 0
	plot8 := func(x, y int) {
		c.setPixel(cx+x, cy+y, col)
		c.setPixel(cx-x, cy+y, col)
		c.setPixel(cx+x, cy-y, col)
		c.setPixel(cx-x, cy-y, col)
		c.setPixel(cx+y, cy+x, col)
		c.setPixel(cx-y, cy+x, col)
		c.setPixel(cx+y, cy-x, col)
		c.setPixel(cx-y, cy-x, col)
	}
	for x >= y {
		plot8(x, y)
		y++
		if err <= 0 {
			err += 2*y + 1
		}
		if err > 0 {
			x--
			err -= 2*x + 1
		}
	}
}

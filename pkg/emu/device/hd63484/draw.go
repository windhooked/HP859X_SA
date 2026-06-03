package hd63484

// lineIterCap caps Bresenham's per-line iteration count to keep an
// erroneous off-screen endpoint from spinning the renderer.
const lineIterCap = 4096

// setPixel writes one pixel into VRAM. The `set` argument controls whether
// the bit is turned on (true = lit) or off (false = cleared). Used by the
// drawing primitives below and by glyph blits.
func (c *Chip) setPixel(x, y int, set bool) {
	if set {
		c.setVRAMPixel(x, y)
	} else {
		c.clearVRAMPixel(x, y)
	}
}

// drawLine rasterises a line from (x0,y0) to (x1,y1) using Bresenham, applying
// the chip's 16-bit line stipple (c.linePattern). The HD63484 walks the line via
// a coordinate ALU with per-step pattern matching for dotted/dashed styles; the
// 8593 firmware programs the pattern per line via WPTN (0xFFFF solid, 0x1111
// dotted minor-division, 0xCCCC dash, 0x0001 sparse). We walk the same pattern
// (bit i&15 along the line) and skip pixels whose bit is 0, so the internal
// graticule grid renders dotted as on the real CRT. A 0 pattern → solid.
func (c *Chip) drawLine(x0, y0, x1, y1 int, set bool) {
	if c.LineLog != nil {
		c.LineLog = append(c.LineLog, LineRec{x0, y0, x1, y1})
	}
	pat := c.linePattern
	if pat == 0 {
		pat = 0xFFFF
	}
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
		if pat&(1<<uint(i&15)) != 0 {
			c.setPixel(x0, y0, set)
		}
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

// drawLineRouted draws a line, routing it to the trace plane in Colorized mode
// when it is SOLID (linePattern 0xFFFF) — the 8593 firmware forces the spectrum
// trace solid while drawing the graticule grid with a stipple, so a solid
// ALINE/RLINE is the trace (green plane), a stippled one is the grid (vram/grey).
// Outside Colorized mode it is a plain solid line into vram, unchanged.
func (c *Chip) drawLineRouted(x0, y0, x1, y1 int) {
	if c.Colorized && c.linePattern == 0xFFFF {
		prev := c.activePlane
		c.activePlane = &c.tracePlane
		c.drawLine(x0, y0, x1, y1, true)
		c.activePlane = prev
		return
	}
	c.drawLine(x0, y0, x1, y1, true)
}

// drawRect draws the outline of the rectangle spanned by (x0,y0)..(x1,y1).
// Used for ARCT / RRCT. (Note: ARCT moves the pen to the second corner;
// the parser updates pen position after calling this.)
func (c *Chip) drawRect(x0, y0, x1, y1 int, set bool) {
	c.drawLine(x0, y0, x1, y0, set) // top
	c.drawLine(x1, y0, x1, y1, set) // right
	c.drawLine(x1, y1, x0, y1, set) // bottom
	c.drawLine(x0, y1, x0, y0, set) // left
}

// drawFilledRect rasterises a solid rectangle. Used for AFRCT / RFRCT and
// by CLR / SCLR (when fill word is non-zero).
func (c *Chip) drawFilledRect(x0, y0, x1, y1 int, set bool) {
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			c.setPixel(x, y, set)
		}
	}
}

// drawCircle rasterises a circle of |radius| at (cx, cy) using the midpoint-
// circle algorithm. Used for CRCL.
func (c *Chip) drawCircle(cx, cy, radius int, set bool) {
	if radius < 0 {
		radius = -radius
	}
	x, y := radius, 0
	err := 0
	plot8 := func(x, y int) {
		c.setPixel(cx+x, cy+y, set)
		c.setPixel(cx-x, cy+y, set)
		c.setPixel(cx+x, cy-y, set)
		c.setPixel(cx-x, cy-y, set)
		c.setPixel(cx+y, cy+x, set)
		c.setPixel(cx-y, cy+x, set)
		c.setPixel(cx+y, cy-x, set)
		c.setPixel(cx-y, cy-x, set)
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

// fillVRAM replicates a 16-bit fill word across the entire VRAM. Used by
// SCLR (screen clear) when the firmware passes a non-zero fill word, and
// internally to reset the chip framebuffer on construction.
func (c *Chip) fillVRAM(word uint16) {
	lo := byte(word)
	hi := byte(word >> 8)
	for i := 0; i+1 < len(c.vram); i += 2 {
		c.vram[i] = lo
		c.vram[i+1] = hi
	}
	// Mirror into the faithful core (full-screen fill/clear).
	for i := range c.core.ram {
		c.core.ram[i] = word
	}
	c.resetBounds()
}

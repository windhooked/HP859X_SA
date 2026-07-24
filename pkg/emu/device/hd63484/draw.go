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
		if pat&(1<<uint(i&15)) != 0 && c.penPhaseLit(x0, y0) {
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

// penPhaseLit reports whether the pen colour register CL1 (WPR 0x01) lights
// the pixel at firmware (x, y). THE PHASE-MULTIPLEX DECODE (2026-07-24): the
// HD63484 draws a pixel with the colour register's bit at the pixel's bit
// position within its frame-buffer word. The 8593 firmware NEVER draws solid —
// CL1 is always 0x5555 or 0xAAAA — so the 1bpp screen is phase-multiplexed by
// pixel parity into two logical planes: the trace is drawn at one phase
// (CL1=0xAAAA -> odd positions) and the per-column flying-erase-bar
// `SCLR AND 0x5555` clears EXACTLY that phase. Draw and erase are
// phase-locked; a solid draw (our former model) put trace pixels in BOTH
// phases and the even half could never be erased -> the trace pile-up.
// CL1==0 (never programmed — raw unit-test draws) falls back to solid.
func (c *Chip) penPhaseLit(x, y int) bool {
	cl := c.regs[0x01]
	if cl == 0 {
		return true
	}
	_, bp := c.core.calcOffset(int16(x), int16(y))
	return cl>>(uint(bp)&15)&1 != 0
}

// drawLineRouted draws a phase-gated line into the core frame buffer and
// triggers the frame snapshot.
func (c *Chip) drawLineRouted(x0, y0, x1, y1 int) {
	c.drawLine(x0, y0, x1, y1, true)
	c.maybeFrameSnapshot(x0, y0, x1, y1)
}

// maybeFrameSnapshot takes a stable frame snapshot when the firmware repaints the
// graticule — i.e. a LONG axis-aligned line inside the graph (a grid division or
// box edge). The firmware repaints the whole screen each cycle as
// clear → trace → graticule-grid; by the time the grid lines are drawn the trace
// is already in place, so the LAST grid line per frame leaves the buffer holding a
// COMPLETE frame. Snapshotting there (rather than at the clear, which under
// clean-clear leaves a momentarily-blank graph) gives the scanout a flicker-free,
// fully-painted image. Cheap: ~16 grid/box lines per frame, one buffer copy each.
func (c *Chip) maybeFrameSnapshot(x0, y0, x1, y1 int) {
	dx, dy := x1-x0, y1-y0
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	axisAligned := dx == 0 || dy == 0
	long := dx >= 80 || dy >= 80
	inGraph := x0 >= 0 && x0 <= 400 && y0 >= 0 && y0 <= 209
	if axisAligned && long && inGraph {
		c.core.snapshot()
	}
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

// fillVRAM replicates a 16-bit fill word across the entire frame buffer
// (the faithful core). Used by SCLR (screen clear) when the firmware passes a
// non-zero fill word, and internally to reset the chip on construction.
func (c *Chip) fillVRAM(word uint16) {
	for i := range c.core.ram {
		c.core.ram[i] = word
	}
}

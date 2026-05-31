package hd63484

import (
	"image"
	"image/color"
)

// Display geometry. The HD63484's PAINT/VRAM area (per the firmware's
// 0x003F=63 / 0x00FF=255 parameter words) covers a 1024×512 bit region.
//
// The DISPLAYED raster — the window of VRAM the ACRTC scans to the CRT — is
// 512×256, set by the boot display-init table at ROM 0xA95E: MWR1=0x40 (64
// words/line) and VWW=0x0100 (256 displayed lines), with ZFR=0 (×1 zoom, no
// magnification) and a single base layer (DCR=0xC000). Empirically the firmware
// draws the graticule as a 400×200 box near (0,0) plus an off-screen back frame
// at Y[240..440]; with only 256 displayed lines that back frame is below the
// visible area, which is why it must NOT be rendered. See
// docs/CRT_GEOMETRY_DIAGNOSIS.md.
//
// VisibleWidth/VisibleHeight are that displayed raster (sampled from VRAM).
// DisplayWidth/DisplayHeight are the OUTPUT framebuffer: the analog CRT stretches
// the 512×256 raster onto a 4:3 tube, so displayed pixels are ~1.5× taller than
// wide. We bake that into a 512×384 (4:3) output by upscaling the 256 visible
// lines ×1.5 vertically (horizontal is 1:1).
const (
	VisibleWidth  = 512
	VisibleHeight = 256

	DisplayWidth  = 512
	DisplayHeight = 384

	// PaintRowPixels / PaintHeight describe the chip's logical 1bpp paint
	// area. 1024 pixels per row × 512 rows = 524,288 bits = 65,536 bytes —
	// exactly the chip's 64 KB external VRAM (U305/U306 = 2× 256-Kbit SRAM
	// per CLIP 5963-2591). Bit packing matches the firmware's raster
	// bursts: each 16-bit word covers 16 horizontal pixels stored little-
	// endian within the word, and within each byte bit 0 is the LEFTMOST
	// pixel of its 8-pixel run.
	PaintRowPixels = 1024
	PaintHeight    = 512
	PaintRowBytes  = PaintRowPixels / 8 // 128 — bytes per scanline

	// VRAMSize is the external video RAM size (64 KB).
	VRAMSize = PaintRowBytes * PaintHeight // 65,536

	// PatternRAMWords is the chip's internal pattern RAM (16 patterns × 16
	// lines = 256 words). Used by the WPTN/RPTN commands and by area/fill
	// operations that tile a pattern.
	PatternRAMWords = 256
)

// fgColor and bgPaintColor define the two intensities the chip renders at.
// The real ACRTC drives the CRT beam with continuous brightness control via
// a colour-attribute register, but the 8593 uses a 1-bit monochrome amber
// CRT, so we collapse to a single "lit" colour. The legacy bgPaintColor
// (dim amber) is preserved for backwards compatibility with any caller that
// referenced it before the VRAM unification, but the unified model uses
// fgColor for all lit bits.
var (
	fgColor      = color.RGBA{R: 0xFF, G: 0xB0, B: 0x00, A: 0xFF}
	bgPaintColor = color.RGBA{R: 0x40, G: 0x2C, B: 0x00, A: 0xFF}
)

// Status-byte bit layout (read from address+1 / offset 0x5FD in the 8593 MMIO).
const (
	StatusCED = 1 << 7 // command-execution-done
	StatusLPD = 1 << 6 // light-pen detect
	StatusARD = 1 << 5 // area-ready
	StatusCER = 1 << 4 // command error
	StatusARR = 1 << 3 // address-read ready
	StatusWFR = 1 << 2 // write-FIFO ready (room for more)
	StatusRFR = 1 << 1 // read-FIFO ready (data available)
	StatusWFE = 1 << 0 // write-FIFO empty
)

// DefaultStatus is the static value our model returns from the status
// register: command-execution-done, area-ready, write-FIFO ready, and
// write-FIFO empty all asserted. That covers every polling pattern the
// firmware uses (bit 5 polled at PC 0xD700, bit 1 at 0xD6C4, bit 2 at
// 0xD70E in the Rev L firmware).
const DefaultStatus = StatusCED | StatusARD | StatusWFR | StatusWFE

// Chip models the HD63484 ACRTC. The chip's external video RAM is the
// single source of truth — every drawing command (lines, dots, rectangles,
// glyph blits, raster bursts) writes 1bpp pixels into vram, and RenderFrame
// materialises an RGBA image from vram on demand. This matches the real
// hardware's architecture (the CRT scans VRAM continuously to generate
// video) and means commands that erase regions — SCLR, CLR, glyph BG fill —
// naturally undo prior drawing without any per-surface tricks.
type Chip struct {
	// Address register (the last value written to the CMD port). Commands
	// dispatched via the parser below; this field is retained for status /
	// debugging.
	addrReg uint16

	// Parameter register file (32 × 16-bit). See registers.go for slot
	// meanings.
	regs [32]uint16

	// Pattern RAM — 256 words, addressable as 16 patterns × 16 lines or as
	// a flat array depending on WPTN parameter setup.
	pattern [PatternRAMWords]uint16

	// External video RAM, 64 KB, treated as a packed 1bpp framebuffer of
	// PaintRowPixels × PaintHeight. See setVRAMPixel / clearVRAMPixel for
	// the bit-addressing convention. This is the FOREGROUND plane: vector
	// draws (graticule, trace), glyph blits, and dots all land here.
	vram [VRAMSize]byte

	// bgVram is the BACKGROUND plane: the firmware paints a faint full-screen
	// dot texture via bulk raster bursts (the 0x4400 fill). Routing it to a
	// separate plane lets RenderFrame draw it at a dim intensity (bgPaintColor)
	// UNDER the bright foreground, instead of letting the uniform 0x4400 word
	// swamp the screen with full-brightness vertical stripes. Same bit-packing
	// and geometry as vram.
	bgVram [VRAMSize]byte

	// Memory-access pointer for raster bursts (advances after each data-
	// port write in raster mode). BYTE offset into vram.
	memPos int

	// dmem is the chip's display memory as seen by the block READ/VERIFY
	// path (word-addressed). The real HD63484 has a single video memory;
	// our renderer consumes a 1bpp view of it (vram / bgVram), while the
	// block memory-test commands the firmware issues during POST operate on
	// raw 16-bit words. The power-up self-test at ROM 0xD6B2 fills a region
	// with a test pattern via the block-fill command (0x5800), rewinds the
	// read pointer (the canonical MAR pair 0x4000/0x0000), then issues 0x4000
	// RD commands reading the words back and XOR-comparing each against the
	// written pattern — any mismatch fails f612 bit 7 (the 16th POST bit).
	// Modelling this faithfully (write pattern → memory → read it back) lets
	// that test pass. Exact 2D RWP/memory-width addressing is NOT modelled —
	// no firmware path other than this self-test reads display memory back,
	// and the fill is a single uniform pattern, so a linear mirror reproduces
	// the observable data path exactly.
	dmem    [VRAMSize / 2]uint16 // 32768 words = the 64 KB VRAM viewed as words
	readPtr int                  // auto-incrementing word read pointer
	fillLen int                  // words populated by the last block fill

	// Pen / drawing state.
	penX, penY int    // current pen position (pixels)
	colorReg   uint16 // current foreground colour selector

	// Last-set Memory Address Register pair (parameter regs 0x0C / 0x0D).
	// Captured whenever the firmware writes BOTH registers in sequence;
	// subsequent raster-burst or area commands start from this address.
	marLow, marHigh uint16

	// Glyph-blit colour state (captured between the WPTN header and the
	// bitmap rows). FG is applied to bits set in the row; BG is applied to
	// bits clear in the row (per HD63484 fill semantics). 0 means "do not
	// touch this class of pixel", non-zero means "set this pixel lit".
	glyphFG uint16
	glyphBG uint16

	// Command-decoder state (parser.go is the state machine).
	dec decoder

	// Output framebuffer materialised by RenderFrame from vram. Allocated
	// lazily; callers that don't render don't pay the 1.2 MB cost.
	img *image.RGBA

	// Drawn-content bounds (for cropped rendering / test inspection).
	minX, minY, maxX, maxY int

	// Optional glyph logger. If non-nil, blitGlyph hands each captured
	// glyph row-tuple here for printable-character extraction; see
	// glyph_logger.go. Constructed by New() when the
	// HD63484_GLYPHLOG environment variable is set to a writable file path.
	glyphLog *GlyphLogger

	// Diagnostics (exported so tests + cmd/* probes can introspect).
	DataWords      int            // total words fed to WriteData
	Moves          int            // MOVE markers seen
	Lines          int            // LINE commands drawn (absolute + relative)
	Rects          int            // ARCT/RRCT outlines drawn
	FilledRects    int            // AFRCT/RFRCT filled rects drawn
	Dots           int            // DOT commands drawn
	Glyphs         int            // glyph (WPTN+count-of-10) packets blitted
	Paints         int            // raster-write bursts entered
	PaintWords     int            // total pixel-data words written into vram
	ScreenClears   int            // SCLR commands executed
	AreaClears     int            // CLR commands executed
	UnknownCmds    int            // commands the parser saw but doesn't model
	UnknownCmdHist map[uint16]int // histogram of unknown opcodes for RE

	// LineLog, when non-nil, records the endpoints of every drawLine call.
	// Trace-draw probes use it to distinguish the regular graticule grid
	// (long axis-aligned segments at fixed pitch) from an irregular data
	// trace (a dense run of short segments tracking sample values). Enable
	// with EnableLineLog(); drawLine appends while it is non-nil.
	LineLog []LineRec

	// DotLog, when non-nil, records the pen position of every DOT command —
	// used to locate a dot-drawn spectrum trace. Enable with EnableDotLog().
	DotLog []DotRec

	// OrgLog records the (X,Y) of every ORG (set-drawing-origin) command. The
	// renderer does NOT yet apply ORG to drawing coordinates; this capture is
	// for the CRT-geometry diagnostic (cmd/crtdiag) to show what origin the
	// firmware expects the chip to add to subsequent draw coordinates.
	OrgLog [][2]int
}

// LineRec is one captured line segment (see Chip.LineLog).
type LineRec struct{ X0, Y0, X1, Y1 int }

// EnableLineLog turns on per-line endpoint capture into Chip.LineLog.
func (c *Chip) EnableLineLog() { c.LineLog = make([]LineRec, 0, 4096) }

// DotRec is one captured DOT pen position (see Chip.DotLog).
type DotRec struct{ X, Y int }

// EnableDotLog turns on per-dot position capture into Chip.DotLog.
func (c *Chip) EnableDotLog() { c.DotLog = make([]DotRec, 0, 4096) }

// New constructs a chip with a cleared VRAM + zeroed state. If the
// HD63484_GLYPHLOG environment variable is set, a GlyphLogger is attached
// (see glyph_logger.go).
func New() *Chip {
	c := &Chip{
		UnknownCmdHist: make(map[uint16]int),
	}
	c.resetBounds()
	c.glyphLog = newGlyphLoggerFromEnv()
	return c
}

func (c *Chip) resetBounds() {
	c.minX, c.minY = VisibleWidth, VisibleHeight
	c.maxX, c.maxY = 0, 0
}

// vramByteAddr returns the byte offset within vram that holds pixel (x, y),
// or -1 if the pixel is outside the paint area. The bit within that byte
// (with bit 0 = leftmost) is (x & 7).
func (c *Chip) vramByteAddr(x, y int) int {
	if x < 0 || y < 0 || x >= PaintRowPixels || y >= PaintHeight {
		return -1
	}
	return y*PaintRowBytes + (x >> 3)
}

// setVRAMPixel lights the pixel at (x, y) and updates the drawn-content
// bounding box. Out-of-range coordinates are silently ignored — matches
// the chip's hardware clipping behaviour.
func (c *Chip) setVRAMPixel(x, y int) {
	addr := c.vramByteAddr(x, y)
	if addr < 0 {
		return
	}
	c.vram[addr] |= 1 << uint(x&7)
	c.expandBounds(x, y)
}

// clearVRAMPixel turns off the pixel at (x, y) — used by glyph BG fills,
// CLR, and SCLR.
func (c *Chip) clearVRAMPixel(x, y int) {
	addr := c.vramByteAddr(x, y)
	if addr < 0 {
		return
	}
	c.vram[addr] &^= 1 << uint(x&7)
}

// isVRAMPixelLit reports whether the pixel at (x, y) is currently set.
// Returns false for out-of-range coordinates.
func (c *Chip) isVRAMPixelLit(x, y int) bool {
	addr := c.vramByteAddr(x, y)
	if addr < 0 {
		return false
	}
	return c.vram[addr]&(1<<uint(x&7)) != 0
}

// expandBounds widens the drawn-content bbox to include (x, y). Visible-
// region clamp so the bbox stays useful for RenderCropped — bounds are in
// VRAM/drawing space (the 512×256 visible window).
func (c *Chip) expandBounds(x, y int) {
	if x < 0 || y < 0 || x >= VisibleWidth || y >= VisibleHeight {
		return
	}
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

// WriteCmd handles a write to the chip's address register (offset 0 in its
// register file; 0x5FC in the 8593's MMIO). In the typical "select-register"
// access pattern the CPU writes a parameter-register number here, then
// reads/writes the data port. In FIFO/command mode the CPU writes commands
// directly into the data port and this register is used only for status
// inquiries — which matches what the 8593 firmware does (~2K writes, mostly
// 0x0000 to clear status / a few non-zero values for control commands).
func (c *Chip) WriteCmd(val uint16) {
	c.addrReg = val
}

// WriteData feeds one 16-bit word into the chip's command/data FIFO. Most
// of the chip's state machine lives here — see parser.go.
func (c *Chip) WriteData(w uint16) {
	c.DataWords++
	c.dec.feed(c, w)
}

// ReadStatus returns the chip status byte. For polling-loop fidelity we
// keep every "ready" bit asserted permanently — the chip in our model
// completes every command instantly, so it's always "ready for more".
func (c *Chip) ReadStatus() uint8 { return DefaultStatus }

// blockFill populates the chip's display memory (dmem) with `count` copies of
// `pattern`, starting at word 0, and rewinds the read pointer to 0. This is the
// write half of the firmware's display-RAM self-test (the 0x5800 command at ROM
// 0xD6B2): a uniform pattern written across the region, subsequently read back
// word-by-word and XOR-verified. count is clamped to the memory size.
func (c *Chip) blockFill(pattern uint16, count int) {
	if count < 0 {
		count = 0
	}
	if count > len(c.dmem) {
		count = len(c.dmem)
	}
	for i := 0; i < count; i++ {
		c.dmem[i] = pattern
	}
	c.fillLen = count
	c.readPtr = 0
}

// ReadData returns the next word from the block READ path — the word at the
// current read pointer of the chip's display memory (dmem), post-incrementing
// the pointer (the HD63484 RWP auto-increments on each read-back). This is the
// data the POST self-test at ROM 0xD6B2 reads after each RD command to verify
// the pattern it filled. The read pointer is rewound to the start of the
// filled region whenever the firmware writes the canonical read MAR pair
// (0x4000/0x0000) — see handleWPRSideEffect. Before any block fill (fillLen==0)
// this returns 0, matching the prior stub for every other (non-readback) path.
func (c *Chip) ReadData() uint16 {
	if c.fillLen == 0 {
		return 0
	}
	w := c.dmem[c.readPtr%c.fillLen]
	c.readPtr++
	return w
}

// Counters / accessors used by tests + cmd/* probes.

func (c *Chip) PenX() int { return c.penX }
func (c *Chip) PenY() int { return c.penY }

// Reg returns parameter register n (0..31), or 0 if out of range. Read-only —
// for diagnostics that inspect the chip's display-control / partition setup.
func (c *Chip) Reg(n int) uint16 {
	if n < 0 || n >= len(c.regs) {
		return 0
	}
	return c.regs[n]
}

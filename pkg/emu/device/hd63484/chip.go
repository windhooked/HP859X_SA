package hd63484

import (
	"image"
	"image/color"
)

// Display geometry. The HP 8593's CRT raster is 640×480; the ACRTC's PAINT
// area (per the firmware's 0x003F=63 / 0x00FF=255 parameter words) covers a
// 1024×N region of which we display the visible left 640. Sized generously
// so the chip never needs to grow its buffers mid-frame.
const (
	DisplayWidth  = 640
	DisplayHeight = 480

	// VRAMSize is the external video RAM size (64 KB on the 8593, U305/U306
	// = 2× 256-Kbit static RAM per CLIP 5963-2591).
	VRAMSize = 64 * 1024

	// PatternRAMWords is the chip's internal pattern RAM (16 patterns × 16
	// lines = 256 words). Used by the WPTN/RPTN commands and by area/fill
	// operations that tile a pattern.
	PatternRAMWords = 256
)

// fgColor and bgPaintColor define the two intensities the chip renders at.
// The real ACRTC drives the CRT beam with continuous brightness control via
// a colour-attribute register, but the 8593 uses a 1-bit monochrome amber
// CRT, so we map "lit foreground" (glyphs, lines, dots, trace) to bright
// amber and "lit background" (PAINT/raster fills) to dim amber. This gives
// the same perceptual hierarchy a real instrument has — bright drawing on
// top of a faint dot-pattern background.
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

// Chip models the HD63484 ACRTC. All state — register file, FIFOs, pattern
// RAM, video RAM, framebuffer — is internal; the host interacts only via
// the MMIO methods (WriteCmd / WriteData / ReadStatus / ReadData) and the
// RenderFrame helper for headless inspection.
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

	// External video RAM, 64 KB. Bytes are little-endian within 16-bit
	// pixel words to match the firmware's word writes (low byte = leftmost
	// pixels of the 16-pixel run).
	vram [VRAMSize]byte

	// Memory-access pointer for raster bursts (advances after each data-port
	// write in raster mode). Word offset into vram.
	memPos int

	// Pen / drawing state.
	penX, penY int    // current pen position (pixels)
	colorReg   uint16 // current foreground colour selector

	// Command-decoder state (parser.go is the state machine).
	dec decoder

	// Composite framebuffer the chip "drives" to the CRT. Built lazily by
	// RenderFrame from the SCI-vector overlay (lines/dots/glyphs paint
	// directly here) plus the vram (composited in RenderFrame).
	img *image.RGBA

	// Drawn-content bounds (for cropped rendering / test inspection).
	minX, minY, maxX, maxY int

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
	UnknownCmds    int            // commands the parser saw but doesn't model
	UnknownCmdHist map[uint16]int // histogram of unknown opcodes for RE
}

// New constructs a chip with a cleared framebuffer + zeroed state.
func New() *Chip {
	c := &Chip{
		img:            image.NewRGBA(image.Rect(0, 0, DisplayWidth, DisplayHeight)),
		UnknownCmdHist: make(map[uint16]int),
	}
	// Opaque black background (image.NewRGBA gives transparent black).
	for i := 3; i < len(c.img.Pix); i += 4 {
		c.img.Pix[i] = 0xFF
	}
	c.resetBounds()
	return c
}

func (c *Chip) resetBounds() {
	c.minX, c.minY = DisplayWidth, DisplayHeight
	c.maxX, c.maxY = 0, 0
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

// ReadData would return a word from the read-FIFO (e.g. after an RPR or
// RPTN command). Stubbed to 0 for now; the 8593 firmware never reads the
// data port at runtime, so this is exercised only by future tests.
func (c *Chip) ReadData() uint16 { return 0 }

// Counters / accessors used by tests + cmd/* probes. Exposing these here
// keeps the diagnostic surface stable as internal decoder state evolves.

func (c *Chip) Image() *image.RGBA { return c.img }
func (c *Chip) PenX() int          { return c.penX }
func (c *Chip) PenY() int          { return c.penY }

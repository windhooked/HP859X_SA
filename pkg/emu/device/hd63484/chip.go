package hd63484

import (
	"image"
	"image/color"
	"os"
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

	// DisplayWidth is wider than VisibleWidth to account for the CRT overscan
	// region. HWR (AR 0x92 = 0x043F) gives H-active = (0x3F+1)*8 = 512px and
	// H-blank = 0x04*8 = 32px. The display scans from VRAM col 0 (orgCol=48 puts
	// the box left edge at col 48, left-margin labels at col 8+, and the M of
	// SPECTRUM/ANALYZER at col 512–527 which the wider image captures). The CRT
	// physically illuminates H-active + H-blank columns; our framebuffer matches.
	DisplayWidth  = 512 + 32 // = 544: HWR H-active + H-blank
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
// CRT, so we collapse to a single "lit" colour (fgColor).
var (
	fgColor = color.RGBA{R: 0xFF, G: 0xB0, B: 0x00, A: 0xFF}

	// gridDimColor is the faint recessive amber RenderCore uses for its dotted
	// rendering of the +0x4000 grid page — dim enough to read as the real CRT's
	// faint dotted grid, not hard bright stripes.
	gridDimColor = color.RGBA{R: 0x3A, G: 0x28, B: 0x00, A: 0xFF}
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
	// Address register (the last value written to the CMD/AR port, 0xFFF5FC).
	// In the HD63484 Address-Register protocol it selects which control register
	// the data port (0xFFF5FE) writes to: AR==0 → command-FIFO (drawing
	// commands, dispatched by the parser below); AR addressing a control register
	// → that register's value (auto-incrementing AR by 2 per word). We decode the
	// display-address registers (AR 0xC8..0xCF) so the display-start can be
	// modelled; other control regs still flow to the parser (harmless — they only
	// appear during init).
	addrReg uint16

	// orgCol / orgRow store the drawing-coordinate origin as set by the ORG command
	// (opcode 0x0400, params XW and XD). Every firmware drawing coordinate is
	// relative to this origin:
	//   VRAM_col = orgCol + fwX
	//   VRAM_row = orgRow - fwY  (Y-up→Y-down flip)
	// Initialised to defaultOrgCol/defaultOrgRow; updated by the ORG handler.
	orgCol int
	orgRow int

	// Display-address registers (the AR-protocol "base screen" set programmed by
	// the firmware's display-init at ROM 0xA95E). RAR1 (AR 0xC8) is the base
	// raster/read start address; MWR1 (AR 0xCA) the memory width (words/line);
	// SAR1 (AR 0xCC:0xCE) the base start address. RenderFrame scans VRAM from
	// displayStartRow() so that if the firmware ever PAGE-FLIPS (re-points RAR1 at
	// the back-buffer) the displayed buffer follows. In the boot we render these
	// are static (RAR1=0 → row 0 → the front buffer), so the render is unchanged.
	dispRAR   uint16 // AR 0xC8 — base raster/read address (display start)
	dispMWR   uint16 // AR 0xCA — base memory width (words per raster line)
	dispSARHi uint16 // AR 0xCC — base start address, high word
	dispSARLo uint16 // AR 0xCE — base start address, low word

	// HD63484 control-register file (ported from MAME video_registers_w). The
	// display is split into up to four vertical layers, each scanned from its own
	// Start Address Register sar[layer] (20-bit word address) at mwr[layer] words
	// per line — draw_graphics_line: srcWord = sar[layer] + line*mwr[layer]. The
	// firmware PAGE-FLIPS / switches modes by re-pointing SAR; that is the
	// mechanism the cal-data display (CAL DISP;) uses to replace the spectrum
	// screen. omr/dcr are the operation-mode and display-control registers; sp[]
	// are the split-screen heights.
	sar [4]uint32 // Start Address Registers (banks via AR 0xC4/0xCC/0xD4/0xDC + 0xC6/.../0xDE)
	mwr [4]uint16 // Memory Width Registers (pitch, words/line) — AR 0xC2/0xCA/0xD2/0xDA
	omr uint16    // Operation Mode Register — AR 0x04
	dcr uint16    // Display Control Register — AR 0x06
	sp  [3]uint16 // Split-screen widths SP0/SP1/SP2 — AR 0x8C/0x8A/0x8E

	// core is the faithful flat-address ACRTC model (acrtc.go) being migrated in
	// per docs/HD63484_REBUILD_PROMPT.md. Phase 2 DUAL-WRITES pen pixels into it
	// (alongside the legacy vram path) so it accumulates the same content in the
	// real address space; Phase 4 switches rendering onto it and deletes the
	// legacy planes + coordinate hacks.
	core acrtc

	// RegionWrites counts pen pixels landing in each of the 5 display regions
	// (indexed by the region* consts) — for asserting writes hit the right region.
	RegionWrites [numRegions]int

	// regwatch.go: env-gated surfacing of control registers we don't model.
	regDump  *os.File
	regPanic bool

	// APLLUpper (experiment): when true, APLL polyline draws are redirected into
	// the upper memory region (+0x4000 words) instead of the Base screen.
	APLLUpper bool

	// GraticuleToUpper (experiment): when true, ALL access (pen draws AND area-op
	// clears) targeting the graticule/center region is redirected into the upper
	// memory region — so the graph's draws and clears stay together there.
	GraticuleToUpper bool

	// areaClip is set by dispatchCmd when the current drawing command has the AREA
	// bit (opcode bit 6) set — the chip then clips the draw to the ADR (area-def
	// rect, regs 0x08-0x0b). Without honouring it the trace/box leak past the graph.
	areaClip bool

	// DisableAreaClip (debug toggle) bypasses the AREA-mode clip in setVRAMPixel.
	// Used to test whether the AREA clip is chopping content that lives outside the
	// current ADR rect (e.g. the top-left hp logo). Default false.
	DisableAreaClip bool

	// CleanClear (option B) reinterprets the firmware's AND-dither area clear
	// (SCLR, cr&3==2) as a CLEAN ERASE of the masked bits, instead of the faithful
	// `data AND pattern`. The faithful AND is idempotent (X AND 0x5555 == X AND
	// 0x5555 …) so old content — the previous sweep's trace and boot-time glyphs —
	// is only thinned to a 50% dither and survives as a permanent ghost (it would
	// read as dim on a real CRT but renders full-bright here). CleanClear honours
	// the firmware's *intent* (the SCLR clears the graph each sweep) and removes the
	// residue. The graticule grid must therefore come from the redraw, not the
	// dither — verify when enabling. Default false (faithful AND).
	CleanClear bool

	// ctrlRegs is the full AR-addressed control-register file. Control-register
	// writes (AR≠0) are decoded here rather than mis-fed to the command parser.
	// Registers whose side-effects we faithfully model have explicit handlers in
	// writeControlReg; the rest are store-only and must be on the strict
	// allowlist (see strict.go) — anything else panics.
	ctrlRegs [256]uint16

	// Parameter register file (32 × 16-bit). See registers.go for slot
	// meanings.
	regs [32]uint16

	// Pattern RAM — 256 words, addressable as 16 patterns × 16 lines or as
	// a flat array depending on WPTN parameter setup.
	pattern [PatternRAMWords]uint16

	// Memory-access pointer for raster bursts (advances after each data-
	// port write in raster mode). BYTE offset into the frame buffer address
	// space (the faithful core; see wptn.go raster path).
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

	// linePattern is the 16-bit line stipple the firmware programs via WPTN
	// (pattern RAM) before drawing vector lines. The 8593 firmware draws the
	// graticule with PER-LINE patterns: 0xFFFF solid (frame/major), 0xCCCC dash,
	// 0x1111 dotted (minor divisions), 0x0001 sparse. drawLine walks this pattern
	// along the line and skips pixels whose bit is 0, so the internal grid
	// renders dotted/dashed as on the real CRT (a 0 pattern is treated as solid).
	linePattern uint16

	// Last-set Memory Address Register pair (parameter regs 0x0C / 0x0D).
	// Captured whenever the firmware writes BOTH registers in sequence;
	// subsequent raster-burst or area commands start from this address.
	marLow, marHigh uint16

	// HD63484 Read/Write Pointer engine (the chip's memory-address logic for the
	// word-granular area ops CLR/SCLR, distinct from the pen used by line/rect
	// drawing). Per MAME hd63484.cpp: rwp[4] are 20-bit word pointers selected by
	// rwpDn; WPR 0x0C sets the bank + high byte, WPR 0x0D the low 12 bits. maskReg
	// (WPR 0x04) gates which bits a logical-op area write affects. These drive the
	// real SCLR (0x5C00) the cal-data display (CAL DISP;) uses to repaint the
	// screen — see area_ops.go.
	rwp     [4]uint32
	rwpDn   int
	maskReg uint16

	// readQ is the data-port read queue fed by the RD command (0x4400): each RD
	// reads the core word at RWP and queues it here; ReadData serves the queue
	// ahead of the legacy dmem block-verify path. See parser.go idRD.
	readQ []uint16

	// Glyph-blit colour state (captured between the WPTN header and the
	// bitmap rows). FG is applied to bits set in the row; BG is applied to
	// bits clear in the row (per HD63484 fill semantics). 0 means "do not
	// touch this class of pixel", non-zero means "set this pixel lit".
	glyphFG uint16
	glyphBG uint16

	// Pending glyph: the HD63484 renders text in TWO commands — WPTN loads the
	// glyph bitmap into pattern RAM, then PTN (0xD000) blits it at the pen sized by
	// SZ. So WPTN stashes the rows here and the PTN handler (drawPattern) does the
	// actual blit. pendGlyph gates whether a glyph is staged. See wptn.go.
	pendRows  [glyphRows]uint16
	pendGlyph bool

	// Command-decoder state (parser.go is the state machine).
	dec decoder

	// Output framebuffer materialised by RenderFrame from vram. Allocated
	// lazily; callers that don't render don't pay the 1.2 MB cost.
	img *image.RGBA

	// Drawn-content bounds (for cropped rendering / test inspection).

	// Optional glyph logger. If non-nil, blitGlyph hands each captured
	// glyph row-tuple here for printable-character extraction; see
	// glyph_logger.go. Constructed by New() when the
	// HD63484_GLYPHLOG environment variable is set to a writable file path.
	glyphLog *GlyphLogger

	// Diagnostics (exported so tests + cmd/* probes can introspect).
	DataWords      int      // total words fed to WriteData
	Moves          int      // MOVE markers seen
	Lines          int      // LINE commands drawn (absolute + relative)
	Rects          int      // ARCT/RRCT outlines drawn
	FilledRects    int      // AFRCT/RFRCT filled rects drawn
	Dots           int      // DOT commands drawn
	Glyphs         int      // glyph (WPTN+count-of-10) packets blitted
	Paints         int      // raster-write bursts entered
	PaintWords     int      // total pixel-data words written into vram
	ScreenClears   int      // SCLR commands executed
	AreaClears     int      // CLR commands executed
	SCLRNoAreaDef  int      // logical SCLRs skipped for lack of an area-def (no-op branch)
	SCLRNoAreaLast [4]int   // last skipped SCLR: rwp, ax, ay, pattern
	SCLRSkipLog    [][4]int // all skipped no-area-def SCLRs when ClearColLogOn
	// AAAARectLog, when set, records every executed clip-branch SCLR with
	// pattern 0xAAAA as (rwp, ax, ay) — the text-cell erase coverage probe.
	AAAARectLog *[][3]int
	// ClearColLog (diagnostic, enabled by ClearColLogOn): per faithful-SCLR
	// column op, records [rwp word, mask, pattern] to establish erase coverage.
	ClearColLogOn bool
	ClearColLog   [][3]uint32
	// APLLColorHist counts polyline draws per current CL1 (WPR 0x01) value —
	// the draw-phase side of the phase-multiplex choreography.
	APLLColorHist  map[uint16]int
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

	// cmdRing is a fixed-size circular log of the last cmdRingCap command-FIFO
	// words fed to the parser (AR==0 path). Enabled with EnableCmdTrace(n); used
	// to capture the live HD63484 draw stream around an event (e.g. the CAL DISP;
	// cal-display render) for offline decoding without the full-frame firehose.
	cmdRing    []uint16
	cmdRingPos int
	cmdRingHas bool // ring has wrapped (all cmdRingCap slots valid)

	// cmdCap is a LINEAR capture armed on demand (StartCmdCapture): it records
	// every command word from the arm point until full. Use it to grab a one-shot
	// transition (e.g. the cal-display mode switch and its foreground clear) that
	// the steady-state ring would overwrite. Separate from cmdRing.
	cmdCap   []uint16
	cmdCapOn bool

	// ctrlCap records AR-addressed control-register writes (the addrReg≠0 path:
	// RAR/MWR/SAR display-window registers etc.) during an armed capture, as
	// (AR, value) pairs. The cal-data display may switch the displayed VRAM
	// window via these, which the command-FIFO capture (cmdCap) never sees.
	ctrlCap [][2]uint16
}

// LineRec is one captured line segment (see Chip.LineLog).
type LineRec struct{ X0, Y0, X1, Y1 int }

// EnableLineLog turns on per-line endpoint capture into Chip.LineLog.
func (c *Chip) EnableLineLog() { c.LineLog = make([]LineRec, 0, 4096) }

// DotRec is one captured DOT pen position (see Chip.DotLog).
type DotRec struct{ X, Y int }

// EnableDotLog turns on per-dot position capture into Chip.DotLog.
func (c *Chip) EnableDotLog() { c.DotLog = make([]DotRec, 0, 4096) }

// EnableCmdTrace arms the command-FIFO ring buffer to keep the last n words
// written to the chip's command stream (AR==0 path). Call CmdTrace() to read
// them back in chronological order. n<=0 disables.
func (c *Chip) EnableCmdTrace(n int) {
	if n <= 0 {
		c.cmdRing = nil
		return
	}
	c.cmdRing = make([]uint16, n)
	c.cmdRingPos = 0
	c.cmdRingHas = false
}

// recordCmd appends one command-FIFO word to the ring (called from WriteData).
func (c *Chip) recordCmd(w uint16) {
	if c.cmdCapOn && len(c.cmdCap) < cap(c.cmdCap) {
		c.cmdCap = append(c.cmdCap, w)
	}
	if c.cmdRing == nil {
		return
	}
	c.cmdRing[c.cmdRingPos] = w
	c.cmdRingPos++
	if c.cmdRingPos >= len(c.cmdRing) {
		c.cmdRingPos = 0
		c.cmdRingHas = true
	}
}

// StartCmdCapture arms a fresh LINEAR capture of up to max command words from
// now until full — use it to record a one-shot transition (arm just before the
// event). Read it back with CmdCapture().
func (c *Chip) StartCmdCapture(max int) {
	c.cmdCap = make([]uint16, 0, max)
	c.ctrlCap = make([][2]uint16, 0, 4096)
	c.cmdCapOn = true
}

// CmdCapture returns the words recorded since the last StartCmdCapture (nil if
// never armed).
func (c *Chip) CmdCapture() []uint16 { return c.cmdCap }

// CtrlCapture returns the AR-addressed control-register writes (AR,value pairs)
// recorded since the last StartCmdCapture.
func (c *Chip) CtrlCapture() [][2]uint16 { return c.ctrlCap }

// CmdTrace returns the captured command-FIFO words in chronological order
// (oldest first). Empty if EnableCmdTrace was never called.
func (c *Chip) CmdTrace() []uint16 {
	if c.cmdRing == nil {
		return nil
	}
	if !c.cmdRingHas {
		return append([]uint16(nil), c.cmdRing[:c.cmdRingPos]...)
	}
	out := make([]uint16, 0, len(c.cmdRing))
	out = append(out, c.cmdRing[c.cmdRingPos:]...)
	out = append(out, c.cmdRing[:c.cmdRingPos]...)
	return out
}

// DispRAR returns the current RAR1 (display-start word address).
func (c *Chip) DispRAR() uint16 { return c.dispRAR }

// DispMWR returns the current MWR1 (words per raster line).
func (c *Chip) DispMWR() uint16 { return c.dispMWR }

// DisplayStartRow returns the VRAM row the display currently scans from (same
// as the internal displayStartRow helper — exposed for probing).
func (c *Chip) DisplayStartRow() int { return c.displayStartRow() }

// CoreORG returns the faithful core's decoded ORG (layer, dpa word address) and
// MWR1, for verifying the Phase-2 dual-write wiring (acrtc.go).
func (c *Chip) CoreORG() (layer int, dpa uint32, mwr1 uint16) {
	return c.core.orgDN, c.core.orgDPA, c.core.mwr[1]
}

// WatchCoreWord arms the write-history diagnostic on one core word offset;
// every write to it is appended to log as (old, new, cmd-tag).
func (c *Chip) WatchCoreWord(off uint32, log *[][3]uint16) {
	c.core.watchWord = off
	c.core.watchLog = log
}

// CoreTagBit returns the command tag of pixel bit (0..15) in core word off —
// for diagnostics that classify lit content (tagGlyph=7 etc.).
func (c *Chip) CoreTagBit(off uint32, bit int) uint8 {
	return c.core.cmdTag[(off&acrtcRAMMask)<<4|uint32(bit&15)]
}

// CoreWord returns the faithful core buffer word at the given word offset.
func (c *Chip) CoreWord(off uint32) uint16 { return c.core.readword(off) }

// CoreBit reports whether the core pixel at (coreRow, px) is set (for probes).
func (c *Chip) CoreBit(coreRow, px int) bool { return c.coreBit(coreRow, px) }

// CoreLitWords counts non-zero words in the faithful core buffer (drawn content
// accumulated via dual-write).
func (c *Chip) CoreLitWords() int {
	n := 0
	for _, w := range c.core.ram {
		if w != 0 {
			n++
		}
	}
	return n
}

// SAR returns Start Address Register n (20-bit word address), for probing the
// chip's display-area / split-screen layer bases.
func (c *Chip) SAR(n int) uint32 {
	if n < 0 || n >= len(c.sar) {
		return 0
	}
	return c.sar[n]
}

// New constructs a chip with a cleared VRAM + zeroed state. If the
// HD63484_GLYPHLOG environment variable is set, a GlyphLogger is attached
// (see glyph_logger.go).
func New() *Chip {
	c := &Chip{
		UnknownCmdHist: make(map[uint16]int),
		linePattern:    0xFFFF, // solid until the firmware programs a stipple
		orgCol:         defaultOrgCol,
		orgRow:         defaultOrgRow,
		maskReg:        0xFFFF, // all bits affected until WPR 0x04 narrows the mask
	}
	// Faithful SCLR by default: the chip's real AND-dither under the mask. This
	// is what keeps the stippled graticule grid visible between redraws (the
	// reference look: screens/trace_longrun.png); CleanClear=true (the former
	// default) erased the grid every sweep along with the ghosts. The dither's
	// side effects (50%-thinned text inside the clear region, old-content
	// residue) are what the real CRT bridges via continuous redraw + phosphor —
	// modelled by the GUI's beam integration.
	c.CleanClear = false
	// Faithful core defaults so the unified buffer addresses correctly before the
	// firmware programs ORG/MWR at runtime (drives unit tests that draw directly).
	// Matches the 8593's display-init: ORG 0x4003,0xa450 → dn=1, dpa=0x3a45; MWR1=64.
	c.core.setORG(0x4003, 0xa450)
	c.core.mwr[1] = 64
	c.core.mask = 0xFFFF // match maskReg: all bits affected until WPR 0x04 narrows it
	c.glyphLog = newGlyphLoggerFromEnv()
	c.initRegWatch()
	return c
}

// defaultOrgCol / defaultOrgRow are the chip's hard-coded ORG values before any
// ORG command is issued. They are set by the 8593 firmware's display-init routine
// (ROM 0xA95E) via the ORG command with XW=0x4003, XD=0xa450, MWR1=0x40=64:
//
//	ORG_col = (XW % MWR1) * 16 + (XD & 0xF) = (3)*16 + 0 = 48
//	ORG_row = XW / MWR1                       = 16387 / 64 = 256
//
// The ORG command sets the drawing-coordinate origin in display memory. All
// firmware drawing coordinates (AMOVE/ALINE/etc.) are relative to this origin:
//
//	VRAM_col = ORG_col + fwX
//	VRAM_row = ORG_row - fwY  (Y-up→Y-down flip)
//
// displayScanStart is the first VRAM row the display's visible window scans from.
// It derives from the HD63484's vertical window registers (VWR-A=5, MWR1=64) and
// the firmware's content span: with ORG_row=256 the firmware draws at fwY∈[-22,220],
// mapping to VRAM rows [36,278]. To fit ALL of that within the 256-row visible
// window the scan must start at or before row 36 and must end at or after row 278.
// The minimum valid range is [23,36], and we pick 23 to leave the maximum head-room
// at the top of the screen for date/time / HP-logo content the firmware may draw
// in future (or which the real instrument draws when the RTC and options are
// present). So:
//
//	displayScanStart = 23
//	effective drawYOrigin = ORG_row - displayScanStart = 256 - 23 = 233
//
// NOTE (2026-06-03): the firmware's RWP-addressed SCLR area-clear targets the
// EFFECTIVE position (≈ vram row 233 for the graph bottom), while the pen STORES
// content at ORG_row=256. So the SCLR's word→vram mapping adds displayScanStart
// rows to land on the stored content (see wordByteAddr in area_ops.go). This is
// localized to the SCLR so orgRow/render/background are untouched. Pinned by
// TestCRTSclrDithersGlyph.
const (
	defaultOrgCol    = 48  // ORG_col derived from XW=0x4003, MWR1=64, XD=0
	defaultOrgRow    = 256 // ORG_row derived from XW=0x4003, MWR1=64
	displayScanStart = 23  // first VRAM row the display scans (VWR-A geometry)
)

// setVRAMPixel lights the pixel at (x, y) and updates the drawn-content
// bounding box. Out-of-range coordinates are silently ignored — matches
// the chip's hardware clipping behaviour.
func (c *Chip) setVRAMPixel(x, y int) {
	// AREA mode (opcode bit 6): clip this draw to the ADR (area-def rect). The
	// firmware sets the ADR to the graph for trace/box draws, so honouring it
	// confines them to the graticule box instead of leaking above/below.
	if c.areaClip && !c.DisableAreaClip {
		xmin, ymin := int(int16(c.regs[0x08])), int(int16(c.regs[0x09]))
		xmax, ymax := int(int16(c.regs[0x0a])), int(int16(c.regs[0x0b]))
		if xmax > xmin && ymax > ymin && (x < xmin || x > xmax || y < ymin || y > ymax) {
			return
		}
	}
	// The pen draws in firmware logical coords, which is exactly what
	// calcOffset/setDot consume — the pixel goes straight into the unified core
	// frame buffer (the single source of truth the scanout reads).
	if c.GraticuleToUpper && regionOf(x, y) == regionCenter {
		c.core.drawOffset = 0x4000
	}
	c.core.setDot(int16(x), int16(y), uint16(1<<uint(c.core.getBpp()))-1)
	c.core.drawOffset = 0
	c.RegionWrites[regionOf(x, y)]++
}

// drawPenPixel draws one pixel with the pen colour (CL1's colour field at the
// pixel position), OR-ed into the existing pixel — the firmware's vector
// commands run OPM=OR (APLL 0x9841 / DOT 0xCC41), so e.g. the trace (colour
// 10) never destroys text/graticule bits (colour 01) sharing the word.
// Applies the AREA clip like setVRAMPixel.
func (c *Chip) drawPenPixel(x, y int) {
	if c.areaClip && !c.DisableAreaClip {
		xmin, ymin := int(int16(c.regs[0x08])), int(int16(c.regs[0x09]))
		xmax, ymax := int(int16(c.regs[0x0a])), int(int16(c.regs[0x0b]))
		if xmax > xmin && ymax > ymin && (x < xmin || x > xmax || y < ymin || y > ymax) {
			return
		}
	}
	col := c.penColor(x, y)
	if col == 0 {
		return
	}
	if c.GraticuleToUpper && regionOf(x, y) == regionCenter {
		c.core.drawOffset = 0x4000
	}
	if cur := c.core.getDot(int16(x), int16(y)); cur|col != cur {
		c.core.setDot(int16(x), int16(y), cur|col)
	} else if cur|col == cur && cur == 0 {
		// nothing to write
	} else {
		// value unchanged — still count the write for region stats? no-op.
		_ = cur
	}
	c.core.drawOffset = 0
	c.RegionWrites[regionOf(x, y)]++
}

// clearVRAMPixel turns off the pixel at (x, y) — used by glyph BG fills,
// CLR, and SCLR.
func (c *Chip) clearVRAMPixel(x, y int) {
	if c.GraticuleToUpper && regionOf(x, y) == regionCenter {
		c.core.drawOffset = 0x4000
	}
	c.core.setDot(int16(x), int16(y), 0)
	c.core.drawOffset = 0
}

// isVRAMPixelLit reports whether the pixel at (x, y) is currently set.
// Returns false for out-of-range coordinates.
func (c *Chip) isVRAMPixelLit(x, y int) bool {
	// Core-direct: read the unified buffer at the firmware logical coordinate.
	return c.core.getDot(int16(x), int16(y)) != 0
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

// WriteData feeds one 16-bit word into the chip via the data port (0x5FE).
// Per the HD63484 Address-Register protocol, when addrReg selects a display-
// address control register (AR 0xC8..0xCF) the word is that register's value —
// capture it (auto-incrementing AR per the chip) and do NOT feed the command
// parser. Any other AR (including 0 = command-FIFO) feeds the parser, so drawing
// is unchanged. See parser.go for the FIFO state machine.
func (c *Chip) WriteData(w uint16) {
	c.DataWords++
	if c.addrReg != 0 {
		// Control-register access (AR≠0): the data word is the addressed
		// register's value, NOT a command-FIFO word. Decode it (writeControlReg
		// faithfully handles or strictly gates each register) and auto-increment
		// the AR per the chip. AR==0 is the command-FIFO entry → the parser.
		c.writeControlReg(c.addrReg, w)
		c.addrReg += 2 // HD63484 AR auto-increment (per 16-bit word)
		return
	}
	c.recordCmd(w)
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
	// RD-command queue first (core words read at RWP — parser.go idRD).
	if len(c.readQ) > 0 {
		w := c.readQ[0]
		c.readQ = c.readQ[1:]
		return w
	}
	if c.fillLen == 0 {
		return 0
	}
	w := c.dmem[c.readPtr%c.fillLen]
	c.readPtr++
	return w
}

// advanceRWP advances the active Read/Write Pointer by n words (20-bit wrap,
// per MAME m_rwp handling), keeping the chip-level mirror and the faithful
// core in sync. Used by the RD/WT data-transfer commands.
func (c *Chip) advanceRWP(n uint32) {
	c.rwp[c.rwpDn] = (c.rwp[c.rwpDn] + n) & 0xfffff
	c.core.rwp[c.rwpDn] = c.rwp[c.rwpDn]
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

// OMR returns the Operation Mode Register (AR 0x04). GBM (bits 10-8) selects
// bits-per-pixel: 000=1bpp mono, 001=2bpp, 010=4bpp, etc.
func (c *Chip) OMR() uint16 { return c.omr }

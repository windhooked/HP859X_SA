package hd63484

// HD63484 command opcodes. Each command is a 16-bit word where the top
// nibble (sometimes top byte) selects the command family and the low bits
// carry mode flags. After the command word, the chip expects a fixed or
// variable number of parameter words via the data port.
//
// The set below covers every command observed in the 8593 Rev L firmware
// stream plus the families MAME's hd63484.cpp implements. Each value has
// a comment with the official ACRTC mnemonic + parameter count.
const (
	// System-control commands (top nibble 0x0).
	cmdORG       = 0x0000 // ORG    — set drawing origin (2 args: X, Y)
	cmdWPRBase   = 0x0800 // WPR    — write parameter register (low 5 bits = reg #; 1 arg)
	cmdWPRMask   = 0xFFE0 // mask to match the WPR family (0x0800..0x081F)
	cmdRPRBase   = 0x0C00 // RPR    — read parameter register (low 5 bits = reg #; 0 args, 1 result)
	cmdRPRMask   = 0xFFE0
	cmdWPTN      = 0x1800 // WPTN   — write pattern RAM (next word = count of pattern words)
	cmdRPTN      = 0x1C00 // RPTN   — read pattern RAM (1 arg, returns count words)
	cmdSCAN      = 0x1400 // SCAN   — scan boundary (rare; 1 arg)

	// Pen-motion commands (top nibble 0x8). Low bit selects line draw vs move.
	cmdAMOVE = 0x8000 // AMOVE  — absolute move (2 args: X, Y)
	cmdRMOVE = 0x8400 // RMOVE  — relative move (2 args: dX, dY)
	cmdALINE = 0x8801 // ALINE  — absolute line draw + move (2 args: endX, endY)
	cmdALIN0 = 0x8800 // ALINE  variant without colour-area flag
	cmdRLINE = 0x8C00 // RLINE  — relative line draw + move (2 args: dX, dY)

	// Rectangle commands (top nibble 0x9).
	cmdARCT = 0x9000 // ARCT   — absolute rectangle outline (2 args: endX, endY)
	cmdRRCT = 0x9400 // RRCT   — relative rectangle outline (2 args: dX, dY)

	// Filled-rectangle commands (top nibble 0xA).
	cmdAFRCT = 0xA000 // AFRCT  — absolute filled rectangle (2 args)
	cmdRFRCT = 0xA400 // RFRCT  — relative filled rectangle (2 args)

	// Polyline / polygon commands (top nibble 0xB).
	cmdAPLL = 0xB000 // APLL   — absolute polyline (variable; ended by RTN)
	cmdRPLL = 0xB400 // RPLL   — relative polyline
	cmdAPLG = 0xB800 // APLG   — absolute polygon (closes back to start)
	cmdRPLG = 0xBC00 // RPLG   — relative polygon

	// Circle / ellipse (top nibble 0xC except DOT).
	cmdCRCL = 0xC000 // CRCL   — circle (1 arg: radius)
	cmdELPS = 0xC400 // ELPS   — ellipse (2 args)
	cmdDOT  = 0xCC00 // DOT    — plot one pixel at the pen (0 args)

	// Memory operations (top nibble 0xE/0xF).
	cmdPAINT = 0xE000 // PAINT  — flood-fill from pen
	cmdDMR   = 0xE800 // DMR    — DMA read
	cmdDMW   = 0xEC00 // DMW    — DMA write
	cmdCLR   = 0xF000 // CLR    — clear an area
	cmdSCLR  = 0xF400 // SCLR   — screen clear
	cmdCPY   = 0xF800 // CPY    — area copy
	cmdSCPY  = 0xFC00 // SCPY   — screen-area copy
)

// decoderState is the parser's "what word do I expect next" state. Each
// multi-word command has its own slot so we can read the parameter words
// without disambiguating from the next command word.
type decoderState int

const (
	stCmd          decoderState = iota // hub: awaiting a command word
	stMoveX                            // AMOVE: next word = X
	stMoveY                            // AMOVE: next word = Y
	stRMoveX                           // RMOVE: next word = dX
	stRMoveY                           // RMOVE: next word = dY
	stLineX                            // ALINE: next word = endX
	stLineY                            // ALINE: next word = endY
	stRLineX                           // RLINE: next word = dX
	stRLineY                           // RLINE: next word = dY
	stRctX                             // ARCT:  next word = endX
	stRctY                             // ARCT:  next word = endY
	stRRctX                            // RRCT:  next word = dX
	stRRctY                            // RRCT:  next word = dY
	stFRctX                            // AFRCT/RFRCT: next word = endX/dX
	stFRctY                            //              next word = endY/dY
	stWPRArg                           // WPR:   consume one value word
	stGlyphA                           // WPTN(10): consume 0x000A header
	stGlyphFG                          //   then  fg selector
	stGlyphBG                          //   then  bg selector
	stGlyphRows                        //   then  glyphRows × bitmap rows
	stGlyphTrailer                     //   then  glyphTrailLen × trailer words
	stWPTNCount                        // WPTN: read count word (for non-glyph variants)
	stRasterData                       // raster (memory-write) mode active
	stORG1                             // ORG: next word = origin X
	stORG2                             //      then     = origin Y
	stCRCLArg                          // CRCL: radius
	stPAINTSeed                        // PAINT: seed colour
)

// Glyph packet layout: a WPTN with count=10 is interpreted as a text-glyph
// blit by the 8593 firmware. The packet has this structure:
//
//	0x1800 0x000A           ← WPTN header + count
//	fg, bg                  ← 2 colour selector words (palette pen indices)
//	row0..row7              ← 8 × 16-bit bitmap rows (LSB = leftmost pixel)
//	trailer × 4             ← post-glyph state (0805 reg-write + 3 values)
//
// Calibrated against the live Rev L firmware stream — see cmd/displayprobe
// for the run-folded view of an actual packet.
const (
	glyphRows     = 8
	glyphTrailLen = 4
	glyphWPTNCount = 0x000A // WPTN count that identifies a glyph packet
)

// decoder is the chip's command-FIFO parser. Each WriteData feeds one word;
// the parser dispatches based on the current state and the word value.
type decoder struct {
	st decoderState

	// In-flight command working storage.
	moveX, moveY int      // captured pen / endpoint coords during multi-word cmds
	wprReg       uint16   // register selected by an in-flight WPR
	rows         [glyphRows]uint16
	rowIdx       int
	trailIdx     int
	wptnCount    int // pending WPTN data-word count
	wptnPos      int // words consumed so far in a non-glyph WPTN
}

// feed dispatches a single 16-bit word into the chip according to the
// current decoder state. Most multi-word commands have their own state
// slots; the hub case (stCmd) decodes the command opcode and transitions.
func (dec *decoder) feed(c *Chip, w uint16) {
	switch dec.st {
	case stCmd:
		dec.dispatchCmd(c, w)
	case stMoveX:
		dec.moveX = int(int16(w))
		dec.st = stMoveY
	case stMoveY:
		c.penX = dec.moveX
		c.penY = int(int16(w))
		c.Moves++
		dec.st = stCmd
	case stRMoveX:
		dec.moveX = int(int16(w))
		dec.st = stRMoveY
	case stRMoveY:
		c.penX += dec.moveX
		c.penY += int(int16(w))
		c.Moves++
		dec.st = stCmd
	case stLineX:
		dec.moveX = int(int16(w))
		dec.st = stLineY
	case stLineY:
		ly := int(int16(w))
		c.drawLine(c.penX, c.penY, dec.moveX, ly, fgColor)
		c.penX, c.penY = dec.moveX, ly
		c.Lines++
		dec.st = stCmd
	case stRLineX:
		dec.moveX = int(int16(w))
		dec.st = stRLineY
	case stRLineY:
		ex := c.penX + dec.moveX
		ey := c.penY + int(int16(w))
		c.drawLine(c.penX, c.penY, ex, ey, fgColor)
		c.penX, c.penY = ex, ey
		c.Lines++
		dec.st = stCmd
	case stRctX:
		dec.moveX = int(int16(w))
		dec.st = stRctY
	case stRctY:
		c.drawRect(c.penX, c.penY, dec.moveX, int(int16(w)), fgColor)
		c.Rects++
		dec.st = stCmd
	case stRRctX:
		dec.moveX = int(int16(w))
		dec.st = stRRctY
	case stRRctY:
		ex := c.penX + dec.moveX
		ey := c.penY + int(int16(w))
		c.drawRect(c.penX, c.penY, ex, ey, fgColor)
		c.Rects++
		dec.st = stCmd
	case stFRctX:
		dec.moveX = int(int16(w))
		dec.st = stFRctY
	case stFRctY:
		c.drawFilledRect(c.penX, c.penY, dec.moveX, int(int16(w)), fgColor)
		c.FilledRects++
		dec.st = stCmd
	case stORG1:
		dec.moveX = int(int16(w))
		dec.st = stORG2
	case stORG2:
		// ORG sets the drawing-origin; we don't model coordinate
		// transformation yet, but record the values so register-reads
		// see them.
		c.regs[0x1F] = uint16(dec.moveX) // stash X
		_ = w
		dec.st = stCmd
	case stCRCLArg:
		// CRCL — circle of radius |w| at current pen.
		c.drawCircle(c.penX, c.penY, int(int16(w)), fgColor)
		dec.st = stCmd
	case stPAINTSeed:
		// PAINT seed colour — flood fill from current pen until boundary.
		// Modelled as a no-op for now; the 8593 firmware doesn't use this
		// path in its boot sequence (we'd see it via the unknown-cmd
		// histogram if it did).
		_ = w
		dec.st = stCmd
	case stWPRArg:
		c.writeRegister(dec.wprReg, w)
		// handleWPRSideEffect may transition the parser into a follow-up
		// state (e.g. stRasterData when the MAR pair primes a video-RAM
		// burst). Only fall back to stCmd if it didn't.
		dec.st = stCmd
		dec.handleWPRSideEffect(c, dec.wprReg, w)
	case stWPTNCount:
		dec.wptnCount = int(w)
		dec.wptnPos = 0
		if w == glyphWPTNCount {
			dec.st = stGlyphFG // 2 colour words then 8 rows then trailer
		} else if w == 0 {
			dec.st = stCmd
		} else {
			// Non-glyph WPTN: read `count` words into pattern RAM.
			dec.st = stRasterData // re-use raster path with a finite count
			// Use a sentinel: if wptnCount > 0 we're writing pattern, not
			// vram. The stRasterData handler honours this distinction by
			// inspecting wptnCount.
		}
	case stGlyphFG, stGlyphBG, stGlyphRows, stGlyphTrailer:
		dec.feedGlyph(c, w)
	case stRasterData:
		dec.feedRaster(c, w)
	}
}

// dispatchCmd decodes a command opcode and transitions the parser to the
// appropriate parameter-collection state.
func (dec *decoder) dispatchCmd(c *Chip, w uint16) {
	// Match WPR / RPR by mask first (they cover 32 register-numbered
	// opcodes each).
	if w&cmdWPRMask == cmdWPRBase {
		dec.wprReg = w & 0x001F
		dec.st = stWPRArg
		return
	}
	if w&cmdRPRMask == cmdRPRBase {
		// RPR has no args; pushes the register value into the read-FIFO.
		// We don't model the FIFO yet; just stay in stCmd.
		return
	}
	// Strict exact-match dispatch. The family-mask approach (each top-6-
	// bits subdivision = one shape command, low 10 bits = attribute
	// flags) is structurally correct per the HD63484 datasheet, BUT in
	// practice the firmware emits MANY 16-bit values as parameter / glyph
	// row data that happen to have top nibbles overlapping the command
	// space (e.g. a glyph row of 0x9300 would mask-match into the ARCT
	// family and swallow 2 unrelated words as "arguments"). Each false
	// positive cascades into 3-word desync. Until we can validate the
	// chip-side parameter framing per command (which would let us tell
	// "real opcode" from "data that looks like opcode"), keep exact
	// matches only and add the specific attribute-bit variants the
	// firmware actually uses (sourced from cmd/_r2survey).
	switch w {
	case cmdORG:
		dec.st = stORG1
	case cmdAMOVE:
		dec.st = stMoveX
	case cmdRMOVE:
		dec.st = stRMoveX
	case cmdALIN0, cmdALINE: // 0x8800 / 0x8801 — ALINE without/with attr
		dec.st = stLineX
	case cmdRLINE, 0x8C01: // 0x8C00 / 0x8C01 — RLINE without/with attr
		dec.st = stRLineX
	case cmdARCT, 0x9001: // 0x9000 / 0x9001 — ARCT without/with attr
		dec.st = stRctX
	case cmdRRCT, 0x9401: // 0x9400 / 0x9401 — RRCT without/with attr
		dec.st = stRRctX
	case cmdAFRCT, 0xA001: // 0xA000 / 0xA001 — AFRCT without/with attr
		dec.st = stFRctX
	case cmdRFRCT, 0xA401: // 0xA400 / 0xA401 — RFRCT without/with attr
		dec.st = stFRctX
	case cmdDOT, 0xCC01: // 0xCC00 / 0xCC01 — DOT without/with attr
		c.setPixel(c.penX, c.penY, fgColor)
		c.Dots++
	case cmdCRCL:
		dec.st = stCRCLArg
	case cmdWPTN:
		dec.st = stWPTNCount
	case cmdPAINT:
		dec.st = stPAINTSeed
	case cmdCLR, cmdSCLR:
		// CLR / SCLR — clear screen / area. We don't model the clear
		// operation (the firmware uses WPR-triggered raster fills for
		// the same effect, which we DO model). Stay in stCmd.
	case cmdRPTN, cmdSCAN:
		// 0-or-1 arg commands we don't currently exercise. Stay in stCmd.
	default:
		// Unknown / unmodelled command. Tally for RE diagnostics; stay
		// in stCmd (don't desync — the next genuine command word will
		// re-anchor the parser).
		c.UnknownCmds++
		if c.UnknownCmdHist != nil {
			c.UnknownCmdHist[w]++
		}
	}
}

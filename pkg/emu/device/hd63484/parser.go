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
	cmdNOP     = 0x0000 // NOP    — no operation (0 args); firmware pads the FIFO with it
	cmdORG     = 0x0400 // ORG    — set drawing origin (2 args: mem-addr, dot). Datasheet 0x0400.
	cmdWPRBase = 0x0800 // WPR    — write parameter register (low 5 bits = reg #; 1 arg)
	cmdWPRMask = 0xFFE0 // mask to match the WPR family (0x0800..0x081F)
	cmdRPRBase = 0x0C00 // RPR    — read parameter register (low 5 bits = reg #; 0 args, 1 result)
	cmdRPRMask = 0xFFE0
	cmdPTN     = 0xD000 // PTN    — Pattern draw: blit the WPTN-staged pattern-RAM glyph at the pen, sized by SZ (1 arg). The 8593 emits it after every text glyph (was mis-identified as a stub "GCHR").
	cmdWPTN    = 0x1800 // WPTN   — write pattern RAM (next word = count of pattern words)
	cmdRPTN    = 0x1C00 // RPTN   — read pattern RAM (1 arg, returns count words)
	cmdSCAN    = 0x1400 // SCAN   — scan boundary (rare; 1 arg)

	// Data-transfer commands (RWP-addressed word access into display memory;
	// MAME hd63484.cpp COMMAND_RD / COMMAND_WT). The RWP is programmed via
	// WPR 0x0C (layer select + address high bits) / 0x0D (address low bits) —
	// see registers.go. First seen live from the softkey-5 (config-menu)
	// redraw once front-panel menus became reachable (2026-07-22 key work).
	cmdRD = 0x4400 // RD — read the word at RWP into the data-port read queue (0 args); RWP++
	cmdWT = 0x4800 // WT — write the operand word at RWP (1 arg); RWP++

	// Pen-motion commands (top nibble 0x8). Low bit selects line draw vs move.
	cmdAMOVE = 0x8000 // AMOVE  — absolute move (2 args: X, Y)
	cmdRMOVE = 0x8400 // RMOVE  — relative move (2 args: dX, dY)
	cmdALINE = 0x8801 // ALINE  — absolute line draw + move (2 args: endX, endY)
	cmdALIN0 = 0x8800 // ALINE  variant without colour-area flag
	cmdRLINE = 0x8C00 // RLINE  — relative line draw + move (2 args: dX, dY)

	// Rectangle commands (top nibble 0x9).
	cmdARCT = 0x9000 // ARCT   — absolute rectangle outline (2 args: endX, endY)
	cmdRRCT = 0x9400 // RRCT   — relative rectangle outline (2 args: dX, dY)

	// Graphic-drawing opcodes are a SEQUENTIAL top-6-bit map per the HD63484 manual
	// (command summary, docs/hd63484_um.txt). The previous constants in the
	// 0xA000–0xCC00 range were SCRAMBLED (e.g. AFRCT was coded 0xA000=APLG, CRCL
	// 0xC000=AFRCT); corrected here. The 8593 firmware only emits AMOVE/RMOVE/
	// ALINE/RLINE/ARCT/RRCT/APLL/RPLL/DOT/WPTN/PTN (verified by a full command-
	// stream trace), so the rest are framed for correctness but their draws are
	// gated/unmodelled.
	cmdAPLL  = 0x9800 // APLL   — absolute polyline (count-prefixed: n then 2n coords)
	cmdRPLL  = 0x9C00 // RPLL   — relative polyline
	cmdAPLG  = 0xA000 // APLG   — absolute polygon (count-prefixed)
	cmdRPLG  = 0xA400 // RPLG   — relative polygon
	cmdCRCL  = 0xA800 // CRCL   — circle (1 arg: radius)
	cmdELPS  = 0xAC00 // ELPS   — ellipse (3 args: a, b, dX)
	cmdAARC  = 0xB000 // AARC   — absolute arc (4 args)
	cmdRARC  = 0xB400 // RARC   — relative arc (4 args)
	cmdAEARC = 0xB800 // AEARC  — absolute ellipse arc (6 args)
	cmdREARC = 0xBC00 // REARC  — relative ellipse arc (6 args)
	cmdAFRCT = 0xC000 // AFRCT  — absolute filled rectangle (2 args: endX, endY)
	cmdRFRCT = 0xC400 // RFRCT  — relative filled rectangle (2 args: dX, dY)
	cmdPAINT = 0xC800 // PAINT  — flood-fill from pen (0 args + E flag)
	cmdDOT   = 0xCC00 // DOT    — plot one pixel at the pen (0 args)

	// Block fill (used by the POST display-memory self-test at ROM 0xD6B2).
	// 0x5800 is the manual's CLR (clear an area); the 8593 uses it to fill a
	// (dx+1)×(dy+1) region with a pattern word (params = pattern, dx, dy), then
	// reads it back via RD to verify the RAM. See dmem.
	cmdBLKFILL = 0x5800 // CLR — (3 args: pattern, dx, dy)

	// NON-STANDARD placeholder opcodes for the legacy area-fill / copy PRIMITIVES
	// (exercised by crtscreen_test.go). These are NOT the manual's real codes — the
	// manual's CLR/SCLR are 0x5800 / 0x5C00 (the real ones the 8593 firmware emits,
	// dispatched above / by the 0x5C00 mask) and CPY/SCPY live in the 0x7xxx range.
	// The 8593 never emits 0xF0xx, so these only drive the unit-test fill/clear
	// path; left as-is to keep that scope separate from the graphic-opcode fix.
	cmdCLR  = 0xF000 // legacy CLR primitive (test-only): clear/fill an area (3 args)
	cmdSCLR = 0xF400 // legacy SCLR primitive (test-only): screen fill (1 arg)
	cmdCPY  = 0xF800 // legacy CPY primitive (test-only): area copy (4 args)
	cmdSCPY = 0xFC00 // legacy SCPY primitive (test-only): screen-area copy (4 args)
)

// Glyph packet: a WPTN with count=glyphWPTNCount (10) is the 8593 firmware's text
// glyph — 2 colour selector words then glyphRows (8) bitmap rows — LOADED into
// pattern RAM and then blitted by the following PTN command. See execWPTN / wptn.go.
const (
	glyphRows      = 8
	glyphWPTNCount = 0x000A
)

// ─────────────────────────────────────────────────────────────────────────────
// Declarative command table + generic operand collector.
//
// The HD63484 command FIFO is a bare stream of 16-bit words with NO framing
// markers, so the parser must know EXACTLY how many parameter words each command
// consumes to stay in sync — a wrong count desyncs everything after it. We keep
// that knowledge as DATA in cmdSpecOf, a direct transcription of the manual's
// command summary, so the framing is exhaustive and auditable in ONE place
// (TestCommandFraming pins it). A generic collector (decoder.collect) gathers the
// operands per the spec; execCmd applies the (separately-verified) side effects.
// This replaces the former one-state-per-parameter-word machine, where operand
// counts were implicit in the state graph and a wrong count (e.g. the old
// WPTN/PTN handling) could slip in unnoticed.
// ─────────────────────────────────────────────────────────────────────────────

// cmdID identifies a decoded command for execCmd.
type cmdID uint8

const (
	idNOP cmdID = iota
	idORG
	idWPR
	idRPR
	idWPTN
	idRPTN
	idSCAN
	idAMOVE
	idRMOVE
	idALINE
	idRLINE
	idARCT
	idRRCT
	idAPLL
	idRPLL
	idAPLG
	idRPLG
	idCRCL
	idELPS
	idAARC
	idAEARC
	idAFRCT
	idRFRCT
	idPAINT
	idDOT
	idPTN
	idRD
	idWT
	idBLKFILL
	idSCLRarea
	idCLRlegacy
	idSCLRlegacy
	idCPY
)

// operandKind tells the collector how to frame a command's operand words.
type operandKind uint8

const (
	opNone          operandKind = iota // 0 operands; execute immediately
	opFixed                            // exactly n operand words
	opCountPrefixed                    // 1 count word N, then n*N operand words
)

// cmdSpecOf maps a command word to its (id, operand framing). System/register
// commands have bespoke encodings; graphic-drawing commands are the top-6-bit
// opcode with AREA/COL/OPM attribute bits in the low 10 (masked off here — safe
// now that the framing is exhaustive, so data words never reach this point). ok
// is false for an unrecognised opcode (a genuine unimplemented command OR a parser
// desync — the caller surfaces it via panic/gate).
func cmdSpecOf(w uint16) (id cmdID, kind operandKind, n int, ok bool) {
	// System / register / data-transfer commands (bespoke encodings).
	switch {
	case w == cmdNOP:
		return idNOP, opNone, 0, true
	case w == cmdORG:
		return idORG, opFixed, 2, true
	case w&cmdWPRMask == cmdWPRBase:
		return idWPR, opFixed, 1, true
	case w&cmdRPRMask == cmdRPRBase:
		return idRPR, opNone, 0, true
	case w == cmdWPTN:
		return idWPTN, opCountPrefixed, 1, true
	case w == cmdRPTN:
		return idRPTN, opFixed, 1, true
	case w == cmdSCAN:
		return idSCAN, opFixed, 1, true
	case w == cmdRD:
		return idRD, opNone, 0, true
	case w == cmdWT:
		return idWT, opFixed, 1, true
	case w == cmdBLKFILL: // 0x5800 — manual CLR
		return idBLKFILL, opFixed, 3, true
	case w&0xFFFC == 0x5C00: // SCLR (logical-op in low 2 bits)
		return idSCLRarea, opFixed, 3, true
	}
	// Graphic-drawing commands: base = top 6 bits (attribute bits masked off).
	switch w & 0xFC00 {
	case cmdAMOVE:
		return idAMOVE, opFixed, 2, true
	case cmdRMOVE:
		return idRMOVE, opFixed, 2, true
	case cmdALIN0: // 0x8800 — ALINE (+ attr variants)
		return idALINE, opFixed, 2, true
	case cmdRLINE: // 0x8C00
		return idRLINE, opFixed, 2, true
	case cmdARCT:
		return idARCT, opFixed, 2, true
	case cmdRRCT:
		return idRRCT, opFixed, 2, true
	case cmdAPLL:
		return idAPLL, opCountPrefixed, 2, true
	case cmdRPLL:
		return idRPLL, opCountPrefixed, 2, true
	case cmdAPLG:
		return idAPLG, opCountPrefixed, 2, true
	case cmdRPLG:
		return idRPLG, opCountPrefixed, 2, true
	case cmdCRCL:
		return idCRCL, opFixed, 1, true
	case cmdELPS:
		return idELPS, opFixed, 3, true
	case cmdAARC:
		return idAARC, opFixed, 4, true
	case cmdRARC:
		return idAARC, opFixed, 4, true
	case cmdAEARC:
		return idAEARC, opFixed, 6, true
	case cmdREARC:
		return idAEARC, opFixed, 6, true
	case cmdAFRCT:
		return idAFRCT, opFixed, 2, true
	case cmdRFRCT:
		return idRFRCT, opFixed, 2, true
	case cmdPAINT:
		return idPAINT, opNone, 0, true
	case cmdDOT:
		return idDOT, opNone, 0, true
	case cmdPTN:
		return idPTN, opFixed, 1, true
	// legacy test-only placeholders (non-standard; see const note).
	case cmdCLR: // 0xF000
		return idCLRlegacy, opFixed, 3, true
	case cmdSCLR: // 0xF400
		return idSCLRlegacy, opFixed, 1, true
	case cmdCPY, cmdSCPY: // 0xF800 / 0xFC00
		return idCPY, opFixed, 4, true
	}
	return 0, 0, 0, false
}

// decoderState — the table-driven parser has just three states.
type decoderState int

const (
	stCmd        decoderState = iota // hub: awaiting a command word
	stCollect                        // collecting operand words for the current command
	stRasterData                     // streaming a bulk video-RAM raster burst (MAR pair)
)

// decoder is the chip's command-FIFO parser. Each WriteData feeds one word; the
// parser frames the command per cmdSpecOf and executes it via execCmd.
type decoder struct {
	st           decoderState
	cmdWord      uint16   // command word being collected (for attr bits / SCLR cr / WPR reg)
	curID        cmdID    // which command
	stride       int      // operand words per count unit (opCountPrefixed)
	args         []uint16 // collected operand words (args[0] = count for opCountPrefixed)
	need         int      // remaining operand words to collect
	countPending bool     // the next operand word is the count (opCountPrefixed)

	// Raster-burst streaming bookkeeping (feedRaster / handleWPRSideEffect).
	wptnCount int
	wptnPos   int
}

// feed dispatches a single 16-bit word according to the current state.
func (dec *decoder) feed(c *Chip, w uint16) {
	switch dec.st {
	case stCmd:
		dec.dispatchCmd(c, w)
	case stCollect:
		dec.collect(c, w)
	case stRasterData:
		dec.feedRaster(c, w)
	}
}

// dispatchCmd decodes a command word and starts operand collection (or executes
// immediately for a 0-operand command).
func (dec *decoder) dispatchCmd(c *Chip, w uint16) {
	// Tag the command class (by-command colour render) and the AREA-clip flag.
	if t := cmdTagOf(w); t != tagNone {
		c.core.curCmd = t
		c.areaClip = (t == tagPoly || t == tagRect || t == tagLine || t == tagDot) && w&0x0040 != 0
	} else {
		c.areaClip = false
	}
	id, kind, n, ok := cmdSpecOf(w)
	if !ok {
		// Unknown opcode: an unimplemented command OR a parser desync. Surface it.
		c.UnknownCmds++
		if c.UnknownCmdHist != nil {
			c.UnknownCmdHist[w]++
		}
		if probeNoPanic {
			return
		}
		c.unimplementedf("command opcode %#04x at command position (no handler)", w)
		return
	}
	dec.cmdWord = w
	dec.curID = id
	dec.stride = n
	dec.args = dec.args[:0]
	switch kind {
	case opNone:
		dec.st = stCmd
		dec.execCmd(c)
	case opFixed:
		if n == 0 {
			dec.st = stCmd
			dec.execCmd(c)
			return
		}
		dec.need = n
		dec.countPending = false
		dec.st = stCollect
	case opCountPrefixed:
		dec.need = 1 // read the count word first
		dec.countPending = true
		dec.st = stCollect
	}
}

// collect gathers one operand word; when the framing is satisfied it executes.
func (dec *decoder) collect(c *Chip, w uint16) {
	dec.args = append(dec.args, w)
	dec.need--
	if dec.countPending {
		// args[0] was the count N: now need stride*N more operand words.
		dec.countPending = false
		dec.need += dec.stride * int(w)
	}
	if dec.need <= 0 {
		dec.st = stCmd // default; execCmd may override (WPR → stRasterData)
		dec.execCmd(c)
	}
}

// execCmd applies a command's side effects from its collected operands (args).
// This is the only place command SEMANTICS live; framing is entirely in
// cmdSpecOf, so the two can be audited independently. Unmodelled-but-framed
// commands gate (panic unless allowlisted) so an unexpected appearance surfaces.
func (dec *decoder) execCmd(c *Chip) {
	a := dec.args
	switch dec.curID {
	case idNOP:
		// no operation

	case idORG:
		// ORG_col = (XW % MWR1)*16 + (XD & 0xF); ORG_row = XW / MWR1.
		xw := int(int16(a[0]))
		xd := int(a[1] & 0xF)
		mwr := int(c.dispMWR)
		if mwr <= 0 {
			mwr = PaintRowBytes / 2 // 64 words/row default
		}
		c.orgCol = (xw%mwr)*16 + xd
		c.orgRow = xw / mwr
		c.core.setORG(uint16(xw), a[1]) // faithful core ORG (dpa/dpd/dn)
		c.regs[0x1F] = uint16(xw)
		c.OrgLog = append(c.OrgLog, [2]int{xw, int(a[1])})

	case idWPR:
		reg := dec.cmdWord & 0x001F
		c.writeRegister(reg, a[0])
		// May transition the parser (MAR pair → stRasterData) — execCmd runs after
		// dec.st was set to stCmd, so this side effect can override it.
		dec.handleWPRSideEffect(c, reg, a[0])
	case idRPR:
		// Read parameter register (read-FIFO not modelled).
		c.gate("cmd:rpr", "command RPR %#04x (read parameter register; read-FIFO not modelled)", dec.cmdWord)

	case idRD:
		// RD (MAME COMMAND_RD): read display memory at RWP into the data-port
		// read queue (served by ReadData) and auto-increment RWP.
		c.readQ = append(c.readQ, c.core.readword(c.core.rwp[c.rwpDn]))
		c.advanceRWP(1)

	case idWT:
		// WT (MAME COMMAND_WT): write the operand word into display memory at
		// RWP and auto-increment. Reaches the visible display through the core
		// buffer the register-derived scanout reads — no separate plumbing.
		c.core.curCmd = tagOther
		c.core.writeword(c.core.rwp[c.rwpDn], a[0])
		c.advanceRWP(1)

	case idAMOVE:
		c.penX, c.penY = int(int16(a[0])), int(int16(a[1]))
		c.Moves++
	case idRMOVE:
		c.penX += int(int16(a[0]))
		c.penY += int(int16(a[1]))
		c.Moves++

	case idALINE:
		ex, ey := int(int16(a[0])), int(int16(a[1]))
		c.drawLineRouted(c.penX, c.penY, ex, ey)
		c.penX, c.penY = ex, ey
		c.Lines++
	case idRLINE:
		ex, ey := c.penX+int(int16(a[0])), c.penY+int(int16(a[1]))
		c.drawLineRouted(c.penX, c.penY, ex, ey)
		c.penX, c.penY = ex, ey
		c.Lines++

	case idARCT:
		c.drawRect(c.penX, c.penY, int(int16(a[0])), int(int16(a[1])), true)
		c.Rects++
	case idRRCT:
		ex, ey := c.penX+int(int16(a[0])), c.penY+int(int16(a[1]))
		c.drawRect(c.penX, c.penY, ex, ey, true)
		c.Rects++

	case idAFRCT:
		c.drawFilledRect(c.penX, c.penY, int(int16(a[0])), int(int16(a[1])), true)
		c.FilledRects++
	case idRFRCT:
		ex, ey := c.penX+int(int16(a[0])), c.penY+int(int16(a[1]))
		c.drawFilledRect(c.penX, c.penY, ex, ey, true)
		c.FilledRects++

	case idAPLL, idRPLL, idAPLG, idRPLG:
		if c.APLLColorHist != nil {
			c.APLLColorHist[c.regs[0x01]]++
		}
		dec.drawPolyline(c)

	case idCRCL:
		c.drawCircle(c.penX, c.penY, int(int16(a[0])), true)

	case idDOT:
		c.drawPenPixel(c.penX, c.penY)
		c.Dots++
		if c.DotLog != nil {
			c.DotLog = append(c.DotLog, DotRec{c.penX, c.penY})
		}

	case idWPTN:
		dec.execWPTN(c)
	case idPTN:
		c.drawPattern(a[0])

	case idBLKFILL: // 0x5800 — fill (dx+1)*(dy+1) words for the POST RAM test
		c.blockFill(a[0], (int(a[1])+1)*(int(a[2])+1))
	case idSCLRarea: // 0x5C00 — RWP-addressed selective area clear/fill
		c.execClear(dec.cmdWord, a[0], int16(a[1]), int16(a[2]))

	case idSCLRlegacy: // 0xF400 — legacy screen-fill primitive (test-only)
		c.fillVRAM(a[0])
		c.ScreenClears++
	case idCLRlegacy: // 0xF000 — legacy area clear/fill primitive (test-only)
		dx := int(int16(a[1]))
		dy := int(int16(a[2]))
		c.drawFilledRect(c.penX, c.penY, c.penX+dx, c.penY+dy, a[0] != 0)
		c.AreaClears++

	case idELPS:
		c.gate("cmd:ellipse", "command ELPS %#04x (ellipse; not modelled)", dec.cmdWord)
	case idAARC:
		c.gate("cmd:arc", "command ARC %#04x (arc; not modelled)", dec.cmdWord)
	case idAEARC:
		c.gate("cmd:earc", "command EARC %#04x (ellipse arc; not modelled)", dec.cmdWord)
	case idPAINT:
		c.gate("cmd:paint", "command PAINT %#04x (flood-fill from pen; not modelled)", dec.cmdWord)
	case idCPY:
		c.gate("cmd:cpy", "command CPY/SCPY %#04x (area copy; consumed but not performed)", dec.cmdWord)
	case idRPTN, idSCAN:
		c.gate("cmd:rptn-scan", "command %#04x (RPTN/SCAN; not modelled)", dec.cmdWord)
	}
}

// drawPolyline renders APLL/RPLL/APLG/RPLG: args[0]=N, then N (X,Y) vertices.
// Relative variants (RPLL/RPLG) accumulate from the pen; polygons (APLG/RPLG)
// close back to the start. Honours the firmware's line stipple (c.linePattern).
func (dec *decoder) drawPolyline(c *Chip) {
	a := dec.args
	n := int(a[0])
	rel := dec.curID == idRPLL || dec.curID == idRPLG
	poly := dec.curID == idAPLG || dec.curID == idRPLG
	startX, startY := c.penX, c.penY
	for k := 0; k < n && 2+2*k < len(a); k++ {
		vx, vy := int(int16(a[1+2*k])), int(int16(a[2+2*k]))
		ex, ey := vx, vy
		if rel {
			ex, ey = c.penX+vx, c.penY+vy
		}
		if c.APLLUpper {
			c.core.drawOffset = 0x4000 // experiment hook: APLL → upper memory region
		}
		c.drawLine(c.penX, c.penY, ex, ey, true)
		c.core.drawOffset = 0
		c.Lines++
		c.penX, c.penY = ex, ey
	}
	if poly && n > 0 {
		c.drawLine(c.penX, c.penY, startX, startY, true) // close the polygon
		c.Lines++
		c.penX, c.penY = startX, startY
	}
}

// execWPTN handles a WPTN: args[0]=count, args[1..]=data. count=glyphWPTNCount
// (10) is the 8593's text glyph (2 colour words + glyphRows bitmap rows) — STAGED
// for the following PTN to blit (see drawPattern/wptn.go). Any other count is a
// pattern-RAM load whose first word also sets the active line stipple.
func (dec *decoder) execWPTN(c *Chip) {
	a := dec.args
	cnt := int(a[0])
	if cnt == glyphWPTNCount && len(a) >= 3+glyphRows {
		c.glyphFG = a[1]
		c.glyphBG = a[2]
		for i := 0; i < glyphRows; i++ {
			c.pendRows[i] = a[3+i]
		}
		c.pendGlyph = true
		if c.glyphLog != nil {
			c.glyphLog.RecordColours(a[1], a[2])
			c.glyphLog.Record(c.penX, c.penY, c.pendRows)
		}
		return
	}
	// Pattern-RAM load.
	for i := 1; i < len(a); i++ {
		if i-1 < len(c.pattern) {
			c.pattern[i-1] = a[i]
		}
	}
	if len(a) > 1 {
		c.linePattern = a[1] // first pattern word = active line stipple
	}
}

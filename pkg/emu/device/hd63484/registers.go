package hd63484

// Parameter register slots. The HD63484 has 32 16-bit parameter registers
// (PR0..PR31) accessed via the WPR/RPR commands. Names and meanings follow
// the chip's User's Manual (1985, Hitachi U75). The 8593 firmware writes a
// small subset; the slots it doesn't touch keep their reset value of 0.
//
// Reading any unset slot returns 0 (the chip's default after reset). Slots
// the model doesn't yet act on are still stored faithfully so register-
// readback round-trips work — the only behavioural sites are those the
// drawing engine actually consults.
const (
	// Block 0 — Operation Mode + Display Control
	PROpMode     = 0x00 // operation mode (graphic/character, RAM cfg)
	PRDisplayCtl = 0x01 // display control (sync, interlace, screen on/off)
	PRDisplayRX  = 0x02 // display Raster X (horizontal sync)
	PRDisplayRY  = 0x03 // display Raster Y (vertical sync)

	// Block 1 — Memory address & width
	PRMemWidth = 0x04 // memory address width (raster bytes per line)
	PRMARLow   = 0x0C // memory access register (low) — write address
	PRMARHigh  = 0x0D // memory access register (high) — write address

	// Block 2 — Pen / drawing
	PRColor   = 0x05 // colour register (foreground colour for line / fill)
	PRPenX    = 0x06 // current pen X (R/W; auto-updates after draw cmds)
	PRPenY    = 0x07 // current pen Y
	PRPattern = 0x08 // pattern selector (which pattern RAM slot)
	PRPatLow  = 0x09 // pattern X register
	PRPatHigh = 0x0A // pattern Y register
	PRArea    = 0x0B // area definition / clipping

	// Block 3 — Pattern RAM access
	PRPatRAMAddr = 0x0E // pattern RAM address (for WPTN/RPTN)
	PRPatRAMCnt  = 0x0F // pattern RAM count

	// Block 4 — Cursor / scroll
	PRCursorX     = 0x10
	PRCursorY     = 0x11
	PRUpperBase   = 0x12 // upper screen base address
	PRUpperWidth  = 0x13
	PRBaseBase    = 0x14
	PRBaseWidth   = 0x15
	PRLowerBase   = 0x16
	PRLowerWidth  = 0x17
	PRWindowBase  = 0x18
	PRWindowWidth = 0x19
	PRBlinkCtl    = 0x1A
	PRHorzScroll  = 0x1B
	PRVertScroll  = 0x1C

	// Block 5 — Light pen
	PRLightPenX = 0x1D
	PRLightPenY = 0x1E
	PRReserved  = 0x1F
)

// Register names for debugging / unknown-command diagnostics. Index = slot
// number (0..31). Helps logging when a probe dumps "unknown WPR" events.
var RegisterNames = [32]string{
	"OPMODE", "DISPCTL", "DISPRX", "DISPRY",
	"MEMWIDTH", "COLOR", "PENX", "PENY",
	"PATTERN", "PATLOW", "PATHIGH", "AREA",
	"MARLOW", "MARHIGH", "PATRAMADDR", "PATRAMCNT",
	"CURSORX", "CURSORY", "UPPERBASE", "UPPERWIDTH",
	"BASEBASE", "BASEWIDTH", "LOWERBASE", "LOWERWIDTH",
	"WINDOWBASE", "WINDOWWIDTH", "BLINKCTL", "HORZSCROLL",
	"VERTSCROLL", "LIGHTPENX", "LIGHTPENY", "RESERVED",
}

// writeRegister stores a value into the parameter register file and applies
// any side effects: PRPenX/Y updates the drawing pen; PRMARLow/High prime
// the memory-access pointer; PRColor selects foreground colour. Other slots
// are stored faithfully but don't trigger immediate behaviour.
func (c *Chip) writeRegister(reg, value uint16) {
	if int(reg) >= len(c.regs) {
		return
	}
	c.regs[reg] = value
	switch reg {
	case PRPenX:
		c.penX = int(int16(value))
	case PRPenY:
		c.penY = int(int16(value))
	case PRColor:
		c.colorReg = value
	case PRMARHigh:
		// We do NOT bind memPos to the raw MAR value. The 8593 firmware
		// uses MARLow=0x4000 + MARHigh=0x0000 as a *trigger* for video-
		// RAM-write mode, then writes the same trigger again before each
		// subsequent burst. If we treated MAR as a literal address each
		// burst would overwrite the same half-vram region. Instead, the
		// `handleWPRSideEffect` hook in wptn.go arms raster mode and lets
		// the existing memPos auto-increment carry across bursts —
		// matching the behaviour that produces the full-screen render
		// (top half + bottom half of paint area).
	}
}

// readRegister returns the stored value for a register, or 0 for indices
// outside the 32-slot file.
func (c *Chip) readRegister(reg uint16) uint16 {
	if int(reg) >= len(c.regs) {
		return 0
	}
	return c.regs[reg]
}

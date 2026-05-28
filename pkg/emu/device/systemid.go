package device

// ───────────────────────────────────────────────────────────────────────────
// SystemID — A16 CPU-board hardware-strap MMIO at 0xFFF73C/F73E/F77C/F77E
//
// At boot, fcn.2E74 at ROM PC 0x2E74 reads these four MMIO words and stores
// them as two longwords at RAM 0xFFBF26 / 0xFFBF2A:
//
//	move.w 0xFFF73C, 0xFFBF26.w   ; high word of LONGWORD A
//	move.w 0xFFF73E, 0xFFBF28.w   ; low  word of LONGWORD A
//	move.w 0xFFF77C, 0xFFBF2A.w   ; high word of LONGWORD B
//	move.w 0xFFF77E, 0xFFBF2C.w   ; low  word of LONGWORD B
//
// fcn.1A3E0 then extracts BOARD ID from LONGWORD A:
//
//	board_id = (RAM[0xFFBF26].l >> 19) & 0x7
//	RAM[0xFFB00C] = board_id
//
// and a jump table dispatches IDNUM (model number) off board_id. For Rev L
// Opt-027 — the 8593E variant our firmware targets — the board strap must
// produce board_id = 3, which means bits 19-21 of LONGWORD A = 0b011, so
// the simplest LONGWORD A value is 0x00180000:
//
//	word at 0xFFF73C = 0x0018  (high)
//	word at 0xFFF73E = 0x0000  (low)
//
// That makes IDNUM = 0x2191 (= 8593) after boot, which the DLP code and
// HP-IB IDNUM; query both consume.
//
// NEEDS FURTHER INVESTIGATION:
//
//   - The OTHER reads of 0xFFBF26 (at PC 0x22350, 0x2266C, 0x22680, 0x24C5A)
//     extract DIFFERENT bit fields — those likely correspond to specific
//     OPTION presence (Option 026/027 freq extension, etc.), not just the
//     base model. Picking 0x00180000 satisfies only the IDNUM=8593
//     condition; other bits in LONGWORD A may need to be set for OPTION
//     026/027 / OPTION 041 / OPTION 043 to be recognised as installed.
//
//   - LONGWORD B (from 0xFFF77C/F77E into 0xFFBF2A) hasn't been traced.
//     If the firmware uses it to encode further option state, returning
//     0 here will keep those options marked as absent.
//
//   - The 114+ DLP `HAVE(BANDS|CNT|GATE|NBW|...)` queries route through a
//     SEPARATE chain that reads CalNVRAM-derived RAM cells. Wiring SystemID
//     correctly only fixes IDNUM; HAVE(*) results stay wrong until CalNVRAM
//     is also populated with option flags.
//
// In a future revision: replace the hardcoded constants with a structured
// Strap field that callers can set per (model, options) tuple, and add a
// CalNVRAM option-flag population pass to complete the option-detection
// chain end-to-end.
// ───────────────────────────────────────────────────────────────────────────

// SystemID strap values for an 8593E with Opt-027 (the Rev L target).
// fcn.1A3E0 reads ((Word73C<<16)|Word73E) >> 19 & 7; for IDNUM=8593 the
// result must be 3, which 0x0018 in Word73C provides.
const (
	SystemIDWord73C = uint16(0x0018) // bits 19-21 of LONGWORD A = 0b011 → board_id=3
	SystemIDWord73E = uint16(0x0000)
	SystemIDWord77C = uint16(0x0000) // LONGWORD B — needs further investigation
	SystemIDWord77E = uint16(0x0000)
)

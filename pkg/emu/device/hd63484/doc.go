// Package hd63484 models the Hitachi HD63484 Advanced CRT Controller (ACRTC),
// the video chip on the HP 8593's A16 board (chip U301, marked 1820-6351 /
// IC63484-8S). The HD63484 is a 68000-family CMOS controller that sits between
// the host CPU and external video RAM, accepting a command stream via two
// memory-mapped ports and rendering pixels into the framebuffer.
//
// Architecture
//
//	┌────────┐   addr (0x5FC)    ┌────────────┐   addr+data   ┌──────────┐
//	│  CPU   │ ────────────────▶ │  ACRTC     │ ────────────▶ │ VRAM     │
//	│  68K   │ ◀──── status ──── │  HD63484   │               │ (64 KB)  │
//	│        │ ────data (0x5FE)─▶│  + FIFO    │               │          │
//	└────────┘                   │  + regs    │               └────┬─────┘
//	                             │  + pattern │                    │
//	                             └────────────┘                    ▼
//	                                                          [video out]
//
// Host interface (3 byte-wide MMIO registers in the 8593's case at 0xFFF5FC/D/E):
//   - 0x5FC: Address register (CMD port). The CPU writes a command opcode
//     here to begin a new operation, OR writes a parameter-register number
//     to select that register for subsequent data-port access.
//   - 0x5FD: Status register (R only). Bit 7 = command-execution-done (CED);
//     bit 6 = light-pen detect (LPD); bit 5 = area-ready (ARD); bit 2 =
//     write-FIFO ready (WFR); bit 1 = read-FIFO ready (RFR); bit 0 =
//     write-FIFO empty (WFE). Configurable per the IRR (interrupt enable
//     register).
//   - 0x5FE: Data register (R/W). Reads/writes the FIFO. For multi-word
//     commands the CPU pours arguments here in sequence; for register
//     access this is the data of the previously addressed parameter
//     register.
//
// Internal state:
//   - Parameter register file (32 registers, see registers.go)
//   - Command FIFO (16 words; we model 0 latency)
//   - Pattern RAM (8 patterns × 16 lines × 16 pixels = 256 words)
//   - Drawing-state registers: current pen X/Y, current colour, area
//     boundaries, scroll positions, cursor position
//   - External video RAM access via the MAR (Memory Address Register) pair
//     of parameter registers (0x0C/0x0D)
//
// Command set: the chip implements ~40 commands (see commands.go). They
// fall into families:
//   - System control:   ORG (0x0000), WPR (0x0800), RPR (0x0C00), WPTN
//     (0x1800), RPTN (0x1C00)
//   - Drawing geometry: AMOVE/RMOVE (0x8000/0x8400), ALINE/RLINE (0x8800/
//     0x8C00), ARCT/RRCT (0x9000/0x9400), APLL/RPLL
//     (poly-line), APLG/RPLG (poly-gon), CRCL (circle),
//     ELPS (ellipse), DOT (0xCC00)
//   - Area operations:  AFRCT/RFRCT (filled rect, 0xA000/0xA400), PAINT
//     (0xE000), CLR (clear, 0xF000), SCLR (screen
//     clear), CPY (copy, 0xF400)
//
// 8593 Rev L firmware usage (empirically observed; see displayprobe and
// docs/research.md): the firmware uses AMOVE, ALINE, ARCT, DOT, WPTN
// (glyph blits with count=10), WPR (parameter registers 0x05, 0x0C, 0x0D),
// and the WPR-triggered raster-write mode that pours 16,384 data words
// into video RAM per burst (background dot pattern + clear operations).
//
// References:
//   - Hitachi HD63484 ACRTC User's Manual (1985), U75 — the primary
//     register-map / command-format reference. URL in docs/research.md.
//   - MAME hd63484.cpp — the structural reference (LGPL-licensed, used
//     here as architectural inspiration; no code copied verbatim).
//   - Empirical RE of the 8593's data-port stream — see cmd/displayprobe
//     and pkg/emu/device/hd63484/*_test.go.
//
// Public API: the Chip type wraps all state and exposes Read / Write
// methods on the three MMIO offsets, plus RenderFrame() for headless
// inspection. The pkg/emu/device/SCIDisplay type now delegates to Chip
// internally; existing client code (HP8593AMMIO, tests) is unaffected.
package hd63484

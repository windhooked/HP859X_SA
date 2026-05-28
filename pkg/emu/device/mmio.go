// Package device contains memory-mapped peripheral models for the HP 8593A
// spectrum analyzer.
//
// All devices implement bus.Device (Read/Write with relative addresses) and
// are mounted on the bus by machine.New8593A.
//
// Phase 2 scope: stubs sufficient to unblock the boot sequence past the
// hardware-polling loops. Each stub documents which real chip it models and
// which registers matter for boot progress.
package device

import (
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// ───────────────────────────────────────────────────────────────────────────
// HP8593AMMIO — 4 KB MMIO window (0xFFF000–0xFFFFFF)
//
// The HP 8593A maps several chips into the top 4 KB of its 24-bit address
// space. This stub covers the full window with RAM-backed read/write, but
// overrides certain registers to return "ready/idle" values so the boot
// firmware does not hang in polling loops.
//
// Known register clusters (offsets from 0xFFF000):
//
//	0x000–0x00F   82C55A PPI — front-panel I/O (control at 0x007)
//	0x200         sweep-start latch (word, written from IRQ1/6 handlers)
//	                bit 13 = "sweep active/trigger"
//	0x300         sweep-status register (word, written from IRQ1/5/6 handlers)
//	                bit 12 = "sweep hardware ready" — polled at 0xF608/0xF626/0xF66E
//	0x400         ADC/sweep DAC output register (word, written by IRQ5 handler)
//	0x5FC–0x5FF   SCI/display controller:
//	                0x5FC (W): command word
//	                0x5FD (R): status byte — bits 0,1,2 = "ready" ← polling target
//	                0x5FE (W/R): data word
//	0x600–0x61F   TMS9914A HP-IB (32-byte window, 2-byte stride per register)
//	0x626         HP-IB extended address register
//	0x634         timer interrupt acknowledge (write 1 to ack IRQ5 tick)
//	0x716         sweep DAC / front-panel (written by boot init)
// ───────────────────────────────────────────────────────────────────────────

const MMIOBase = uint32(0xFFF000)
const MMIOSize = uint32(0x001000) // 4 KB

// sciStatusOffset is the byte offset of the SCI/display controller status
// register within the MMIO window.
//
// Known polling locations and the bits they test (mixed across revisions —
// PCs cited are from whichever firmware they were first observed in):
//
//	17.12.90  0x220C / 0x2252 / 0x3690  btst #1, $f5fd.w  — bit 1 = "transmit ready"
//	17.12.90  0x7394 / 0x7426 / 0x73D0  btst D5, (A3)     — D5=0 → bit 0
//	Rev L     0xD6C4                    btst #1, $f5fd.w  — bit 1 (display init)
//	Rev L     0xD700                    btst #5, $f5fd.w  — bit 5 ("Area Ready")
//	Rev L     0xD70E                    btst #2, $f5fd.w  — bit 2
//
// All polled bits are asserted via sciStatusReady so the emulator's
// "instant-complete" hardware model never blocks the firmware.
const sciStatusOffset = 0x5FD

// sciStatusReady is the constant value returned by hard-override reads of
// the SCI status byte. Bits 0, 1, 2, 5 are asserted — covers every
// polling pattern observed across the 17.12.90 and Rev L firmware builds.
// Bit 5 on a real HD63484 is "Area Ready" (set after the chip finishes a
// pattern/area operation); permanently asserting it is consistent with the
// rest of the stub which treats every operation as instantaneous.
const sciStatusReady = uint32(0x27)

// sweepStatusOffset is the word offset of the sweep-status register.
// The firmware polls bit 12 (0x1000 = "sweep hardware ready") at
// multiple points during the main-loop entry sequence (first observed at
// 0xF608). In real hardware this bit is set by the sweep/ADC hardware when
// a new sweep sample is available; the emulator asserts it permanently so
// that display-update loops do not stall waiting for the sweep clock.
const sweepStatusOffset = 0x300

// sweepStatusReady is OR'd into all word reads of sweepStatusOffset.
// Bit 12 = sweep-hardware-ready. Additional bits (e.g. 0x0004 = sweep-step
// mode) are preserved from whatever the firmware wrote.
const sweepStatusReady = uint32(0x1000)

// 0xFFF75C / 0xFFF75E are an indirect register-select-and-data pair —
// the A16's analog-control hybrid (multiplexer + DAC + ADC readback) based
// on the way the firmware drives it. The firmware writes a select to
// 0xFFF75C, then reads/writes the addressed register through 0xFFF75E.
// Selects observed:
//
//	0x20         calibration setup (write only)
//	0x90/91/93   sweep / IF DAC writes
//	0x95/96/97   front-end mux / gain setup (write)
//	0x9A         ← read-only status / "ready" register (the operating-loop poll target)
//	0x9D/9F      misc post-cal register reads
//
// Operating-loop poll at ROM 0x5E5FA:
//
//	move.w #$9A, $f75c.w
//	move.w $f75e.w,   $9492.w      ← read F75E into RAM[0x9492] (word)
//	move.b (-1,A6), D6              ← D6 = stack(-1,A6) = TEST byte
//	and.b  $9493.w,  D6              ← D6 &= low byte of read (= MASK)
//	cmp.b  (9,A6),  D6              ← compare against stack(+9,A6) = EXPECTED
//	bne    skip_set; move.w #$FFFF, (-12,A6)
//	...
//	beq    poll_top                  ← loop until match
//
// Captured stack values during a real poll (via instrumentation; see
// commit history): stack(-1,A6) = 0x12, stack(+9,A6) = 0x02. Match needs
// `(0x12 & low_byte) == 0x02`; the simplest natural-match return is 0x0002.
//
// Modelled here as a self-clearing match register: 0xFFF75E reads return
// 0x0002 ONCE then snap back to 0 so the polling cadence is preserved
// (the firmware still does its background annunciator-redraw work between
// "ADC ready" events). Returning 0x0002 every time fast-exits the loop and
// degrades the render (see "DO NOT" comment that was here before — now we
// have a real model instead of an override-or-don't binary choice).
const indirectDataOffset = 0x75E

// HP8593AMMIO is a RAM-backed stub covering the full 4 KB MMIO window.
// On construction it pre-sets any "always-ready" register values.
//
// Writes are stored in the backing array so read-modify-write sequences
// (notb, orb, andb …) work correctly. The SCI status byte is the only
// register with a hard override on read.
//
// Display: writes to the SCI command register (0x5FC) and data FIFO (0x5FE)
// are forwarded to Display (if non-nil), which decodes the in-band glyph/vector
// stream into a framebuffer. See display.go.
type HP8593AMMIO struct {
	b       [MMIOSize]byte
	Display *SCIDisplay

	// indirectMatchPending models a one-shot "ADC ready" on 0xFFF75E. The
	// firmware's poll at ROM 0x5E5FA wants `(0x12 & low_byte(read)) == 0x02`;
	// returning 0x0002 satisfies this. We arm the match periodically (every
	// indirectMatchEveryNReads reads) and clear it on first consumption so
	// the firmware experiences the same "occasionally ready, mostly busy"
	// cadence a real ADC produces — preserving the annunciator-redraw work
	// the firmware does between ready events.
	indirectMatchPending bool
	indirectReadCount    uint64
}

// indirectMatchEveryNReads controls how often the 0xFFF75E read returns a
// "match" value (0x0002). Higher = the firmware does more background work
// between sample-ready events. 256 is a balance: ~85 ready events per 22k
// reads in a 30M-cycle boot, comparable to a real instrument's sweep rate.
const indirectMatchEveryNReads = 256

// NewHP8593AMMIO returns an initialised MMIO stub with an attached SCIDisplay.
func NewHP8593AMMIO() *HP8593AMMIO {
	m := &HP8593AMMIO{Display: NewSCIDisplay()}

	// SCI/display controller: pre-assert all ready bits so every firmware
	// polling pattern (bits 0, 1, and 2) returns immediately.
	m.b[sciStatusOffset] = byte(sciStatusReady)

	// Sweep-status register: pre-assert "ready" (bit 12).
	// The Read override also ORs this in, but pre-seeding the backing store
	// means read-modify-write sequences (ori, bset …) do not clear it.
	m.b[sweepStatusOffset] = byte(sweepStatusReady >> 8)     // high byte: 0x10
	m.b[sweepStatusOffset+1] = byte(sweepStatusReady & 0xFF) // low byte: 0x00

	// TMS9914A HP-IB (base offset 0x600):
	// IS0 (offset 0x600) = 0x00: no interrupt assertions at idle.
	// All other registers zero — the firmware initialises them itself.

	return m
}

func (m *HP8593AMMIO) Read(addr uint32, sz bus.Size) uint32 {
	if int(addr)+int(sz) > len(m.b) {
		return 0
	}
	v := beRead(m.b[:], addr, sz)

	// Hard-override: SCI status byte is always "ready" regardless of what the
	// firmware may have written — the real hardware asserts this asynchronously.
	// Sweep-status register bit 12 is always asserted so display-update loops
	// that synchronise on the sweep clock do not stall indefinitely.
	switch sz {
	case bus.Byte:
		if addr == sciStatusOffset {
			return sciStatusReady
		}
	case bus.Word:
		// Word read that covers 0x5FD: override only the low byte (0x5FD
		// position) so all ready bits are visible to word-width polls too.
		if addr == 0x5FC {
			return (v & 0xFF00) | sciStatusReady
		}
		// Sweep-status register: OR in the "ready" bit regardless of the stored
		// value so every firmware polling pattern returns immediately.
		if addr == sweepStatusOffset {
			return v | sweepStatusReady
		}
		// Indirect analog-bus data port: see indirectDataOffset comment.
		// One match per indirectMatchEveryNReads reads; clear on consume.
		if addr == indirectDataOffset {
			m.indirectReadCount++
			arm := m.indirectReadCount%indirectMatchEveryNReads == 0
			if arm || m.indirectMatchPending {
				m.indirectMatchPending = false
				return 0x0002
			}
			return 0
		}
	}

	return v
}

func (m *HP8593AMMIO) Write(addr uint32, sz bus.Size, val uint32) {
	if int(addr)+int(sz) > len(m.b) {
		return
	}
	beWrite(m.b[:], addr, sz, val)

	// After any write to the SCI command register (0x5FC), immediately
	// re-assert the "ready" bit so that the next status poll sees it.
	// (On real hardware this happens asynchronously after the command completes;
	// the emulator completes every command instantly.)
	if addr <= sciStatusOffset && addr+uint32(sz) > sciStatusOffset {
		m.b[sciStatusOffset] |= byte(sciStatusReady)
	}

	// Forward SCI command (0x5FC) and data-FIFO (0x5FE) word writes to the
	// display decoder. The firmware always writes these as 16-bit words.
	if m.Display != nil && sz == bus.Word {
		switch addr {
		case 0x5FC:
			m.Display.WriteCmd(uint16(val))
		case 0x5FE:
			m.Display.WriteData(uint16(val))
		}
	}
}

// beRead / beWrite are copies of the bus-internal helpers so the device
// package does not import them from bus (which would create a cycle through
// the generated cgo stub).
func beRead(b []byte, addr uint32, sz bus.Size) uint32 {
	switch sz {
	case bus.Byte:
		return uint32(b[addr])
	case bus.Word:
		return uint32(b[addr])<<8 | uint32(b[addr+1])
	default:
		return uint32(b[addr])<<24 | uint32(b[addr+1])<<16 |
			uint32(b[addr+2])<<8 | uint32(b[addr+3])
	}
}

func beWrite(b []byte, addr uint32, sz bus.Size, val uint32) {
	switch sz {
	case bus.Byte:
		b[addr] = byte(val)
	case bus.Word:
		b[addr] = byte(val >> 8)
		b[addr+1] = byte(val)
	default:
		b[addr] = byte(val >> 24)
		b[addr+1] = byte(val >> 16)
		b[addr+2] = byte(val >> 8)
		b[addr+3] = byte(val)
	}
}

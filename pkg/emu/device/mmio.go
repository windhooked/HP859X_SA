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

// A16→A7 analog-interface I/O bus indirect register pair (see a7iobus.go).
// Write a select word to a7SelectOffset, then read/write the addressed A7
// register via a7DataOffset.
const (
	a7SelectOffset = 0x728
	a7DataOffset   = 0x72A
)

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

	// abus models the A16 analog-control hybrid (mux + ADC + DAC) behind
	// the indirect register pair at 0xFFF75C/0xFFF75E. See analogbus.go.
	abus analogBus

	// a7bus models the A16→A7 analog-interface "I/O bus" behind the indirect
	// register pair at 0xFFF728/0xFFF72A — the digital control + status
	// readback for the A7 board (LO/YIG/attenuator/gain/bandwidth DACs + A25
	// counterlock/status). Separate from abus. See a7iobus.go.
	a7bus a7IOBus

	// HPIB models the TMS9914A IEEE-488 controller at MMIO offset
	// 0x600..0x61F. The firmware initialises it during boot then leaves
	// it alone; in the operating loop it accesses these registers via
	// the IRQ4 handler when HP-IB activity occurs. See tms9914a.go.
	HPIB *TMS9914A

	// SweepActive enables the detector-ADC model at 0xFFF200. The IRQ6
	// sample-capture handler reads 0xFFF200 ("sweep-start latch / detector
	// ADC" per docs/research.md) once per ADC_SYNC to get the detected video
	// level for the current sweep point. When SweepActive is set, word reads
	// of 0xFFF200 return the synthesized detector level for the current sweep
	// position (a noise floor + a CAL-like peak) and advance the position; the
	// machine's sweep clock fires IRQ6 to step through points. When clear,
	// 0xFFF200 reads return the stored byte-buffer value (the prior behaviour),
	// so non-sweep firmware paths are unaffected. We model only READS — writes
	// (the firmware's LO/sweep-start latching) still store normally, so we do
	// not trigger spurious sweep-starts.
	SweepActive bool
	// sweepPoint advances on each 0xFFF200 detector read; SweepPoints is the
	// sweep length (samples per sweep) over which the synthesized spectrum
	// repeats.
	sweepPoint  int
	SweepPoints int

	// Sweep is the analog-model sweep engine backing the 0xFFF200 video-ADC
	// reads (faithful spectrum: CAL peak + noise floor). See sweepengine.go.
	Sweep *SweepEngine

	// addrLatch is the A16 write-address diagnostic latch read back at
	// 0x320000. The POST address-decoder test (ROM 0x4AA0) writes 0x2555 to
	// 0xFFF700+i*2 for i=0..31 and, after each write, reads 0x320000 expecting
	// its low 5 bits to equal i — verifying the A16 correctly latches the low
	// address bits of every f700-register-block write. We capture that index
	// on each f700-block word write; A16AddrLatch (mapped at 0x320000) returns
	// it. Without this, POST f612 bit 6 stays clear → "FAIL".
	addrLatch uint16
}

// A16AddrLatch is the A16 write-address diagnostic latch at bus address
// 0x320000. It returns the index of the most recent 0xFFF700-block write so
// the POST address-decoder self-test passes. See HP8593AMMIO.addrLatch.
type A16AddrLatch struct{ mmio *HP8593AMMIO }

// AddrLatch returns a Device for 0x320000 backed by this MMIO's write-address
// latch.
func (m *HP8593AMMIO) AddrLatch() *A16AddrLatch { return &A16AddrLatch{mmio: m} }

func (l *A16AddrLatch) Read(addr uint32, sz bus.Size) uint32       { return uint32(l.mmio.addrLatch) }
func (l *A16AddrLatch) Write(addr uint32, sz bus.Size, val uint32) {}

// sweepDetector returns the synthesized detected video level (the ADC reading)
// for sweep position pt: a low noise floor plus a single CAL-like peak. Values
// are in the firmware's video-ADC range (≈0..0x1FF, 0 V→bottom graticule, +2 V
// →top). This is a placeholder spectrum until the real LO/IF/detector chain is
// modelled; it gives the firmware coherent, peaked data to paint.
func sweepDetector(pt, total int) uint16 {
	if total <= 0 {
		total = 401
	}
	pt %= total
	v := 0x20 // noise floor near the bottom
	// a Gaussian-ish peak at ~1/3 of the span (a "signal")
	c := total / 3
	d := pt - c
	if d < 0 {
		d = -d
	}
	if d < total/12 {
		v += (total/12 - d) * 0x180 / (total / 12)
	}
	return uint16(v)
}

// readSweepADC returns the detector level for the current sweep position and
// advances it. Called for word reads of 0xFFF200 when SweepActive. Backed by
// the analog-model SweepEngine (faithful spectrum: CAL peak + noise floor); the
// legacy sweepDetector is retained only as a fallback if the engine is nil.
func (m *HP8593AMMIO) readSweepADC() uint16 {
	if m.Sweep != nil {
		return m.Sweep.DetectADC()
	}
	v := sweepDetector(m.sweepPoint, m.SweepPoints)
	m.sweepPoint++
	return v
}

// ResetSweep rewinds the detector position to the start of the sweep (sweep
// retrace). The sweep clock calls this when the firmware re-arms a new sweep.
func (m *HP8593AMMIO) ResetSweep() {
	m.sweepPoint = 0
	if m.Sweep != nil {
		m.Sweep.Reset()
	}
}

// NewHP8593AMMIO returns an initialised MMIO stub with an attached SCIDisplay.
func NewHP8593AMMIO() *HP8593AMMIO {
	m := &HP8593AMMIO{Display: NewSCIDisplay(), HPIB: NewTMS9914A(), Sweep: NewSweepEngine()}

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

	// A16 power-on self-test (POST) configuration straps at 0xFFF614/0xFFF616.
	// The POST routine at ROM 0x49A0 reads these: when EITHER is non-zero it
	// sets bb2c bits 12/13 and takes the "mark all self-tests pass" branch (ROM
	// 0x49C2 writes f610=f612=0xFF), skipping the detailed analog self-test
	// suite at ROM 0x4534+ (YTF cal, mixer-bias cal, FM-span sense, DAC wraps —
	// the "CAL YTF FAILED" / "MIXER BIAS CAL FAILED" string family). Those
	// detailed tests probe analog hardware we do not yet model, so on our
	// virtual instrument they would all "fail", leaving the f610/f612 POST
	// result latches at f610=0xF0/f612=0x20 → the reporter at ROM 0x184DE
	// renders "FAIL: DF0F" (NOT(f612):NOT(f610)) plus the dependent ADC-*/REF
	// annunciators. Asserting these straps makes the virtual A16 pass POST.
	// bb2c is a self-test-local accumulator (all 27 ROM refs live in 0x4500..
	// 0x49E8), so this only affects the POST verdict. Faithfully *running* the
	// detailed analog suite needs the full analog model — see
	// docs/ANALOG_MODEL_PLAN.md and docs/POST_SELFTEST.md.
	m.b[0x614] = 0xFF
	m.b[0x616] = 0xFF

	return m
}

func (m *HP8593AMMIO) Read(addr uint32, sz bus.Size) uint32 {
	if int(addr)+int(sz) > len(m.b) {
		return 0
	}
	v := beRead(m.b[:], addr, sz)

	// A16 data-path wrap: 0xFFF780..0xFFF7FF mirrors 0xFFF700..0xFFF77F (the
	// low address bit 7 is not decoded). The POST loopback test (ROM 0x4A0E)
	// writes the patterns 0x0000/0xFFFF/0x5555/0xAAAA to f700 and reads them
	// back at f780; the mirror makes f780 echo f700 so the data-path self-test
	// passes (sets the f612 loopback PASS bits) instead of failing → FAIL.
	if addr >= 0x780 && addr <= 0x7FF {
		return beRead(m.b[:], addr-0x80, sz)
	}

	// TMS9914A HP-IB controller at offset 0x600..0x60F (8 registers,
	// 2-byte stride). Byte reads route to the chip; reads outside that
	// window fall through to the backing-store value.
	if addr >= 0x600 && addr <= 0x60F && sz == bus.Byte {
		return uint32(m.HPIB.ReadByte(addr - 0x600))
	}

	// HP-IB data path via the front-panel μC ports (Rev L Empirically
	// derived from the IRQ4 handler at PC 0x2642+):
	//   $f160 (read) — HP-IB status byte. Bit 0 = "I/O active", bit 1
	//                  = "data byte available" — the firmware checks
	//                  these before reading $f140.
	//   $f140 (read) — HP-IB data byte. Returns the next byte the chip
	//                  has queued; reading consumes it.
	// We route both through the TMS9914A's input buffer so the
	// SendHPIB/Push API can feed bytes via the chip and the firmware
	// will receive them via this hardware path (which is how the
	// 8593A wires up HP-IB inside the box).
	if sz == bus.Byte {
		switch addr {
		case 0x160:
			// Report bits 0+1 set when bytes are pending.
			if m.HPIB.PendingInput() > 0 {
				return 0x03
			}
			return 0
		case 0x140:
			// Pop one byte from the chip's input buffer when read.
			return uint32(m.HPIB.ReadByte(0xE)) // chip's DIR
		}
	}

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
		// Indirect analog-bus data port: dispatch via analogBus, which holds
		// the most-recent select written to 0xFFF75C and returns a different
		// quantity per select (status / ADC / register-file). See analogbus.go.
		if addr == indirectDataOffset {
			return uint32(m.abus.readData())
		}
		// A16→A7 analog-interface I/O-bus data port: dispatch via a7IOBus,
		// which returns the addressed A7 register (selected by the word last
		// written to 0xFFF728). See a7iobus.go.
		if addr == a7DataOffset {
			return uint32(m.a7bus.readData())
		}
		// Detector ADC at 0xFFF200: when a sweep is active, the IRQ6 capture
		// handler reads the detected video level for the current sweep point.
		if addr == 0x200 && m.SweepActive {
			return uint32(m.readSweepADC())
		}
		// A16 system-ID hardware-strap registers — fcn.2E74 reads these at
		// boot to populate RAM[0xFFBF26+] which fcn.1A3E0 then turns into
		// IDNUM (model number). See systemid.go for the full chain and the
		// NEEDS-FURTHER-INVESTIGATION notes on option bits beyond the model.
		switch addr {
		case 0x73C:
			return uint32(SystemIDWord73C)
		case 0x73E:
			return uint32(SystemIDWord73E)
		case 0x77C:
			return uint32(SystemIDWord77C)
		case 0x77E:
			return uint32(SystemIDWord77E)
		}
	}

	return v
}

func (m *HP8593AMMIO) Write(addr uint32, sz bus.Size, val uint32) {
	if int(addr)+int(sz) > len(m.b) {
		return
	}
	beWrite(m.b[:], addr, sz, val)

	// A16 write-address latch: capture the low address-bit index of every
	// f700-register-block write so the POST address-decoder test (ROM 0x4AA0)
	// reads it back at 0x320000. See the addrLatch field + A16AddrLatch.
	if addr >= 0x700 && addr <= 0x77F {
		m.addrLatch = uint16((addr & 0x7F) >> 1)
	}

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

	// Analog-bus dispatch: 0xFFF75C latches the select; 0xFFF75E is the
	// data port whose write semantics depend on the current select. See
	// analogbus.go for the per-select model.
	if sz == bus.Word {
		switch addr {
		case 0x75C:
			m.abus.writeSelect(uint16(val))
		case indirectDataOffset:
			m.abus.writeData(uint16(val))
		case a7SelectOffset:
			m.a7bus.writeSelect(uint16(val))
		case a7DataOffset:
			m.a7bus.writeData(uint16(val))
		}
	}

	// TMS9914A HP-IB controller writes (byte-only).
	if addr >= 0x600 && addr <= 0x60F && sz == bus.Byte {
		m.HPIB.WriteByte(addr-0x600, byte(val))
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

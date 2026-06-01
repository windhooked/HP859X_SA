package device

import (
	"time"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// ───────────────────────────────────────────────────────────────────────────
// FrontPanel — model of the HP 8593A front-panel processor (keys + RPG knob)
//
// The front panel is a separate microcontroller reached over a byte-wide port
// at 0xEF4000–0xEF401F (8-bit registers at odd addresses). It is interrupt
// driven: when a key is pressed (or the RPG turned) it raises IRQ3. The IRQ3
// handler (ROM 0x1582) just sets RAM flag bd77 bit 0 and acks 0xEF401B; the
// main loop then calls the key-read routine (ROM 0x3AB52) which reads the
// key-matrix bitmap and stores it to RAM 0x8F1E.
//
// Data-frame protocol (request handshake = ROM fcn.59998), from disassembly:
//
//	0xEF401B  handshake/status. The requester writes 0x4 (strobe) then 0x5
//	          (request) and polls bit 1 (busy). bit 1 = 0 means "data ready". We
//	          always read back bit 1 = 0 so the handshake completes immediately.
//	0xEF4001..0xEF4017 (odd, step 2): 12 nibble registers, read as 6 packed
//	          bytes:
//	            b0 = (4017&F)<<4 | (4015&F)
//	            b1 = (4013&1)<<4 | (4011&F)
//	            b2 = (400F&3)<<4 | (400D&F)
//	            b3 = (400B&3)<<4 | (4009&F)
//	            b4 = (4007&7)<<4 | (4005&F)
//	            b5 = (4003&7)<<4 | (4001&F)
//	0xEF401D / 0xEF401F: control strobes written by the output/handshake path.
//
// RTC (corrected 2026-06-01): these 12 nibble registers are primarily a
// battery-backed BCD real-time clock in the LRTC select domain (the "LRTC" PAL
// name is literal, not a misnomer). Every firmware reader of 0xEF4001..0xEF4017
// is in the time/date subsystem (ROM 0x59xxx): the RTC-read routine fcn.59E2C
// (+ a twin) packs the 6 bytes as year/month/day/hour/min/sec — the per-register
// masks (F/1, F/3, F/3, F/7, F/7) are the BCD tens-ranges of a calendar clock
// (MSM6242-class), and SET DATE/TIME writes BCD back at 0x59B5A. The earlier
// claim that ROM 0x3AB52 reads these as a "key matrix" was wrong — 0x3AB52 never
// touches 0xEF40xx. We model them as the RTC (rtcNibble), retained across reboots
// like the real battery-backed part, which makes the timedate display (CONFIG >
// TIMEDATE) render. The legacy key-injection API (InjectMatrix/SetBit) still
// writes these registers and takes precedence while a key event is `pending`, so
// existing key experiments are unaffected. Not a boot/cal blocker (the RTC is
// read only in timedate mode). See docs/rom_annotations.md "CORRECTION
// (2026-06-01)" and docs/rom_analysis.md (LRTC row).
//
// IRQ3 is delivered by the machine run loop (the device cannot assert it
// directly): when Pending() is true the loop raises IRQ5-style IRQ3 until the
// firmware reads the matrix (Consumed()).
//
// The semantic key-code map (which matrix bit = which front-panel key) is not
// yet decoded — InjectMatrix takes the raw 6-byte bitmap. SetBit presses one
// matrix bit by (byte,bit) position for experiments.
// ───────────────────────────────────────────────────────────────────────────

const (
	FrontPanelBase = uint32(0xEF4000)
	FrontPanelSize = uint32(0x000020) // 0xEF4000–0xEF401F
)

const fpStatusReg = 0x1B // 0xEF401B handshake/status

// defaultRTC is the power-up real-time-clock value when no SetRTC override is
// given: the Rev L firmware build date (1998-06-15 12:00:00). A fixed default
// keeps renders deterministic (the boot golden never reads the RTC because
// timedate mode is off by default); call SetRTC to use a real/host time.
var defaultRTC = time.Date(1998, time.June, 15, 12, 0, 0, 0, time.UTC)

type FrontPanel struct {
	regs [FrontPanelSize]byte

	pending  bool // a key event awaits IRQ3 delivery
	consumed bool // firmware has read the matrix this event

	// rtc is the battery-backed BCD real-time clock exposed through the 12
	// nibble registers 0xEF4001..0xEF4017 (see the type doc). It is a static
	// settable value (does not auto-advance) — enough for the timedate display
	// to render a correct, retained date. SetRTC overrides it.
	rtc time.Time
}

// NewFrontPanel returns an idle front panel (no keys pressed) with the RTC at
// the default power-up time.
func NewFrontPanel() *FrontPanel { return &FrontPanel{rtc: defaultRTC} }

// SetRTC sets the real-time-clock value the front panel reports through the BCD
// nibble registers (e.g. machine code may call SetRTC(time.Now()) so the
// instrument's timedate display shows the host wall clock).
func (f *FrontPanel) SetRTC(t time.Time) { f.rtc = t }

// isRTCDataReg reports whether off is one of the 12 odd nibble registers
// 0xEF4001..0xEF4017 that carry the multiplexed key-matrix / RTC-BCD data.
func isRTCDataReg(off uint32) bool { return off >= 0x01 && off <= 0x17 && off&1 == 1 }

// rtcNibble returns the BCD nibble (0..9) the real-time clock presents at data
// register off. The register→field map is the one the firmware's RTC-read
// routine (ROM fcn.59E2C) reconstructs: high/low nibble pairs for year, month,
// day, hour, minute, second (see the type doc table).
func (f *FrontPanel) rtcNibble(off uint32) byte {
	t := f.rtc
	switch off {
	case 0x17:
		return byte((t.Year() % 100) / 10) // year tens
	case 0x15:
		return byte(t.Year() % 10) // year units
	case 0x13:
		return byte(int(t.Month()) / 10) // month tens
	case 0x11:
		return byte(int(t.Month()) % 10) // month units
	case 0x0F:
		return byte(t.Day() / 10) // day tens
	case 0x0D:
		return byte(t.Day() % 10) // day units
	case 0x0B:
		return byte(t.Hour() / 10) // hour tens
	case 0x09:
		return byte(t.Hour() % 10) // hour units
	case 0x07:
		return byte(t.Minute() / 10) // minute tens
	case 0x05:
		return byte(t.Minute() % 10) // minute units
	case 0x03:
		return byte(t.Second() / 10) // second tens
	case 0x01:
		return byte(t.Second() % 10) // second units
	}
	return 0
}

func (f *FrontPanel) Read(addr uint32, sz bus.Size) uint32 {
	addr &= FrontPanelSize - 1

	// Handshake/status: always report "ready" (busy bit 1 clear).
	if addr == fpStatusReg {
		return uint32(f.regs[addr]) &^ 0x02
	}

	// The 12 nibble registers 0xEF4001..0xEF4017 are MULTIPLEXED between the
	// injected key matrix and the BCD real-time clock (see the type doc):
	//   - while a key event is pending (InjectMatrix/SetBit), they return the
	//     injected key bits — and reading the first register (0x17) commits the
	//     read so the run loop drops IRQ3;
	//   - otherwise (the normal case, e.g. the timedate display refresh) they
	//     return the RTC's BCD nibbles.
	// The firmware reads keys and the clock through the same registers, selected
	// by the µC command; gating on `pending` reproduces that split for our use.
	if isRTCDataReg(addr) {
		if f.pending {
			if addr == 0x17 {
				f.consumed = true
			}
			return uint32(f.regs[addr])
		}
		return uint32(f.rtcNibble(addr))
	}
	return uint32(f.regs[addr])
}

func (f *FrontPanel) Write(addr uint32, sz bus.Size, val uint32) {
	addr &= FrontPanelSize - 1
	f.regs[addr] = byte(val)
}

// InjectMatrix presses the keys described by a raw 6-byte key-matrix bitmap and
// arms IRQ3. The bytes use the same packing the firmware reconstructs in
// ROM 0x3AB52 (see type doc). Call Pending()/Consumed() from the run loop to
// drive IRQ3 delivery.
func (f *FrontPanel) InjectMatrix(m [6]byte) {
	f.regs[0x17] = (m[0] >> 4) & 0x0F
	f.regs[0x15] = m[0] & 0x0F
	f.regs[0x13] = (m[1] >> 4) & 0x01
	f.regs[0x11] = m[1] & 0x0F
	f.regs[0x0F] = (m[2] >> 4) & 0x03
	f.regs[0x0D] = m[2] & 0x0F
	f.regs[0x0B] = (m[3] >> 4) & 0x03
	f.regs[0x09] = m[3] & 0x0F
	f.regs[0x07] = (m[4] >> 4) & 0x07
	f.regs[0x05] = m[4] & 0x0F
	f.regs[0x03] = (m[5] >> 4) & 0x07
	f.regs[0x01] = m[5] & 0x0F
	f.pending = true
	f.consumed = false
}

// SetBit presses a single key-matrix bit (byte 0..5, bit 0..7) and arms IRQ3 —
// a convenience for mapping experiments.
func (f *FrontPanel) SetBit(byteIdx, bit int) {
	var m [6]byte
	if byteIdx >= 0 && byteIdx < 6 && bit >= 0 && bit < 8 {
		m[byteIdx] = 1 << uint(bit)
	}
	f.InjectMatrix(m)
}

// Release clears all keys (no IRQ).
func (f *FrontPanel) Release() {
	for i := uint32(1); i <= 0x17; i += 2 {
		f.regs[i] = 0
	}
	f.pending = false
	f.consumed = false
}

// Pending reports whether a key event is waiting to be delivered via IRQ3.
func (f *FrontPanel) Pending() bool { return f.pending && !f.consumed }

// Consumed reports whether the firmware has read the most recent key event.
func (f *FrontPanel) Consumed() bool { return f.consumed }

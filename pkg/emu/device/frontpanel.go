package device

import "github.com/windhooked/HP859X_SA/pkg/emu/bus"

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
// Key-read protocol (ROM 0x3AB52), confirmed from disassembly:
//
//	0xEF401B  handshake/status. Handler writes 0x4 (strobe) then 0x5 (request)
//	          and polls bit 1 (busy). bit 1 = 0 means "data ready". We always
//	          read back bit 1 = 0 so the handshake completes immediately.
//	0xEF4001..0xEF4017 (odd, step 2): 12 nibble registers. The handler packs
//	          them into a 6-byte key-matrix bitmap:
//	            b0 = (4017&F)<<4 | (4015&F)
//	            b1 = (4013&1)<<4 | (4011&F)
//	            b2 = (400F&3)<<4 | (400D&F)
//	            b3 = (400B&3)<<4 | (4009&F)
//	            b4 = (4007&7)<<4 | (4005&F)
//	            b5 = (4003&7)<<4 | (4001&F)
//	          (the per-register masks reflect the physical key-matrix width.)
//	0xEF401D / 0xEF401F: control strobes written by the output/handshake path.
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

type FrontPanel struct {
	regs [FrontPanelSize]byte

	pending  bool // a key event awaits IRQ3 delivery
	consumed bool // firmware has read the matrix this event
}

// NewFrontPanel returns an idle front panel (no keys pressed).
func NewFrontPanel() *FrontPanel { return &FrontPanel{} }

func (f *FrontPanel) Read(addr uint32, sz bus.Size) uint32 {
	addr &= FrontPanelSize - 1

	// Reading the first matrix register (0x17) is the firmware committing to a
	// key read — mark the event consumed so the run loop drops IRQ3.
	if addr == 0x17 {
		f.consumed = true
	}

	// Handshake/status: always report "ready" (busy bit 1 clear).
	if addr == fpStatusReg {
		return uint32(f.regs[addr]) &^ 0x02
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

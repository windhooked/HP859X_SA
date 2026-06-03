package device

// GPIBController models the HP 8593 Option 041 "GPIB + Parallel" I/O board — a
// *smart I/O microcontroller* (not a flat register file) mapped at MMIO offsets
// 0xFFF100–0xFFF160. It is the option that makes HP-IB available; the firmware
// detects it via the I/O-board descriptor (0xFFF11E → bf09=4) and talks to it
// through a small command/data/status register set:
//
//	0x120  (r/w) event/data port — firmware reads option-board EVENT flags here
//	             (fcn.1d58) and WRITES outgoing response bytes here (fcn.22f0 @
//	             ROM 0x2340: pop bbca FIFO → `move.b D0,$f120`).
//	0x122  (r/w) secondary event/data port (boot-init + alt output path).
//	0x124  (r)   address nibble — fcn.1cec reads the live HP-IB address here.
//	0x12a  (r/w) chip TX-ready status (bit 5 gates the f120 output drain).
//	0x130  (w)   command register (codes 0x0C, 0x6F, 0x73 … select sub-op).
//	0x140  (r)   data-in byte (received command bytes; IRQ4 pops into bc12).
//	0x142  (r)   data-in byte (alt; same input stream).
//	0x144  (w)   data-out latch used by the 0x552B0 output dance.
//	0x148  (r)   init handshake status — bit0 ack (set), bit1 busy (clear).
//	0x150  (w)   clear strobe.
//	0x160  (r)   primary status byte read by IRQ4 (0x2642) and dispatched bit-
//	             wise: bit0 I/O-active, bit1 data-byte-available (→ read 0x140),
//	             bit2 addressing-change event (→ fcn.1d58), bit5 output-service.
//
// Fidelity: FOCUSED-FAITHFUL. The receive path (controller→instrument) and the
// transmit capture (instrument→controller, off f120/f122) are modeled to the
// firmware's register contract. The IEEE-488 *addressing* handshake is modeled
// at the event level — AddressListener/AddressTalker raise the addressing-change
// status the firmware decodes — which is enough to drive the firmware's own
// query-response path. It is NOT a general multi-device 488 bus. See
// docs/HPIB_E2E_FLOW.md.
type GPIBController struct {
	// Installed marks the option board present (descriptor byte 0xFFF11E=4).
	Installed bool

	// PrimaryAddr is the instrument's HP-IB primary address (HP default 18).
	PrimaryAddr byte

	// rx holds bytes sent by the bus controller for the firmware to read
	// (via 0x140/0x142). Pushed by Push(); popped one byte per data read.
	rx []byte

	// tx accumulates bytes the firmware writes as HP-IB output (0x120/0x122)
	// — the genuine response stream produced by the firmware's formatters.
	tx []byte

	// talker/listener reflect the modeled addressing state. When the
	// instrument is addressed as talker the firmware drains its output FIFO.
	talker   bool
	listener bool

	// addrEvent, when true, makes the next 0x160 read report the addressing-
	// change status bit (bit 2) so the IRQ4 handler runs the option-board
	// event decoder (fcn.1d58/1cec). Cleared once consumed.
	addrEvent bool

	// addrNibble is presented at 0x124; fcn.1cec reads it as the live HP-IB
	// address state (rol/not/&0xF). Encodes listener/talker addressing.
	addrNibble byte

	// cmds holds option-board addressing-notification command bytes the board
	// delivers to the firmware via f140 (0x13 = talk-addressed → bef7 bit2 at
	// ROM 0x259a; 0x11 = talk-unaddressed). Dispatched by IRQ4 → fcn.2444 when
	// f160 bit4 + f14a bit0 are asserted. See docs/HPIB_E2E_FLOW.md.
	cmds []byte
}

// DeliverCommand queues an option-board addressing-notification command byte
// (e.g. 0x13 = addressed-as-talker) for delivery to the firmware via the f140
// command path. The next f160 read will report bit4 (→ IRQ4 fcn.2444) and f14a
// bit0 (byte-ready); the command byte is returned on the next f140 read.
func (g *GPIBController) DeliverCommand(b byte) { g.cmds = append(g.cmds, b) }

// NewGPIBController returns an option-board model with the HP default primary
// address (18). Off until Installed is set (via InstallHPIB on the MMIO).
func NewGPIBController() *GPIBController {
	return &GPIBController{PrimaryAddr: 18}
}

// Push queues controller→instrument bytes for the firmware to read. Returns the
// new pending count.
func (g *GPIBController) Push(b []byte) int {
	g.rx = append(g.rx, b...)
	return len(g.rx)
}

// PendingInput reports how many received bytes are still queued.
func (g *GPIBController) PendingInput() int { return len(g.rx) }

// popRx returns the next received byte (0 if none) and consumes it.
func (g *GPIBController) popRx() byte {
	if len(g.rx) == 0 {
		return 0
	}
	b := g.rx[0]
	g.rx = g.rx[1:]
	return b
}

// TX returns the bytes the firmware has emitted as HP-IB output since the last
// ResetTX, and is the captured query response.
func (g *GPIBController) TX() []byte { return g.tx }

// ResetTX clears the captured output buffer (call before a query).
func (g *GPIBController) ResetTX() { g.tx = nil }

// captureTX records a firmware output byte (write to 0x120/0x122).
func (g *GPIBController) captureTX(b byte) { g.tx = append(g.tx, b) }

// AddressListener models the controller addressing this instrument as a
// listener (it will receive the bytes that follow). Raises an addressing-change
// event for the firmware's IRQ4 decoder.
func (g *GPIBController) AddressListener() {
	g.listener, g.talker = true, false
	g.addrNibble = lagNibble
	g.addrEvent = true
}

// AddressTalker models the controller addressing this instrument as a talker
// (it should now drive its output onto the bus). Raises an addressing-change
// event so the firmware enters the query-response/output path.
func (g *GPIBController) AddressTalker() {
	g.talker, g.listener = true, false
	g.addrNibble = tagNibble
	g.addrEvent = true
}

// Addressing nibble encodings presented at 0x124. fcn.1cec applies
// `rol.b #1; not.b; and #0xF`; these constants are refined empirically by the
// addressing-injection probe (cmd/caldump) so the decode lands on the
// listener/talker state the firmware's gate expects.
const (
	lagNibble byte = 0x00 // listener-addressed
	tagNibble byte = 0x00 // talker-addressed (refined during iteration)
)

// ReadReg returns the value for an option-board register read at MMIO offset
// `off` (addr - 0xFFF000, e.g. 0x160 for 0xFFF160). Returns (value, true) if
// this device owns the offset.
func (g *GPIBController) ReadReg(off uint32) (uint32, bool) {
	if !g.Installed {
		return 0, false
	}
	switch off {
	case 0x160: // primary status byte (IRQ4 dispatch)
		// Match the proven receive model: 0x03 (bit0 I/O-active + bit1 data-
		// available) when bytes are queued, else idle. The IRQ4 handler only
		// takes the data path when bit0+bit1 are set (ROM 0x2660/0x2668); a
		// lone bit2 (with bit0 clear) routes to fcn.1d58 via ROM 0x26CE.
		if len(g.rx) > 0 {
			return 0x03, true
		}
		if len(g.cmds) > 0 {
			// bit0 (I/O-active) + bit4 (command pending). The IRQ4 dispatch
			// only checks bit4 after bit0 is set + bit1/bit2 clear (ROM
			// 0x2660→0x2668→0x26a0→0x26ae → fcn.2444).
			return 0x11, true
		}
		if g.addrEvent {
			// Addressing-change event — one-shot, reported only when the
			// receive queue is drained so the data path doesn't mask it.
			g.addrEvent = false
			return 0x04, true // bit2: addressing change → fcn.1d58
		}
		return 0x00, true
	case 0x14a: // option-board byte-ready status (fcn.2444: bit0 = command byte ready)
		if len(g.cmds) > 0 {
			return 0x01, true
		}
		return 0, true
	case 0x140, 0x142: // data-in byte (addressing command takes priority)
		if len(g.cmds) > 0 {
			b := g.cmds[0]
			g.cmds = g.cmds[1:]
			return uint32(b), true
		}
		return uint32(g.popRx()), true
	case 0x148: // init handshake: bit0 ack set, bit1 busy clear
		return 0x01, true
	case 0x124: // address nibble (fcn.1cec)
		return uint32(g.addrNibble), true
	case 0x12a: // chip TX-ready: bit5 set so the f120 output drain proceeds
		return 0x20, true
	case 0x120, 0x122: // event/data port — no pending event byte by default
		return 0, true
	}
	return 0, false
}

// WriteReg handles an option-board register write at chip-local offset `off`.
// Returns true if this device owns the offset. Captures outgoing HP-IB bytes
// written to the data ports.
func (g *GPIBController) WriteReg(off uint32, val byte) bool {
	if !g.Installed {
		return false
	}
	switch off {
	case 0x120, 0x122: // firmware writes outgoing response bytes here
		g.captureTX(val)
		return true
	case 0x130, 0x144, 0x150, 0x12a, 0x148: // command/data-out/clear/status — accept
		return true
	}
	return false
}

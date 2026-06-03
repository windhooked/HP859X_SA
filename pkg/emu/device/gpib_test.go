package device

import "testing"

// TestGPIBControllerOff: an uninstalled controller owns no registers.
func TestGPIBControllerOff(t *testing.T) {
	g := NewGPIBController()
	if _, ok := g.ReadReg(0x160); ok {
		t.Errorf("uninstalled controller should not own 0x160")
	}
	if g.WriteReg(0x120, 0x41) {
		t.Errorf("uninstalled controller should not accept writes")
	}
}

// TestGPIBReceivePath: the status byte and data port model the firmware's
// receive contract — 0x160 reports 0x03 (I/O-active + data-available) while
// bytes are queued, and 0x140/0x142 pop them in order.
func TestGPIBReceivePath(t *testing.T) {
	g := NewGPIBController()
	g.Installed = true

	// idle: no data, no event
	if v, _ := g.ReadReg(0x160); v != 0x00 {
		t.Errorf("idle status = %#02x, want 0x00", v)
	}
	g.Push([]byte("CF?"))
	if g.PendingInput() != 3 {
		t.Fatalf("PendingInput = %d, want 3", g.PendingInput())
	}
	if v, _ := g.ReadReg(0x160); v != 0x03 {
		t.Errorf("data-pending status = %#02x, want 0x03", v)
	}
	// pop the three bytes via the data-in register
	got := []byte{}
	for g.PendingInput() > 0 {
		v, _ := g.ReadReg(0x142)
		got = append(got, byte(v))
	}
	if string(got) != "CF?" {
		t.Errorf("received %q, want %q", got, "CF?")
	}
	if v, _ := g.ReadReg(0x160); v != 0x00 {
		t.Errorf("drained status = %#02x, want 0x00", v)
	}
}

// TestGPIBHandshakeStatus: the init handshake register reads ready/not-busy so
// the boot option-board init completes (regression guard).
func TestGPIBHandshakeStatus(t *testing.T) {
	g := NewGPIBController()
	g.Installed = true
	if v, ok := g.ReadReg(0x148); !ok || v != 0x01 {
		t.Errorf("0x148 = %#02x ok=%v, want 0x01 true", v, ok)
	}
}

// TestGPIBTransmitCapture: bytes the firmware writes to the data ports are
// captured as the response stream; ResetTX clears it.
func TestGPIBTransmitCapture(t *testing.T) {
	g := NewGPIBController()
	g.Installed = true
	for _, b := range []byte("HP8593E") {
		if !g.WriteReg(0x120, b) {
			t.Fatalf("WriteReg(0x120, %#02x) not owned", b)
		}
	}
	if string(g.TX()) != "HP8593E" {
		t.Errorf("TX = %q, want %q", g.TX(), "HP8593E")
	}
	g.ResetTX()
	if len(g.TX()) != 0 {
		t.Errorf("after ResetTX, TX = %q, want empty", g.TX())
	}
}

// TestGPIBAddressingEvent: AddressTalker/AddressListener raise the addressing-
// change status (bit 2) exactly once, but only when the receive queue is drained
// (so the data path doesn't mask it), and present the address nibble at 0x124.
func TestGPIBAddressingEvent(t *testing.T) {
	g := NewGPIBController()
	g.Installed = true

	g.AddressTalker()
	if !g.talker {
		t.Errorf("AddressTalker should set talker state")
	}
	// addressing event reported once on the status read
	if v, _ := g.ReadReg(0x160); v != 0x04 {
		t.Errorf("addressing status = %#02x, want 0x04 (bit2)", v)
	}
	// one-shot: cleared after reporting
	if v, _ := g.ReadReg(0x160); v != 0x00 {
		t.Errorf("addressing event not one-shot: second read = %#02x, want 0x00", v)
	}
	// address nibble is presented at 0x124
	if _, ok := g.ReadReg(0x124); !ok {
		t.Errorf("0x124 (address nibble) should be owned")
	}

	// data pending masks the addressing event (data path wins in IRQ4)
	g.AddressTalker()
	g.Push([]byte{0x41})
	if v, _ := g.ReadReg(0x160); v != 0x03 {
		t.Errorf("with data pending, status = %#02x, want 0x03 (data wins)", v)
	}
}

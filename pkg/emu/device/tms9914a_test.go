package device

import "testing"

// TestTMS9914A_ReadWriteRoundTripIsAsymmetric verifies the chip's
// fundamental design property: the SAME address selects a DIFFERENT
// register for read vs write. The firmware's HP-IB driver depends on
// this — reads return chip status, writes program configuration.
func TestTMS9914A_ReadWriteRoundTripIsAsymmetric(t *testing.T) {
	c := NewTMS9914A()

	// Write 0xFF to AUXCR (offset 4, write side).
	c.WriteByte(4, 0xFF)

	// Reading at offset 4 returns ADSR (read side) which is still 0
	// — NOT the 0xFF we wrote.
	if got := c.ReadByte(4); got != 0 {
		t.Errorf("read after write at offset 4 = %#02X, want 0 (ADSR, not AUXCR)", got)
	}

	// But the chip DID store the value internally — readable for tests.
	if c.wregs[tms9914Reg2] != 0xFF {
		t.Errorf("internal wregs[2] = %#02X, want 0xFF (AUXCR latched)", c.wregs[tms9914Reg2])
	}
}

// TestTMS9914A_IgnoresOddOffsets verifies the 2-byte-stride access
// model: only even chip-local offsets address registers. Odd offsets
// read 0 / drop the write (matches real bus wiring where only the
// upper byte lane is connected to the chip).
func TestTMS9914A_IgnoresOddOffsets(t *testing.T) {
	c := NewTMS9914A()
	c.WriteByte(1, 0xAA) // odd offset
	c.WriteByte(3, 0xBB) // odd offset
	if c.wregs[0] != 0 || c.wregs[1] != 0 {
		t.Errorf("odd-offset writes leaked into wregs: %02X %02X", c.wregs[0], c.wregs[1])
	}
	if got := c.ReadByte(1); got != 0 {
		t.Errorf("odd-offset read = %#02X, want 0", got)
	}
}

// TestTMS9914A_AuxCmdSoftwareReset verifies that an AUXCR write with
// the swrst command (bit 7 set + command 0x00) clears IS0/IS1. The
// firmware uses this during boot init to ensure the chip starts in
// a known state.
func TestTMS9914A_AuxCmdSoftwareReset(t *testing.T) {
	c := NewTMS9914A()
	// Set some interrupt status bits.
	c.SetIS0(tms9914IS0_SRQ | tms9914IS0_BO)
	c.SetIS1(tms9914IS1_MA)
	if c.IS0() == 0 || c.IS1() == 0 {
		t.Fatal("set IS0 / IS1 bits failed")
	}

	// Write AUXCR = 0x80 → swrst command, SET flag.
	c.WriteByte(4, 0x80)

	if c.IS0() != 0 {
		t.Errorf("IS0 after swrst = %#02X, want 0", c.IS0())
	}
	if c.IS1() != 0 {
		t.Errorf("IS1 after swrst = %#02X, want 0", c.IS1())
	}
}

// TestTMS9914A_IRQAssertedWhenMaskedBitSet verifies the basic chip-IRQ
// trigger: any bit set in both IS0 and IMR0 (or IS1 and IMR1) asserts
// the interrupt line. The host wires this to M68K autovector IRQ4.
func TestTMS9914A_IRQAssertedWhenMaskedBitSet(t *testing.T) {
	c := NewTMS9914A()
	// No mask programmed yet → no IRQ even with status bit set.
	c.SetIS0(tms9914IS0_BO)
	if c.IRQAsserted() {
		t.Error("IRQ asserted with no mask set")
	}

	// Program IMR0 to unmask BO.
	c.WriteByte(0, tms9914IS0_BO)
	if !c.IRQAsserted() {
		t.Error("IRQ not asserted after unmasking the set bit")
	}

	// Clear the status bit by overwriting (no auto-clear in this
	// minimal model; firmware would do this explicitly).
	c.rregs[tms9914Reg0] = 0
	if c.IRQAsserted() {
		t.Error("IRQ still asserted after status cleared")
	}
}

// TestTMS9914A_IRQViaIS1 verifies the IS1/IMR1 pathway.
func TestTMS9914A_IRQViaIS1(t *testing.T) {
	c := NewTMS9914A()
	c.SetIS1(tms9914IS1_ERR)
	c.WriteByte(2, tms9914IS1_ERR) // IMR1 unmask ERR
	if !c.IRQAsserted() {
		t.Error("IRQ via IS1 not asserted")
	}
}

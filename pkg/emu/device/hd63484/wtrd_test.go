package hd63484

import "testing"

// TestWTWritesAtRWP pins the WT (0x4800) data-transfer command per MAME
// COMMAND_WT: the operand word is written into display memory at the RWP
// (programmed via WPR 0x0C/0x0D) and the RWP auto-increments. First hit live
// by the softkey-5 config-menu redraw once front-panel menus became reachable.
func TestWTWritesAtRWP(t *testing.T) {
	c := New()
	// Program the RWP: WPR 0x0C selects layer (bits 15-14) + address high bits;
	// WPR 0x0D the low bits (value>>4). dn=1 (Base), address = 0x0020>>4 = 2.
	c.WriteData(0x080C)
	c.WriteData(0x4000)
	c.WriteData(0x080D)
	c.WriteData(0x0020)
	if got := c.rwp[1]; got != 2 {
		t.Fatalf("RWP[1] after WPR pair = %#x, want 2", got)
	}

	c.WriteData(cmdWT)
	c.WriteData(0xBEEF)
	if got := c.core.readword(2); got != 0xBEEF {
		t.Errorf("core word[2] after WT = %#04x, want 0xBEEF", got)
	}
	if got := c.rwp[1]; got != 3 {
		t.Errorf("RWP[1] after WT = %#x, want 3 (auto-increment)", got)
	}
	if got := c.core.rwp[1]; got != 3 {
		t.Errorf("core RWP[1] after WT = %#x, want 3 (mirror in sync)", got)
	}

	// Second WT continues at the advanced pointer.
	c.WriteData(cmdWT)
	c.WriteData(0x1234)
	if got := c.core.readword(3); got != 0x1234 {
		t.Errorf("core word[3] after 2nd WT = %#04x, want 0x1234", got)
	}
}

// TestRDReadsAtRWP pins the RD (0x4400) data-transfer command per MAME
// COMMAND_RD: the word at RWP is queued for the data port (ReadData) and the
// RWP auto-increments.
func TestRDReadsAtRWP(t *testing.T) {
	c := New()
	// Write two words via WT, rewind the RWP, read them back via RD.
	c.WriteData(0x080C)
	c.WriteData(0x4000)
	c.WriteData(0x080D)
	c.WriteData(0x0050) // RWP = 5
	c.WriteData(cmdWT)
	c.WriteData(0xAA55)
	c.WriteData(cmdWT)
	c.WriteData(0x0F0F)

	c.WriteData(0x080D)
	c.WriteData(0x0050) // rewind RWP = 5
	c.WriteData(cmdRD)
	c.WriteData(cmdRD)
	if got := c.rwp[1]; got != 7 {
		t.Errorf("RWP[1] after 2×RD = %#x, want 7", got)
	}
	if got := c.ReadData(); got != 0xAA55 {
		t.Errorf("ReadData #1 = %#04x, want 0xAA55", got)
	}
	if got := c.ReadData(); got != 0x0F0F {
		t.Errorf("ReadData #2 = %#04x, want 0x0F0F", got)
	}
}

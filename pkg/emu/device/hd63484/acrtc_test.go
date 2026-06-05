package hd63484

import "testing"

// newACRTC8593 returns a core configured exactly as the 8593 firmware programs it:
// ORG 0x4003,0xa450 (layer 1, dpa=0x3a45, dpd=0), MWR1=64, 1bpp (GBM=0).
func newACRTC8593() *acrtc {
	a := &acrtc{}
	a.mwr[1] = 64
	a.setORG(0x4003, 0xa450)
	return a
}

// TestACRTCSetORG pins the ORG decode against the firmware's command and the MAME
// COMMAND_ORG formula.
func TestACRTCSetORG(t *testing.T) {
	a := newACRTC8593()
	if a.orgDN != 1 {
		t.Errorf("orgDN = %d, want 1", a.orgDN)
	}
	if a.orgDPA != 0x3a45 {
		t.Errorf("orgDPA = 0x%05x, want 0x3a45", a.orgDPA)
	}
	if a.orgDPD != 0 {
		t.Errorf("orgDPD = %d, want 0", a.orgDPD)
	}
	if a.getBpp() != 1 {
		t.Errorf("bpp = %d, want 1 (GBM=0)", a.getBpp())
	}
}

// TestACRTCCalcOffset pins logical→physical against hand-computed values from the
// MAME calc_offset formula (offset = dpa + x/16 − y*MWR, bit = x%16, Y-up).
func TestACRTCCalcOffset(t *testing.T) {
	a := newACRTC8593()
	cases := []struct {
		x, y    int16
		wantOff uint32
		wantBit uint8
	}{
		{0, 0, 0x3a45, 0},      // logical origin → dpa
		{15, 0, 0x3a45, 15},    // last bit of the origin word
		{16, 0, 0x3a46, 0},     // next word
		{32, 0, 0x3a47, 0},     // two words over
		{0, 1, 0x3a45 - 64, 0}, // one line UP (Y subtracted) = −MWR
		{0, 209, 0x0605, 0},    // graph top (firmware ymax): 0x3a45 − 209*64
		{16, 209, 0x0606, 0},
	}
	for _, c := range cases {
		off, bit := a.calcOffset(c.x, c.y)
		if off != c.wantOff || bit != c.wantBit {
			t.Errorf("calcOffset(%d,%d) = (0x%05x, %d), want (0x%05x, %d)",
				c.x, c.y, off, bit, c.wantOff, c.wantBit)
		}
	}
}

// TestACRTCSetGetDot round-trips a pixel through the unified buffer: drawing at a
// logical coordinate and reading it back must agree, and an adjacent pixel must be
// independent (1bpp bit addressing).
func TestACRTCSetGetDot(t *testing.T) {
	a := newACRTC8593()
	a.setDot(100, 50, 1)
	if a.getDot(100, 50) != 1 {
		t.Errorf("getDot(100,50) = %d, want 1 after setDot", a.getDot(100, 50))
	}
	if a.getDot(101, 50) != 0 {
		t.Errorf("getDot(101,50) = %d, want 0 (adjacent pixel must be untouched)", a.getDot(101, 50))
	}
	if a.getDot(100, 51) != 0 {
		t.Errorf("getDot(100,51) = %d, want 0 (adjacent row must be untouched)", a.getDot(100, 51))
	}
}

// TestACRTCClrExecFillAndClear exercises the area op on the flat buffer: a REPLACE
// fill lights a region (read back via getDot), and an AND-0x0000 SCLR over the same
// region clears it — the RWP-addressed clear the operating display uses.
func TestACRTCClrExecFillAndClear(t *testing.T) {
	a := newACRTC8593()
	a.mask = 0xffff
	a.rwpDN = 1

	// Anchor the RWP at the logical origin (dpa) and REPLACE-fill 4 words × 8 rows.
	a.rwp[1] = a.orgDPA
	a.clrExec(0x0000, 0xffff, 3, 7) // cr bit10=0 → replace with 0xffff
	if a.getDot(0, 0) != 1 || a.getDot(63, 0) != 1 || a.getDot(0, 7) != 1 {
		t.Fatalf("REPLACE fill did not light the region (0,0)/(63,0)/(0,7)")
	}

	// SCLR AND 0x0000 over the same region clears it.
	a.rwp[1] = a.orgDPA
	a.clrExec(0x0400|0x0002, 0x0000, 3, 7) // cr bit10=1, mm=2 (AND), pattern 0
	if a.getDot(0, 0) != 0 || a.getDot(63, 0) != 0 || a.getDot(0, 7) != 0 {
		t.Errorf("SCLR AND 0x0000 did not clear the region")
	}
}

package hd63484

import "testing"

// TestCommandFraming pins the declarative command table (cmdSpecOf) against the
// HD63484 manual's command summary (docs/hd63484_um.txt). This is the audit the
// table-driven parser exists for: the operand framing of every command is DATA in
// one place, so a wrong operand count — the class of bug that produced the old
// WPTN/PTN mis-handling — fails here instead of silently desyncing the stream.
func TestCommandFraming(t *testing.T) {
	cases := []struct {
		w    uint16
		id   cmdID
		kind operandKind
		n    int
	}{
		// System / register / data-transfer.
		{0x0000, idNOP, opNone, 0},
		{0x0400, idORG, opFixed, 2},
		{0x0805, idWPR, opFixed, 1}, // WPR reg 5
		{0x081F, idWPR, opFixed, 1}, // WPR reg 31 (mask covers 0x0800..0x081F)
		{0x0C05, idRPR, opNone, 0},
		{0x1800, idWPTN, opCountPrefixed, 1},
		{0x1C00, idRPTN, opFixed, 1},
		{0x1400, idSCAN, opFixed, 1},
		{0x5800, idBLKFILL, opFixed, 3},  // manual CLR
		{0x5C00, idSCLRarea, opFixed, 3}, // SCLR
		{0x5C02, idSCLRarea, opFixed, 3}, // SCLR logical-op variant
		// Graphic drawing (base opcode + attribute bits).
		{0x8000, idAMOVE, opFixed, 2},
		{0x8400, idRMOVE, opFixed, 2},
		{0x8800, idALINE, opFixed, 2},
		{0x8801, idALINE, opFixed, 2}, // attr variant
		{0x88E5, idALINE, opFixed, 2}, // AREA/COL/OPM attr variant
		{0x8C00, idRLINE, opFixed, 2},
		{0x9000, idARCT, opFixed, 2},
		{0x9400, idRRCT, opFixed, 2},
		{0x9800, idAPLL, opCountPrefixed, 2},
		{0x9C00, idRPLL, opCountPrefixed, 2},
		{0xA000, idAPLG, opCountPrefixed, 2},
		{0xA400, idRPLG, opCountPrefixed, 2},
		{0xA800, idCRCL, opFixed, 1},
		{0xAC00, idELPS, opFixed, 3},
		{0xB000, idAARC, opFixed, 4},
		{0xB400, idAARC, opFixed, 4},
		{0xB800, idAEARC, opFixed, 6},
		{0xBC00, idAEARC, opFixed, 6},
		{0xC000, idAFRCT, opFixed, 2},
		{0xC400, idRFRCT, opFixed, 2},
		{0xC800, idPAINT, opNone, 0},
		{0xCC00, idDOT, opNone, 0},
		{0xCC01, idDOT, opNone, 0}, // attr variant
		{0xD000, idPTN, opFixed, 1},
	}
	for _, c := range cases {
		id, kind, n, ok := cmdSpecOf(c.w)
		if !ok {
			t.Errorf("%#04x: not recognised", c.w)
			continue
		}
		if id != c.id || kind != c.kind || n != c.n {
			t.Errorf("%#04x: got (id=%d kind=%d n=%d), want (id=%d kind=%d n=%d)",
				c.w, id, kind, n, c.id, c.kind, c.n)
		}
	}
}

// TestCollectorFramesCountPrefixed checks the generic collector frames a
// count-prefixed command (a 3-vertex polyline) exactly — consuming the count word
// plus 2×N coordinate words and no more — by confirming the parser returns to the
// command hub right after the last coordinate.
func TestCollectorFramesCountPrefixed(t *testing.T) {
	c := New()
	feedWords(c, cmdAMOVE, 10, 10)
	// APLL with 3 vertices: count + 6 coords = 7 operand words.
	feedWords(c, cmdAPLL, 3, 20, 20, 30, 10, 40, 30)
	// After the 7 operands the parser must be back at the command hub, so a
	// following NOP is accepted as a command (not swallowed as a stray operand).
	if c.dec.st != stCmd {
		t.Fatalf("after count-prefixed APLL the parser is in state %d, want stCmd (%d)", c.dec.st, stCmd)
	}
	feedWords(c, cmdNOP) // must be treated as a command (no panic / desync)
	if c.dec.st != stCmd {
		t.Fatalf("NOP after APLL left parser in state %d, want stCmd", c.dec.st)
	}
}

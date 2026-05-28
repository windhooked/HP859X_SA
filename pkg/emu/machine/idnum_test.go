package machine_test

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestIDNUMIdentifiesAs8593 verifies the system-ID hardware-strap +
// model-detection chain ends at the right IDNUM for Rev L Opt-027:
//
//	MMIO 0xFFF73C/F73E (SystemID strap)
//	  → fcn.2E74 stores at 0xFFBF26
//	  → fcn.1A3E0 extracts (bf26.l >> 19) & 7 = 3 → 0xFFB00C = 3
//	  → IDNUM dispatch sets 0xFFBFEE = 0x2191 (= 8593)
//
// If this test fails, the SystemID strap values (device/systemid.go) or
// the board-detect chain have changed. See cmd/probeoptions for the
// full diagnostic.
//
// NEEDS FURTHER INVESTIGATION: see systemid.go — only the BASE MODEL is
// pinned here; specific option flags (BANDS / CNT / GATE / Opt-026/027
// frequency extension) ride on bits of LONGWORD A and LONGWORD B that
// we have not yet decoded. HAVE() queries from DLP code will not yet
// return correct results.
func TestIDNUMIdentifiesAs8593(t *testing.T) {
	m := newMachine(t)
	m.CPU.Reset()
	m.BootToOperating(30_000_000)

	board := uint16(m.Bus.Read(0xFFB00C, bus.Word))
	idnum := uint16(m.Bus.Read(0xFFBFEE, bus.Word))

	if board != 3 {
		t.Errorf("0xFFB00C (board strap) = %#04X, want 3 — SystemID strap may not be reaching fcn.1A3E0",
			board)
	}
	if idnum != 0x2191 {
		t.Errorf("0xFFBFEE (IDNUM) = %#04X, want 0x2191 (= 8593) — model dispatch broken",
			idnum)
	}
}

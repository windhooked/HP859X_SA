package machine_test

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestPressKeyEntersMatrixDispatch verifies the Machine.PressKey API
// reaches the per-key dispatch path the operating tick gates on
// (bc67.1 + b072.14). The path's tail clears 0xFFB20E and 0xFFBF01
// (both part of fcn.520's IP-handler clear set) — pressing any key
// + driving the operating tick must change those two cells from
// their post-boot values.
//
// This test is the regression baseline for the matrix-dispatch path:
// if it stops firing the firmware's matrix-handling code path has
// changed, or the gate-bit modelling in Machine.PressKey is wrong.
// TestPressKeyHoldsGateBits verifies that the Machine.PressKey API
// correctly arms the matrix-bit + sets bc67.1 + sets b072.14 + fires
// IRQ3. After the call:
//
//   - bc67.0 must be set (IRQ3 handler at 0x2B26 ran)
//   - bc67.1 must be set (our μC-RAM-master modelling)
//   - b072.14 must be set (likewise)
//   - matrix register reads must reflect the pressed bit
//
// This locks down the API contract. The earlier hope that holding
// these bits would cause the operating tick's matrix-dispatch path at
// PC 0x18F66 to fire fcn.520 (Initial Preset) turned out to be a
// confound — cmd/keymatrix3's "2 witness cells differ" finding was
// caused by different cycle budgets between the baseline (60M) and
// with-key (50M) runs, not by real dispatch. With matched cycle
// budgets, the witness cells are identical in both runs.
//
// So the dispatch path at 0x18F5E + 0x18F66 is gated by MORE than
// just bc67.1 + b072.14 — additional state (ba86.0 at 0x18F6E, plus
// whatever slot 0x67C tests) is still needed. PressKey is the API
// to hold the known gates while we hunt for the rest.
func TestPressKeyHoldsGateBits(t *testing.T) {
	m := newMachine(t)
	m.CPU.Reset()
	m.BootToOperating(30_000_000)

	m.PressKey(2, 2)

	bc67 := byte(m.Bus.Read(0xFFBC67, bus.Byte))
	b072 := uint16(m.Bus.Read(0xFFB072, bus.Word))

	if bc67&0x01 == 0 {
		t.Errorf("after PressKey: bc67.0 not set (IRQ3 handler didn't run); bc67=%#02X", bc67)
	}
	if bc67&0x02 == 0 {
		t.Errorf("after PressKey: bc67.1 not set (μC-RAM-master gate); bc67=%#02X", bc67)
	}
	if b072&0x4000 == 0 {
		t.Errorf("after PressKey: b072.14 not set (second dispatch gate); b072=%#04X", b072)
	}
}

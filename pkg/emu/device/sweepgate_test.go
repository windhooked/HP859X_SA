package device

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestSweepRegionPollClassification pins the analog sweep gate's core signal:
// a 0xFFF300 (sweep-status) read opens the gate ONLY when it comes from a
// firmware sweep-wait code region (the measurement processor 0x171xx or the
// operating-loop idle-wait at 0x188BA). Reads from the background poll (0x3F00)
// or the op-tick entry (0x18570) must NOT open it — that is what keeps CAL DISP /
// menus (which reach only those) from being wiped by phantom sweeps. See
// HP8593AMMIO.ConsumeSweepRegionPoll + machine.driveSweepCycle.
func TestSweepRegionPollClassification(t *testing.T) {
	m := NewHP8593AMMIO()
	var pc uint32
	m.PCFunc = func() uint32 { return pc }

	read := func() { m.Read(sweepStatusOffset, bus.Word) }

	// Non-sweep read sites must NOT open the gate.
	for _, p := range []uint32{0x3F00, 0x18570, 0x18568, 0x5E5FA} {
		pc = p
		read()
		if m.ConsumeSweepRegionPoll() {
			t.Errorf("0xFFF300 read from PC %#x must NOT open the sweep gate", p)
		}
	}

	// Sweep-wait sites must open the gate (and it clears on consume).
	for _, p := range []uint32{0x17460, 0x1723A, 0x17258, 0x188BA} {
		pc = p
		read()
		if !m.ConsumeSweepRegionPoll() {
			t.Errorf("0xFFF300 read from sweep-wait PC %#x must open the sweep gate", p)
		}
		if m.ConsumeSweepRegionPoll() {
			t.Errorf("gate must clear after consume (PC %#x)", p)
		}
	}

	// A read of a DIFFERENT register from a sweep-wait PC must NOT open the gate —
	// only the sweep-status register (0xFFF300) is the signal.
	pc = 0x17460
	m.Read(0x200, bus.Word) // video-ADC, not the sweep-status register
	if m.ConsumeSweepRegionPoll() {
		t.Error("only 0xFFF300 reads should open the sweep gate, not 0xFFF200")
	}
}

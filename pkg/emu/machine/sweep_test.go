package machine_test

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/analog"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestBootToOperatingWithSweep verifies the analog-sweep-driven boot. With the
// sweep interrupts driven (IRQ1 step + IRQ6 capture), the firmware runs real sweep
// cycles: it captures the modelled spectrum into the trace buffer, completes a
// sweep the faithful way (its own IRQ6 end-of-sweep handler sets befa bit13), and
// draws the trace. This is distinct from the plain BootToOperating, which never
// fires the sweep IRQs so the buffer stays empty and no trace draws.
//
// See docs/TRACE_DISPLAY_PATH.md "TWO-IRQ SWEEP MODEL".
func TestBootToOperatingWithSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("250M-cycle sweep boot is slow; skipped under -short")
	}
	m := newMachine(t)
	// Inject a visible mid-band tone so the captured trace has content beyond the
	// 300 MHz CAL peak.
	m.MMIO.Sweep.Spectrum.Signals = []analog.Signal{{Hz: 1.45e9, DBm: -30}}
	m.BootToOperatingWithSweep(220_000_000)

	// The firmware's own end-of-sweep IRQ handler must have raised the sweep-done
	// flag (befa bit13) — i.e. a sweep completed without us forcing the flag.
	if befa := uint16(m.Bus.Read(0xFFBEFA, bus.Word)); befa&0x2000 == 0 {
		t.Errorf("befa bit13 (sweep-done) not set: befa=%#04x — sweep did not complete", befa)
	}

	// The trace buffer (0x2FD508..bf30=0x2FD82A) must hold captured samples — the
	// CAL peak and the injected tone are non-zero against the off-screen floor.
	nz := 0
	for a := uint32(0x2FD508); a < 0x2FD82A; a += 2 {
		if uint16(m.Bus.Read(a, bus.Word)) != 0 {
			nz++
		}
	}
	if nz == 0 {
		t.Error("trace buffer empty — no samples captured during the sweep")
	}

	// The sweep must have produced substantially more drawing than a non-swept
	// boot (~7.9k vectors); the trace + repeated redraws push this well higher.
	if lines := m.MMIO.Display.Chip.Lines; lines < 15000 {
		t.Errorf("only %d vectors drawn — trace not drawn (expected the sweep to draw)", lines)
	}
}

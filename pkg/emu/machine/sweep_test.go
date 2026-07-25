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

	// The firmware's own end-of-sweep IRQ handler must raise the sweep-done flag
	// (befa bit13) — i.e. a sweep completes without us forcing the flag. The
	// flag is TRANSIENT (the operating loop clears it at 0x121CA when it
	// processes the sweep), so sample across a short window rather than at one
	// arbitrary instant.
	seen := false
	for i := 0; i < 200 && !seen; i++ {
		m.BootToOperatingWithSweep(100_000)
		if uint16(m.Bus.Read(0xFFBEFA, bus.Word))&0x2000 != 0 {
			seen = true
		}
	}
	if !seen {
		t.Errorf("befa bit13 (sweep-done) never observed across the window — sweep did not complete")
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

// TestBootWithHPIBOption verifies the HP-IB option board (Option 041) can be
// installed without regressing the boot. Previously, setting the I/O-board
// descriptor to "HP-IB present" hung the boot in the option-board serial-chip
// init handshake (it polls 0xFFF148 forever); the f148 status model fixes that.
// With the option installed the firmware enters HP-IB mode (bf09=4) AND the boot
// still reaches the operating loop and renders the UI.
func TestBootWithHPIBOption(t *testing.T) {
	if testing.Short() {
		t.Skip("250M-cycle boot is slow; skipped under -short")
	}
	m := newMachine(t)
	m.MMIO.InstallHPIB() // Option 041 present
	m.BootToOperatingWithSweep(250_000_000)

	if bf09 := byte(m.Bus.Read(0xFFBF09, bus.Byte)); bf09 != 0x04 {
		t.Errorf("bf09 = %#02x, want 0x04 (HP-IB mode active)", bf09)
	}
	// The boot must still render the operating UI — the regression was a blank
	// screen (Lines=0) stuck in the f148 handshake. >10k vectors confirms the UI.
	if lines := m.MMIO.Display.Chip.Lines; lines < 10000 {
		t.Errorf("only %d vectors drawn with HP-IB installed — boot regressed (handshake hang?)", lines)
	}
}

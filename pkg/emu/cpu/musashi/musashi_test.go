package musashi

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func eepromDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "../../../../hp8593a_eeproms")
}

// romBus builds a Bus with the ROM image mapped at address 0 and a
// zero-returning fault handler for unmapped regions (mirrors flat-memory zeros).
func romBus(t *testing.T) (*bus.Bus, []byte) {
	t.Helper()
	img, err := romloader.LoadDir(eepromDir(t))
	if err != nil {
		t.Fatalf("romloader.LoadDir: %v", err)
	}
	b := &bus.Bus{}
	b.Map(0, uint32(len(img)), "ROM", bus.NewROM(img))
	b.OnFault = func(addr uint32, sz bus.Size, write bool) uint32 { return 0 }
	return b, img
}

func newReset(t *testing.T) *CPU {
	t.Helper()
	b, _ := romBus(t)
	c, err := New(b)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.Reset()
	return c
}

// TestResetVector mirrors the equivalent test in the unicorn adapter: Musashi's
// m68k_pulse_reset() must load SP and PC from the reset vector. Values are for
// Rev L 98.06.15 Opt-027 (the canonical firmware).
func TestResetVector(t *testing.T) {
	c := newReset(t)
	if got := c.Reg(cpu.PC); got != 0x00001B34 {
		t.Errorf("PC = %#X, want 0x1B34 (Rev L)", got)
	}
	if got := c.Reg(cpu.A7); got != 0x00FF948A {
		t.Errorf("A7 (SP) = %#X, want 0xFF948A (Rev L)", got)
	}
}

// TestStepBootPrologue mirrors the unicorn test: one step advances PC by 4.
//
//	0x1B34: movea.l  (0x0).w, a7   ; 4-byte opword+EA, reads SP from addr 0
//	                              ; (Rev L's first boot instruction; same
//	                              ; idiom as 17.12.90's 0x0B3E)
func TestStepBootPrologue(t *testing.T) {
	c := newReset(t)
	if err := c.Step(); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if got := c.Reg(cpu.PC); got != 0x00001B38 {
		t.Errorf("PC after 1 step = %#X, want 0x1B38 (Rev L)", got)
	}
	if got := c.Reg(cpu.A7); got != 0x00FF948A {
		t.Errorf("A7 after movea.l (0).w,a7 = %#X, want 0xFF948A (Rev L)", got)
	}
}

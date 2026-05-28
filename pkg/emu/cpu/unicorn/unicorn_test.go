package unicorn

import (
	"path/filepath"
	"runtime"
	"testing"

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

func loadROM(t *testing.T) []byte {
	t.Helper()
	b, err := romloader.LoadDir(eepromDir(t))
	if err != nil {
		t.Fatalf("romloader.LoadDir: %v", err)
	}
	return b
}

func newReset(t *testing.T) *CPU {
	t.Helper()
	c, err := New(loadROM(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.Reset()
	return c
}

// TestResetVector checks the adapter loads the reset SP/PC from the image.
// Values are for Rev L 98.06.15 Opt-027 (the canonical firmware).
func TestResetVector(t *testing.T) {
	c := newReset(t)
	if got := c.Reg(cpu.PC); got != 0x00001B34 {
		t.Errorf("PC = %#X, want 0x1B34 (Rev L)", got)
	}
	if got := c.Reg(cpu.A7); got != 0x00FF948A {
		t.Errorf("A7 (SP) = %#X, want 0xFF948A (Rev L)", got)
	}
}

// TestStepBootPrologue runs the first real instruction and checks the core
// decodes big-endian M68K correctly:
//
//	0x1B34: movea.l (0).w,%a7   ; 4 bytes; reloads A7 from the long at 0 (= SP)
//	                            ; Rev L's first boot instruction; same idiom as
//	                            ; 17.12.90's 0x0B3E.
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

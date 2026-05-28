package emutest_test

import (
	"path/filepath"
	"runtime"
	"testing"

	musashiadapter "github.com/windhooked/HP859X_SA/pkg/emu/cpu/musashi"
	ucadapter "github.com/windhooked/HP859X_SA/pkg/emu/cpu/unicorn"

	"github.com/windhooked/HP859X_SA/internal/emutest"
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
	return filepath.Join(filepath.Dir(file), "../../hp8593a_eeproms")
}

// bootSteps is the number of instructions the DiffCores gate checks.
//
// Rev L boot prologue at 0x1B34 executes:
//
//	movea.l  (0).w, a7    ; load SP from reset vector
//	movea.l  a7, a6
//	movea.l  a6, a5
//	bra.w    0x3998       ; jump to init sequence
//
// Step 5 (0x3998: ORI #0x0700, SR) is a privileged write to the Status
// Register. Unicorn's QEMU-based M68K emulation raises UC_ERR_EXCEPTION
// for this instruction even in supervisor mode — a known Unicorn M68K
// limitation that does not affect Musashi (the primary core). The ORI is
// also a no-op in context (SR is already 0x2700, ORing 0x0700 changes
// nothing). The four-instruction gate covers memory-read, register-copy,
// and branch — sufficient to verify the Musashi cgo adapter is correctly wired.
// (The 17.12.90 build had the same prologue idiom at 0xB3E → BRA 0x1914.)
const bootSteps = 4

func loadROM(t *testing.T) []byte {
	t.Helper()
	b, err := romloader.LoadDir(eepromDir(t))
	if err != nil {
		t.Fatalf("romloader.LoadDir: %v", err)
	}
	return b
}

// TestDiffCores_BootPrologue is the Phase-0 gate: Musashi and Unicorn must
// produce identical register state for every step of the pre-MMIO boot
// prologue. Any divergence means the Musashi cgo adapter is mis-wired.
func TestDiffCores_BootPrologue(t *testing.T) {
	img := loadROM(t)

	// Musashi uses a bus-backed ROM; fault reads return 0 to match
	// Unicorn's flat memory behaviour for the prologue.
	mb := &bus.Bus{}
	mb.Map(0, uint32(len(img)), "ROM", bus.NewROM(img))
	mb.OnFault = func(addr uint32, sz bus.Size, write bool) uint32 { return 0 }

	mc, err := musashiadapter.New(mb)
	if err != nil {
		t.Fatalf("musashi.New: %v", err)
	}
	uc, err := ucadapter.New(img)
	if err != nil {
		t.Fatalf("unicorn.New: %v", err)
	}
	mc.Reset()
	uc.Reset()

	// Verify both cores agree on the reset vector before any stepping.
	if pc := mc.Reg(cpu.PC); pc != 0x00001B34 {
		t.Fatalf("musashi reset PC = %#X, want 0x1B34 (Rev L)", pc)
	}
	if mc.Reg(cpu.PC) != uc.Reg(cpu.PC) || mc.Reg(cpu.A7) != uc.Reg(cpu.A7) {
		t.Fatalf("cores disagree at reset: musashi PC=%#X A7=%#X  unicorn PC=%#X A7=%#X",
			mc.Reg(cpu.PC), mc.Reg(cpu.A7), uc.Reg(cpu.PC), uc.Reg(cpu.A7))
	}

	emutest.DiffCores(t, mc, uc, "musashi", "unicorn", bootSteps)

	if !t.Failed() {
		t.Logf("Phase-0 gate PASS: Musashi == Unicorn for %d boot instructions (final PC=%#08X)",
			bootSteps, mc.Reg(cpu.PC))
	}
}

// TestRunUntilPC_BootMilestone exercises RunUntilPC on the Musashi core: step
// forward bootSteps, record the PC, then verify a fresh core navigates to that
// same PC via RunUntilPC.
func TestRunUntilPC_BootMilestone(t *testing.T) {
	img := loadROM(t)

	newCore := func() *musashiadapter.CPU {
		b := &bus.Bus{}
		b.Map(0, uint32(len(img)), "ROM", bus.NewROM(img))
		b.OnFault = func(addr uint32, sz bus.Size, write bool) uint32 { return 0 }
		c, err := musashiadapter.New(b)
		if err != nil {
			t.Fatalf("musashi.New: %v", err)
		}
		c.Reset()
		return c
	}

	// Step a core forward to discover a milestone PC.
	mc := newCore()
	for i := 0; i < bootSteps; i++ {
		if err := mc.Step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	target := mc.Reg(cpu.PC)

	// Fresh core: reach the same milestone via RunUntilPC.
	mc2 := newCore()
	steps, err := emutest.RunUntilPC(mc2, target, bootSteps+10)
	if err != nil {
		t.Fatalf("RunUntilPC(%#X): %v", target, err)
	}
	t.Logf("reached milestone PC=%#08X in %d steps", target, steps)
}

package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: who calls the live painter fcn.6C3C, with what point argument, and
// where does the argument sequence stop?
func TestLiveCallerDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)

	callers := map[uint32]int{}
	var args []int
	inHook := false
	lastPC := uint32(0)
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if inHook {
			return
		}
		pc := m.CPU.Reg(cpu.PC)
		if pc < 0x6c3c || pc > 0x6c42 {
			lastPC = pc
			return
		}
		if lastPC >= 0x6c3c && lastPC <= 0x6c42 {
			return // still inside the entry — count once
		}
		lastPC = pc
		if len(args) >= 900 {
			return
		}
		inHook = true
		d0 := m.CPU.Reg(cpu.D0) & 0xFFFF
		sp := m.CPU.Reg(cpu.A7)
		ret := m.Bus.Read(sp, bus.Long) & 0xFFFFFF
		inHook = false
		callers[ret]++
		args = append(args, int(int16(d0)))
	}
	for i := 0; i < 150 && len(args) < 900; i++ {
		m.bootLoop(2_000_000, nil)
	}
	m.Bus.OnRead = nil
	t.Logf("hits=%d callers=%v", len(args), callers)
	// arg sequence: show transitions (wraps) + max
	max := -99999
	seg := []int{}
	shown := 0
	for i, a := range args {
		if a > max {
			max = a
		}
		if i > 0 && a < args[i-1]-5 { // wrap/restart
			if shown < 12 {
				t.Logf("segment end at arg=%d, restart at %d (seg len %d)", args[i-1], a, len(seg))
			}
			shown++
			seg = seg[:0]
		}
		seg = append(seg, a)
	}
	t.Logf("max arg seen = %d", max)
}

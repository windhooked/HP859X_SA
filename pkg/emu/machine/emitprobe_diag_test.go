package machine

import (
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestEmitBranchDiag — differential probe for the emit/translate branch: WHY is
// a typed command's own action token never emitted into the interpreter stream
// 0x34C94 services, while CAL DISP's effect fires? Run with ATCMD="CAL DISP;"
// (positive) vs "KEYEXC 30003;" (negative) over EQUAL fixed windows and diff.
//
// Stage PC-presence tracked (all fetches sampled via bus OnRead):
//   LOOKUP  fcn.32bda  name lookup (reads cmd-table base 0xFFBB54)
//   REG     fcn.34644  synthetic-source registrar (descriptor 0xA62A/0xA964/0xA972)
//   KEYBLD  fcn.34746  "KEYEXC n;" builder (calls REG at 0x347ee)
//   SCHED   0x3497E-0x349E0 DLP scheduler incl. ring-push prologue
//   SRCRD   fcn.316fa  source-byte reader (pops the fg ring)
//   INTERP  0x34B44-0x34CB0 interpreter step incl. dispatcher 0x34C94
//
// Plus: ordered dispatch stream at 0x34C94, SEPARATE fg-ring (0xFFA60C-0xFFA65C)
// and alt-ring (0xFFBB96-0xFFBBE6) write histograms, and the fg-ring/synthetic
// descriptor cells dumped at the end. Diagnostic (DIAG=1).
func TestEmitBranchDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("set DIAG=1 to run the emit-branch differential probe")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom not available")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)

	const (
		tblLo, tblHi = 0x00071D76, 0x000727CA
		dispPCLo     = 0x00034c88
		dispPCHi     = 0x00034c98
	)
	type stage struct {
		name   string
		lo, hi uint32
	}
	stages := []stage{
		{"LOOKUP(32bda)", 0x32bda, 0x32C80},
		{"REG(34644)", 0x34644, 0x346D6},
		{"KEYBLD(34746)", 0x34746, 0x34808},
		{"SCHED(3497E)", 0x3497E, 0x349E0},
		{"SRCRD(316fa)", 0x316fa, 0x31740},
		{"INTERP(34B44)", 0x34B44, 0x34CB0},
	}
	stageHits := make([]int, len(stages))
	type dispatch struct{ token, handler uint32 }
	var dispatches []dispatch
	fgW := map[string]int{}
	altW := map[string]int{}
	watchOn := false

	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if !watchOn {
			return
		}
		pc := m.CPU.Reg(cpu.PC)
		if addr >= tblLo && addr <= tblHi && pc >= dispPCLo && pc <= dispPCHi {
			if len(dispatches) < 400 {
				dispatches = append(dispatches, dispatch{(addr - 0x71D76) / 4, val & 0xFFFFFF})
			}
		}
		for i := range stages {
			if pc >= stages[i].lo && pc <= stages[i].hi {
				stageHits[i]++
				break
			}
		}
	}
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if !watchOn {
			return
		}
		pc := m.CPU.Reg(cpu.PC)
		switch {
		case addr >= 0xFFA60C && addr <= 0xFFA65C:
			fgW[fmt.Sprintf("%X->%X", pc, addr)]++
		case addr >= 0xFFBB96 && addr <= 0xFFBBE6:
			altW[fmt.Sprintf("%X->%X", pc, addr)]++
		}
	}

	atKeyFor := func(r rune) (device.ATKey, bool) {
		switch {
		case r >= 'A' && r <= 'Z':
			return device.ATKeyA + device.ATKey(r-'A'), true
		case r >= '0' && r <= '9':
			return device.ATKey0 + device.ATKey(r-'0'), true
		case r == ' ':
			return device.ATKeySpace, true
		case r == ';':
			return device.ATKeySemicolon, true
		}
		return 0, false
	}
	typeKey := func(k device.ATKey) {
		m.ATKeyboard.Enqueue(device.ATMake(k)...)
		m.driveHPIB(4_000_000, func() bool { return !m.ATKeyboard.Pending() })
		m.ATKeyboard.Enqueue(device.ATBreak(k)...)
		m.driveHPIB(4_000_000, func() bool { return !m.ATKeyboard.Pending() })
	}

	cmd := os.Getenv("ATCMD")
	if cmd == "" {
		cmd = "KEYEXC 30003;"
	}
	menuBefore := m.Bus.Read(0xFF956A, bus.Word)
	watchOn = true
	typeKey(device.ATKeyF8)
	for _, r := range cmd {
		if k, ok := atKeyFor(r); ok {
			typeKey(k)
		}
	}
	typeKey(device.ATKeyEnter)
	m.driveHPIB(150_000_000, nil) // EQUAL fixed window — no early stop
	watchOn = false

	t.Logf("=== emit-branch differential for %q (fixed 150M window) ===", cmd)
	t.Logf("menu 0x956a: %d -> %d", menuBefore, m.Bus.Read(0xFF956A, bus.Word))
	for i, s := range stages {
		t.Logf("  stage %-14s = %d", s.name, stageHits[i])
	}
	t.Logf("-- dispatches at 0x34C94 (%d) --", len(dispatches))
	for i := 0; i < len(dispatches); {
		j := i
		for j < len(dispatches) && dispatches[j] == dispatches[i] {
			j++
		}
		t.Logf("   tok 0x%03X -> 0x%06X  x%d", dispatches[i].token, dispatches[i].handler, j-i)
		i = j
	}
	dumpW := func(name string, h map[string]int) {
		var ks []string
		for k := range h {
			ks = append(ks, k)
		}
		sort.Slice(ks, func(i, j int) bool { return h[ks[i]] > h[ks[j]] })
		if len(ks) > 16 {
			ks = ks[:16]
		}
		s := ""
		for _, k := range ks {
			s += fmt.Sprintf(" %s=%d", k, h[k])
		}
		t.Logf("-- %s --%s", name, s)
	}
	dumpW("FG-ring writes (pc->addr)", fgW)
	dumpW("ALT-ring writes (pc->addr)", altW)
	rd := func(a uint32, sz bus.Size) uint32 { return m.Bus.Read(a, sz) }
	t.Logf("fg desc @FFA61C: cap=%04X buf=%08X rd=%04X wr=%04X | synth: a964=%08X a972=%08X a62a..=%08X %08X",
		rd(0xFFA61C+0xE, bus.Word), rd(0xFFA61C+0x10, bus.Long), rd(0xFFA61C+0x14, bus.Word), rd(0xFFA61C+0x16, bus.Word),
		rd(0xFFA964, bus.Long), rd(0xFFA972, bus.Long), rd(0xFFA62A, bus.Long), rd(0xFFA62E, bus.Long))
}

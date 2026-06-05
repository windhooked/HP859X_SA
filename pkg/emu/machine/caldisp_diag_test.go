package machine

import (
	"fmt"
	"image/png"
	"os"
	"sort"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// atCharKeys maps the characters of "CAL DISP;" to AT keys for the typed-command
// path (the one the GUI/user actually use).
var atCharKeys = map[rune]device.ATKey{
	'A': device.ATKeyA, 'C': device.ATKeyC, 'D': device.ATKeyD, 'I': device.ATKeyI,
	'L': device.ATKeyL, 'P': device.ATKeyP, 'S': device.ATKeyS,
	' ': device.ATKeySpace, ';': device.ATKeySemicolon,
}

// TestCalDispRootCause diagnoses why CAL DISP; flashes then is replaced. It boots
// the sweeping instrument, sends CAL DISP; through the firmware's real HP-IB
// parser, then snapshots the LIVE display at four stages — and crucially drives
// the post-CAL operating loop both WITHOUT and WITH the forced sweep — to isolate
// whether the forced sweep is what overwrites the cal table. Diagnostic: logs +
// PNGs to ./screens, no assertions (set DIAG=1 to run).
func TestCalDispRootCause(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("set DIAG=1 to run the CAL DISP root-cause diagnosis")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom not available")
	}
	m, _ := New8593A(rom)
	m.MMIO.InstallHPIB()
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)

	chip := m.MMIO.Display.Chip

	// Instrument writes to the sweep/analog MMIO region + key RAM sweep vars, so
	// we can compare what the firmware writes while actively sweeping (stage 1)
	// vs while idle in cal mode (stage 3) — the register written in one but not
	// the other is the true "sweep active" signal driveSweepCycle should gate on.
	wh := map[uint32]int{}
	rh := map[uint32]int{} // READS of the sweep-status/ADC the firmware polls
	hookOn := false
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if !hookOn {
			return
		}
		if (addr >= 0xFFF200 && addr <= 0xFFF7FF) ||
			addr == 0xFFBF34 || addr == 0xFFBF30 || addr == 0xFFBEFA ||
			addr == 0xFFF716 || addr == 0xFFF70A {
			wh[addr]++
		}
	}
	f300pc := map[uint32]int{} // PC histogram of FFF300 reads (find the sweep-wait site)
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if !hookOn {
			return
		}
		if addr == 0xFFF300 || addr == 0xFFF200 {
			rh[addr]++ // the firmware's sweep-wait poll (0x188b6) reads FFF300
			if addr == 0xFFF300 {
				f300pc[m.CPU.Reg(cpu.PC)]++
			}
		}
	}
	dumpWrites := func() string {
		type kv struct {
			a uint32
			n int
		}
		var ks []kv
		for a, n := range wh {
			ks = append(ks, kv{a, n})
		}
		sort.Slice(ks, func(i, j int) bool { return ks[i].n > ks[j].n })
		out := ""
		for i, k := range ks {
			if i >= 8 {
				break
			}
			out += fmt.Sprintf(" %X=%d", k.a, k.n)
		}
		wh = map[uint32]int{}
		return out
	}

	save := func(name string) {
		// Mirror the GUI: stable snapshot while sweeping (grid stays), live while
		// idle (CAL DISP / menus refresh). See Machine.SweepIdle + cmd/gui.
		chip.SetRenderLive(m.SweepIdle())
		out, e := os.Create("../../../screens/" + name + ".png")
		if e != nil {
			t.Fatal(e)
		}
		png.Encode(out, chip.RenderScanoutByCmd())
		out.Close()
	}

	// dumpStream writes the captured HD63484 command-FIFO words (one hex word per
	// line) to a file, for static analysis against the HD63484 manual command set.
	dumpStream := func(name string) {
		ws := chip.CmdTrace()
		out, e := os.Create("../../../screens/" + name + ".txt")
		if e != nil {
			t.Fatal(e)
		}
		for _, w := range ws {
			fmt.Fprintf(out, "%04X\n", w)
		}
		out.Close()
		t.Logf("dumped %d command words to screens/%s.txt", len(ws), name)
	}

	// Decode the command-trace ring into a cal-table-vs-spectrum summary: glyph Y
	// spread (cal table = many rows filling the graph; spectrum = a few annunciator
	// rows), SCLR clears, and APLL trace polylines.
	summarize := func(tag string) {
		ws := chip.CmdTrace()
		s16 := func(v uint16) int {
			if v&0x8000 != 0 {
				return int(v) - 0x10000
			}
			return int(v)
		}
		rows := map[int]int{}
		var nGlyph, nSCLR, nAPLL int
		curY := 0
		for i := 0; i < len(ws); {
			w := ws[i]
			switch {
			case w == 0x8000 && i+2 < len(ws): // AMOVE x y
				curY = s16(ws[i+2])
				i += 3
			case w == 0x8400 && i+2 < len(ws): // RMOVE dx dy
				curY += s16(ws[i+2])
				i += 3
			case w == 0x1800 && i+1 < len(ws) && ws[i+1] == 0x000A: // WPTN glyph
				rows[curY]++
				nGlyph++
				i += 16
			case (w & 0xFFFC) == 0x5C00:
				nSCLR++
				i++
			case (w&0xFC00) == 0x9800 || (w&0xFC00) == 0x9C00:
				nAPLL++
				i++
			default:
				i++
			}
		}
		minY, maxY := 1<<30, -(1 << 30)
		for y := range rows {
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
		bf34 := m.Bus.Read(0xFFBF34, bus.Long)
		// top f300-read PCs
		type pc struct {
			a uint32
			n int
		}
		var pcs []pc
		for a, n := range f300pc {
			pcs = append(pcs, pc{a, n})
		}
		sort.Slice(pcs, func(i, j int) bool { return pcs[i].n > pcs[j].n })
		f300pcStr := ""
		for i, p := range pcs {
			if i >= 5 {
				break
			}
			f300pcStr += fmt.Sprintf(" %X=%d", p.a, p.n)
		}
		f300pc = map[uint32]int{}
		reads := fmt.Sprintf(" rdF300=%d rdF200=%d", rh[0xFFF300], rh[0xFFF200])
		rh = map[uint32]int{}
		t.Logf("%-22s glyphs=%-5d glyphRows=%-3d Yspan=[%d..%d] SCLR=%-4d APLL=%-4d litWords=%-6d bf34=0x%X PC=0x%X\n    writes:%s\n    reads:%s  f300readPCs:%s",
			tag, nGlyph, len(rows), minY, maxY, nSCLR, nAPLL, chip.CoreLitWords(), bf34, m.CPU.Reg(cpu.PC), dumpWrites(), reads, f300pcStr)
	}

	// ── Stage 1: baseline spectrum display ──
	hookOn = true // start counting sweep/analog-region writes
	chip.EnableCmdTrace(400_000)
	m.bootLoop(20_000_000, nil) // a few more sweeps (SweepDrive still on)
	summarize("1-baseline-spectrum")
	save("diag_1_baseline")
	dumpStream("cmdtrace_op")

	// ── Stage 1b: spectrum display, forced sweep OFF — does the firmware keep
	// polling FFF300 (autonomously wanting a sweep) or go quiet? This decides
	// whether FFF300-read is a clean, injection-independent "wants sweep" signal. ──
	m.SweepDrive = false
	chip.EnableCmdTrace(400_000)
	m.bootLoop(20_000_000, nil)
	summarize("1b-spectrum-no-force")
	m.SweepDrive = true

	// ── Stage 2: TYPE "CAL DISP;" on the AT keyboard, then Enter (the path the
	// GUI uses — IRQ4 byte-inject + operating-loop echo/parse) ──
	fireIRQ4Drain := func() {
		// The IRQ4 handler routes to the AT-keyboard PIT path (0x26DC) only when
		// $b05f bit0 is CLEAR (set ⇒ HP-IB f160 path, which never drains the AT
		// FIFO). Force the AT path each IRQ. Cap iterations so a non-consuming
		// firmware state can't spin forever.
		for n := 0; m.ATKeyboard.Pending() && n < 8; n++ {
			b05f := byte(m.Bus.Read(0xFFB05F, bus.Byte)) &^ 0x01
			m.Bus.Write(0xFFB05F, bus.Byte, uint32(b05f))
			m.CPU.SetIRQ(4)
			m.CPU.Run(bootIRQServiceCost)
			m.CPU.SetIRQ(0)
			m.bootLoop(200_000, nil) // let IRQ4 handler + operating tick run
		}
		m.bootLoop(800_000, nil) // settle / echo
	}
	typeKey := func(k device.ATKey) {
		m.ATKeyboard.Enqueue(device.ATMake(k)...)
		fireIRQ4Drain()
		m.ATKeyboard.Enqueue(device.ATBreak(k)...)
		fireIRQ4Drain()
	}
	chip.EnableCmdTrace(400_000)
	for _, r := range "CAL DISP;" {
		typeKey(atCharKeys[r])
	}
	typeKey(device.ATKeyEnter)
	summarize("2-typed-CAL-DISP")
	save("diag_2_aftercal")
	dumpStream("cmdtrace_cal")

	// ── Stage 3: drive the operating loop WITHOUT the forced sweep ──
	m.SweepDrive = false
	chip.EnableCmdTrace(400_000)
	m.bootLoop(20_000_000, nil)
	summarize("3-post-CAL-no-sweep")
	save("diag_3_nosweep")

	// ── Stage 4: drive the operating loop WITH the forced sweep ──
	m.SweepDrive = true
	chip.EnableCmdTrace(400_000)
	m.bootLoop(20_000_000, nil)
	summarize("4-post-CAL-with-sweep")
	save("diag_4_withsweep")

	t.Logf("PC after run: 0x%X", m.CPU.Reg(cpu.PC))
}

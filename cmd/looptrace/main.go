// Command looptrace boots to steady state and answers, for the trace-draw
// blocker: (a) does PC ever enter the trace-process (0x20A40) or sweep-done
// (0x4E730) regions? (b) does $b0a0 bit11 (the trace-draw busy gate) ever
// clear? (c) what is the exact innermost spin PC? It drives the sweep the
// hardware way (IRQ1 step + IRQ6 capture) so acquisition progresses.
package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	for done := 0; done < 60_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}

	pcHist := map[uint32]int{}
	// DLP command-dispatch trace: at scheduler entry 0x349B6, a0 = the DLP
	// sub-source the trampoline pushed, and the index word is at (a7+4). The
	// sequence of these IS the DLP command execution order — the loop shows
	// directly.
	type cmdRec struct {
		src uint32
		idx uint16
	}
	var cmdSeq []cmdRec
	var parsed []byte // char stream read by the DLP text parser at 0x34F30
	const chunks = 6000
	for i := 0; i < chunks; i++ {
		for s := 0; s < 300; s++ {
			if m.CPU.Step() != nil {
				break
			}
			pc := m.CPU.Reg(cpu.PC)
			pcHist[pc]++
			if pc == 0x349B6 && len(cmdSeq) < 4000 {
				cmdSeq = append(cmdSeq, cmdRec{
					src: m.CPU.Reg(cpu.A0),
					idx: uint16(m.Bus.Read(m.CPU.Reg(cpu.A7)+4, bus.Word)),
				})
			}
			if pc == 0x34F30 && len(parsed) < 8000 { // parser just read a char into d0
				parsed = append(parsed, byte(m.CPU.Reg(cpu.D0)))
			}
		}
		if i%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		// Drive a full sweep (IRQ1 step + IRQ6 capture).
		for n := 0; n < 450 && m.CPU.Reg(cpu.A5) < rdL(0xFFBF30); n++ {
			m.CPU.SetIRQ(1)
			m.CPU.Run(250)
			m.CPU.SetIRQ(0)
			m.Bus.Write(0xFFF200, bus.Word, uint32(0x0140+(n%200)))
			m.CPU.SetIRQ(6)
			m.CPU.Run(250)
			m.CPU.SetIRQ(0)
		}
	}
	// Identify each DLP sub-source pointer by the command whose handler lea's
	// it (load the jumptable name map from /tmp/jt.txt if present).
	names := loadCmdNames("/tmp/jt.txt")
	id := func(src uint32) string {
		// handlers sit just above their source ptr; find nearest handler <= src+0x400
		best, bestName := uint32(0), ""
		for h, nm := range names {
			if h >= src && h < src+0x400 && (best == 0 || h < best) {
				best, bestName = h, nm
			}
		}
		if bestName != "" {
			return bestName
		}
		return "?"
	}
	// Show the DLP source text the parser is chewing on (the loop is visible
	// as a repeating substring).
	clean := make([]byte, 0, len(parsed))
	for _, b := range parsed {
		if b >= 32 && b < 127 {
			clean = append(clean, b)
		} else {
			clean = append(clean, '.')
		}
	}
	fmt.Printf("parsed char stream (%d chars at 0x34F30):\n%s\n\n", len(parsed), string(clean))
	fmt.Printf("DLP command-dispatch trace: %d scheduler entries captured\n", len(cmdSeq))
	// Print the first ~40 in order (the script's command sequence + loop).
	fmt.Println("command sequence (src ptr, idx, nearest cmd):")
	for i, c := range cmdSeq {
		if i >= 48 {
			break
		}
		fmt.Printf("  [%02d] src=%06X idx=%04X  %s\n", i, c.src, c.idx, id(c.src))
	}
	// Tally distinct source ptrs to see what dominates the loop.
	cnt := map[uint32]int{}
	idxOf := map[uint32]uint16{}
	for _, c := range cmdSeq {
		cnt[c.src]++
		idxOf[c.src] = c.idx
	}
	type sc struct {
		s uint32
		n int
	}
	var scs []sc
	for s, n := range cnt {
		scs = append(scs, sc{s, n})
	}
	sort.Slice(scs, func(i, j int) bool { return scs[i].n > scs[j].n })
	fmt.Println("top sub-sources by dispatch count:")
	for i, e := range scs {
		if i >= 14 {
			break
		}
		fmt.Printf("  src=%06X idx=%04X x%-5d  %s\n", e.s, idxOf[e.s], e.n, id(e.s))
	}
}

// loadCmdNames parses cmd/jumptable output lines like
// "  __GGTSWSW (80 06 17) slot 0x7246A → jmp 0x066296" into handler→name.
func loadCmdNames(path string) map[uint32]string {
	out := map[uint32]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	re := regexp.MustCompile(`^\s*(\S+)\s+\([0-9A-Fa-f ]+\)\s+slot\s+\S+\s+\S+\s+jmp\s+0x([0-9A-Fa-f]+)`)
	for _, line := range strings.Split(string(b), "\n") {
		if mch := re.FindStringSubmatch(line); mch != nil {
			var h uint64
			fmt.Sscanf(mch[2], "%x", &h)
			out[uint32(h)] = mch[1]
		}
	}
	return out
}

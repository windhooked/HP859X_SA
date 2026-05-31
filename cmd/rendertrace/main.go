// Command rendertrace cracks the table-dispatched boot status render. It
// fast-forwards exactly to the render call site (ROM 0x184B6, jsr fcn.17546),
// then single-steps fcn.17546 maintaining a depth counter (jsr/bsr +1, rts -1)
// to dump the call tree THROUGH the slot dispatch — defeating the A6-frame
// backtrace problem. Each distinct call target is reported with the status-word
// reads + ACRTC text draws seen inside it, so the annunciator drawer + its
// status condition fall out.
package main

import (
	"fmt"
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

	// fast-forward exactly to the render call site
	const renderCall = 0x184B6
	hit := false
	for done := 0; done < 250_000_000; {
		n, stopped := m.CPU.RunUntil(2000, renderCall)
		done += n
		if stopped {
			hit = true
			break
		}
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	if !hit {
		fmt.Println("never reached render call 0x184B6")
		return
	}
	fmt.Printf("reached render call 0x184B6 (PC=%06X). Tracing fcn.17546...\n\n", m.CPU.Reg(cpu.PC))

	// record per-call-target observations
	type obs struct {
		calls int
		acrtc int      // ACRTC data writes (glyph/vector draws)
		reads []uint32 // distinct RAM status-word addresses read
		seen  map[uint32]bool
	}
	bytarget := map[uint32]*obs{}
	curTarget := uint32(0x17546)
	var stack []uint32 // target call stack

	m.Bus.OnRead = func(a uint32, sz bus.Size, v uint32) {
		if a >= 0xFFB000 && a <= 0xFFBFFF && sz == bus.Word { // status-var region
			o := bytarget[curTarget]
			if o != nil && !o.seen[a] {
				o.seen[a] = true
				o.reads = append(o.reads, a)
			}
		}
	}
	m.Bus.OnWrite = func(a uint32, sz bus.Size, v uint32) {
		if a == 0xFFF5FE {
			if o := bytarget[curTarget]; o != nil {
				o.acrtc++
			}
		}
	}
	getObs := func(t uint32) *obs {
		if bytarget[t] == nil {
			bytarget[t] = &obs{seen: map[uint32]bool{}}
		}
		return bytarget[t]
	}
	getObs(0x17546)

	depth := 0
	for steps := 0; steps < 4_000_000; steps++ {
		pc := m.CPU.Reg(cpu.PC)
		mn, _ := m.CPU.Disasm(pc)
		isCall := strings.HasPrefix(mn, "jsr") || strings.HasPrefix(mn, "bsr")
		isRet := strings.HasPrefix(mn, "rts") || strings.HasPrefix(mn, "rte")
		if m.CPU.Step() != nil {
			break
		}
		if isCall {
			depth++
			stack = append(stack, curTarget)
			curTarget = m.CPU.Reg(cpu.PC) // the call target (function entry)
			getObs(curTarget).calls++
		} else if isRet {
			depth--
			if len(stack) > 0 {
				curTarget = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
			}
			if depth <= 0 {
				fmt.Printf("fcn.17546 returned after %d steps\n", steps)
				break
			}
		}
	}

	// report call targets that drew text (acrtc>0) or read status vars
	var ts []uint32
	for t := range bytarget {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return bytarget[ts[i]].acrtc > bytarget[ts[j]].acrtc })
	fmt.Println("\ncall targets in the render (target: calls, acrtcDraws, statusReads):")
	for _, t := range ts {
		o := bytarget[t]
		if o.acrtc == 0 && len(o.reads) == 0 {
			continue
		}
		d, _ := m.CPU.Disasm(t)
		fmt.Printf("  %06X  calls=%-3d draws=%-5d reads=%v  %s\n", t, o.calls, o.acrtc, fmtAddrs(o.reads), d)
	}
}

func fmtAddrs(a []uint32) string {
	if len(a) == 0 {
		return "-"
	}
	var s []string
	for i, x := range a {
		if i >= 8 {
			s = append(s, "...")
			break
		}
		s = append(s, fmt.Sprintf("%X", x&0xFFFF))
	}
	return strings.Join(s, ",")
}

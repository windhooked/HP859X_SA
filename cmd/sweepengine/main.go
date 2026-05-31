// Command sweepengine drives a faithful continuous-sweep cycle per the service
// manual (HSWP/ADC_SYNC model) and checks whether the firmware processes+draws
// the trace. Per the manual: HSWP high=sweeping, low=retrace=done; one trace
// point per ADC_SYNC; completion inferred from HSWP-low + trace-buffer fill
// (A5->FFBF30). The sweep state machine fcn.5ECEE tests befa bit13 (sweep done,
// set by IRQ1) and 0xFFF300 bit11 (sweep-state). We drive IRQ1(step)+IRQ6
// (capture) for 401 points with a synthetic detector (noise floor + a peak),
// manage the $f300 sweep-state bit, and report trace draws (Lines jump) +
// whether the sweep state machine / trace-draw DLP are reached. Renders to
// ./screens/.
package main

import (
	"fmt"
	"image/png"
	"os"

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
	rdW := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }

	// Boot to the live UI state (~160M, past the reg-3 un-freeze).
	for done := 0; done < 160_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("booted: Lines=%d bf34=%08X befa=%04X A5=%08X bf30=%08X f300=%04X\n",
		m.MMIO.Display.Lines, rdL(0xFFBF34), rdW(0xFFBEFA), m.CPU.Reg(cpu.A5), rdL(0xFFBF30), rdW(0xFFF300))

	linesBefore := m.MMIO.Display.Lines
	sweptMachine, traceDLP := 0, 0
	detector := func(pt int) uint32 {
		// noise floor near graticule bottom + a peak around the centre (a 300 MHz
		// cal-like signal). ADC range is ~[-0x200,+0x1FF]; bottom ~ small value.
		v := 0x40
		d := pt - 200
		if d < 0 {
			d = -d
		}
		if d < 30 {
			v += (30 - d) * 12 // a peak
		}
		return uint32(v)
	}

	for sweep := 0; sweep < 200; sweep++ {
		// Arm if needed (firmware sets bf34=0x40B8 when it wants to capture).
		// Drive a full 401-point sweep: HSWP high (sweep-in-progress = $f300 bit11),
		// step+capture each point, then HSWP low (retrace) at buffer fill.
		m.Bus.Write(0xFFF300, bus.Word, uint32(rdW(0xFFF300))|0x0800) // bit11 = sweeping
		for pt := 0; pt < 410 && m.CPU.Reg(cpu.A5) < rdL(0xFFBF30); pt++ {
			m.CPU.SetIRQ(1) // sweep step (sets befa bit13, programs DACs)
			m.CPU.Run(200)
			m.CPU.SetIRQ(0)
			m.Bus.Write(0xFFF200, bus.Word, detector(pt)) // ADC_SYNC point value
			m.CPU.SetIRQ(6)                               // capture
			m.CPU.Run(200)
			m.CPU.SetIRQ(0)
		}
		// Retrace: HSWP low (sweep done). Let the firmware process+draw.
		m.Bus.Write(0xFFF300, bus.Word, uint32(rdW(0xFFF300))&^uint32(0x0800))
		for k := 0; k < 1500; k++ {
			for s := 0; s < 8; s++ {
				if m.CPU.Step() != nil {
					break
				}
				pc := m.CPU.Reg(cpu.PC)
				if pc >= 0x5ECEE && pc < 0x5ED80 {
					sweptMachine++
				}
				if pc >= 0x65986 && pc < 0x659A0 {
					traceDLP++
				}
			}
			if k%5 == 0 {
				m.CPU.SetIRQ(5)
				m.CPU.Step()
				m.CPU.SetIRQ(0)
			}
		}
		if m.MMIO.Display.Lines > linesBefore+50 {
			fmt.Printf("** trace likely drawn at sweep %d: Lines %d -> %d\n", sweep, linesBefore, m.MMIO.Display.Lines)
			break
		}
	}
	fmt.Printf("after sweeps: Lines %d -> %d (drawn=%v)  sweepSM hits=%d  traceDLP(__GTTDRW) hits=%d\n",
		linesBefore, m.MMIO.Display.Lines, m.MMIO.Display.Lines > linesBefore+50, sweptMachine, traceDLP)
	fmt.Printf("  befa=%04X bf34=%08X A5=%08X\n", rdW(0xFFBEFA), rdL(0xFFBF34), m.CPU.Reg(cpu.A5))

	if f, err := os.Create("screens/sweepengine.png"); err == nil {
		png.Encode(f, m.MMIO.Display.RenderFrame())
		f.Close()
		fmt.Println("wrote screens/sweepengine.png")
	}
}

// Command runlevel investigates reaching the operating (run) level with the
// RF/IF region mapped: it boots with a configurable 0x200A3C, histograms where
// the firmware spends time (halted on self-test vs looping in the operating
// code), reports trace activity, and renders the screen. Drives the pragmatic
// "reach run level, UNCAL tolerated" path.
//
// Usage:
//
//	go run ./cmd/runlevel/ [a3c-hex] [out.png]
package main

import (
	"fmt"
	"image/png"
	"os"
	"strconv"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	musashi "github.com/windhooked/HP859X_SA/pkg/emu/cpu/musashi"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// detMMIO wraps HP8593AMMIO and synthesises a detector reading on 0xFFF200
// (noise floor + a peak). The ADC mux-select register hasn't been pinned down
// yet (port B 0xFFF003 tried, made the firmware hang under sweep-drive — so
// either it's not the mux or my response values confuse the cal loop).
type detMMIO struct {
	inner *device.HP8593AMMIO
	pos   int
}

func (m *detMMIO) Read(a uint32, s bus.Size) uint32 {
	// HP-IB self-test gate at 0x10048: f610=0xFF, f612 has bits 0xEC set.
	if s == bus.Byte && a == 0x610 {
		return 0xFF
	}
	if s == bus.Byte && a == 0x612 {
		return 0xEC
	}
	if s == bus.Word && a == 0x200 {
		m.pos = (m.pos + 1) % 401
		v := 0x0140
		d := m.pos - 200
		if d < 0 {
			d = -d
		}
		if d < 25 {
			v += (25 - d) * 0x60
		}
		return uint32(v)
	}
	return m.inner.Read(a, s)
}
func (m *detMMIO) Write(a uint32, s bus.Size, v uint32) { m.inner.Write(a, s, v) }

type rfif struct {
	b   [0x10000]byte
	a3c uint32
}

func (r *rfif) Read(a uint32, s bus.Size) uint32 {
	if a == 0xA3C {
		return r.a3c
	}
	switch s {
	case bus.Byte:
		return uint32(r.b[a])
	case bus.Word:
		return uint32(r.b[a])<<8 | uint32(r.b[a+1])
	default:
		return uint32(r.b[a])<<24 | uint32(r.b[a+1])<<16 | uint32(r.b[a+2])<<8 | uint32(r.b[a+3])
	}
}
func (r *rfif) Write(a uint32, s bus.Size, v uint32) {
	switch s {
	case bus.Byte:
		r.b[a] = byte(v)
	case bus.Word:
		r.b[a] = byte(v >> 8)
		r.b[a+1] = byte(v)
	default:
		r.b[a] = byte(v >> 24)
		r.b[a+1] = byte(v >> 16)
		r.b[a+2] = byte(v >> 8)
		r.b[a+3] = byte(v)
	}
}

func main() {
	a3c := uint32(1)
	out := "runlevel.png"
	if len(os.Args) > 1 {
		if n, err := strconv.ParseUint(os.Args[1], 16, 32); err == nil {
			a3c = uint32(n)
		}
	}
	if len(os.Args) > 2 {
		out = os.Args[2]
	}

	img, _ := romloader.LoadDir("hp8593a_eeproms")
	mmio := &detMMIO{inner: device.NewHP8593AMMIO()}
	rf := &rfif{a3c: a3c}
	b := &bus.Bus{}
	b.OnFault = func(a uint32, s bus.Size, w bool) uint32 { return 0 }
	b.Map(0x000000, uint32(len(img)), "ROM", bus.NewROM(img))
	b.Map(0x200000, 0x10000, "RFIF", rf)
	b.Map(0xEF4000, 0x20, "FrontPanel", device.NewFrontPanel())
	b.Map(0xEF8000, 0x100, "PIT", bus.NewRAM(0x100))
	b.Map(0xFEC000, 0x4000, "TestRAM", bus.NewRAM(0x4000))
	b.Map(0xFF0000, 0xF000, "RAM", bus.NewRAM(0xF000))
	b.Map(device.MMIOBase, device.MMIOSize, "MMIO", mmio)

	c, _ := musashi.New(b)
	c.Reset()
	lb := emutest.NewLoopBreaker(50)

	// Boot in bulk first (IRQ5 timer only).
	for done := 0; done < 30_000_000; done += 2000 {
		c.Run(2000)
		lb.Check(c.Reg(cpu.PC), c.SetReg)
		if (done/2000)%5 == 0 {
			c.SetIRQ(5)
			c.Run(400)
			c.SetIRQ(0)
		}
	}

	rd := func(a uint32) uint32 { return b.Read(a, bus.Long) }
	fmt.Printf("after boot: bfea(IRQ6 disp)=%06X bfe6(buf end)=%06X A5=%06X\n",
		rd(0xFFBFEA), rd(0xFFBFE6), c.Reg(cpu.A5))

	// Sweep-drive phase: ramp 0x200A3C as the points-acquired counter in lockstep
	// with IRQ6 sample captures to drive the acquisition (kept for trace work).
	sweepPos := 0
	for done := 0; done < 40_000_000; done += 2000 {
		c.Run(2000)
		lb.Check(c.Reg(cpu.PC), c.SetReg)
		if (done/2000)%5 == 0 {
			c.SetIRQ(5)
			c.Run(400)
			c.SetIRQ(0)
		}
		sweepPos++
		if sweepPos > 401 {
			sweepPos = 0
		}
		rf.a3c = uint32(sweepPos)
		c.SetIRQ(6)
		c.Run(250)
		c.SetIRQ(0)
		if sweepPos == 1 {
			c.SetIRQ(1)
			c.Run(250)
			c.SetIRQ(0)
		}
	}

	d := mmio.inner.Display
	fmt.Printf("a3c=%X finalPC=%06X  SCI moves=%d lines=%d glyphs=%d\n",
		a3c, c.Reg(cpu.PC), d.Moves, d.Lines, d.Glyphs)
	fmt.Printf("after sweep-drive: bfea=%06X bfe6=%06X A5=%06X\n",
		rd(0xFFBFEA), rd(0xFFBFE6), c.Reg(cpu.A5))

	f, _ := os.Create(out)
	defer f.Close()
	png.Encode(f, d.RenderFrame())
	fmt.Printf("wrote %s\n", out)
}

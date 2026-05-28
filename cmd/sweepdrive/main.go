// Command sweepdrive prototypes a mock RF/IF acquisition peripheral at the
// 0x200000 region. The firmware gates every sweep on `cmpi.w #1,$200a3c.l;
// ble skip` — 0x200A3C is the hardware "sweep points acquired" counter (compared
// against 0x191 = 401 points). Unmapped it reads 0, so sweeps are always
// skipped. This maps a logging mock that returns a configurable point count and
// records which 0x200000-region addresses the firmware reads once the gate
// opens — revealing where it expects the trace sample data.
//
// Usage:
//
//	go run ./cmd/sweepdrive/ [points-hex] [out.png]   # default 191 (=401)
package main

import (
	"fmt"
	"image/png"
	"os"
	"sort"
	"strconv"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	musashi "github.com/windhooked/HP859X_SA/pkg/emu/cpu/musashi"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
	"github.com/windhooked/HP859X_SA/internal/emutest"
)

const rfBase = 0x200000

type rfif struct {
	b       [0x10000]byte
	points  uint32 // value returned for 0x200A3C
	reads   map[uint32]int
	enabled *bool
}

func (r *rfif) Read(addr uint32, sz bus.Size) uint32 {
	if *r.enabled {
		r.reads[rfBase+addr]++
	}
	if addr == 0xA3C { // 0x200A3C — points acquired
		return r.points
	}
	if int(addr)+int(sz) <= len(r.b) {
		switch sz {
		case bus.Byte:
			return uint32(r.b[addr])
		case bus.Word:
			return uint32(r.b[addr])<<8 | uint32(r.b[addr+1])
		default:
			return uint32(r.b[addr])<<24 | uint32(r.b[addr+1])<<16 | uint32(r.b[addr+2])<<8 | uint32(r.b[addr+3])
		}
	}
	return 0
}

func (r *rfif) Write(addr uint32, sz bus.Size, val uint32) {
	if int(addr)+int(sz) > len(r.b) {
		return
	}
	switch sz {
	case bus.Byte:
		r.b[addr] = byte(val)
	case bus.Word:
		r.b[addr] = byte(val >> 8)
		r.b[addr+1] = byte(val)
	default:
		r.b[addr] = byte(val >> 24)
		r.b[addr+1] = byte(val >> 16)
		r.b[addr+2] = byte(val >> 8)
		r.b[addr+3] = byte(val)
	}
}

func main() {
	points := uint32(0x191)
	out := "sweep.png"
	if len(os.Args) > 1 {
		if n, err := strconv.ParseUint(os.Args[1], 16, 32); err == nil {
			points = uint32(n)
		}
	}
	if len(os.Args) > 2 {
		out = os.Args[2]
	}

	img, _ := romloader.LoadDir("hp8593a_eeproms")
	var enabled bool
	rf := &rfif{points: points, reads: map[uint32]int{}, enabled: &enabled}
	mmio := device.NewHP8593AMMIO()

	b := &bus.Bus{}
	b.OnFault = func(a uint32, s bus.Size, w bool) uint32 { return 0 }
	b.Map(0x000000, uint32(len(img)), "ROM", bus.NewROM(img))
	b.Map(rfBase, 0x10000, "RFIF", rf)
	b.Map(0xEF4000, 0x20, "FrontPanel", device.NewFrontPanel())
	b.Map(0xEF8000, 0x100, "PIT", bus.NewRAM(0x100))
	b.Map(0xFEC000, 0x4000, "TestRAM", bus.NewRAM(0x4000))
	b.Map(0xFF0000, 0xF000, "RAM", bus.NewRAM(0xF000))
	b.Map(0xFFF000, 0x1000, "MMIO", mmio)

	c, _ := musashi.New(b)
	c.Reset()

	const chunk = 2000
	const irqPeriod = 5
	lb := emutest.NewLoopBreaker(50)
	glyph0, move0 := 0, 0
	for done := 0; done < 60_000_000; done += chunk {
		c.Run(chunk)
		lb.Check(c.Reg(cpu.PC), c.SetReg)
		if (done/chunk)%irqPeriod == 0 {
			c.SetIRQ(5)
			c.Run(400)
			c.SetIRQ(0)
		}
		if !enabled && c.Reg(cpu.PC) >= 0x5000 && c.Reg(cpu.PC) < 0x12000 {
			enabled = true
			glyph0, move0 = mmio.Display.Glyphs, mmio.Display.Moves
		}
	}

	fmt.Printf("points(0x200A3C)=%04X  finalPC=%06X\n", points, c.Reg(cpu.PC))
	fmt.Printf("SCI after operating: +%d moves, +%d glyphs (total moves=%d glyphs=%d)\n",
		mmio.Display.Moves-move0, mmio.Display.Glyphs-glyph0, mmio.Display.Moves, mmio.Display.Glyphs)

	fmt.Println("--- 0x200000-region reads (operating), top 25 by freq ---")
	addrs := make([]uint32, 0, len(rf.reads))
	for a := range rf.reads {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return rf.reads[addrs[i]] > rf.reads[addrs[j]] })
	for i, a := range addrs {
		if i >= 25 {
			break
		}
		fmt.Printf("  %06X  reads=%d\n", a, rf.reads[a])
	}

	f, _ := os.Create(out)
	defer f.Close()
	png.Encode(f, mmio.Display.RenderFrame())
	fmt.Printf("wrote %s\n", out)
}

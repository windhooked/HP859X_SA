// Command scianalyze captures the ordered SCI data-FIFO stream during a boot and
// categorises the commands by what follows each MOVE (0x8000) marker. Glyph
// packets start with 0x1800; anything else is a vector / line-draw / control
// command that SCIDisplay currently skips. This dumps the distinct non-glyph
// post-MOVE opcodes and sample payloads so the vector command set can be decoded.
//
// Usage:
//
//	go run ./cmd/scianalyze/ [a3c-hex]   # optional 0x200A3C mock value (RF/IF)
package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	musashi "github.com/windhooked/HP859X_SA/pkg/emu/cpu/musashi"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// capMMIO wraps the real MMIO and records the ordered 0x5FE data words.
type capMMIO struct {
	inner  *device.HP8593AMMIO
	data   []uint16
	cap    int
	enable *bool
}

func (m *capMMIO) Read(a uint32, s bus.Size) uint32 { return m.inner.Read(a, s) }
func (m *capMMIO) Write(a uint32, s bus.Size, v uint32) {
	m.inner.Write(a, s, v)
	if *m.enable && s == bus.Word && a == 0x5FE && len(m.data) < m.cap {
		m.data = append(m.data, uint16(v))
	}
}

// rfif is a minimal RF/IF mock returning a fixed 0x200A3C.
type rfif struct {
	b   [0x10000]byte
	a3c uint32
}

func (r *rfif) Read(a uint32, s bus.Size) uint32 {
	if a == 0xA3C {
		return r.a3c
	}
	return 0
}
func (r *rfif) Write(a uint32, s bus.Size, v uint32) {}

func main() {
	a3c := uint32(0)
	if len(os.Args) > 1 {
		if n, err := strconv.ParseUint(os.Args[1], 16, 32); err == nil {
			a3c = uint32(n)
		}
	}

	img, _ := romloader.LoadDir("hp8593a_eeproms")
	var enable bool
	cm := &capMMIO{inner: device.NewHP8593AMMIO(), cap: 400_000, enable: &enable}

	b := &bus.Bus{}
	b.OnFault = func(a uint32, s bus.Size, w bool) uint32 { return 0 }
	b.Map(0x000000, uint32(len(img)), "ROM", bus.NewROM(img))
	if a3c != 0 {
		b.Map(0x200000, 0x10000, "RFIF", &rfif{a3c: a3c})
	}
	b.Map(0xEF4000, 0x20, "FrontPanel", device.NewFrontPanel())
	b.Map(0xEF8000, 0x100, "PIT", bus.NewRAM(0x100))
	b.Map(0xFEC000, 0x4000, "TestRAM", bus.NewRAM(0x4000))
	b.Map(0xFF0000, 0xF000, "RAM", bus.NewRAM(0xF000))
	b.Map(0xFFF000, 0x1000, "MMIO", cm)

	c, _ := musashi.New(b)
	c.Reset()
	lb := emutest.NewLoopBreaker(50)
	for done := 0; done < 30_000_000; done += 2000 {
		c.Run(2000)
		lb.Check(c.Reg(cpu.PC), c.SetReg)
		if (done/2000)%5 == 0 {
			c.SetIRQ(5)
			c.Run(400)
			c.SetIRQ(0)
		}
		if !enable && c.Reg(cpu.PC) >= 0x5000 && c.Reg(cpu.PC) < 0x12000 {
			enable = true
		}
	}

	// Segment the stream by MOVE (0x8000): each segment = X, Y, then payload up
	// to the next MOVE. Classify glyph (payload[0]==0x1800) vs other.
	d := cm.data
	type opInfo struct {
		count   int
		samples [][]uint16
	}
	ops := map[uint16]*opInfo{}
	glyphs, vectors, segs := 0, 0, 0
	i := 0
	for i < len(d) {
		if d[i] != 0x8000 {
			i++
			continue
		}
		// MOVE at i; X=d[i+1], Y=d[i+2]; payload from i+3 to next 0x8000.
		j := i + 3
		for j < len(d) && d[j] != 0x8000 {
			j++
		}
		segs++
		if i+3 <= len(d) && i+3 < j {
			first := d[i+3]
			if first == 0x1800 {
				glyphs++
			} else {
				vectors++
				oi := ops[first]
				if oi == nil {
					oi = &opInfo{}
					ops[first] = oi
				}
				oi.count++
				if len(oi.samples) < 3 {
					end := j
					if end > i+3+8 {
						end = i + 3 + 8
					}
					seg := append([]uint16{d[i+1], d[i+2]}, d[i+3:end]...)
					oi.samples = append(oi.samples, seg)
				}
			}
		}
		i = j
	}

	fmt.Printf("a3c=%X  captured %d data words; %d MOVE segments: %d glyph, %d non-glyph\n",
		a3c, len(d), segs, glyphs, vectors)
	fmt.Println("--- non-glyph post-MOVE opcodes (first word after X,Y), by freq ---")
	keys := make([]uint16, 0, len(ops))
	for k := range ops {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return ops[keys[a]].count > ops[keys[b]].count })
	for _, k := range keys {
		oi := ops[k]
		fmt.Printf("  op=%04X  count=%d\n", k, oi.count)
		for _, s := range oi.samples {
			fmt.Printf("      X=%04X Y=%04X | payload:", s[0], s[1])
			for _, w := range s[2:] {
				fmt.Printf(" %04X", w)
			}
			fmt.Println()
		}
	}
}

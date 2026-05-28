// Command renderframe boots the HP 8593A into its operating loop and writes the
// SCI display framebuffer to a PNG so the rendered screen can be inspected.
//
// Usage:
//
//	go run ./cmd/renderframe/ [cycles] [out.png]
//
// The machine is always constructed with a valid-checksum cal NVRAM
// (machine.New8593A calls CalNVRAM.Synthesize automatically). The legacy
// --synth flag is accepted but has no additional effect.
package main

import (
	"fmt"
	"image/png"
	"os"
	"strconv"

	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	cycles := 30_000_000
	out := "frame.png"
	synth := false

	positional := []string{}
	for _, a := range os.Args[1:] {
		switch a {
		case "--synth", "-synth":
			synth = true
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) > 0 {
		if n, err := strconv.Atoi(positional[0]); err == nil && n > 0 {
			cycles = n
		}
	}
	if len(positional) > 1 {
		out = positional[1]
	}

	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m, err := machine.New8593A(rom)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if synth {
		m.CalNVRAM.Synthesize()
	}
	m.CPU.Reset()
	m.BootToOperating(cycles)

	d := m.MMIO.Display
	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, d.RenderFrame()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("synth=%v  final PC=%06X  moves=%d glyphs=%d lines=%d rects=%d dots=%d  paints=%d paintWords=%d  wrote %s\n",
		synth, m.CPU.Reg(cpu.PC), d.Moves, d.Glyphs, d.Lines, d.Rects, d.Dots,
		d.Paints, d.PaintWords, out)
}

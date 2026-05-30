// Command gui is a live Ebiten window for the virtual HP 8593A: it boots the
// real Rev L firmware from reset, renders the HD63484 framebuffer in real time,
// and maps the host keyboard onto the front-panel key matrix so you can probe
// keys interactively and watch the display respond.
//
// Run on a machine with a display (the firmware boots to its UI in ~1–2 s of
// wall-clock):
//
//	DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib go run ./cmd/gui/
//
// Keys:
//   - The first 48 host keys (1234567890-=, QWERTYUIOP[], ASDFGHJKL;', ZXCVBNM,./)
//     map 1:1 onto the 48 front-panel matrix bits (byte 0 bit 0 .. byte 5 bit 7).
//     Press one → that matrix bit is injected + IRQ3 fired; the active (byte,bit)
//     is shown in the window title. This is the interactive sweep for locating a
//     real key (e.g. PRESET) — watch which bit changes the display.
//   - Backspace releases all keys.
//   - The title bar shows cycles, PC, the front-panel state, and the last bit.
package main

import (
	"fmt"
	"log"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

const (
	cyclesPerFrame = 2_000_000 // ~7× a 16 MHz CPU at 60 fps; boot reaches UI in ~1–2 s
	chunkCycles    = 2000
	irqEvery       = 5   // inject an IRQ5 timer tick every N chunks
	irqServiceCost = 400 // cycles for the IRQ handler to run
)

// the 48 host keys mapped 1:1 to the front-panel matrix bits (byte0 bit0 first).
var matrixKeys = []ebiten.Key{
	ebiten.Key1, ebiten.Key2, ebiten.Key3, ebiten.Key4, ebiten.Key5, ebiten.Key6, ebiten.Key7, ebiten.Key8,
	ebiten.Key9, ebiten.Key0, ebiten.KeyMinus, ebiten.KeyEqual, ebiten.KeyQ, ebiten.KeyW, ebiten.KeyE, ebiten.KeyR,
	ebiten.KeyT, ebiten.KeyY, ebiten.KeyU, ebiten.KeyI, ebiten.KeyO, ebiten.KeyP, ebiten.KeyBracketLeft, ebiten.KeyBracketRight,
	ebiten.KeyA, ebiten.KeyS, ebiten.KeyD, ebiten.KeyF, ebiten.KeyG, ebiten.KeyH, ebiten.KeyJ, ebiten.KeyK,
	ebiten.KeyL, ebiten.KeySemicolon, ebiten.KeyQuote, ebiten.KeyZ, ebiten.KeyX, ebiten.KeyC, ebiten.KeyV, ebiten.KeyB,
	ebiten.KeyN, ebiten.KeyM, ebiten.KeyComma, ebiten.KeyPeriod, ebiten.KeySlash, ebiten.KeyBackslash, ebiten.KeyTab, ebiten.KeyGraveAccent,
}

type game struct {
	m        *machine.Machine
	lb       *emutest.LoopBreaker
	fb       *ebiten.Image
	chunks   int
	cycles   uint64
	lastBit  int // last matrix bit index injected (-1 = none)
	keyReads int
}

func (g *game) Update() error {
	// Free-run the CPU for one frame's worth of cycles, mirroring the boot
	// cadence (LoopBreaker + periodic IRQ5) so the firmware reaches and stays
	// in its operating loop.
	for done := 0; done < cyclesPerFrame; done += chunkCycles {
		g.m.CPU.Run(chunkCycles)
		g.lb.Check(g.m.CPU.Reg(cpu.PC), g.m.CPU.SetReg)
		g.chunks++
		g.cycles += chunkCycles
		if g.chunks%irqEvery == 0 {
			g.m.CPU.SetIRQ(5)
			g.m.CPU.Run(irqServiceCost)
			g.m.CPU.SetIRQ(0)
			g.cycles += irqServiceCost
		}
	}

	// Deliver the front-panel interrupt while a key event is pending so the
	// firmware's IRQ3 handler latches it.
	if g.m.FrontPanel.Pending() {
		g.m.CPU.SetIRQ(3)
		g.m.CPU.Run(irqServiceCost)
		g.m.CPU.SetIRQ(0)
	}

	// Host keyboard → front-panel matrix bit (edge-triggered).
	for i, k := range matrixKeys {
		if inpututil.IsKeyJustPressed(k) {
			g.m.FrontPanel.SetBit(i/8, i%8)
			g.lastBit = i
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) {
		g.m.FrontPanel.Release()
		g.lastBit = -1
	}
	if g.m.FrontPanel.Consumed() {
		g.keyReads++
	}
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	img := g.m.MMIO.Display.RenderFrame()
	g.fb.WritePixels(img.Pix)
	screen.DrawImage(g.fb, nil)

	bit := "none"
	if g.lastBit >= 0 {
		bit = fmt.Sprintf("byte%d bit%d", g.lastBit/8, g.lastBit%8)
	}
	ebiten.SetWindowTitle(fmt.Sprintf(
		"HP 8593A  |  %.0fM cyc  PC=%#06x  bc67=%#02x  key=%s  consumed=%v(%d)",
		float64(g.cycles)/1e6, g.m.CPU.Reg(cpu.PC),
		byte(g.m.Bus.Read(0xFFBC67, 1)), bit, g.m.FrontPanel.Consumed(), g.keyReads))
}

func (g *game) Layout(int, int) (int, int) { return device.DisplayWidth, device.DisplayHeight }

func main() {
	img, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		log.Fatal(err)
	}
	m, err := machine.New8593A(img)
	if err != nil {
		log.Fatal(err)
	}
	m.CPU.Reset()

	g := &game{
		m:       m,
		lb:      emutest.NewLoopBreaker(50),
		fb:      ebiten.NewImage(device.DisplayWidth, device.DisplayHeight),
		lastBit: -1,
	}

	ebiten.SetWindowSize(device.DisplayWidth*2, device.DisplayHeight*2)
	ebiten.SetWindowTitle("HP 8593A — booting…")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}

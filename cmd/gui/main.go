// Command gui is a live Ebiten window for the virtual HP 8593A: it boots the
// real Rev L firmware from reset, renders the HD63484 framebuffer in real time,
// and provides two keyboard paths that mirror the real instrument:
//
//  1. AT keyboard path (external keyboard, rear-panel EXT KEYBOARD connector):
//     alphanumeric and function keys are translated to AT Set-2 scan codes and
//     injected into the MC68230 PIT receiver (0xEF8002/0xEF8000), triggering
//     IRQ4. The firmware decodes them and dispatches:
//     - Letters/digits/punctuation: typed text (screen titles, HP-IB commands,
//       DLP programs).
//     - F1–F6: softkeys 1–6 of the current menu.
//     - F7: prefix mode; F8: remote-commands mode.
//     - F9: MKR menu; F10: SPAN menu; F11: AMPLITUDE menu; F12: title recall.
//     - Escape: title mode; PrintScreen: copy screen.
//     (See HP 8590 E/L-Series Programmer's Guide Table 5-8.)
//
//  2. Front-panel matrix path (direct key injection, IRQ3):
//     Host function-key shortcuts for named front-panel keys whose matrix
//     (byte,bit) positions have been probed. Tab cycles through probe bits
//     (legacy matrix-sweep mode) when the named map has no binding. As bit
//     positions are discovered with cmd/keymatrix they are filled into
//     device.FrontPanelKeys and the shortcut works automatically.
//     Current shortcuts: see fpBindings below.
//
//	DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib go run ./cmd/gui/
package main

import (
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// saveScreen writes the current HD63484 framebuffer to screens/gui_<timestamp>.png
// and returns the path, or an error message.
func saveScreen(m *machine.Machine) string {
	img := m.MMIO.Display.Chip.RenderScanoutByCmd()
	ts := time.Now().Format("20060102_150405")
	path := fmt.Sprintf("screens/gui_%s.png", ts)
	f, err := os.Create(path)
	if err != nil {
		return "save error: " + err.Error()
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return "encode error: " + err.Error()
	}
	// Also dump the captured HD63484 command-FIFO stream (16-bit words, hex, one
	// per line) so the draw/clear commands behind the current screen can be
	// decoded. Prefer the armed LINEAR capture (Ctrl+R, records a transition from
	// the arm point); fall back to the steady-state ring (last 16K words).
	trace := m.MMIO.Display.Chip.CmdCapture()
	kind := "capture"
	if len(trace) == 0 {
		trace, kind = m.MMIO.Display.Chip.CmdTrace(), "ring"
	}
	// Dump AR-addressed control-register writes (RAR/MWR/SAR display-window
	// switches etc.) captured during the armed window — these never appear in the
	// command-FIFO stream.
	if ctrl := m.MMIO.Display.Chip.CtrlCapture(); len(ctrl) > 0 {
		cpath := fmt.Sprintf("screens/crt_%s_ctrl.txt", ts)
		if cf, cerr := os.Create(cpath); cerr == nil {
			for _, p := range ctrl {
				fmt.Fprintf(cf, "AR=%02x %04x\n", p[0]&0xff, p[1])
			}
			cf.Close()
		}
	}
	if len(trace) > 0 {
		tpath := fmt.Sprintf("screens/crt_%s.txt", ts)
		if tf, terr := os.Create(tpath); terr == nil {
			for _, w := range trace {
				fmt.Fprintf(tf, "%04x\n", w)
			}
			tf.Close()
			return fmt.Sprintf("saved %s + %s (%d cmd words, %s; +ctrl)", path, tpath, len(trace), kind)
		}
	}
	return "saved " + path
}

const (
	cyclesPerFrame = 2_000_000
	chunkCycles    = 2000
	irqEvery       = 5
	irqServiceCost = 400
	irq4Cost       = 600 // IRQ4 handler is slightly heavier (transport + ring-buf)
)

// atBindings maps Ebiten keys to ATKey codes for the AT keyboard path.
// Covers all alphanumeric, punctuation, and function keys the firmware handles.
var atBindings = map[ebiten.Key]device.ATKey{
	// Letters
	ebiten.KeyA: device.ATKeyA, ebiten.KeyB: device.ATKeyB, ebiten.KeyC: device.ATKeyC,
	ebiten.KeyD: device.ATKeyD, ebiten.KeyE: device.ATKeyE, ebiten.KeyF: device.ATKeyF,
	ebiten.KeyG: device.ATKeyG, ebiten.KeyH: device.ATKeyH, ebiten.KeyI: device.ATKeyI,
	ebiten.KeyJ: device.ATKeyJ, ebiten.KeyK: device.ATKeyK, ebiten.KeyL: device.ATKeyL,
	ebiten.KeyM: device.ATKeyM, ebiten.KeyN: device.ATKeyN, ebiten.KeyO: device.ATKeyO,
	ebiten.KeyP: device.ATKeyP, ebiten.KeyQ: device.ATKeyQ, ebiten.KeyR: device.ATKeyR,
	ebiten.KeyS: device.ATKeyS, ebiten.KeyT: device.ATKeyT, ebiten.KeyU: device.ATKeyU,
	ebiten.KeyV: device.ATKeyV, ebiten.KeyW: device.ATKeyW, ebiten.KeyX: device.ATKeyX,
	ebiten.KeyY: device.ATKeyY, ebiten.KeyZ: device.ATKeyZ,
	// Digits (top row)
	ebiten.Key0: device.ATKey0, ebiten.Key1: device.ATKey1, ebiten.Key2: device.ATKey2,
	ebiten.Key3: device.ATKey3, ebiten.Key4: device.ATKey4, ebiten.Key5: device.ATKey5,
	ebiten.Key6: device.ATKey6, ebiten.Key7: device.ATKey7, ebiten.Key8: device.ATKey8,
	ebiten.Key9: device.ATKey9,
	// Numpad (maps to DATA keypad on the SA front panel when in numeric entry)
	ebiten.KeyNumpad0: device.ATKeyNum0, ebiten.KeyNumpad1: device.ATKeyNum1,
	ebiten.KeyNumpad2: device.ATKeyNum2, ebiten.KeyNumpad3: device.ATKeyNum3,
	ebiten.KeyNumpad4: device.ATKeyNum4, ebiten.KeyNumpad5: device.ATKeyNum5,
	ebiten.KeyNumpad6: device.ATKeyNum6, ebiten.KeyNumpad7: device.ATKeyNum7,
	ebiten.KeyNumpad8: device.ATKeyNum8, ebiten.KeyNumpad9: device.ATKeyNum9,
	ebiten.KeyNumpadDecimal: device.ATKeyNumDecimal,
	ebiten.KeyNumpadEnter:   device.ATKeyNumEnter,
	// Punctuation
	ebiten.KeySpace:        device.ATKeySpace,
	ebiten.KeyEnter:        device.ATKeyEnter,
	ebiten.KeyBackspace:    device.ATKeyBackspace,
	ebiten.KeyEscape:       device.ATKeyEscape,
	ebiten.KeyPeriod:       device.ATKeyPeriod,
	ebiten.KeyComma:        device.ATKeyComma,
	ebiten.KeyMinus:        device.ATKeyMinus,
	ebiten.KeyEqual:        device.ATKeyEquals,
	ebiten.KeySemicolon:    device.ATKeySemicolon,
	ebiten.KeySlash:        device.ATKeySlash,
	ebiten.KeyBackslash:    device.ATKeyBackslash,
	ebiten.KeyBracketLeft:  device.ATKeyBracketLeft,
	ebiten.KeyBracketRight: device.ATKeyBracketRight,
	ebiten.KeyGraveAccent:  device.ATKeyGrave,
	ebiten.KeyApostrophe:   device.ATKeyApostrophe,
	// Function keys → firmware Table 5-8 (Programmer's Guide):
	//   F1–F6 = softkeys 1–6; F7 = prefix; F8 = remote cmds;
	//   F9 = MKR; F10 = SPAN; F11 = AMPLITUDE; F12 = title recall
	ebiten.KeyF1: device.ATKeyF1, ebiten.KeyF2: device.ATKeyF2,
	ebiten.KeyF3: device.ATKeyF3, ebiten.KeyF4: device.ATKeyF4,
	ebiten.KeyF5: device.ATKeyF5, ebiten.KeyF6: device.ATKeyF6,
	ebiten.KeyF7: device.ATKeyF7, ebiten.KeyF8: device.ATKeyF8,
	ebiten.KeyF9: device.ATKeyF9, ebiten.KeyF10: device.ATKeyF10,
	ebiten.KeyF11: device.ATKeyF11, ebiten.KeyF12: device.ATKeyF12,
	// Navigation / data entry
	ebiten.KeyArrowUp:    device.ATKeyUp,
	ebiten.KeyArrowDown:  device.ATKeyDown,
	ebiten.KeyArrowLeft:  device.ATKeyLeft,
	ebiten.KeyArrowRight: device.ATKeyRight,
}

// fpBindings maps Ebiten keys to named front-panel keys (matrix path, IRQ3).
// The mapped key is injected as a matrix bit when Known(); stubs are no-ops
// until the (byte,bit) is probed with cmd/keymatrix.
// Keys here take priority over atBindings when both would fire.
var fpBindings = map[ebiten.Key]device.FPKey{
	// Instrument state — top row
	// (assign host keys that don't clash with AT text-entry path)
	// These will be effective once the matrix bits are probed.
}

type game struct {
	m        *machine.Machine
	lb       *emutest.LoopBreaker
	fb       *ebiten.Image
	chunks   int
	cycles   uint64
	lastKey  string
	lastMsg  string // status message shown in title bar (e.g. save result)
	keyReads int
	// probe mode: cycle through all 48 matrix bits to find key positions
	probeMode bool
	probeBit  int
	// byCmd selects the by-command coloured display view (default) vs realistic
	// amber; toggled with Ctrl+V.
	byCmd bool
}

// renderDisplay returns the current display image: the register-derived scanout,
// either coloured by drawing command (with a legend) or plain amber.
func (g *game) renderDisplay() *image.RGBA {
	if g.byCmd {
		return g.m.MMIO.Display.Chip.RenderScanoutByCmd()
	}
	return g.m.MMIO.Display.Chip.RenderScanout()
}

func (g *game) Update() error {
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
		g.m.DriveOneSweepChunk()
	}

	// ── Host-side screen save (Ctrl+S) ──────────────────────────────────────
	// Saves the current framebuffer to screens/gui_<timestamp>.png.
	// Distinct from the firmware's PrintScreen (which sends a scan code to the
	// plotter); this is a host-side convenience that doesn't touch the firmware.
	if ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight) {
		if inpututil.IsKeyJustPressed(ebiten.KeyS) {
			g.lastMsg = saveScreen(g.m)
		}
		// Ctrl+R: arm a one-shot linear capture of the HD63484 command stream —
		// press it just before an event (e.g. the Enter that renders CAL DISP;) so
		// the transition's draw/clear commands are recorded, then Ctrl+S dumps them.
		if inpututil.IsKeyJustPressed(ebiten.KeyR) {
			g.m.MMIO.Display.Chip.StartCmdCapture(262144)
			g.lastMsg = "cmd capture armed — do the action, then Ctrl+S"
		}
		// Ctrl+V: toggle the display view between the realistic amber scanout and
		// the by-command coloured view (each pixel tinted by the command that drew
		// it, with a legend strip).
		if inpututil.IsKeyJustPressed(ebiten.KeyV) {
			g.byCmd = !g.byCmd
			if g.byCmd {
				g.lastMsg = "view: by-command (amber: Ctrl+V)"
			} else {
				g.lastMsg = "view: amber (by-command: Ctrl+V)"
			}
		}
	}

	// ── Front-panel matrix path (IRQ3) ────────────────────────────────────────
	// Named front-panel keys: fire when a binding has a known matrix position.
	for k, fp := range fpBindings {
		if inpututil.IsKeyJustPressed(k) && fp.Known() {
			g.m.FrontPanel.SetBit(fp.Byte, fp.Bit)
			g.lastKey = fp.Name
		}
	}
	// Probe mode: Tab steps through all 48 matrix bits to locate unknown keys.
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		if g.probeMode {
			g.probeBit = (g.probeBit + 1) % 48
		}
		g.probeMode = true
		g.m.FrontPanel.SetBit(g.probeBit/8, g.probeBit%8)
		g.lastKey = fmt.Sprintf("probe byte%d bit%d", g.probeBit/8, g.probeBit%8)
	}
	// Deliver IRQ3 while a front-panel event is pending.
	if g.m.FrontPanel.Pending() {
		g.m.CPU.SetIRQ(3)
		g.m.CPU.Run(irqServiceCost)
		g.m.CPU.SetIRQ(0)
	}
	if g.m.FrontPanel.Consumed() {
		g.keyReads++
	}

	// ── AT keyboard path (IRQ4) ───────────────────────────────────────────────
	// Inject AT scan codes for make (key-down) events.
	// Skip when Ctrl is held — those are host shortcuts (Ctrl+S = save screen).
	ctrlHeld := ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight)
	for k, atk := range atBindings {
		if ctrlHeld {
			break
		}
		if inpututil.IsKeyJustPressed(k) {
			if make := device.ATMake(atk); make != nil {
				g.m.ATKeyboard.Enqueue(make...)
				g.lastKey = fmt.Sprintf("AT%v", k)
			}
		}
		// Break (key-up): inject the F0 release code.
		if inpututil.IsKeyJustReleased(k) {
			if brk := device.ATBreak(atk); brk != nil {
				g.m.ATKeyboard.Enqueue(brk...)
			}
		}
	}
	// Fire IRQ4 while scan-code bytes are pending delivery.
	for g.m.ATKeyboard.Pending() {
		g.m.CPU.SetIRQ(4)
		g.m.CPU.Run(irq4Cost)
		g.m.CPU.SetIRQ(0)
	}

	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	img := g.renderDisplay()
	b := img.Bounds()
	// The scanout dimensions (and the legend strip) differ from the legacy
	// RenderFrame size, so size the framebuffer to whatever the render produces;
	// WritePixels requires an exact match.
	if g.fb == nil || g.fb.Bounds().Dx() != b.Dx() || g.fb.Bounds().Dy() != b.Dy() {
		g.fb = ebiten.NewImage(b.Dx(), b.Dy())
	}
	g.fb.WritePixels(img.Pix)
	screen.DrawImage(g.fb, nil)
	msg := ""
	if g.lastMsg != "" {
		msg = "  |  " + g.lastMsg
	}
	ebiten.SetWindowTitle(fmt.Sprintf(
		"HP 8593A  |  %.0fM cyc  PC=%#06x  bc67=%#02x  key=%s  reads=%d%s",
		float64(g.cycles)/1e6, g.m.CPU.Reg(cpu.PC),
		byte(g.m.Bus.Read(0xFFBC67, 1)), g.lastKey, g.keyReads, msg))
}

func (g *game) Layout(int, int) (int, int) {
	if g.fb != nil {
		b := g.fb.Bounds()
		return b.Dx(), b.Dy()
	}
	return device.DisplayWidth, device.DisplayHeight
}

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
	m.SweepDrive = true
	m.MMIO.SweepActive = true

	// Capture the last N HD63484 command-FIFO words so Ctrl+S dumps the live draw
	// stream (e.g. the CAL DISP; cal-display render) next to the screenshot, for
	// offline decoding of which clear/draw commands the firmware issues.
	m.MMIO.Display.Chip.EnableCmdTrace(16384)

	// Render the LIVE buffer so EVERY mode refreshes (CAL DISP, command echo,
	// menus) — the stable-frame snapshot only updates on the operating display's
	// graticule redraw and would otherwise freeze the GUI in other modes.
	m.MMIO.Display.Chip.SetRenderLive(true)

	g := &game{
		m:     m,
		lb:    emutest.NewLoopBreaker(50),
		fb:    ebiten.NewImage(device.DisplayWidth, device.DisplayHeight),
		byCmd: true, // default to the by-command coloured view (Ctrl+V toggles)
	}

	// The scanout is 1024 px wide × 256 lines (+ a 20-px legend strip). Show it at
	// 1× width with a 1.5× vertical stretch (≈ the 4:3 CRT geometry); the window is
	// resizable and Layout adapts to the actual render size.
	ebiten.SetWindowSize(1024, 414)
	ebiten.SetWindowTitle("HP 8593A — booting…")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}

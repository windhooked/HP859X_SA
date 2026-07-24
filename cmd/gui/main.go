// Command gui is a live Ebiten window for the virtual HP 8593A: it boots the
// real Rev L firmware from reset, renders the HD63484 frame buffer like the
// instrument's CRT, and mirrors the real keyboard path:
//
//   - AT keyboard (external keyboard, rear-panel EXT KEYBOARD connector):
//     alphanumeric and function keys are translated to AT Set-2 scan codes and
//     injected into the MC68230 PIT receiver (0xEF8002/0xEF8000), triggering
//     IRQ4. The firmware decodes them and dispatches:
//   - Letters/digits/punctuation: typed text (screen titles, HP-IB commands,
//     DLP programs).
//   - F1–F6: softkeys 1–6 of the current menu.
//   - F7: prefix mode; F8: remote-commands mode.
//   - F9: FREQUENCY (MKR fn); F10: SPAN; F11: AMPLITUDE; F12: title recall.
//   - Escape: title mode; arrows: RPG knob / step keys.
//     (See HP 8590 E/L-Series Programmer's Guide Table 5-8 + docs/KEYBOARD_MAP.md.)
//
// CRT model: the display renders the LIVE frame buffer every frame — exactly
// like the real CRT scanning VRAM — with a host-side LONG-PERSISTENCE PHOSPHOR
// decay (the instrument CRTs use long-persistence phosphor so slow sweeps and
// mid-redraw states don't flicker). There is no snapshot/live switching.
//
// Toolbar (clickable) + shortcuts: view Amber/ByCmd (Ctrl+V), Phosphor on/off,
// Speed Fast/Real (real = 16 MHz / 60 fps), Save PNG (Ctrl+S), capture arm
// (Ctrl+R).
//
//	DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib go run ./cmd/gui/
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// saveScreen writes the given (currently displayed) frame to
// screens/gui_<timestamp>.png and returns the path, or an error message.
func saveScreen(m *machine.Machine, img *image.RGBA) string {
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
	// fastCyclesPerFrame runs the emulation well above real time (fast boot,
	// snappy interaction). realCyclesPerFrame is the REAL instrument's pace:
	// a 16 MHz 68000 across a 60 fps frame — sweep/refresh dynamics then match
	// the physical unit (a 58 ms sweep spans ~3.5 frames, as on the CRT).
	fastCyclesPerFrame = 2_000_000
	realCyclesPerFrame = 16_000_000 / 60

	chunkCycles    = 2000
	irqEvery       = 5
	irqServiceCost = 400
	irq4Cost       = 600 // IRQ4 handler is slightly heavier (transport + ring-buf)

	// phosphorDecay is the per-frame retention of the host-side long-persistence
	// phosphor model (presentation-layer only — the frame buffer is untouched).
	// 0.90 at 60 fps ≈ 130 ms half-life, in the range of the long-persistence
	// phosphors these analyzer CRTs used; bright new content overwrites decay.
	phosphorDecay = 0.90

	toolbarH = 22 // toolbar strip height in layout pixels
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
	//   F9 = FREQUENCY/MKR fn; F10 = SPAN; F11 = AMPLITUDE; F12 = title recall
	ebiten.KeyF1: device.ATKeyF1, ebiten.KeyF2: device.ATKeyF2,
	ebiten.KeyF3: device.ATKeyF3, ebiten.KeyF4: device.ATKeyF4,
	ebiten.KeyF5: device.ATKeyF5, ebiten.KeyF6: device.ATKeyF6,
	ebiten.KeyF7: device.ATKeyF7, ebiten.KeyF8: device.ATKeyF8,
	ebiten.KeyF9: device.ATKeyF9, ebiten.KeyF10: device.ATKeyF10,
	ebiten.KeyF11: device.ATKeyF11, ebiten.KeyF12: device.ATKeyF12,
	// Navigation / data entry. The arrows ARE the front-panel RPG knob / step
	// keys (firmware fcn.57278 → active-function adjust). See docs/KEYBOARD_MAP.md.
	ebiten.KeyArrowUp:    device.ATKeyUp,
	ebiten.KeyArrowDown:  device.ATKeyDown,
	ebiten.KeyArrowLeft:  device.ATKeyLeft,
	ebiten.KeyArrowRight: device.ATKeyRight,
	// Editor/cursor navigation (title / DLP editor).
	ebiten.KeyHome:     device.ATKeyHome,
	ebiten.KeyEnd:      device.ATKeyEnd,
	ebiten.KeyInsert:   device.ATKeyInsert,
	ebiten.KeyDelete:   device.ATKeyDelete,
	ebiten.KeyPageUp:   device.ATKeyPageUp,
	ebiten.KeyPageDown: device.ATKeyPageDown,
}

type game struct {
	m       *machine.Machine
	lb      *emutest.LoopBreaker
	fb      *ebiten.Image
	chunks  int
	cycles  uint64
	lastKey string
	lastMsg string // status message shown in title bar (e.g. save result)

	// byCmd selects the by-command coloured diagnostic view (legend strip) vs
	// the realistic monochrome amber CRT view (default). Toolbar / Ctrl+V.
	byCmd bool
	// phosphor enables the host-side long-persistence phosphor decay on the
	// amber view (the byCmd diagnostic view is always raw). Toolbar toggle.
	phosphor bool
	// realtime paces the emulation at the real instrument's 16 MHz (sweep and
	// refresh dynamics match the physical unit); off = fast (default).
	realtime bool

	// phos is the phosphor intensity accumulator (one float per pixel of the
	// current render size); disp is the composited output buffer.
	phos []float32
	disp *image.RGBA
	// lastImg is the most recent image handed to Draw (for Save).
	lastImg *image.RGBA
}

// toolbar buttons: fixed-geometry clickable rects at the top of the window.
type button struct {
	label func(g *game) string
	click func(g *game)
}

var buttons = []button{
	{
		label: func(g *game) string {
			if g.byCmd {
				return "View: ByCmd"
			}
			return "View: Amber"
		},
		click: func(g *game) { g.byCmd = !g.byCmd },
	},
	{
		label: func(g *game) string {
			if g.phosphor {
				return "Phosphor: On"
			}
			return "Phosphor: Off"
		},
		click: func(g *game) { g.phosphor = !g.phosphor; g.resetPhosphor() },
	},
	{
		label: func(g *game) string {
			if g.realtime {
				return "Speed: Real"
			}
			return "Speed: Fast"
		},
		click: func(g *game) { g.realtime = !g.realtime },
	},
	{
		label: func(g *game) string { return "Save PNG" },
		click: func(g *game) {
			if g.lastImg != nil {
				g.lastMsg = saveScreen(g.m, g.lastImg)
			}
		},
	},
}

const (
	btnW, btnH, btnGap, btnX0, btnY0 = 112, 18, 8, 4, 2
)

func buttonRect(i int) image.Rectangle {
	x := btnX0 + i*(btnW+btnGap)
	return image.Rect(x, btnY0, x+btnW, btnY0+btnH)
}

func (g *game) resetPhosphor() {
	for i := range g.phos {
		g.phos[i] = 0
	}
}

// renderDisplay returns the current display image: the register-derived LIVE
// scanout — the CRT view — either realistic amber (with the phosphor decay
// composited) or the by-command coloured diagnostic (raw, with legend).
func (g *game) renderDisplay() *image.RGBA {
	if g.byCmd {
		return g.m.MMIO.Display.Chip.RenderScanoutByCmd()
	}
	img := g.m.MMIO.Display.Chip.RenderScanout()
	if !g.phosphor {
		return img
	}
	return g.compositePhosphor(img)
}

// compositePhosphor applies the long-persistence phosphor model: per pixel,
// intensity = max(lit-now, previous*phosphorDecay). The output is the amber
// pen colour scaled by intensity — bright fresh strokes over a fading tail,
// which is exactly how the real CRT hides mid-redraw states and slow sweeps.
func (g *game) compositePhosphor(img *image.RGBA) *image.RGBA {
	b := img.Bounds()
	n := b.Dx() * b.Dy()
	if len(g.phos) != n {
		g.phos = make([]float32, n)
		g.disp = image.NewRGBA(b)
	}
	src := img.Pix
	dst := g.disp.Pix
	for i := 0; i < n; i++ {
		v := g.phos[i] * phosphorDecay
		if src[i*4] > 0 { // lit now (R channel of the amber pen)
			v = 1
		}
		g.phos[i] = v
		dst[i*4+0] = uint8(255 * v)
		dst[i*4+1] = uint8(176 * v)
		dst[i*4+2] = 0
		dst[i*4+3] = 0xFF
	}
	return g.disp
}

func (g *game) Update() error {
	perFrame := fastCyclesPerFrame
	if g.realtime {
		perFrame = realCyclesPerFrame
	}
	for done := 0; done < perFrame; done += chunkCycles {
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

	// ── Toolbar clicks ──────────────────────────────────────────────────────
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		mx, my := ebiten.CursorPosition()
		for i := range buttons {
			if image.Pt(mx, my).In(buttonRect(i)) {
				buttons[i].click(g)
				break
			}
		}
	}

	// ── Host shortcuts (Ctrl held) ──────────────────────────────────────────
	if ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight) {
		if inpututil.IsKeyJustPressed(ebiten.KeyS) {
			if g.lastImg != nil {
				g.lastMsg = saveScreen(g.m, g.lastImg)
			}
		}
		// Ctrl+R: arm a one-shot linear capture of the HD63484 command stream —
		// press it just before an event (e.g. the Enter that renders CAL DISP;) so
		// the transition's draw/clear commands are recorded, then Ctrl+S dumps them.
		if inpututil.IsKeyJustPressed(ebiten.KeyR) {
			g.m.MMIO.Display.Chip.StartCmdCapture(262144)
			g.lastMsg = "cmd capture armed — do the action, then Ctrl+S"
		}
		// Ctrl+V: toggle the display view (same as the toolbar button).
		if inpututil.IsKeyJustPressed(ebiten.KeyV) {
			g.byCmd = !g.byCmd
		}
	}

	// ── AT keyboard path (IRQ4) ─────────────────────────────────────────────
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
	g.lastImg = img
	b := img.Bounds()
	// The scanout dimensions (and the legend strip) differ per view, so size
	// the framebuffer to whatever the render produces; WritePixels requires an
	// exact match.
	if g.fb == nil || g.fb.Bounds().Dx() != b.Dx() || g.fb.Bounds().Dy() != b.Dy() {
		g.fb = ebiten.NewImage(b.Dx(), b.Dy())
	}
	g.fb.WritePixels(img.Pix)

	// Toolbar strip.
	w := screen.Bounds().Dx()
	vector.DrawFilledRect(screen, 0, 0, float32(w), toolbarH, color.RGBA{0x28, 0x28, 0x28, 0xFF}, false)
	for i := range buttons {
		r := buttonRect(i)
		vector.DrawFilledRect(screen, float32(r.Min.X), float32(r.Min.Y),
			float32(r.Dx()), float32(r.Dy()), color.RGBA{0x48, 0x48, 0x48, 0xFF}, false)
		ebitenutil.DebugPrintAt(screen, buttons[i].label(g), r.Min.X+5, r.Min.Y+1)
	}

	// Display below the toolbar.
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(0, toolbarH)
	screen.DrawImage(g.fb, op)

	msg := ""
	if g.lastMsg != "" {
		msg = "  |  " + g.lastMsg
	}
	ebiten.SetWindowTitle(fmt.Sprintf(
		"HP 8593A  |  %.0fM cyc  PC=%#06x  key=%s%s",
		float64(g.cycles)/1e6, g.m.CPU.Reg(cpu.PC), g.lastKey, msg))
}

func (g *game) Layout(int, int) (int, int) {
	if g.fb != nil {
		b := g.fb.Bounds()
		return b.Dx(), b.Dy() + toolbarH
	}
	return device.DisplayWidth, device.DisplayHeight + toolbarH
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

	// The CRT scans the LIVE frame buffer, always — mid-redraw states are
	// smoothed by the phosphor model, exactly as on the real instrument. (The
	// former snapshot/live switching is gone.)
	m.MMIO.Display.Chip.SetRenderLive(true)

	g := &game{
		m:        m,
		lb:       emutest.NewLoopBreaker(50),
		fb:       ebiten.NewImage(device.DisplayWidth, device.DisplayHeight),
		phosphor: true, // realistic CRT look by default
	}

	// The scanout is 1024 px wide × 256 lines (+ toolbar). Show it at 1× width;
	// the window is resizable and Layout adapts to the actual render size.
	ebiten.SetWindowSize(1024, 256+toolbarH+138)
	ebiten.SetWindowTitle("HP 8593A — booting…")
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}

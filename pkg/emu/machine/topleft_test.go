package machine

import (
	"fmt"
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/device/hd63484"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestTopLeftArtifact boots the firmware and renders the live display BOTH ways:
//   - RenderScanout: the real display window (what the virtual screen shows)
//   - RenderByCmd:    the whole core memory tinted by drawing command
//
// It exists to answer "the discontinued ALINE/RLINE top-left (italic-h + a
// line/block)": is that figure actually ON the emulated screen (scanout), or is
// it boot-time content that accumulated in core memory and only appears in the
// by-command debug dump (i.e. off the live scanout window)? Writes both PNGs to
// screens/ for inspection.
func TestTopLeftArtifact(t *testing.T) {
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom not available")
	}
	noclip := os.Getenv("NOAREACLIP") != ""
	faithful := os.Getenv("FAITHFUL") != "" // FAITHFUL=1 → old AND-dither (default = clean-clear)
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.MMIO.Display.Chip.DisableAreaClip = noclip
	m.MMIO.Display.Chip.CleanClear = !faithful
	m.MMIO.Display.Chip.EnableLineLog()
	m.MMIO.Display.Chip.StartCmdCapture(1 << 22)
	m.BootToOperatingWithSweep(250_000_000)
	m.BootToOperatingWithSweep(40_000_000)

	suffix := ""
	if faithful {
		suffix = "_faithful"
	}
	ch := m.MMIO.Display.Chip
	// Canonical view: register-derived scanout coloured by drawing command, with a
	// legend at the bottom, rendered from the stable (complete-frame) snapshot.
	if f, err := os.Create("../../../screens/boot_scanout" + suffix + ".png"); err == nil {
		png.Encode(f, ch.RenderScanoutByCmd())
		f.Close()
		t.Logf("wrote screens/boot_scanout%s.png", suffix)
	}

	t.Logf("total segments=%d dots=%d rects=%d", len(ch.LineLog), len(ch.DotLog), ch.Rects)

	// Does the firmware draw explicit graticule GRID LINES each cycle? Those would
	// be LONG axis-aligned segments in the graph (firmware x[0..400] y[0..209]) —
	// horizontals at division heights, verticals at division columns — distinct
	// from the trace (a dense run of short dx=1 segments). Dedup by geometry and
	// count how many times each repeats (= per-cycle redraw count).
	{
		type key struct{ x0, y0, x1, y1 int }
		long := map[key]int{}
		for _, ln := range ch.LineLog {
			dx, dy := ln.X1-ln.X0, ln.Y1-ln.Y0
			if dx < 0 {
				dx = -dx
			}
			if dy < 0 {
				dy = -dy
			}
			axisAligned := (dx == 0 || dy == 0)
			if axisAligned && (dx >= 80 || dy >= 80) {
				long[key{ln.X0, ln.Y0, ln.X1, ln.Y1}]++
			}
		}
		t.Logf("=== long axis-aligned segments (graticule grid candidates), %d distinct ===", len(long))
		shown := 0
		for k, n := range long {
			t.Logf("  (%d,%d)->(%d,%d)  ×%d", k.x0, k.y0, k.x1, k.y1, n)
			if shown++; shown >= 40 {
				break
			}
		}
	}

	// The hp logo is a COMPACT figure (~20px). Find connected runs of line
	// segments (the polyline draws pen->v1->v2->... as a chain) whose whole
	// bounding box is small — that isolates the logo (and other small glyph-like
	// vector figures) from the 400px-wide trace. Print the first few small figures
	// with their segment lists.
	type seg struct{ X0, Y0, X1, Y1 int }
	type fig struct {
		start      int
		segs       []seg
		minX, minY int
		maxX, maxY int
	}
	var figs []fig
	var cur *fig
	for i, ln := range ch.LineLog {
		connected := cur != nil && len(cur.segs) > 0 &&
			cur.segs[len(cur.segs)-1].X1 == ln.X0 && cur.segs[len(cur.segs)-1].Y1 == ln.Y0
		if !connected {
			if cur != nil {
				figs = append(figs, *cur)
			}
			cur = &fig{start: i, minX: 1 << 30, minY: 1 << 30, maxX: -(1 << 30), maxY: -(1 << 30)}
		}
		cur.segs = append(cur.segs, seg{ln.X0, ln.Y0, ln.X1, ln.Y1})
		for _, p := range [][2]int{{ln.X0, ln.Y0}, {ln.X1, ln.Y1}} {
			if p[0] < cur.minX {
				cur.minX = p[0]
			}
			if p[0] > cur.maxX {
				cur.maxX = p[0]
			}
			if p[1] < cur.minY {
				cur.minY = p[1]
			}
			if p[1] > cur.maxY {
				cur.maxY = p[1]
			}
		}
	}
	if cur != nil {
		figs = append(figs, *cur)
	}

	_ = figs
	// Dump EVERYTHING (segments + dots) in the logo bbox (firmware x[-45..-15],
	// y[210..228]) for the FIRST frame only — the complete hp-logo figure.
	inLogo := func(x, y int) bool { return x >= -45 && x <= -15 && y >= 210 && y <= 228 }
	t.Log("--- ALL line segments in logo bbox (first frame) ---")
	for i, ln := range ch.LineLog {
		if i > 500 {
			break
		}
		if inLogo(ln.X0, ln.Y0) || inLogo(ln.X1, ln.Y1) {
			t.Logf("  seg %3d  (%d,%d)->(%d,%d)", i, ln.X0, ln.Y0, ln.X1, ln.Y1)
		}
	}
	t.Log("--- ALL dots in logo bbox (first frame) ---")
	for i, d := range ch.DotLog {
		if i > 200 {
			break
		}
		if inLogo(d.X, d.Y) {
			t.Logf("  dot %3d  (%d,%d)", i, d.X, d.Y)
		}
	}

	// Replay the captured command FIFO through a fresh chip one word at a time,
	// printing the exact opcode window whenever a NEW segment lands in the logo
	// bbox. This shows the RAW firmware commands (opcodes + coordinate words) that
	// build the logo — revealing whether the gaps are the firmware's own separate
	// strokes or our decode dropping/misframing words.
	words := ch.CmdCapture()

	// SCLR-pattern analysis: for each clear region (RWP), record which dither
	// patterns (0x5555 / 0xAAAA) the firmware ANDs in. If a region only ever gets
	// ONE phase, the AND is idempotent (X AND X == X) and old content (boot glyphs,
	// previous trace) never erases — it survives as a ghost. A clean erase needs
	// BOTH phases (0x5555 AND 0xAAAA == 0).
	{
		type cnt struct{ p5555, pAAAA, other int }
		regions := map[uint32]*cnt{}
		var rwp uint32
		patHist := map[uint16]int{}
		maskHist := map[uint16]int{} // WPR reg 4 (mask) values seen
		clrReplace := 0              // SCLR/CLR with bit10=0 (REPLACE, real erase)
		for i := 0; i < len(words); i++ {
			w := words[i]
			switch {
			case w == 0x0804 && i+1 < len(words):
				maskHist[words[i+1]]++
				i++
			case w == 0x080c && i+1 < len(words):
				rwp = (rwp & 0x00fff) | ((uint32(words[i+1]) & 0xff) << 12)
				i++
			case w == 0x080d && i+1 < len(words):
				rwp = (rwp & 0xff000) | ((uint32(words[i+1]) & 0xfff0) >> 4)
				i++
			case (w&0xFFFC == 0x5C00 || w&0xFFFC == 0x5800) && w&0x0400 == 0:
				clrReplace++
			case w&0xFFFC == 0x5C00 && i+1 < len(words):
				pat := words[i+1]
				patHist[pat]++
				c := regions[rwp]
				if c == nil {
					c = &cnt{}
					regions[rwp] = c
				}
				switch pat {
				case 0x5555:
					c.p5555++
				case 0xAAAA:
					c.pAAAA++
				default:
					c.other++
				}
				i++
			}
		}
		both, only5, onlyA, oth := 0, 0, 0, 0
		for _, c := range regions {
			switch {
			case c.p5555 > 0 && c.pAAAA > 0:
				both++
			case c.p5555 > 0:
				only5++
			case c.pAAAA > 0:
				onlyA++
			default:
				oth++
			}
		}
		t.Logf("=== SCLR dither analysis ===")
		t.Logf("mask register (WPR 0x04) values: %v", maskHist)
		t.Logf("REPLACE clears (bit10=0, real erase): %d", clrReplace)
		t.Logf("pattern histogram (pattern: count): %v", patHist)
		t.Logf("SCLR regions=%d  both(5555+AAAA)=%d  only5555=%d  onlyAAAA=%d  other=%d",
			len(regions), both, only5, onlyA, oth)

		// Dump the first SCLR commands in stream order (rwp, pattern, ax, ay) to
		// reveal whether the phase is spatial (alternates as RWP walks rows) or
		// temporal (same RWP, pattern flips between frames).
		t.Log("--- first 40 SCLR commands in stream order (rwp, pattern, ax, ay) ---")
		rwp = 0
		shown := 0
		for i := 0; i < len(words) && shown < 40; i++ {
			w := words[i]
			switch {
			case w == 0x080c && i+1 < len(words):
				rwp = (rwp & 0x00fff) | ((uint32(words[i+1]) & 0xff) << 12)
				i++
			case w == 0x080d && i+1 < len(words):
				rwp = (rwp & 0xff000) | ((uint32(words[i+1]) & 0xfff0) >> 4)
				i++
			case w&0xFFFC == 0x5C00 && i+3 < len(words):
				t.Logf("  cr=%04x rwp=%05x pat=%04x ax=%d ay=%d",
					w, rwp, words[i+1], int16(words[i+2]), int16(words[i+3]))
				shown++
				i += 3
			}
		}
	}

	// Per-frame ORDER: replay word-by-word and emit, in stream order, the graph
	// CLEAR events (SCLR over the graph RWP range) and the graticule GRID-LINE
	// draws. If GRID comes BEFORE CLEAR each frame, the clear erases the grid (so a
	// clean-clear loses it); if GRID comes AFTER, the grid survives and the clear
	// can safely be clean.
	{
		fr := hd63484.New()
		fr.EnableLineLog()
		var rwp uint32
		prevClears, prevLines := 0, 0
		events := 0
		traceSeen := false
		clearRun := 0 // collapse consecutive graph-clear columns into one marker
		for i := 0; i < len(words) && events < 40; i++ {
			w := words[i]
			if w == 0x080c && i+1 < len(words) {
				rwp = (rwp & 0x00fff) | ((uint32(words[i+1]) & 0xff) << 12)
			} else if w == 0x080d && i+1 < len(words) {
				rwp = (rwp & 0xff000) | ((uint32(words[i+1]) & 0xfff0) >> 4)
			}
			fr.WriteData(w)
			if fr.AreaClears > prevClears {
				prevClears = fr.AreaClears
				inGraph := rwp >= 0x3900 && rwp <= 0x3b00
				if inGraph {
					if clearRun == 0 {
						t.Logf("  [%d] CLEAR graph sweep starts (rwp=%05x) — resets trace marker", events, rwp)
						events++
						traceSeen = false
					}
					clearRun++
				}
			} else if w&0xFFFC != 0x5C00 && clearRun > 0 {
				// a non-SCLR command ends the clear sweep
				clearRun = 0
			}
			if len(fr.LineLog) > prevLines {
				ln := fr.LineLog[len(fr.LineLog)-1]
				prevLines = len(fr.LineLog)
				dx, dy := ln.X1-ln.X0, ln.Y1-ln.Y0
				adx, ady := dx, dy
				if adx < 0 {
					adx = -adx
				}
				if ady < 0 {
					ady = -ady
				}
				inGraph := ln.X0 >= 0 && ln.X0 <= 400 && ln.Y0 >= 0 && ln.Y0 <= 209
				switch {
				case (adx == 0 || ady == 0) && (adx >= 80 || ady >= 80) && inGraph:
					t.Logf("  [%d] GRID  (%d,%d)->(%d,%d)", events, ln.X0, ln.Y0, ln.X1, ln.Y1)
					events++
				case adx <= 3 && inGraph && !traceSeen:
					t.Logf("  [%d] TRACE first segment (%d,%d)->(%d,%d)", events, ln.X0, ln.Y0, ln.X1, ln.Y1)
					events++
					traceSeen = true
				}
			}
		}
	}

	// DECISIVE: feed the capture into a fresh chip with CleanClear, truncated right
	// after the LAST graticule grid redraw (before any later clear), then render.
	// If the grid + trace both show cleanly, the grid's absence in the live snapshot
	// was a frame-boundary timing artifact (the faithful AND masked it with a
	// persistent dotted residue), NOT a real loss — confirming clean-clear is right.
	{
		// find the last word index where a vertical grid line (360,5)->(360,196) or a
		// horizontal (4,175)->(397,175) was just completed — approximate by scanning
		// for the last long-grid-line draw.
		fr := hd63484.New()
		fr.CleanClear = true
		fr.EnableLineLog()
		lastGridWord := 0
		prevL := 0
		for i, w := range words {
			fr.WriteData(w)
			if len(fr.LineLog) > prevL {
				ln := fr.LineLog[len(fr.LineLog)-1]
				prevL = len(fr.LineLog)
				dx, dy := ln.X1-ln.X0, ln.Y1-ln.Y0
				if dx < 0 {
					dx = -dx
				}
				if dy < 0 {
					dy = -dy
				}
				if (dx == 0 || dy == 0) && (dx >= 80 || dy >= 80) &&
					ln.X0 >= 0 && ln.X0 <= 400 && ln.Y0 >= 0 && ln.Y0 <= 209 {
					lastGridWord = i
				}
			}
		}
		// Re-feed up to a bit past the last grid line (so the grid is the freshest
		// graph content, with the trace already drawn before it within the frame).
		end := lastGridWord + 8
		if end > len(words) {
			end = len(words)
		}
		fr2 := hd63484.New()
		fr2.CleanClear = true
		for _, w := range words[:end] {
			fr2.WriteData(w)
		}
		if f, err := os.Create("../../../screens/replay_cleanclear_frame.png"); err == nil {
			png.Encode(f, fr2.RenderScanout())
			f.Close()
			t.Logf("wrote screens/replay_cleanclear_frame.png (truncated at word %d/%d, just after last grid line)", end, len(words))
		}
	}

	t.Logf("--- raw command words for logo draws (captured %d words) ---", len(words))
	fresh := hd63484.New()
	fresh.EnableLineLog()
	window := make([]uint16, 0, 18)
	prevN, hits := 0, 0
	for _, w := range words {
		window = append(window, w)
		if len(window) > 16 {
			window = window[1:]
		}
		fresh.WriteData(w)
		if len(fresh.LineLog) > prevN {
			ln := fresh.LineLog[len(fresh.LineLog)-1]
			prevN = len(fresh.LineLog)
			if inLogo(ln.X0, ln.Y0) || inLogo(ln.X1, ln.Y1) {
				hex := ""
				for _, x := range window {
					hex += fmt.Sprintf("%04x ", x)
				}
				t.Logf("seg (%d,%d)->(%d,%d)\n     recent: %s", ln.X0, ln.Y0, ln.X1, ln.Y1, hex)
				if hits++; hits >= 12 {
					break
				}
			}
		}
	}
}

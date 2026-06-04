# HD63484 ACRTC — screen drawing commands & the hp-logo routine

Reference for the HD63484 drawing-command stream as the 8593 Rev L firmware
emits it and as [pkg/emu/device/hd63484/](../pkg/emu/device/hd63484/) decodes it.
Opcodes and decode behaviour are sourced from the implementation
([parser.go](../pkg/emu/device/hd63484/parser.go),
[draw.go](../pkg/emu/device/hd63484/draw.go),
[area_ops.go](../pkg/emu/device/hd63484/area_ops.go),
[wptn.go](../pkg/emu/device/hd63484/wptn.go)) and verified against a live
firmware command capture (`screens/crt_*.txt`). Chip-level architecture and the
host MMIO interface are in [pkg/emu/device/hd63484/doc.go](../pkg/emu/device/hd63484/doc.go);
the Hitachi manual is [docs/hd63484_um.txt](hd63484_um.txt).

The firmware drives the chip through three byte ports (8593 addresses):
`0xFFF5FC` address/command, `0xFFF5FD` status, `0xFFF5FE` data. A draw is one
**command word** (opcode in the high bits) followed by a fixed or variable run of
**parameter words** poured into the data port. The parser is a state machine —
each multi-word command has its own `decoderState` so parameter words are never
mistaken for the next opcode.

---

## 1. Coordinate system

- **Firmware coordinates are Y-up** (Y increases upward, screen-math convention).
  `setVRAMPixel` applies the flip to VRAM rows (`vramY = orgRow − y`); the faithful
  core stores via `calcOffset` (a Y-up MAME port). So a high firmware-Y is the
  **top** of the screen.
- **The drawing origin (ORG)** is programmed once at init: the firmware sends
  `ORG 0x4003, 0xa450` → `orgDN=1, orgDPA=0x3a45`. All draw coordinates are
  relative to this origin.
- **Pen** = current (X, Y). MOVE commands relocate it without drawing; LINE/RECT/
  POLY commands draw from the pen and leave it at the endpoint; DOT plots at it.
- **The graph (graticule) box** sits at firmware X ∈ [0..400], Y ∈ [0..209].
  Content **left of X=0 is the left label column** (REF / PEAK / LOG / …) — so the
  hp logo and those labels live at **negative firmware-X**. Content **above
  Y=209** is the top status band.

---

## 2. Command set (as decoded)

Legend: **✓ rendered** · **�props** = framed & parameters consumed but visual
effect not modelled · **stub** = recognised, gated, no effect.

| Opcode (word) | Mnemonic | Args | Decode / effect | FW use |
|---|---|---|---|---|
| `0x0000` | NOP | 0 | no-op; FIFO padding | heavy |
| `0x0400` | ORG | 2 (mem-addr, dot) | set drawing origin → `setORG` | init |
| `0x08RR` | WPR | 1 | write parameter register `RR` (low 5 bits) ✓ | heavy |
| `0x0CRR` | RPR | 0 (→1 result) | read parameter register — read-FIFO **stub** | rare |
| `0x1800` | WPTN | 1 count + N | write pattern — **glyph blit** if count=`0x000A`, else pattern-RAM / raster ✓ | heavy |
| `0x1C00` / `0x1400` | RPTN / SCAN | 1 | **stub** | — |
| `0x8000` | AMOVE | 2 (X, Y) | pen ← (X, Y) ✓ | heavy |
| `0x8400` | RMOVE | 2 (dX, dY) | pen += (dX, dY) ✓ — **this is the "pen-up" between strokes** | heavy |
| `0x8800`/`0x8801` | ALINE | 2 (X, Y) | draw pen→(X,Y), pen←(X,Y) ✓ | yes |
| `0x8C00`/`0x8C01` | RLINE | 2 (dX, dY) | draw pen→pen+(d), pen←end ✓ | yes |
| `0x9000`/`0x9001` | ARCT | 2 (X, Y) | rectangle outline pen↔(X,Y) ✓ | yes |
| `0x9400`/`0x9401` | RRCT | 2 (dX, dY) | relative rectangle outline ✓ — **the graticule box** (`9401 0190 00c8` = 400×200) | yes |
| `0x98xx` | APLL | count + 2·N | absolute polyline: pen→v1→…→vN ✓ (mask `0xFC00`) | **trace** |
| `0x9Cxx` | RPLL | count + 2·N | relative polyline ✓ | logo, vectors |
| `0xA000`/`0xA001` | AFRCT | 2 | absolute filled rectangle ✓ | yes |
| `0xA400`/`0xA401` | RFRCT | 2 | relative filled rectangle ✓ | yes |
| `0xC000` | CRCL | 1 (radius) | circle ✓ | rare |
| `0xCC00`/`0xCC01` | DOT | 0 | plot one pixel at the pen ✓ | yes |
| `0x5C0x` | SCLR (area) | pattern, dX, dY | **selective area clear/fill** — RWP-addressed, logical-op under mask. *This is the firmware's actual per-sweep screen clear* (`5c02 5555 …`) ✓ | heavy |
| `0x5800` | BLKFILL | pattern, dX, dY | block-fill display memory (POST RAM self-test at ROM 0xD6B2) ✓ | boot |
| `0xF000`/`0xF001` | CLR | 3 (data, dX, dY) | area clear (legacy REPLACE path) ✓ | — |
| `0xF400`/`0xF401` | SCLR | 1 (fill) | screen clear (datasheet form) **props** | — |
| `0xE000` | PAINT | seed… | flood fill — **stub** | — |
| `0xF8xx`/`0xFC xx` | CPY / SCPY | 4 | area copy — framed, **not performed** | — |
| `0xD000` | GCHR | 1 | post-glyph attribute/terminator — **stub** (1 arg framed) | per-glyph |

Anything reaching the dispatch with no handler increments `UnknownCmds` and (by
default) panics — desync and genuinely-missing commands are surfaced, never
silently tallied. On the Rev L operating display `UnknownCmds == 0`.

### 2.1 Attribute bits (`AREA : COL : OPM`)

Every graphic draw command (LINE/RECT/POLY/DOT/FRCT) carries attribute flags in
its low 10 bits (Hitachi manual §"drawing command", lines 9083/9377). We
interpret them as:

- **AREA — bit 6 (`0x40`).** When set, the chip clips the draw to the
  area-definition rect (parameter regs `0x08`–`0x0b`). `dispatchCmd` sets
  `c.areaClip`; `setVRAMPixel` skips pixels outside the rect (only when the rect is
  valid). The firmware sets the ADR to the graph before the trace/box draws, so
  honouring this confines them to the graticule. (Disprovable via the
  `Chip.DisableAreaClip` debug toggle.)
- **OPM — bits 1-0 (logical op: 0 REPLACE / 1 OR / 2 AND / 3 EOR).** **Not**
  threaded into the line/poly path — `drawLine` always *sets* the pixel. Every
  line the firmware emits is OPM 0 or 1 (REPLACE/OR ⇒ "set bit"), which is
  behaviourally identical on a 1-bit display, so this is currently harmless.
  (For SCLR the op *is* honoured — see §2.2.)
- **COL — colour bits.** Ignored (monochrome).

### 2.2 SCLR (`0x5C0x`) — the real per-sweep clear

`0x5C0x` is the selective area clear MAME calls SCLR. It addresses the frame
buffer by the 20-bit Read/Write Pointer (RWP, regs `0x0C`/`0x0D`), `MWR` words
per raster line, and applies the pattern word through a logical operation under
the mask register. `cr` bit 10 set ⇒ logical op `cr&3` (0 REPLACE / 1 OR /
2 AND / 3 EOR) under `maskReg`; bit 10 clear ⇒ plain replace. The firmware's
graph clear is `5c02 5555 …` = **AND** with `0x5555` (mask `0xFFFF`), repeated
column-by-column every cycle.

**Why we CLEAN-CLEAR it (`Chip.CleanClear`, default on).** `X AND 0x5555` is
idempotent and the firmware never emits the complementary `0xAAAA` for the graph
(measured: 3338×`0x5555`, 0 regions get both phases) — so a faithful AND only
*thins* the previous sweep's trace and the boot glyphs to a 50% residue that never
erases (the trace "forest" + ghost glyphs). The graph is in a **single
framebuffer repainted every cycle** (graticule grid + trace are explicit
per-cycle draws — §3), so honoring the clear's *intent* (erase) and letting the
repaint restore the content is both clean and correct. `execClear` therefore zeroes
the masked bits for the AND/EOR dither-clear. A **stable-frame snapshot**
([draw.go](../pkg/emu/device/hd63484/draw.go) `maybeFrameSnapshot`, taken when the
graticule grid is repainted) feeds the scanout, so the momentary blank between a
clear and its repaint never shows. Full trail: [DISPLAY_FINDINGS.md](DISPLAY_FINDINGS.md).

### 2.3 WPTN (`0x1800`) — glyph blit & raster

`WPTN` + count word disambiguates three uses ([wptn.go](../pkg/emu/device/hd63484/wptn.go)):

- **count = `0x000A`** → text **glyph**: `fg, bg, row0..row7` (8×16 cell, rows
  bottom-to-top, bit 0 = leftmost). `fg/bg` are pen selectors, not RGB.
- **count = 1** → set the **line stipple** (`c.linePattern`): `0xFFFF` solid,
  `0x1111` dotted, etc. Applied by `drawLine`.
- **MAR-armed bulk raster** → 16,384 data words poured into video RAM (the
  `0x4400` background dot fill); routed to the background plane.

---

## 3. How one display frame is composed

Order observed in a live operating-display capture (`screens/crt_*.txt`):

```
5c02 5555 0000 00d1     SCLR — AND-0x5555 dither-clear the graph region
1800 0001 ffff          WPTN count=1 — set line stipple solid (0xFFFF)
0805 0000               WPR reg5 = 0
8000 0187 000a          AMOVE to the trace start
9841 0008 …             APLL ×8 — a trace segment (AREA bit set → clipped to graph)
…                       (401-vertex trace polyline, X stepping 0→400)
9401 0190 00c8          RRCT 400×200 — the graticule box outline
1800 000a …             WPTN count=10 — text glyphs (REF / PEAK / labels / readouts)
…                       (the hp logo — see §4)
```

So a frame = **clear (SCLR) → stipple setup → trace (APLL) → box (RRCT) →
glyphs (WPTN) → static chrome (logo)**. The trace and box are AREA-clipped to the
graph; glyphs and the logo are not.

---

## 4. The hp-logo drawing routine

The Hewlett-Packard **italic "hp" logo** in the top-left corner is drawn every
frame as a short vector routine at firmware **X ∈ [−40..−26], Y ∈ [216..225]**
(negative X = left label column; high Y = top of screen). The exact captured
command stream and its decode:

```
8000 ffd8 00da   AMOVE(-40, 218)              pen → (-40,218)
8c00 0007 0007   RLINE(+7, +7)      → seg     (-40,218)→(-33,225)   ┐ "h" ascender
8400 fffd fffd   RMOVE(-3, -3)      PEN-UP     → (-36,222)          │ ← gap (intentional)
8c00 0004 0000   RLINE(+4,  0)      → seg     (-36,222)→(-32,222)   │ "h" bowl top
8c00 fffc fffc   RLINE(-4, -4)      → seg     (-32,222)→(-36,218)   │ "h" bowl diagonal
cc00             DOT               → pixel    (-36,218)             ┘
8400 0000 fffe   RMOVE(0, -2)       PEN-UP     → (-36,216)          ← gap (intentional)
9c00 0004        RPLL count=4                                       ┐ the "p"
   0006 0006        v1 (+6,+6)      → seg     (-36,216)→(-30,222)   │
   0004 0000        v2 (+4, 0)      → seg     (-30,222)→(-26,222)   │
   fffc fffc        v3 (-4,-4)      → seg     (-26,222)→(-30,218)   │
   fffc 0000        v4 (-4, 0)      → seg     (-30,218)→(-34,218)   ┘
```

Rendered shape (firmware coords, Y-up — top row is high Y):

```
.......#.......    h-ascender top
......#........
.....#.........
....#####.#####    h-bowl-top  +  p-top
...#...#.#...#.
..#...#.#...#..
.#...#.#...#...
#...#.#####....    h-bowl-bottom + p-bottom
.....#.........
....#..........    h-ascender bottom
```

**Key facts (settled, do not re-litigate):**

1. **Every opcode and coordinate decodes faithfully** — no masked bits, nothing
   dropped (`UnknownCmds == 0`). Verified by replaying the captured FIFO through a
   fresh chip word-by-word ([topleft_test.go](../pkg/emu/machine/topleft_test.go),
   [hplogo_test.go](../pkg/emu/device/hd63484/hplogo_test.go)).
2. **The "discontinuities" are the firmware's own `RMOVE` (pen-up, `0x8400`)
   commands** between strokes — like lifting a pen between letters. They are not a
   decode bug.
3. **AREA-clip is not involved** — the logo's commands render pixel-identically
   with `DisableAreaClip` on (clip rect is the graph; the logo is outside it but
   its draw commands don't have the AREA bit set the way the trace/box do).
4. The figure is a recognisable italic "hp"; it looks rough on screen only
   because it is a ~15×10-pixel amber monogram.

**Open (minor):** in the live scanout the "p" bowl renders cleanly but the "h"
diagonal ascender shows only ~2 of its 8 pixels. Clipping is ruled out, so this
is **scanout addressing** (calc-offset aliasing) in the negative-X / high-Y
corner near the SAR1 window edge — the same addressing layer as the top-left wrap
band, not the command decode.

---

## 5. Diagnostic tooling

- [topleft_test.go](../pkg/emu/machine/topleft_test.go) — boots the firmware,
  renders the live scanout + a by-command core view, isolates compact vector
  figures (the logo), and replays the captured FIFO word-by-word to dump the raw
  opcodes behind any segment in a region. `NOAREACLIP=1` re-renders with the AREA
  clip disabled.
- [hplogo_test.go](../pkg/emu/device/hd63484/hplogo_test.go) — replays a
  `screens/crt_*.txt` capture, dumps line/dot logs and the by-command render.
- `Chip.EnableLineLog()` / `EnableDotLog()` / `StartCmdCapture()` / `CmdCapture()`
  — the introspection hooks these probes use.
- `Chip.DisableAreaClip` — debug toggle to bypass the AREA-mode clip.

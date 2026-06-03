# HD63484 ACRTC Core Rebuild — Prompt & Plan

> Self-contained spec to rebuild the HD63484 (ACRTC) **core** in
> `pkg/emu/device/hd63484/` on a faithful flat-address-space model. Hand this to a
> fresh agent or follow it directly. Front-loaded with **measured facts** so the
> rebuild doesn't re-derive them or reintroduce the coordinate hacks.

## Goal
Replace the HD63484 core with a faithful model of the chip's real architecture — a
**flat, word-addressed frame buffer** with **logical→physical address translation**
and **per-scanline display scanout** — so the HP 8593A operating screen (graticule
grid, spectrum trace, annunciators, softkeys, readouts) renders correctly **by
construction**, without the coordinate hacks the current model accumulated. Keep the
existing command **decoder/parser** and grow the existing **test suite**. Build
monochrome-correct first, with clean seams for layers/colors.

## Locked decisions
1. **Hybrid** — rebuild the addressing + frame-buffer + scanout core; **reuse** the
   command parser/decoder (`parser.go`, `wptn.go` state machine, `strict.go` register
   decode) and the tests.
2. **Faithful core, stub the unused** — model the real flat address space,
   logical→physical addressing (ORG/MWR/bit-depth), and per-scanline scanout with
   multi-screen/window **compositing hooks**; only fully implement what the 8593
   exercises (1 screen, 1 bpp), but leave the seams real, not faked.
3. **Verification = structural tests + golden PNG** (no external photo required).
4. **Mono-correct first**, palette/layer architecture designed in but inert until a
   later phase.

## Established facts — DO NOT re-derive, DO NOT reintroduce the old hacks
*(All measured this session via command-stream capture + Hitachi manual `docs/hd63484_um.txt`.)*

- **One screen.** The firmware programs only **bank 1**: `SAR1`, `MWR1=64`,
  `OMR=0xc588`, `DCR=0xc000`. `SAR0/2/3` never configured. (Pinned by
  `TestScreenLayout`.) Model the 4-screen/window mechanism, exercise one screen.
- **Flat physical frame buffer.** Drawing commands use logical X-Y → physical word
  addresses via **ORG** (origin), **MWR** (words/line pitch), **bits-per-pixel**. The
  **RWP** (WPR 0x0C/0x0D) is the drawing/area pointer; `CP` is the current pen
  pointer. **Every op (pen draw, SCLR/CLR, raster fill) indexes the SAME physical
  buffer.** The current `vram`/`bgVram` split, `orgRow=256`, `displayScanStart=+23`,
  and the off-screen `0x4400` routing are all symptoms of NOT unifying these —
  eliminate them; correct alignment must fall out of honoring ORG/SAR/MWR.
- **The firmware's ORG.** Display-init (ROM `0xA95E`) sets ORG with
  `XW=0x4003, XD=0xa450, MWR1=0x40`. The operating draws and the SCLR RWP both live
  near physical word `0x3a45` — pen-space and clear-space are the **same physical
  space**; honor it instead of offsetting one to match the other.
- **SCLR is an AREA op, not a frame clear.** Every operating SCLR is RWP-anchored,
  clipped to area-def **(0,0)-(400,209)** (the graph), tiling **1-word-wide ×
  209-row vertical strips** (Δx=0, Δy=209) across the graph. Nothing outside the
  graph is ever cleared (why labels/softkeys persist). Max extent 144px×209.
- **No dithering.** The `0x5555`/`0xAAAA` SCLR patterns are a 1-bit-CRT fade trick;
  render the **intent = clear** (clean erase). Do not reproduce dither dots.
- **The graticule grid is a PATTERN, not vectors.** A full-sweep geometry scan finds
  **0 grid polylines**. The only large raster fill is **`0x4400`** (bits 10 & 14 per
  16-px word = paired vertical lines), written **twice** (double-buffered,
  2×16384 words) at MAR=`0x4000` during init, then **persistent**. **NO horizontal
  grid lines** — horizontal divisions are **axis tick marks** (small per-sweep
  `0xFFFF` fills near the axis) plus the box (`ARCT`). 8593 graticule = vertical
  pattern + box + ticks.
- **The graph is repainted every sweep** via `APLL` polylines (0x9800, ~314/window)
  + `ARCT` (~148) for trace/box/ticks, interleaved with strip-SCLRs. The `0x4400`
  grid page is NOT repainted per sweep — it's the persistent display background.
- **Double-buffer / page mapping is the real mechanism** behind "grid only shows a
  sliver." The grid is filled into a page (MAR `0x4000` = row 256) and displayed via
  `SAR1`. Faithful fix: **honor SAR1/ORG so the displayed page contains the grid** —
  NOT the current `+209` render-time offset (a stopgap to delete once addressing is
  unified).
- **Geometry:** output `544×384` (CRT-stretched from `VisibleHeight=256`); logical
  paint space `1024×512` @ 1bpp, `MWR1=64` words/line; graph area-def `(0,0)-(400,209)`.

## Architecture to build
1. **`framebuffer`**: one flat word-addressed buffer (≥ `0x10000` words). No plane split.
2. **`addressing`**: `logicalToPhysical(x,y)` using current **ORG**, **MWR**,
   **bits-per-pixel**; bit-extract/mask for sub-word pixels. Pen (`CP`), RWP area ops,
   and raster MAR all resolve through this one model. Read ORG/SAR/MWR from registers
   — never hardcoded.
3. **`area ops`** (`CLR`/`SCLR`): RWP-anchored rect, logical-op under mask, area-def
   clip, on the flat buffer. Port MAME `command_clr_exec`; keep intent=clear, no dither.
4. **`scanout`**: per-scanline reader — display line `L` → source word
   `SAR1 + L*MWR1`, emit pixels per bit-depth. Compositing hook walking screens by
   priority (`Window > Upper/Base/Lower`), implemented for 1, structured for 4+window.
   Replaces `displayStartRow()+displayScanStart` and the `+209` grid hack.
5. **`palette`/attributes**: scanout emits a *logical pixel value*; palette maps to
   RGBA. Mono = 2-entry palette. Seam for per-screen attributes + multi-bit color.
6. **Reused decoder** feeds 3–5 unchanged: AMOVE/RMOVE, ALINE/RLINE, ARCT/RRCT,
   **APLL/RPLL** (count-prefixed), DOT, WPTN-glyph, WPTN-raster, WPR/AR writes.

## Reuse vs. rebuild
- **Reuse:** `parser.go` decoder + opcode state machine, `wptn.go` glyph/raster state
  machine, `strict.go` AR/WPR decode (already routes SAR/MWR/RAR/OMR/DCR), the whole
  `*_test.go` suite, color constants.
- **Rebuild:** `chip.go` buffer/ORG/SAR state, `area_ops.go` (onto flat buffer),
  `render.go` (→ scanout+palette).
- **Delete:** `bgVram`, `orgRow=256`/`displayScanStart` hardcodes, `+209` grid offset,
  off-screen `0x4400` redirect, `wordByteAddr` +23.

## Build order (each phase ends green)
1. **Flat buffer + addressing**, ORG/MWR/bit-depth from registers. Port MAME
   logical→physical. Unit-test the address math against MAME values.
2. **Pen primitives** (MOVE/LINE/RCT/PLL/DOT/glyph/raster) onto the flat buffer via the
   addressing layer. Keep `TestGlyph*`, polyline tests green.
3. **Area ops** (CLR/SCLR) RWP-addressed on the flat buffer, area-def clip,
   intent=clear. Keep `clearaccuracy_test.go` + `TestCRTSclrClearsGlyph` green
   (re-express in the unified space — no `bgVram`).
4. **Scanout from SAR1/MWR1** + mono palette. Grid/box/ticks/trace/text/annunciators
   land correctly **without offsets**. Keep `TestGraticuleGridVisible`,
   `TestScreenLayout`, `TestMachineBootScreen` (regenerate golden once), `POST` green.
5. **Seams verified**: smoke test flips bit-depth / adds a 2nd screen / swaps palette,
   shows scanout/compositor honors it (even if the 8593 never does). The "extensible" proof.
6. **(Later) color/layers**: populate palette + per-screen attributes + window
   compositing. No core changes.

## Verification (structural + golden)
On a booted operating screen, assert:
- Vertical graticule grid present in the graph interior (extend `TestGraticuleGridVisible`).
- Trace present lower graph; box outline present; axis tick marks present.
- Annunciators/softkeys/readouts present **outside** the graph and **never cleared** by SCLR.
- `TestScreenLayout` (one screen) holds.
- `TestMachineBootScreen` golden (regenerate once render is correct) + `POST` pass.
- Addressing unit test pinning logical→physical against known firmware ORG/MWR.

Each phase: `DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib go test ./pkg/emu/...` green.

## MAME port targets (`src/devices/video/hd63484.cpp`)
Map each rebuild component to the MAME function to port from. (Confirmed names —
`command_clr_exec`, `draw_graphics_line`, `video_registers_w` — are already cited in
the current `pkg/emu/device/hd63484/` port; verify exact signatures against the source.)

| Rebuild component | MAME function(s) to port |
|---|---|
| Command pump / opcode dispatch | `process_fifo()` (the big opcode `switch`) |
| Register file (AR/WPR: ORG, SAR, MWR, RAR, OMR, DCR, mask, area-def) | `video_registers_w(int offset)` |
| Logical→physical addressing + pixel read/write under bit-depth/mask | the `m_ram` addressing + pixel get/set helpers used inside the command handlers |
| Area ops (CLR / SCLR — RWP-anchored fill, logical-op, mask) | `command_clr_exec(...)` |
| Pen primitives (MOVE/LINE/RCT/PLL/DOT/PTN) | the per-opcode handlers dispatched from `process_fifo()` (Bresenham line/rect, dot, polyline, pattern) |
| Per-scanline display scanout (`SAR1 + L*MWR1`, bit-depth emit) | `draw_graphics_line(...)` |
| Display timing / geometry from sync regs | `recompute_parameters()` |

## Reference sources
- MAME `src/devices/video/hd63484.cpp` (addressing, area ops, scanout — port target).
- `docs/hd63484_um.txt` (manual: screens/SAR/MWR/RWP/CP, §1.5 logical/physical
  addressing, §3 screen organization + superimposition).
- Committed tests: `TestScreenLayout`, `TestGraticuleGridVisible`, `clearaccuracy_test.go`.

## Definition of done
Operating screen renders correctly from a unified flat-address model with **no
coordinate hacks** (`grep` shows no `displayScanStart`, no `+209`, no `bgVram`, no
hardcoded `orgRow`); all structural tests + golden + POST green; bit-depth/screen/
palette seams demonstrably functional though inert for the 8593.

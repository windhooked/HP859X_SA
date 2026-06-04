# Display investigation — running ledger

Authoritative record so we STOP re-deriving and re-proposing disproven ideas.
Update this as facts change. If a theory is in "DISPROVEN", do not raise it again.

> **Command reference:** the full HD63484 drawing-command set (opcode table,
> attribute bits, per-frame composition order) and the decoded **hp-logo routine**
> live in [HD63484_DRAWING_COMMANDS.md](HD63484_DRAWING_COMMANDS.md).

## ESTABLISHED (deterministic, don't re-derive)
- **The top-left "hp" logo is decoded faithfully** (italic Hewlett-Packard
  monogram, firmware X[−40..−26] Y[216..225]). Its "discontinuities" are the
  firmware's own `RMOVE` pen-ups (`0x8400`), NOT a decode bug; `UnknownCmds==0`.
  AREA-clip disproved as a cause. Residual: scanout aliasing drops most of the "h"
  ascender (addressing layer, not decode). See HD63484_DRAWING_COMMANDS.md §4.
- **Register-derived scanout works** (committed): displayed image = `SP1`(=256 lines)
  × `MWR1`(=64 words/line, 1024 px) scanned from `SAR1`. No `coreXOrigin`/`+209`/
  `graphCoreRow*` magic. Renders the full display incl. all softkeys.
- Firmware programs **one Base screen** (`SAR1=0xFFFFF`); `SAR0/2/3` unprogrammed.
- **Glyphs (WPTN) render correctly.**
- **Faithful `SCLR`** (real `AND` under mask, not "clean clear") makes the **dotted
  graticule grid correct**.
- **0 unknown/unhandled commands** — remaining issues are MISINTERPRETATION, not
  missing handlers.
- The `0x4400` fill is a paired-vertical-line pattern at MAR `0x4000` (upper region).
- Tools: `HD63484_REGDUMP`/`HD63484_REGPANIC` (unmodeled-register watch);
  `RenderMemoryAreasCollage`, write histogram; experiment flags `APLLUpper`,
  `GraticuleToUpper` (default off).

## DISPROVEN / RULED OUT (do NOT propose these again)
- ❌ **CRT persistence** is NOT the cause of the trace "forest". (User: #1 doesn't hold.)
- ❌ **Moving the trace / graticule region to a separate (upper) buffer does NOT fix
  the forest** — tested with `GraticuleToUpper`; the trace still forests in the
  isolated region. So it is NOT cross-region contamination and NOT a memory-region
  problem.
- ❌ The `0x4400`/Window **superimpose is NOT "the answer"** to the trace problem.
- ❌ The old **"clean clear" interpretation of `SCLR`** was wrong.

## FIXED THIS SESSION
- ✅ **SCLR addressing offset (+2 words / 32 px).** `execClear` round-tripped the
  RWP through the legacy `wordByteAddr` (`orgRow=256`,`+23`,`orgCol=48`) →
  `calcOffset` = `off + 2` — so the area-op cleared **32 px away** from where the
  pen draws (trace/box). The trace landed OUTSIDE the SCLR region. **Fix:** address
  the SCLR core write **directly from the RWP word `off`** (same space as the pen),
  area-def clip computed as the exact inverse of `calcOffset` about `orgDPA`. Trace
  is now INSIDE the SCLR region (`base_bycmd.png`). (User identified this.)

- ✅ **AREA-mode clip ignored → trace/box leaked past the graph.** Every graphic
  drawing command encodes `AREA:COL:OPM` in its low bits (manual 9083-9084); the
  chip clips the draw to the ADR (area-def) when the AREA bit is set. We masked the
  low bits off (`& 0xFC00`), so draws weren't clipped → trace leaked above/below.
  Deterministic: in-graph `APLL 0x9841` + `ARCT 0x9070` BOTH have **bit 6 (0x40)**
  set (= AREA clip); glyphs are WPTN (no AREA). **Fix:** `dispatchCmd` sets
  `c.areaClip` when a drawing command has bit 6; `setVRAMPixel` clips to the
  area-def (regs 0x08-0x0b). Trace now confined to the graph box
  (`scanout_clipped.png`). (User identified this — "mode not closed".)

## RESOLVED — trace forest + ghosts + graticule (the whole thread)
- ✅ **The graticule is the firmware's 10×8 VECTOR grid, repainted EVERY cycle**
  (9 vertical + 7 horizontal long axis-aligned `ALINE` lines + an `ARCT` box,
  ×11 per capture) — NOT the `0x4400` raster dither (disproven). Single
  framebuffer, deterministic repaint; **there is no phosphor to fade** (user's
  correction — the "fading" theory is dead).
- ✅ **The trace "forest" + ghost glyphs were the idempotent AND-dither clear.**
  The firmware clears the graph each cycle with `SCLR data AND 0x5555` (mask
  `0xFFFF`, no complementary `0xAAAA`, no REPLACE) — `X AND 0x5555` is idempotent,
  so old content (previous trace, boot glyphs) only thinned to a 50% residue and
  never erased.
- ✅ **Fix = CLEAN CLEAR (option B, now default).** `execClear` reinterprets the
  AND/EOR dither-clear as a clean erase of the masked bits (`Chip.CleanClear`,
  default true). The graticule + trace are then the firmware's per-cycle repaints,
  not a dither residue; the forest and ghosts are gone.
- ✅ **Frame-boundary snapshot** ([acrtc.snapshot] via `maybeFrameSnapshot` on the
  grid-line repaint): the scanout renders a STABLE, complete frame, so the
  clean-clear's momentary blank-then-repaint never shows as a missing graticule.
- ✅ **Clean-clear restores the original `clearaccuracy`/`crtscreen` tests** (they
  were written for clean clear; the AND experiment had broken them). Their
  `sclrRect`/glyph helpers were realigned to address the RWP via the core
  `calcOffset`, fixing the `xmax`-column edge coverage. Golden regenerated
  (`RenderScanoutByCmd`, by-command + legend). All display tests pass.
- Full command-level writeup: [HD63484_DRAWING_COMMANDS.md](HD63484_DRAWING_COMMANDS.md).

## Still open (separate)
- Top-left hp-logo "h" ascender shows ~2 of 8 pixels — scanout addressing aliasing
  in the negative-X/high-Y corner, not the decode (HD63484_DRAWING_COMMANDS.md §4).
- Top-left garbage band (SAR1=0xFFFFF wrap) — register-derived addressing follow-up.

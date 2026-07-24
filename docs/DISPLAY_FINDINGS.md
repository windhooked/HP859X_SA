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

## Glyph overlap on the FIRST menu switch (2026-07-24) — transient, self-healing
- Symptom: freq_menu.png (first F9 after boot) shows merged label text
  ("CoFREQ"): boot-DLP-drawn softkey labels sit at cell offsets the operating-
  loop label walker (fcn.e7a2) does not exactly cover, so its opaque glyphs
  leave partial residue. Steady state is CLEAN (menu_nav.png — subsequent
  redraws align cell-for-cell and the opaque blits fully overwrite).
- NOT a missing clear: instrumented the F9+F2 redraw — the logical-SCLR
  no-area-def no-op branch fired 0x (counter Chip.SCLRNoAreaDef); 4754 CLR
  area ops executed. TestGlyphOverlapDiag (DIAG probe).
- Verdict: minor first-switch transient; revisit only if a real-HW capture
  shows the first switch is clean there (would imply a missed label-column
  clear in the boot->operating handoff).

## Graticule grid mechanism + the CleanClear trade-off (2026-07-24)
- **The dotted reticule = stippled vector lines + the faithful SCLR dither.**
  With `CleanClear=true` (former default) the grid was ERASED every sweep and
  missing from natural renders (trace_natural.png had no grid at all); with the
  faithful SCLR (AND under mask — now the DEFAULT) the grid renders exactly as
  the reference screens/trace_longrun.png. Locked by the longrun comparison
  (screens/longrun_faithful.png vs longrun_clean.png, TestLongrunGridDiag).
- **The graph erase is a per-column flying erase bar,** decoded from the live
  op stream (screens/cmdtrace_op.txt): per column `WPR1(CL1)=AAAA; WPR4(mask)=
  FFFF; RWP=column; SCLR AND D=5555 (ax=0, ay=0xD1)` — 326 column ops sweeping
  all 25 words (400 px) of the graph; plus 42 small AAAA rect ops (annunciator
  regions). ClearColLog (chip diagnostic) + TestColEraseDiag measured full
  coverage, mask always FFFF, D always 5555.
- **The AND is IDEMPOTENT (manual-verified, um.txt SCLR §13 = MAME
  command_clr_exec):** old lit pixels at bit positions where D has 1 survive
  forever. A trace spike at even x survives the 5555 phase FULLY — that is the
  trace-forest residue seen in faithful natural renders, plus 50%-thinned text
  inside the column range and stale-readout ghosts.
- **RESOLVED (2026-07-24, same session): the residue question was OUR BUG — the
  display is PHASE-MULTIPLEXED by pixel parity.** The HD63484 draws a pixel
  with the colour register's bit at the pixel's bit position within its word;
  the 8593 firmware NEVER draws solid: measured, ALL 1872 APLL trace draws run
  with CL1(WPR 0x01)=0xAAAA (odd-phase pixels) and ALL 944 flying-erase columns
  are `SCLR AND 0x5555` (clears exactly the odd phase). Draw and erase are
  phase-locked; the grid/box/static chrome live at the 5555 phase, untouched by
  the trace eraser. Our pen drew SOLID (both phases) → the even-phase half of
  every trace could never be erased → the historic "trace forest".
  Fix: `penPhaseLit` gates drawLine/DOT pixels by CL1's bit at the pixel's
  calcOffset position (CL1==0 → solid fallback for raw unit-test draws).
  Verified: graph-region phase census after a 300M-cycle run = odd 1199 (≈2
  in-flight traces) / even 1942 (box+grid) — a clean steady state; the LIVE
  render matches the trace_longrun reference (thin floor, full dotted grid).
- **Stale-snapshot caveat:** the stable frame snapshot (maybeFrameSnapshot) can
  lag far behind the live buffer and SHOW long-erased accumulation — diagnostic
  renders should use the LIVE buffer (the GUI beam integration does).
- **OPEN (small): stopped-content ghosts** — content that ceases being redrawn
  (old readouts/markers) leaves a 50%-dither residue until a region clear;
  span/menu changes wipe it (verified). Check real-HW footage for the same.
  2026-07-24 follow-up: the BOOT emits a TWO-PHASE full-screen erase (477x
  text-row blocks AND-0xAAAA + 210x columns AND-0x5555, rwp 0x202..0x3a44) that
  our area-def gate silently SKIPPED — now executed faithfully (no-area-def
  logical SCLRs run per MAME, full-word mask; also fixed core.mask never being
  initialised to 0xFFFF). The mid-graph ghost bands PERSIST regardless: they
  are drawn at operating-entry (after the boot erases) and only the odd-phase
  column erase covers their rows. ❌ DISPROVEN: gating GLYPH fg pixels by CL1
  (like the pen) — dithers ALL text to fragments AND does not remove the bands;
  the glyph blit colour source is NOT CL1 (fg/bg packet words are 0000/0000 —
  the real source is undecoded; candidates: pattern-RAM colour pair, PR5, or an
  explicit readout-region clear we have not identified). Do not re-propose.
- GUI note: the beam integration + phosphor bridges the mid-cycle text
  thinning (text repaints each annunciator cycle); only STATIC probe renders
  show it eaten.

## Sweep-sync anchor: the horizontal trace drift (2026-07-24, SOLVED)
- Symptom (GUI): the whole spectrum slid horizontally, continuously, rate
  proportional to emulated-cycle rate. NOT the frequency window: the YTO coil
  DACs are STATIC during natural runs (TestDACDriftDiag — no ramp, no walk).
- Root: BOTH sample paths used free-running counters. DetectADC advanced its
  index on EVERY 0xFFF200 read, but the IRQ1 handler + background polls also
  read f200 — measured ~3.2x faster than the store pointer A5 advanced
  (TestPollSyncDiag) → the served spectrum position walked relative to the
  buffer slot being stored → horizontal slide. The polled-video hook had the
  same shape (polledReads/spp modulo counter).
- Fix: SweepEngine.PosFunc — the machine serves f200 reads by the FIRMWARE'S
  OWN store index, (A5 − 0x2FD508)/2, whenever A5 points inside the trace
  buffer (Machine.traceBufIndex; same anchor tried first in the polled hook).
  The sample always matches the slot about to be written; drift is
  structurally impossible. Verified: enginePos tracks A5 in lockstep, wraps
  cleanly per sweep; longrun render = thin single floor + stable CAL peak.

## ★★★ THE DISPLAY IS 2 BITS PER PIXEL (2026-07-24) — the definitive model
- Boot display-init (ROM 0xA95E table) programs **CCR=0x0180 → GBM=1 → 2bpp,
  8 px/word**; MWR1=64 words × 8 px = the 512-px visible width exactly. The
  "phase multiplex by pixel parity" was the 1bpp mis-mapping of this: the two
  parities are really the TWO BIT-PLANES of a 2bpp pixel. Colour-register
  replication (manual): CL 0x5555 = solid colour 01 (text/graticule plane),
  0xAAAA = colour 10 (trace plane), 0xFFFF = colour 11 (both). NOTHING is
  dithered on the real chip.
- All ops close: `SCLR AND 5555` clears the trace plane, `AND AAAA` the text
  plane, `AND 0` both; trace APLL/DOT are OPM=OR (never destroy text bits);
  glyph PTN (COL=00 REPLACE, cell 8×10 from `SZ=0x0907`) writes CL1/CL0 colour
  fields per pixel — one AND-AAAA rect fully retires text (the eraser is
  fcn.C084, template @0xBFF4, callers = the annunciator/readout module
  fcn.E54E/E5C4 etc.).
- PR-map corrections: **Pr05-07 = Pattern-RAM Control (PRC)**, not colour
  (Pr05=pattern pointer+zoom counter — the post-glyph `WPR5=0` is the pointer
  reset; Pr06=scan start; Pr07=scan end: 0x9070=8×10 glyph window, 0x00F0=16×1
  stipple window). WPTN `$180X`: X = pattern-RAM start address; the glyph
  packet's 10 words are 10 PATTERN ROWS (2 descender rows + 8 body), NOT
  2 colours + 8 rows.
- The "ghost text bands" were the REAL power-on text (HP-IB ADRS / COPYRIGHT /
  rev) — legitimate content the real instrument also shows until a key clears
  it — rendered half-dithered by the 1bpp mis-mapping. At 2bpp it renders
  fully legible and erases correctly.
- Implemented: core CCR feed (strict.go ar:0x02), penColor/colorField OR-draw
  (draw.go), glyph REPLACE with CL1/CL0 fields (wptn.go), bpp-aware scanout
  family (512-wide output). Unit tests keep 1bpp defaults (CCR unset).
- OPEN (2bpp polish): (a) right-half solid trace block in long runs (erase/
  draw extent mismatch to re-measure under 2bpp); (b) left-edge column of
  wrapped negative-x content; (c) right softkey-label clipping at 512 —
  ORG/window fine-tuning; (d) glyph 10-row blit (descender rows 0-1 currently
  dropped); (e) 2bpp intensity mapping (colour 01 vs 10 brightness — needs
  real-HW footage).

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

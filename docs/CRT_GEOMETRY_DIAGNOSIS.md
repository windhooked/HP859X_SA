# CRT-driver geometry diagnosis (HD63484)

**Date:** 2026-05-31 · **Status:** diagnosis only (no rendering changes) · **Tool:** `cmd/crtdiag`
**Ground truth:** the real 8593E boot photo (`docs/8593E_disp.jpeg`).

## Symptom

Our boot render (`screens/boot_clean_post.png`) places the graticule **half-size in
the top-left** of the screen, with a **second empty box** overlapping below it and the
content **anchored at (0,0)**. The real instrument shows ONE graticule filling the
centre of the 640×480 screen with annunciator columns in the right margin.

## What the firmware actually emits (measured, `cmd/crtdiag`, 200M-cycle boot)

269 `drawLine` calls captured. Coordinate bounding box **x −40..400, y 0..440**.

- **Graticule frame + grid** = a **400 wide × 200 tall** box at `X[0..400] Y[~5..200]`:
  - 11 vertical grid lines at x = 0,40,80,…,400 → **10 columns, pitch 40**
  - 8 horizontal grid lines at y = 0,25,50,…,200 → **8 rows, pitch 25**
  - i.e. a 10×8 graticule whose divisions are **40 px wide × 25 px tall** (1.6:1).
- **A SECOND empty frame** at `X[0..400] Y[240..440]` (no internal grid).
- A few marks at **negative X** (−40..−32) near y≈220.
- HD63484 display-partition registers (`PR12/14/16/18` upper/base/lower/window base,
  scroll) are all **0**; only `PR04 MEMWIDTH = 0x3F` is set. So the firmware is **not**
  using the chip's split-screen base-address registers.

## Root cause — the renderer applies NO coordinate→raster mapping

[draw.go](../pkg/emu/device/hd63484/draw.go) writes each command coordinate `(x,y)`
directly as VRAM pixel `(x,y)`, and [render.go](../pkg/emu/device/hd63484/render.go)
shows the raw **top-left 640×480** of the 1024×512 VRAM. The HD63484's coordinate and
display controls are stored but never applied:

1. **No vertical display scale.** The graticule is drawn 200 px tall but on the real CRT
   fills ~340 px (divisions are ~square, not 1.6:1). The real chip's display-raster setup
   maps one drawing unit to >1 scanline vertically; we render 1:1, so the graticule is
   **vertically compressed to ~42 % of screen height**. (Horizontally 400/640 ≈ 62 % ≈
   correct — the annunciators legitimately sit at x>400, matching the photo.)
2. **No display-window selection.** We render raw top-left VRAM, so the **second frame**
   (`Y[240..440]`) — off-screen / alternate content the real display window does not show —
   is drawn on top of the graticule. Hence the "duplicate box".
3. **ORG (drawing-origin) ignored.** Captured but never added to draw coords
   ([parser.go](../pkg/emu/device/hd63484/parser.go) `stORG2`). The negative-X marks
   clip off the left edge instead of being shifted into view.

## Secondary bug — parser false-dispatch on `0x0000` (and other opcode-like data)

`cmd/crtdiag` counted **5137 "ORG" commands** in one boot — impossible for a real UI.
The parser exact-matches `0x0000` as the ORG opcode (`dispatchCmd`), so any `0x0000`
**data** word (coordinates, fill words, glyph rows — extremely common) is falsely
dispatched as ORG and **consumes the next two words as an origin, desyncing the stream**.
This corrupts subsequent draws and is a likely contributor to the garbled layout
independent of the geometry mapping. (dispatchCmd already documents this class of
false-positive for the shape opcodes; `0x0000`/ORG is the worst case because `0x0000` is
the most common data word.)

## Fix path to MATCH the real 8593E (future work, not done here)

1. Model the HD63484 **display-raster / vertical scale** so the 200-px-tall drawing maps
   to the full graticule height (≈ scale 480/«display-RY units»); horizontal is ~1:1.
2. Render only the **active display window** (the region the chip's display-base/raster
   selects) instead of raw top-left VRAM, so off-screen content (the second frame) is not
   composited onto the visible screen.
3. Apply **ORG** to drawing coordinates.
4. Harden the **parser** against `0x0000`/opcode-like data desync (parameter-framing
   validation, or context that suppresses ORG mid-parameter), so the draw stream stays in
   sync.

Tools added: `cmd/crtdiag` (geometry probe), `Chip.Reg(n)` (read-only register accessor),
`Chip.OrgLog` (ORG-command capture). All diagnostic — no rendering behaviour changed.

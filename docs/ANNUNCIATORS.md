# Status annunciators (OVEN COLD / ADC FAIL / …) — findings

The boot UI flashes `OVEN COLD` and `ADC…FAIL`. Investigation of what drives
them and how modelable they are.

## What they are

- **Length-prefixed string table** at ROM `0x2b35x`–`0x2b51x`, indexed by an
  annunciator number: `… TH / LIN / MKR / CNTR / ADC-TIME FAIL / ADC-GND FAIL /
  ADC-2V FAIL / OVEN COLD / PG / FREQ UNCAL / MEAS UNCAL / AVG / SRQ / OFFST /
  WA SB / SC FC / CORR / HRM / TG UNLVL / REF UNLOCK / …`.
- The **annunciator subsystem** assembles the active set into a 10-byte buffer
  at RAM `0xFFAC9E` (`$ac9e`), gated by `$abfc`/`$abfe`, and the display routine
  (~`0x2b2b0`/`0x2b300`) blits them. The **flashing is normal** alarm-annunciator
  behaviour (blink), not a bug.

## Status sources

- `0xFFF614`/`0xFFF616` (`tst.b` at `0x49A0`/`0x49AC`) → `$bb2c` bits 13/12 →
  drive `0xFFF610`/`0xFFF612`. This is **board-config / presence detection**
  (which A-assemblies are installed), *not* oven/reference. The honest model
  (per docs/rom_analysis.md) is `0xFFF618` as a serial board-ID/option register
  returning the correct config word.
- **ADC-TIME / ADC-GND / ADC-2V FAIL** are **actively-evaluated cal results**,
  sensitive to **sweep timing** (rom_analysis.md): `ADC-TIME` clears when the
  sweep is driven (IRQ1 step + IRQ6 sample with detector data on `0xFFF200`);
  `ADC-GND`/`ADC-2V` need **mux-aware ADC detector values** at the ground / +2V
  reference channels (ties to the analog conversion model in `analogbus.go`).
  Black-box injection is fragile — ramping `0x200A3C` + IRQ6 re-broke ADC-TIME.
- **OVEN COLD / REF UNLOCK** are 10 MHz-reference status (oven-warm comparator /
  PLL lock). Their exact read site was not pinned in this pass; they are not the
  `0xFFF614/616` board-config path.

## Assessment

These annunciators are **cosmetic** — a real 8590 operates with `OVEN COLD`
showing (the oven warms over minutes) and `ADC FAIL` during cal. They do **not**
gate the operating loop. Clearing them faithfully requires:

1. The **sweep/trace acquisition** subsystem (IRQ1/IRQ6 + detector data on
   `0xFFF200`) so the ADC cal re-evaluation passes — a substantial, separate
   task (also what's needed to draw a real trace).
2. **Mux-aware ADC reference values** (GND → bottom-scale, +2V → top-scale) in
   `analogbus.go` for the cal channels.
3. The **reference-status** read for OVEN/REF (modelled as warm/locked).

So this is a multi-part subsystem, not a one-line fix, and it overlaps heavily
with the **sweep/trace** work (which would also fix the blank graticule/trace
the GUI shows). Recommendation: tackle annunciator-clearing *together with* the
sweep/trace acquisition, rather than as standalone cosmetic patches.

# Comprehensive analog-model plan (8593A virtual instrument)

Synthesized from the 08590-90316 service guide, the 08590-90235 programmer's
guide, the CLIP, and the project's firmware RE. Scope chosen with the user:

- **Fidelity: semi-physical** — model DAC→frequency + a frequency-domain spectrum
  + RBW/detector shaping, so the trace tracks center-freq/span/RBW. We do NOT
  simulate actual RF circuits (mixers/IF), only their input→output behaviour as
  the firmware observes it through the control/readback registers.
- **Trace signal: thermal noise floor + the internal 300 MHz CAL signal**
  (−20 dBm), as a real boxed analyzer shows after PRESET.
- **Milestone order: (1) clean boot → (2) visible trace → (3) interactive tuning.**

## Ground-truth facts the model is built on

- **Trace/measurement units:** 0–8000 MU, 8000 = reference level (top of screen),
  1000 MU/div = 10 dB/div, 1 MU = 0.01 dB. `dBm = (MU−8000)·0.01 + ref_level`.
  401 points/sweep = 802 bytes (matches our IRQ6 buffer 0x2FD508→0x2FD82A).
- **A7 tuning DACs (0xFFF728/72A):** Span, YTO coarse, YTO fine, YTO FM — all
  12-bit (0–4095). REF_LVL_CAL 8-bit (default 200, healthy 130–185). BW5/6/7
  companding DACs. (Exact A7 register *numbers* per DAC still need the CLIP or
  rom.asm; we infer from select usage.)
- **Counterlock is a servo:** firmware iterates the YTO tune DACs until the
  *counted* first-LO = target (`1st LO = N·F_SO + samplerIF`); FREQ UNCAL if
  >20 MHz off. The model must return a count consistent with the programmed DAC,
  or CAL FREQ never converges.
- **ADC (0xFFF75C/75E):** 12-bit signed, range-checked [−0x200,+0x1FF]; 0x9A
  status state machine (0x06 idle / 0x07 EOC); 2-point cal GND→bottom, +2V→top;
  ADC-TIME FAIL = conversion interval too long.
- **OVEN COLD = a fake 5-minute timer**, no temperature sensor.
- **A7 reg 3 (select 0x13xx)** = analog settled/lock status; bit6 valid (already
  modelled bits 6-7 = settled; un-froze the UI).
- **HSWP** (sweep high / retrace low) + **ADC_SYNC** (one strobe per ADC
  conversion / trace point, resets the peak detector) = the sweep handshake.

## Architecture — `pkg/emu/analog` (new) + extend `pkg/emu/device`

```
analog.Subsystem
 ├─ FrequencyModel   DAC(span,coarse,fine,fm)+band → tuned f; counterlock count
 ├─ SpectrumModel    f → input level (dBm): noise floor + 300 MHz CAL + injected
 ├─ Detector         level + RBW + detector-mode + ref-level/atten → ADC MU
 ├─ SweepEngine      IDLE→ARMED→SWEEP(HSWP↑)→RETRACE(HSWP↓); ADC_SYNC→IRQ6/point
 ├─ CalModel         ADC 2-pt, REF_LVL_CAL servo, counterlock convergence
 └─ StatusModel      lock (REF/φLOCK), OVEN 5-min timer, A7 reg-3 settle
```

Wiring: the A7 bus (0xFFF728/72A) and analog bus (0xFFF75C/75E) reads dispatch
into FrequencyModel/Detector/CalModel/StatusModel. The SweepEngine is driven
from the machine run loop (it owns the IRQ6 cadence + HSWP + the RAM-flag
handshake), reading the Detector per point.

## Milestone 1 — CLEAN BOOT (current target)

Make every power-up self-test/cal pass so the screen shows no FAIL/UNLOCK/COLD.
Order (tractable → deep):

1. **OVEN COLD** — find the 5-min timer counter + threshold; ensure our IRQ5
   tick cadence advances it so it elapses (or model the option flag so it's
   never shown). *Pure timer.*
2. **ADC self-cal (ADC-GND/2V/TIME FAIL)** — extend analogbus.go: GND channel →
   near bottom in-range, +2V → near top in-range, conversions complete within
   the ADC-TIME window. Make fcn.5E6E8 measure consistent values → cal passes.
3. **REF UNLOCK / φ LOCK OFF** — StatusModel: report the A9/10 MHz reference
   locked + the A7 lock-detect asserted (the lock status bit the firmware polls).
4. **FREQ UNCAL** — FrequencyModel counterlock: return a count == target for the
   programmed YTO DAC so the YTO-tune servo converges (<20 MHz error).
5. **FAIL: DFCF (POST / PWRUPOB)** — decode the POST result; model the failing
   subsystem self-test (likely the 68230 PIT stub at 0xEF8000 and/or the I/O-bus
   self-test) so PWRUPOB reports pass.

## Milestone 2 — VISIBLE TRACE

SweepEngine + Detector produce a real swept measurement: per sweep, for each of
401 points compute f (from the ramp position + tuned center/span), evaluate
SpectrumModel(f) → dBm → Detector → MU (0–8000) → ADC; drive IRQ6/ADC_SYNC so the
firmware fills the buffer, returns to the orchestrator, clears $b0a0 bit11, and
paints the trace (a noise floor with a peak at the 300 MHz CAL bin when in span).

## Milestone 3 — INTERACTIVE TUNING

FrequencyModel honours CF/span/RBW so changing them (via HP-IB or front panel)
re-tunes the YTO DACs and the trace follows; RBW changes the peak shape; ref-
level/atten shift the MU mapping. A genuinely controllable virtual 8593A.

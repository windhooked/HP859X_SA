# Measure-mode / trace-draw blocker — session handoff (2026-06-05)

Single entry point for the next session on the trace-draw blocker. The deep detail
lives in the docs linked below; this is the bridge so a fresh session doesn't
re-derive — or re-try the ruled-out angles.

## TASK

Make the virtual HP 8593A draw the spectrum trace — crack the operating-loop /
measure-mode blocker so the firmware enters continuous-sweep spectrum **MEASURE**
mode (the trace + graticule then self-sustain). This is the documented multi-session
subsystem.

## READ FIRST (canonical, already committed)

- [docs/TRACE_DISPLAY_PATH.md](TRACE_DISPLAY_PATH.md) — esp. "WHY a9a0 SETTLES -1" + the 2026-06-05 CORRECTION
- [docs/DRIVETICK_BLOCKER.md](DRIVETICK_BLOCKER.md) — the Gate 1+2 map
- [docs/A7_ANALOG_IO_BUS.md](A7_ANALOG_IO_BUS.md), [docs/ANALOG_BUS_MODEL.md](ANALOG_BUS_MODEL.md)
- [docs/DISPLAY_FINDINGS.md](DISPLAY_FINDINGS.md) — the "don't re-derive disproven ideas" ledger
- Probes (run with `DIAG=1`): `pkg/emu/machine/sweeparm_diag_test.go` + `pkg/emu/machine/oploop_diag_test.go`
- Branch: `display-clean-clear-graticule`

## CURRENT STATE (measured, sweep-driven boot via `BootToOperatingWithSweep`)

- `fcn.18568` (operating loop) **IS entered ~699×** (reliable signal: `b010` write
  @0x1856C). The PASSIVE boot (`BootToOperating`) does NOT enter it — sweep-driving
  advances it in.
- Dominant post-boot idle is the **BOOT-MEASUREMENT analog loop**: PC pages `0x4800`
  (detector) + `0x5E400` (analog `0x9A` poll, ~9800 reads of `0xFFF75E`). The firmware
  is actively MEASURING in boot-measurement mode and never transitions to the
  operating-loop spectrum sweep.
- Sweep never arms: `a9a0 = -1` (`0xFFA9A0`, point counter, computed by `fcn.8f04`).
  It disables because the firmware is **NOT in spectrum measure mode**:
  - `0xB0EC = 0x01` (PRESET default; spectrum modes are `0x2D/0x31/0x36`), hardcoded by
    PRESET `fcn.4df34` (`moveq #1,d6; move.w d6,0xB0EC` @0x4E01C). Spectrum `0x31` is set
    by mode setter `fcn.21c96` (clamp), called only from the measurement state machine
    `0x22xxx`.
  - CONTS (`0xB0A1` bit 3, continuous-sweep mode) is clear. Set ONLY by `fcn.5f968`
    (slot 0x550) via command executor `fcn.12b10` (@0x183B6 in fcn.18568) ← `fcn.12288`
    (@0x126D8). At boot it runs **0×** — no CONTS command is ever queued.
- The A7 reg-3 settle poll (measurement-state-machine gate) is **ALREADY modelled**
  (a7iobus.go returns `0x80` settled); gate flags `b1e0` bit11 / `bf26` bit16 / `ad7d`
  bit5 are SATISFIED. So the idle is NOT a stuck flag — it's a missing **TRANSITION**.

## GATE CHAIN

```
trace not drawn / graticule eroded
  ← firmware never enters continuous-sweep spectrum MEASURE mode
  ← boot-measurement orchestrator 0x17400 never hands off to fcn.18568 in measure mode
  ← sweep never arms (a9a0 = -1)
  ← mode 0xB0EC ≠ spectrum AND CONTS (b0a1 bit3) unset
  ← the measure-mode ENTRY (PRESET measurement cycle completing + mode/CONTS set) never fires
```

DRIVETICK Gate 2: even forcing the sweep, the continuous-sweep DLP source
(slot 0x12CA → `0x5ECEE` → `__GTTDRW` @0x65986) that paints the trace is never
scheduled (DLP scheduler `fcn.d18` = 0× during the sweep).

## RULED OUT — DO NOT RE-TRY (proven this session, evidence in the docs)

- **CRT-controller sync of the sweep** — the HD63484 has NO frame/vsync MPU interrupt
  (datasheet §2.3.3). Disproven.
- **Plane separation (`GraticuleToUpper`) + CRT phosphor/persistence** — disproven
  (DISPLAY_FINDINGS.md "DISPROVEN").
- **Cal data / cal-validity gating the measure mode** — PROVEN irrelevant: A/B boot
  (blank vs valid NVRAM) is BYTE-IDENTICAL (b0ec=0x1, a9a0=-1, b0a1=0, vectors=21310);
  cal NVRAM read only by the 0x454A checksum (`TestCalGatesMeasureModeDiag`).
- **Forcing leaf cells** (a9a0 / befa / b0a1 bit3 / b0ec=0x31) — renders the operating
  UI but does NOT schedule `__GTTDRW`. The DLP scheduling needs the firmware to
  NATURALLY enter measure mode. Per the project rule "a half-mock is worse than the
  clean screen", do not ship forced cells.

## NEXT STEPS (hypotheses, in order)

1. **Why doesn't boot-measurement transition to operating-loop spectrum?** Likely the
   PRESET ADC cal `fcn.5E6E8` (a placeholder in analogbus.go — ANALOG_BUS_MODEL.md)
   never "completes". Instrument whether `fcn.5E6E8` completes or loops, and what
   analog reading (via `0xFFF75C/75E` `0x9A`, or the `0xFFF200` video ADC / SweepEngine)
   it needs to finish so the firmware advances.
2. **Find the measure-mode-entry routine** (what calls `fcn.21c96` with `0x31` / queues
   CONTS) and its precondition — i.e. what state the firmware must reach before it
   selects spectrum continuous-sweep mode.
3. **Model the sweep state machine end-to-end**: `a9a0` armed 0→401 in lock-step with
   IRQ6 buffer fill + `befa` bit13 + the sweep-complete handshake, so the `0x17400`
   orchestrator completes a sweep and hands off. SweepEngine
   (`pkg/emu/device/sweepengine.go`) is data-ready and waiting on that handshake.

## CONSTRAINTS

- **Faithful, not forced** — the firmware must reach measure mode via its own logic.
- Commit only when asked.
- Probes = DIAG-gated Go tests in `pkg/emu/machine` (not `cmd/` tools).
- Firmware commands are `;`-terminated.

# Measure-mode / trace-draw blocker — session handoff (2026-06-05)

Single entry point for the next session on the trace-draw blocker. The deep detail
lives in the docs linked below; this is the bridge so a fresh session doesn't
re-derive — or re-try the ruled-out angles.

## TASK

Make the virtual HP 8593A draw the spectrum trace — crack the operating-loop /
measure-mode blocker so the firmware enters continuous-sweep spectrum **MEASURE**
mode (the trace + graticule then self-sustain). This is the documented multi-session
subsystem.

**DONE WHEN:** the trace-draw DLP command `__GTTDRW` (@0x65986) actually executes —
i.e. the DLP scheduler `fcn.d18` (which is `0×` during the sweep today) fires during
a sweep and a trace line lands in the framebuffer. That is the single end-to-end
success signal; everything below is in service of reaching it FAITHFULLY (no forced
cells).

## READ FIRST (canonical, already committed)

- [docs/TRACE_DISPLAY_PATH.md](TRACE_DISPLAY_PATH.md) — esp. "WHY a9a0 SETTLES -1" + the 2026-06-05 CORRECTION
- [docs/DRIVETICK_BLOCKER.md](DRIVETICK_BLOCKER.md) — the Gate 1+2 map
- [docs/A7_ANALOG_IO_BUS.md](A7_ANALOG_IO_BUS.md), [docs/ANALOG_BUS_MODEL.md](ANALOG_BUS_MODEL.md)
- [docs/DISPLAY_FINDINGS.md](DISPLAY_FINDINGS.md) — the "don't re-derive disproven ideas" ledger
- Probes (run with `DIAG=1`): `pkg/emu/machine/sweeparm_diag_test.go` + `pkg/emu/machine/oploop_diag_test.go`.
  The decisive ones for the corrected model: `TestIdleStackScanDiag` (fcn.18568 IS on the stack —
  not a derail), `TestADCCadenceSweepDiag` (analog timing is NOT the trace gate), `TestIdleStackDiag`
  (the analog idle's call chain → DLP-RAM 0xFC9A32).
- Branch: `display-clean-clear-graticule`

## CURRENT STATE (measured 2026-06-05 — supersedes the earlier "derail / boot-measurement" model)

> **MODEL CORRECTION.** The earlier framing — "the firmware idles in a boot-measurement
> analog loop and never transitions to the operating loop" — is WRONG. The operating loop
> `fcn.18568` is **running the whole time**; it is simply in the wrong measurement MODE. The
> blocker is narrower than "complete the boot self-cal": it is purely **CONTS command delivery**.
> Evidence below, all from `pkg/emu/machine/oploop_diag_test.go` (DIAG=1).

- **`fcn.18568` is RUNNING, not derailed/stuck.** `TestIdleStackScanDiag`: a raw-A7-stack scan
  at the analog-poll idle finds an `fcn.18568` (0x18568..0x18B00) return address on the stack in
  **200/200** samples; the DLP interpreter `fcn.34EE8`, DLP scheduler, and command dispatcher are
  **ABSENT**. So the firmware is inside the operating loop, calling the boot-default analog
  measurement DIRECTLY (the compiled routine at DLP-RAM `0xFC9A32` → analog ops `fcn.5e88c` /
  `fcn.5f0c4` → poll `fcn.5e5de`). NOT a DLP derail, NOT a DLP-step block.
- **The analog idle is a SECONDARY cycle-sink, NOT the trace gate.** `fcn.5e5de` waits for
  `(0x9A_status & 0x12) == 0x02` with a ~1000-unit timeout. Our `0x9A` model
  (`analogbus.go statusReadyEveryNReads=256`) presents real status (`0x06`/`0x07`, bit1 set) only
  every 256th read and returns `0x00` (bit1 **clear**) the other 255 — so the poll mostly times
  out, burning ~19k reads/window and starving the loop. `TestADCCadenceSweepDiag` A/B (cadence
  256→1): analog reads `18997→735`, op-loop entries `71→547` — **but `b0ec`, `a9a0`, `b0a1` are
  byte-identical at every cadence.** So analog timing is real (and the `0x00` busy return is
  unfaithful — bit1/bit2 are static-on when powered) but does NOT gate the trace.
- **The trace gate is CONTS (`b0a1` bit 3), pinned to ONE instruction.** Sweep-arm `fcn.8f04`
  gates on `btst #3, b0a1`. `b0a1` bit 3 has **exactly one writer in the whole ROM** —
  `0x5f980 bchg.b #3,b0a1` inside `fcn.5f968` (`b0a1` is NEVER wholesale-written, only bit-poked).
  `fcn.5f968` has **exactly one call site** — `0x126d8 jsr fcn.00000550`, the CONTS *case* of the
  command dispatcher `fcn.12288` (`move.w -0x8(a6),d0` = command arg → `jsr` handler → set CONTS
  to arg bit0). So CONTS can be set ONLY by the command executor processing a CONTS opcode+arg;
  at power-up none is delivered ⇒ bit3 stays 0 ⇒ `a9a0=-1` ⇒ no sweep ⇒ no trace.
- Mode `0xB0EC = 0x01` (PRESET default) also never becomes spectrum (`0x2D/0x31/0x36`), set by
  `fcn.21c96` from the measurement state machine — but the IMMEDIATE sweep-arm lever is CONTS
  (`fcn.8f04` keys only on `b0a1` bit3, confirmed by `TestSweepArmDiag`).

## GATE CHAIN (corrected)

```
trace not drawn / graticule eroded
  ← sweep never arms (a9a0 = -1; fcn.8f04 btst #3,b0a1 = clear)
  ← CONTS (b0a1 bit3) never set
  ← fcn.5f968 (its ONLY writer, bchg @0x5f980) never called
  ← its ONLY call site — the CONTS case @0x126d8 in dispatcher fcn.12288 — never runs
  ← no CONTS command opcode is ever delivered to the command executor at power-up
  ← (the power-up default-config that should issue CONTS — NOT YET FOUND; see NEXT STEPS)
```

The operating loop `fcn.18568` IS running (it just lacks the CONTS command); this is a
COMMAND-DELIVERY gap, not a measurement-completion or DLP-derail gap.

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
- **Analog-timing / boot-measurement completion gates the trace** — DISPROVEN this session.
  `TestADCCadenceSweepDiag`: at every cadence (even 1, op-loop free-running) `b0ec/a9a0/b0a1`
  are byte-identical. The analog `0x9A` cadence is worth fixing for FAITHFULNESS (it's a
  cycle-sink and the `0x00` busy return is wrong), but it is NOT the trace gate. Don't chase
  the PRESET ADC cal `fcn.5E6E8` as the trace blocker — it COMPLETES (returns `0xFFFF` uncal;
  the `fcn.5e63c` sub-poll is only on the cal-VALID path), it does not infinite-loop.

## NEXT STEPS (hypotheses, in order)

1. **(DO THIS) Find what should DELIVER the CONTS command at power-up.** The whole chain
   reduces to: the CONTS case `@0x126d8` in dispatcher `fcn.12288` never runs because no CONTS
   opcode reaches the command executor. **Newly measured (this session):**
   - The dispatcher `fcn.12288` runs **exactly ONCE** in the whole 260M-cycle boot — one command,
     code `0x12D6` (class `0x12`, index `(0x12D6>>8)-0xd = 5`), NOT CONTS (`TestCmdDispatchDiag`).
     So the power-up does **not** replay a default-config command BURST; the command engine is
     essentially idle. CONTS is not "omitted from a list that runs" — the list/burst never runs.
   - There is **no default-STATE path** either: `TestB0A1WritersDiag` logs every `b0a1`/`b0a0`
     writer across the boot — bit3 is set **0×** by anyone (no block-copy / state-template / RAM-
     init seeds it). So CONTS genuinely requires the command-dispatch path, which is idle.
   - PRESET `fcn.4df34` DOES run (it sets `b0ec=0x01`, writes the `b0a0` region @0x4E300/0x4E508)
     but does NOT issue CONTS — so either this firmware's power-up isn't a full IP, or the
     CONTS-issuing step is gated.

   Next: find the power-up default-configuration routine (instrument-preset sequence or the
   startup DLP) that should ENQUEUE the CONTS command record into `fcn.12b10`'s queue, and why it
   doesn't run / doesn't reach the enqueue. The command CODE for the CONTS case is reachable by
   decoding `fcn.12288`'s PC-relative jump table after `0x12798 jsr fcn.6862` (table data at
   `0x127a4+`); grep where that code is moved into a command record (`0x8(a4)`).

   **★★ DEFINITIVE CORRECTION (2026-06-05) — commands DO execute; my "blocked" conclusions were
   HARNESS ARTIFACTS.** Two earlier reconciliations this session ("0x18F3E never reached", then
   "resolve→dispatch gap / `fcn.349b6` never fires") were BOTH WRONG — artifacts of a bad test
   harness, not a firmware gap. Proven by replicating the GUI keyboard path that actually runs
   CAL DISP (`TestSendCONTSDiag`, now a positive assertion):
   - **Match the GUI EXACTLY**: NO `InstallHPIB` (it routes IRQ4 to the HP-IB path via `b05f` bit0
     and STARVES the AT keyboard at `0xEF8000`); deliver the command as **AT scancodes** (F8 remote
     mode → type chars → Enter) over IRQ4; drive **GUI-style** (`driveHPIB` = Run+IRQ4+IRQ5+sweep);
     and **drive LONG** (command execution takes many operating-loop ticks — >>30M cycles).
   - With that: typing `CAL DISP;` **EXECUTES** — the cal-display routine reads its label region
     (`0x5057c`/`0x50620`), the command echoes on screen ("CAL DISP: Entr→Command"),
     `fcn.320fe` + `fcn.349b6` both fire. Asserted green; screen `screens/cal_disp_kbd.png`.
   - So the operating loop, parser, name-lookup, DLP scheduler, and command HANDLERS all work. The
     trace also draws (grass + peaks visible in the renders). The prior "never executes" findings
     came from (1) wrong input path (InstallHPIB), (2) wrong terminator, (3) drives far too short.

   **CONTS specifically** still does NOT reach its handler `fcn.5f968` (b0a1 bit3 stays 0) when typed
   the same way that runs CAL DISP — a NARROW open question, NOT a general command-execution gap. It
   may not be a keyboard-typeable remote command in the current menu context, or the firmware's
   visible sweep isn't gated on `b0a1`/`a9a0` the way assumed. ⇒ Next: trace what CONTS resolves to
   when typed (vs CAL DISP), or find the front-panel/softkey path for continuous-sweep; and re-check
   whether the trace goal is actually already met (the firmware shows a live trace).
2. **DONE (2026-06-05) — `0x9A` status cadence fixed.** `analogbus.go` now presents the
   ready/settled bits (`0x06`) statically on every read and pulses only EOC (bit0) via the
   conversion state machine (the `statusReadyEveryNReads=256` "busy 0x00" hack is removed). Frees
   ~8× loop time; the old "ready-every-read collapses render" justification was disproven. Locked
   by `TestADCStaticStatusDiag` + device `TestAnalogBus_StatusStatic`/`_ConversionLifecycle`.
   SIDE EFFECT: the faster boot shifts the boot-screen trace snapshot (CAL-peak feature out of
   frame) — `TestMachineBootScreen` + `TestTraceSolid` are SKIPPED (deferred), to be regen'd /
   retuned after this trace-paint task settles the render. Capture is intact (buffer max=0x17F).
3. **Once CONTS fires**: verify the sweep state machine end-to-end — `a9a0` armed 0→401 in
   lock-step with IRQ6 buffer fill + `befa` bit13 + the sweep-complete handshake. SweepEngine
   (`pkg/emu/device/sweepengine.go`) is data-ready and waiting on that handshake; then confirm
   `__GTTDRW` (@0x65986) is finally scheduled (the DONE-WHEN signal).

## RELATED, NOT THIS TASK (same branch — don't collide)

There is a separate display-clearing plan on THIS branch
(`display-clean-clear-graticule`): the `CAL DISP;` cal-table overlay + the per-sweep
trace pile-up — fixed by area-def-clipped foreground SCLR in
`pkg/emu/device/hd63484/area_ops.go`, then optional GUI-only colorization. That is
DOWNSTREAM cosmetics: it only matters once the trace actually draws. Do NOT interleave
it with this measure-mode work — finish the trace-draw blocker first. (Plan file:
`~/.claude/plans/vivid-jingling-raccoon.md`.)

## CONSTRAINTS

- **Faithful, not forced** — the firmware must reach measure mode via its own logic.
- Commit only when asked.
- Probes = DIAG-gated Go tests in `pkg/emu/machine` (not `cmd/` tools).
- Firmware commands are `;`-terminated.

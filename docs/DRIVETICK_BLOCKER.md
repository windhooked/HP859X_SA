# DriveOperatingTick — Rev L Verification (skip rationale)

> ## ⚠ 2026-05-29 — Path B finding REFRAMES this whole blocker
>
> The long-run integration probe (`cmd/naturalkey`, the "Path B" approach
> recommended below) established something more fundamental than the
> deep-dispatch gate this document was written about:
>
> **Under the 8593 SystemID strap, the firmware never reaches the operating
> loop `fcn.18568` at all.** Post-boot the CPU is *frozen* in an analog-bus
> status poll: ~66 % of time in the timer-window helper `fcn.4824` and ~32 %
> in the analog poll `fcn.5E5DE` (PC `0x5E5FA`). Over 300 M post-boot cycles
> the operating-loop body `[0x18568, 0x18A88]` is visited **0 times**, IRQ3
> sets the key flag (`bc67: 0x04 → 0x05`) but it is **never** processed, and
> `FrontPanel.Consumed()` stays false. The deep-dispatch gate discussed in
> the rest of this doc is therefore *downstream of a stall the firmware never
> gets past* — the current "boot success" (banner renders, `TestMachineBootScreen`
> green) is actually the firmware **stuck at this poll**, not running a live UI.
>
> ### Root cause (single-step trace, `cmd/naturalkey -trace`)
>
> The stuck poll is `fcn.5E63C → fcn.5E5DE`, polling analog select **`0x9A`**
> on `0xFFF75C/E`. Its match contract is:
>
> ```
> test(-1,A6) = 0x01 ;  D6 = test & read_low_byte ;  expected(9,A6) = 0x01
> match  ⟺  (0x01 & status_low_byte) == 0x01   →   status bit 0 must be SET
> ```
>
> Our analog model (`pkg/emu/device/analogbus.go`) returns **`0x0006`** for
> select `0x9A` every 256 reads — bits 1+2, **no bit 0**. So `0x01 & 0x06 = 0`
> ≠ `0x01`: this poll can *never* match. Confirmed: in 2 M single-steps the
> match branch (`0x5E614`) fires **0 times** across 60 751 poll iterations.
> The `0x0006` value was tuned for two *other* `0x9A` polls:
>
> | Poll | Site | Contract | Needs |
> |---|---|---|---|
> | operating-loop | `fcn.5E5DE` (dominant) | `(0x12 & x) == 0x02` | bit 1 (`0x06`✓ `0x07`✓) |
> | init/cal | `0x5E708` | `x == 0x06` exactly | `0x06` only |
> | **8593 boot (stuck)** | `fcn.5E63C` | `(0x01 & x) == 0x01` | **bit 0** (`0x07`✓ `0x06`✗) |
>
> ### Why the naive fix fails (tested, reverted)
>
> A single constant cannot satisfy all three (init/cal wants *exactly* `0x06`
> = no bit 0; boot wants bit 0). Rotating the armed value through
> `{0x06, 0x07}` *did* unstick the boot poll — the firmware progressed past
> the long-standing stall — but then **derailed to a garbage PC** (`0x499…`)
> and regressed `TestMachineBootScreen`. The status value materially steers
> boot branches; feeding alternating/blind values sends the firmware down
> failure paths into unmodelled territory. **This is Path C work**: the
> `0x9A` status register needs context-aware (or genuinely time-varying,
> per-flag) modelling, and there are almost certainly further analog/IF
> dependencies past this poll that must be satisfied for the boot to reach a
> live `fcn.18568`. The change was reverted to keep the suite green; the
> probe `cmd/naturalkey` (with `-nokey` / `-trace` modes) is the tool to
> drive the next iteration.
>
> **Net:** "make the instrument interactive" now decomposes into (1) get the
> boot past the `fcn.5E63C` analog poll and any downstream analog gates so it
> reaches a live operating loop, *then* (2) the key-dispatch work below. (1)
> is the prerequisite and is squarely an analog-modelling RE task.

---

Two `pkg/emu/machine` tests are SKIPPED under the canonical Rev L
firmware:

- `TestDriveOperatingTickClearsKeyAndSweepFlags`
- `TestSendHPIBPlusDriveOperatingTickDrainsParserFIFO`

Both rely on the **deep-block force** strategy of
`Machine.DriveOperatingTick`: set RAM[`0xFFB1E0`, `0xFFBEFA`, `0xFF9AFB`]
to specific values, then force `PC = 0x18ADC` (mid-`fcn.18568`) and pump.
The strategy was tuned for the 17.12.90 build (and a different SystemID
strap). Under Rev L 98.06.15 Opt-027 it does not reach the verified
observables.

## What the verification established

`cmd/tickprobe` single-step trace + bulk-Run experiments, in order of
escalating diagnostic depth:

1. **Single landmark sample** (200M cycles, 100-cycle PC sampling): PC
   visits 0x18BB8 once, then never returns to `fcn.18568`. Final PC =
   `0x05E606` (the indirect-analog-bus poll loop documented in
   `CLAUDE.md`).
2. **Hypothesis: coroutine resume via `0xFFB218`** at PC 0x18BB8 → `jmp
   (a0)`. Tested by clearing `b218 = 0` first. **No effect** — the
   probe shows `b218` is already `0` after boot.
3. **Single-step trace** (200k instructions): the deep block IS
   correctly running its prologue end-to-end — 104 unique PCs visited
   inside `fcn.18568`, climbing through `0x18ADC → 0x18B14 → 0x18BA6 →
   0x18BB8 → 0x18BD6 → 0x18BF6 → 0x18C0C → 0x18C48 → 0x18C78 → 0x18C84
   → 0x18CB4 → 0x18CD2 → 0x18CEC → 0x18CFC → 0x18D22 → 0x18D78 →
   0x18D88 → 0x18DA4 → 0x18DC2 → 0x18DE6 → 0x18DF6 → 0x18DFA → 0x18E00
   → 0x18E1C → 0x18E20 → 0x18E54 → 0x18E5A → 0x18E62 → 0x18E68 →
   0x18E6E → 0x18E74 → 0x18E76`. The 9afb bit 2 test at 0x18E6E sees
   the bit set (our pre-arm); falls through to `jsr 0x68E.w`. After
   that jsr, the firmware never returns to `fcn.18568` within the
   trace budget.
4. **Focused trace into `jsr 0x68E`**: dispatches to `fcn.569B6` →
   `fcn.568F6`. Key Rev-L-specific finding:
   - PC 0x568FC **explicitly clears bit 2 of `0x9afb`** — undoing our
     pre-arm — then branches on `b1e4 == 0x34`.
   - Post-boot Rev L has `b1e4 = 0x0000`, so the "alt" path is taken:
     `fcn.568F6` calls `fcn.0xC16 → fcn.11DF4`.
   - `fcn.11DF4` runs a 100-word checksum (`fcn.0x6B1C`) + a 100-word
     buffer copy + nested annunciator/display state updates. By step
     5000 of single-stepping, call depth is **31+** and not unwinding.
5. **Hypothesis: `b1e4 = 0x34` pre-arm** (the "fast return" indicator).
   Tested. **No effect** — `fcn.11750` (called via slot 0x76C) writes
   `b1e4 = (input arg)` at PC 0x11798, resetting our pre-arm to the
   caller-provided value (which is `0`).
6. **1-billion-cycle bulk run** with the full Rev-L-adjusted pre-arm
   set: bc67 bit 0 never clears. The CPU bounces between `fcn.4824`
   (compare helper) and `fcn.5E5FA` (analog-bus poll loop) for the
   entire run. The deep-block bclr at PC 0x18F42 is structurally
   unreachable from this state.

## Why this is not a "small re-tune" job

The original `DriveOperatingTick` was a pinhole hack: force a specific
PC, pre-arm 3 RAM cells, and the deep block falls through to a 6-cycle
`bclr` you can observe. That worked because 17.12.90's `fcn.18568`
branches on roughly the same 3 cells the pre-arms set up.

Rev L's `fcn.18568` reaches a much deeper sub-call tree before
arriving at the bclr — including state-management code (`fcn.11750`,
`fcn.11DF4`, `fcn.568F6`) that **overwrites the pre-armed cells with
caller-supplied values** as a normal part of its work. There is no
fixed set of cells you can pre-arm to make this path a no-op; the
firmware genuinely wants to do the work and reach a specific
mode/menu state before processing the keypress.

## Realistic fixes (for whoever picks this up)

A. **Re-frame the verification around a different observable.** Don't
   force the deep block; instead, test the IRQ3 handler at PC
   0x2B1E directly (it's a thin assembly routine that just `bset #0,
   $bc67`), and test the parser by calling `fcn.58C2E` (`slot 0x69A`)
   with a controlled `0xFFBC12` FIFO. Both are unit-testable without
   the operating-loop context.

B. **Re-build the operating-loop test as a long-run integration.**
   Stop forcing PC; let the natural fcn.18568 entry path run for
   minutes (10-100B cycles); inject a key event via the
   `FrontPanel.SetBit` device API so IRQ3 is delivered alongside a
   key-matrix bitmap the firmware will read. Observe whether the
   firmware's natural key-processing chain fires. This is slow but
   honest, and the `DriveOperatingTickUntil` predicate API supports it.

C. **Trace what RAM state Rev L's `fcn.568F6 → fcn.11DF4` chain wants
   to see for a no-op return.** This is a 1-2 day reverse-engineering
   job: most checks in that chain branch on `bef9.x`, `bef8.x`, `b072.x`,
   `b020.x`, `a7d6[...]`. Most of these are state-machine bits that
   the firmware itself toggles during normal operation; you'd need to
   reproduce the full state of the analyzer mid-tick.

## Tools

- `cmd/tickprobe` — the probe used for the empirical findings above.
  Rewrite the body to test any hypothesis (the file is small and
  rebuilds in ~1s).
- `Machine.DriveOperatingTickUntil(pred func() bool, maxCycles int)` —
  predicate-driven pump in `pkg/emu/machine/machine.go`. Use this for
  any future test that knows the right post-condition to wait for.

## Cross-references

- `docs/DLP_RUNTIME.md` — operating-loop / DLP-runtime architecture
  (operating loop is C; per-tick work is draining DLP rings). Provides
  the architectural backdrop for why these tests are interesting in
  the first place.
- `docs/rom_annotations.md` — boot-menu loader chain, dispatch tables.
- Memory `rev-l-key-consumer-chain.md` — earlier (now stale) notes
  from when these tests passed under 17.12.90 + the 8595 strap.

## 2026-05-31: CRACKED — the operating-loop idle gate is 0xFFF300 bit 11 (sweep-complete)

The 2026-05-29 analog-poll freeze is RESOLVED (the analog-bus model un-froze it);
the firmware now reaches the operating loop. But it gets stuck in an **idle wait
loop at ROM 0x188b6** (cmd/looptrace2). Decoded, that loop polls for work and
exits to the work path 0x18910 when any of: **0xFFF300 bit 11 set** (sweep
complete), fcn.11da8≠0, or a DLP/key queue head≠tail. With none asserted it spins
forever — the long-standing "DriveTick" stall.

**0xFFF300 bit 11 = SWEEP COMPLETE.** We modelled bit 12 (sweep-ready) but never
bit 11. Asserting it makes the firmware exit the idle loop → 0x18910 → (bit11
set) → **0x18a8c, the continuous-sweep handler**: it re-arms the next sweep
(loads bf34 from the sweep vtable at b1e8, writes the sweep DACs f716/f70a) and
calls fcn.171f6 to process the completed sweep. At 0x1892A the firmware ACKs by
writing 0xFFF300 (clears bit 11). This is the continuous-sweep cycle.

Verified (cmd/looptrace2): asserting 0xFFF300 bit 11 un-sticks the firmware — it
leaves the idle loop and runs (renders COPYRIGHT HP 1986-98 / rev 980515, drives
the softkey menu, +250 display lines). The idle gate is cracked.

**Remaining (sweep-clock integration):** model bit 11 faithfully as a sweep clock
— assert on buffer-full (A5≥bf30), let the firmware ACK-clear it at 0x1892A,
re-assert after the next sweep period, in sync with IRQ6 buffer fill — so the
firmware runs a clean continuous-sweep cycle and fcn.171f6 draws the trace
(rather than the arbitrary timing that currently sends it menu-walking). This is
the M2 sweep engine. The KEY blocker (the idle gate) is solved. Tool:
cmd/looptrace2; analog data: pkg/emu/device/sweepengine.go.

### Sweep-clock handshake works; secondary blocker = firmware is in CONFIG mode

Implemented the faithful one-shot sweep-clock (cmd/looptrace2): clear f300 bit11
when the firmware re-arms (writes sweep DAC 0xFFF716), drive IRQ6 to fill the
buffer from the SweepEngine, set bit11 once when full (A5>=bf30). This avoids the
crude-timing menu-walk. fcn.171f6 (the sweep executor) returns-early when bit11 is
clear and processes/draws when set — confirmed the contract.

BUT: with the gate open the firmware navigates to the COPY/CONFIG softkey menu
(Config Print/Plot, "PRNT PLT COPY DEV") rather than staying in the spectrum
continuous-sweep MEASURE mode, so fcn.171f6's draw path isn't sustained (only ~6
sweeps complete then it stops re-arming). The main graticule is shown but no trace
line. **Next: get the firmware into MEASURE mode** — now that the operating loop
runs, the front-panel key path should work too (it was blocked by the same idle
gate); inject a PRESET / FREQUENCY key via FrontPanel.SetBit + IRQ3 to put the
firmware in continuous-sweep spectrum mode, then the sweep-clock + SweepEngine
paint the trace. The KEYSTONE blocker (frozen operating loop) is solved; this is
mode/menu navigation on a now-living firmware.

### Sweep cycle runs (fill→process→re-arm); trace-PLOT is the last piece (DLP)

With f300 bit11 + IRQ6 fill, the firmware runs the full sweep cycle: cmd/looptrace2
single-sweep mode shows A5 fills to bf30 then RESETS to 0x2FD508 each sweep (the
firmware re-arms — confirms fcn.171f6 processes the completed sweep). But the
trace LINE doesn't paint (Δdots=28, Δlines=2 over 3 sweeps; a real trace is ~401
points). So the sweep cycle is alive but the trace-PAINT step — the __GTTDRW DLP
command (0x65986) — still doesn't produce the line, consistent with the DLP
runtime not executing the trace-draw source. So the trace draw reduces to: get the
sweep-trace DLP source (0x5fa22, scheduled by fcn.5ED7E) onto the DLP ring and run
it. The frozen-loop + sweep-cycle are solved; the trace paint is the final DLP
step. Tools: cmd/looptrace2, cmd/tracehunt, cmd/rendertrace.

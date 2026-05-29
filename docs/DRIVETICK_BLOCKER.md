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

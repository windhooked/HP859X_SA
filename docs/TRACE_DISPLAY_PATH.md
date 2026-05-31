# Trace display path — why the trace never draws (Rev L, RE 2026-05-31)

## Question

The HD63484 renders the graticule grid, the box, and the text/annunciators, but
the **measurement trace itself never appears**. Is this a DLP trace-display
scheduling problem, an ADC-cal problem, or something else?

## Answer (resolved)

The trace-draw is **measurement-completion-gated, not DLP-scheduling-gated and
not ADC-cal-gated.** In our boot the firmware **never reaches the C operating
loop `fcn.18568`** — it stays in a continuous boot/measurement loop, and the
trace-draw (which is C measurement code *upstream* of the DLP operating loop) is
skipped behind a sweep-busy gate that the measurement never clears because the
sweep-completion handshake isn't faithfully modelled.

This supersedes the vaguer "DLP trace display path" / `DRIVETICK_BLOCKER`
framing: the draw is not gated by DLP ring scheduling (that lives in
`fcn.18568`, which is never entered), it is gated by the sweep/measurement
orchestrator that runs *before* the handoff to `fcn.18568`.

## Evidence (`cmd/tracedraw`, `cmd/looptrace`)

Over a 4000-chunk post-boot window driving the sweep the hardware way (IRQ1 =
sweep step, IRQ6 = sample capture):

- **0 `drawLine` calls. `Lines` +0.** The only display activity is `Moves`
  +833, `Glyphs` +280, `Dots` +90 — i.e. text/annunciator refresh and a few
  dots, **no vectors at all** (not even a graticule redraw; the graticule is
  drawn once at boot and persists).
- **Hot PC regions** (1 KB-page histogram, single-stepped):
  - `0x4800–0x4BFF` ~33% — per-sample detector accumulation of `$bf12`
    (`move.l $bf12,(a4); add accumulator; jmp (a0)` continuation form).
  - `0x7C00–0x7FFF` ~20% — sweep/LO DAC programming (`$f708/$f710/$f712` from
    `$b204/$b206`); also the `$befb` bit7 / `$bffe` software sync-handshake at
    `0x7C4C`.
  - `0x5E400–0x5E7FF` ~10% — analog/ADC measurement (`$948e` compare + `dbra`
    settling loops; the `fcn.5E63C` family).
  - **`fcn.18568` (the C operating loop) is never entered.**
- **Sweeps DO complete.** Injecting IRQ6 with A5 gated `< $bf30` fills the trace
  buffer (`A5 → 0x2FD82A`) and sets `$befa` bit13 (`befa=0x2404`). The firmware
  then **re-arms and re-samples instead of drawing** — it stays in the lower
  sampling/poll level and never returns up to the sweep-cycle code that would
  process+draw the completed trace.

## The trace-draw gate

The trace processing/draw is C measurement code: the `0x17400` sweep
orchestrator calls the trace-process/scale function at `0x20A40` (the one that
walks `$b0c8` from 0..`$9fb4` calling the scalers `0x5556`/`0x54c6`). The
orchestrator gates the processing at `0x174C0–0x174E0` on:

| cell        | condition                | observed   | verdict |
|-------------|--------------------------|------------|---------|
| `$9fb4`     | `> 1` (sweep/trace count)| `0x0005`   | pass    |
| **`$b0a0` bit11** | **`== 0`** (sweep-busy / trace-blank) | **`0x0800` (set)** | **FAIL → `bne 0x17534` skips the draw** |
| `0x104dc()` | bit0 clear               | —          | secondary |
| `$adc4` b15 | branch                   | `0x0001`   | secondary |

`$b0a0` bit11 = "sweep busy / trace blanked". It is **set** during measurement
(`0x20A5C`, `0x20A7E`) and **cleared** only by the sweep-DONE path: `0x4E78A`
(`bclr #11,$b0a0; bclr #11,$b1e0` in the sweep-complete handler) or `0x20A76`
(when the trace-process work-count hits 0 with `$9fb4 > 1`). It is stuck **set**
because the measurement never executes that completion path.

**Decisive test:** force-clearing `$b0a0` bit11 every step (`FORCE_GATE=1`)
does **not** make the trace draw and does **not** advance to `fcn.18568` — the
firmware never reaches the productive `0x174C8` check; it is stuck one level
down in the `0x4800`/`0x5E400` sampling/poll loop. So bit11 is a *symptom* of
the un-completed measurement, not the root lever.

## Root cause + path to a drawn trace

The firmware's measurement state machine completes a sweep and runs the
trace-process+draw only when it sees the real **sweep-completion handshake**:
the sweep-ramp/sync signals (`$f300` bit11 polled at `0x17466`; the `$befb`
bit7 / `$bffe` mailbox at `0x7C4C`) and per-mux-channel ADC conversion
sequencing. We currently approximate acquisition with **manual IRQ injection**,
which fills the buffer but does not satisfy the state machine's
completion/return path — so it re-arms and re-samples forever, never returning
up to the draw.

Getting a visibly drawn trace therefore needs a **faithful sweep+ADC completion
model**, not more IRQ poking:

1. Model `0xFFF300` bit11 (and the `$befb`/`$bffe` sync mailbox owner) as the
   sweep-ramp/trigger-complete signal the `0x17424` poll waits on.
2. Drive IRQ6 from that model (sample-ready) instead of open-loop, so the
   firmware's point counter and `$befa` bit13 advance *in lock-step* with the
   buffer fill and the sweep returns to the orchestrator.
3. Then the orchestrator clears `$b0a0` bit11 via `0x4E78A`, calls `0x20A40`,
   and emits the trace vectors — at which point `cmd/tracedraw` will capture
   non-axis-aligned `drawLine` segments inside the graticule.

## CORRECTION / deeper finding (2026-05-31, later) — the stall is in DLP, not a missing sweep handshake

The "model the sweep-completion handshake" conclusion above was **premature**.
A call-stack capture at the innermost spin (`cmd/looptrace`, A6-chain walk)
shows the measurement handler **does run** — it just stalls *inside its own DLP
processing*. The captured stack, bottom-up:

```
0x017560  boot PRESET-measurement driver
0x04E790  sweep-done / measurement handler  (the 0x4E78A $b0a0-bit11 clearer)
0x03F7E4  bclr #2,$a5d5
0x043366 / 0x0355CE / 0x0349xx
0x034690  DLP scheduler            (fcn.349B6: tst 8(a6)≤0 → exit; else step+recurse)
0x034C96 / 0x035806  DLP interpreter step (fcn.34EE8)   ← recurses 3× through
0x0662A6 / 0x065F16  compiled DLP token handlers (__ trampolines push sources)
0x032B70  DLP record search        ← the 115k-hit innermost spin
```

So the real picture: the boot PRESET-measurement (`0x17560`) **does** reach the
measurement handler (`0x4E790`), which invokes the **DLP interpreter** to run a
boot/measurement DLP script. That script **never terminates** — its `__`-token
handlers keep pushing sources onto the include stack and the scheduler keeps
re-resolving the same ~20 DLP record keys (`1..0x14`, ~27× each over the window:
`cmd/looptrace` key histogram). The DLP record search `fcn.32B70` (lookup by
key+type in the record table at `$bb54`, count `$bfe6`) does a full backward
table scan per call and dominates (~33% earlier reads were *also* this, mislabel
"detector accumulation"). The scheduler exits only when its source arg reaches 0
(`fcn.349B6 @ 0x349C0`) or the `fcn.34644` check returns bit0 clear — neither
happens.

**Consequence:** `$b0a0` bit11 never clears not because the handler isn't called
but because the handler **never returns** — it's blocked inside the
non-terminating DLP script. The trace-draw is downstream of that return.

**Revised path forward:** this is **DLP-VM** work, not analog-handshake work.
Next concrete step: trace `fcn.34EE8` (the interpreter step) to identify the
specific DLP token/source the boot script loops on, and what condition (a status
read, a flag, a record value) would let that script's loop terminate. This is
the same class as the historic startup-DLP derail (see DLP_STARTUP_DERAIL.md /
DLP_VM_ARCHITECTURE.md), now in plain Rev L and past `__PKIP` — a *different*
non-terminating script, reached only after the corrupt-dump fix let the boot get
this far.

## DLP-command-level (2026-05-31, latest) — the trace-draw is `__GTTDRW`, gated behind a looping `__GGTSWSW`

Mapping the looping token handlers (`cmd/jumptable`) names the script:

- The recursion runs in the **WININIT / graticule DLP source** — the `__GT*`
  command family (handlers clustered `0x65000–0x67000`): `__GTREDG __GTCLRP
  __GTWID __GTPRIZ __GTGTRI __GTTDRW __GTSHPP __GTCRBW __GTCVBW __GTCST __GTUPCP
  __GGTSWSW __GTKSW __GTVDFS __GTWINSET __GTMAKWINA/B __GTONHK __GTNEXT`, plus
  `WININIT` (`0x066A02`).
- The two handlers on the captured stack are **`__GGTSWSW`** (`0x066296`, "get
  sweep sw/state") and **`__GTCST`** (`0x065ED4`). The script loops here.
- **`__GTTDRW`** (`0x065986`) = "graticule **T**race **DRW**" is the
  **trace-draw command**. Like the others it is a **trampoline** (`move.w
  #idx,-(a7); lea source(pc),a0; jsr $d18` — pushes a DLP sub-source and calls
  the scheduler `0x349B6`; `__GTTDRW` uses index `0x2B`, `__GGTSWSW` index
  `0x248`). So the trace is drawn by *running a DLP sub-script*, not by compiled
  C.

**Net:** the boot graticule/window DLP script polls sweep state via
`__GGTSWSW` and never advances to `__GTTDRW`, so the trace is never drawn. The
trace-draw target is now identified by name (`__GTTDRW`). Cracking it = RE the
`__GGTSWSW` sub-script's loop and the sweep-state condition that would let the
graticule script progress to `__GTTDRW`. (So the sweep state *does* matter — but
it is consumed through a DLP command, not the direct C polls examined earlier.)

## CORRECTION #2 (2026-05-31, final) — boot DLP init COMPLETES; freeze is a downstream measurement state machine

A long-run progress monitor (`cmd/longrun`, light IRQ driving, 16×25M-cycle
windows) overturns the "non-terminating DLP" reading too. The boot **does
finish**:

```
window 0 (25M):  Lines=39 b0a0=0801(bit11 SET)  a62a=0000
window 1 (50M):  Lines=77 b0a0=0000(bit11 CLEAR) a62a=01A6   <- init progressing
window 4 (125M): Lines=77 b0a0=0000              a62a=004D
window 5..15:    Lines=77 Dots=185 Glyphs=13536 b0a0=0000 a62a=004E  <- FROZEN, identical
```

So the boot DLP personality-init (declaring the ACP/OBW/CHP/`__CZ*` measurement
variables via `VRD`, parsed as text) **completes by ~150M cycles**, and `$b0a0`
bit11 (the trace-draw busy gate) **clears on its own**. The earlier "DLP loops
forever" was an artifact of *heavy* sweep-IRQ driving (`cmd/looptrace`) keeping
the DLP scheduler busy; under light driving the init finishes.

**The real steady state is a hard freeze** in a measurement state-machine loop
at **`0x22532–0x22826`** (44 distinct PCs, every display counter static). That
loop:
- does trace processing (`$b0c8` sample index, scalers `0x553c`/`0x5532`);
- drives an indirect control-register interface — write address to **`0xFFF728`**
  (built from shadow `$ad7c`), read data from **`0xFFF72A`** (both **unmodelled**
  in `mmio.go`);
- branches on RAM flags set by IRQ handlers: `$bf26` bit16 (via helper
  `0x22668`), `$b1e0` bit11, `$b212` bit12, `$ad7d` bit5.

It is frozen because those flags sit in a fixed state — the loop is waiting for
**sweep-cycle events** (the IRQ1/IRQ6/timer sequence that a continuously
sweeping analog board would generate) that our approximate open-loop IRQ
injection doesn't reproduce. `$b0a0` bit11 being *clear* here confirms (third
time) the trace-draw is **not** gated by that bit.

**Corrected bottom line:** neither cal, nor a single sweep handshake, nor a
non-terminating DLP. The firmware boots to completion and then idles in a
measurement state machine (`0x22532`) waiting for a faithful sweep cycle. Path
to a drawn trace = model the sweep subsystem end-to-end: the `0xFFF728/0xFFF72A`
indirect register, and the IRQ-driven RAM-flag handshake (`$bf26`/`$b1e0`/
`$b212`) that advances the state machine through sweep→process→`__GTTDRW`. This
is the faithful-sweep modelling task, now bounded to a specific loop and a
specific register pair.

## Tools added

- **`cmd/tracedraw`** — drives a sweep (IRQ1 step + IRQ6 capture), captures
  every `drawLine` via the chip's new `LineLog`, histograms PC pages, reports
  whether `fcn.18568` is reached, and dumps the trace-draw gate cells.
  `FORCE_GATE=1` force-clears `$b0a0` bit11 (the decisive test above).
- **`cmd/looptrace`** — boots to steady state and dumps the actual instruction
  loop (period detection) instead of inferring from a histogram.
- **`hd63484.Chip.EnableLineLog()` / `Chip.LineLog`** — per-line endpoint
  capture for distinguishing graticule grid from a data trace.

## 2026-05-31: trace-draw unified with the DRIVETICK_BLOCKER (cmd/tracehunt)

Drove the sweep to completion post-boot and instrumented the operating loop:
- Sweep MECHANICS fully work: the armed sweep (bf34=0x410A positive-peak handler)
  fills the trace buffer A5→bf30, sweeps complete 77081×, and **befa bit13
  (sweep-done) DOES fire**. The analog-model SweepEngine supplies faithful data.
- BUT **__GTTDRW (ROM 0x65986, the trace-draw DLP command) is reached 0 times**,
  and a PC histogram shows the operating loop spends its time in 0x188xx (loop)
  + 0x11Dxx (the annunciator/checksum chain) and **never enters the trace-state
  machine fcn.5ECEE / scheduler fcn.5ED7E**.
- fcn.5ED7E (schedule the trace DLP source 0x5fa22) and fcn.5ECEE are dispatched
  via DLP slots 0xB68 / 0x12CA — they run only when the continuous-sweep DLP
  source executes, which it doesn't: the firmware reaches the operating loop but
  does not run those DLP sources.

**Conclusion: the trace-draw is NOT a sweep/handshake/analog-data problem (all of
that works). It is the SAME operating-loop/DLP obstruction as the key-consumer —
the DRIVETICK_BLOCKER (docs/DRIVETICK_BLOCKER.md): the firmware never enters the
continuous-sweep DLP path.** The trace-draw, the front-panel key consume, and the
full annunciator-update all unblock together once that obstruction is resolved
(the firmware runs its operating-loop DLP sources). The analog model is data-ready
and waiting on that single firmware-side blocker. Tool: cmd/tracehunt.

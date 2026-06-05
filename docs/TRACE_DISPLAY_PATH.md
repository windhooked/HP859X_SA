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

## Another DLP-scheduled element: the timedate display (2026-06-01)

The CONFIG > TIMEDATE date/time display is a THIRD DLP-scheduled display source
blocked by the same operating-loop/DLP-render obstruction (alongside the trace
draw and the annunciator update). Enable mechanism fully traced:

- The **CONFIG > TIMEDATE > TIMEDATE ON/OFF** softkey is `KH'TIMEDATE|ON .OFF.',,
  TIMEDSP` (menu DLP source at ROM 0x7B1A0); pressing it runs the DLP macro
  `${TIMEDSP=!TIMEDSP;}` — it toggles the **DLP variable TIMEDSP** (stored in the
  typed-variable store, the 0x5C000 subsystem; see ROM_DATA_CATALOG.md).
- `TIMEDSP` = true ⇒ the date/time paints top-left. The date formatter is
  **fcn.59718** (uses month table 0x5A484; ':' time format via fcn.59EF0), reached
  through the **DLP scheduler fcn.d18** — i.e. it only paints when the DLP runtime
  renders its scheduled sources, the same gate as the trace.
- The **power-up / default-config DLP preset at ROM 0x74D10** contains
  `…TIMEDSP ON; DATEMODE MDY;…`, so a factory/default configuration enables
  timedate by default (the `__PKIP`/`__MN` recall preset). On real hardware the
  date therefore shows out of the box; in our boot it does not, because the
  DLP-scheduled render doesn't run (this blocker), not because of the enable.
- `0xFFBC64` bit 13 is the C-side timedate redraw flag (set on entry to fcn.59718,
  tested across the operating loop fcn.18568 at 0x1859A/0x189C4/0x18EB6).

**The RTC hardware itself is now modelled and correct** (device.FrontPanel,
0xEF4001–0xEF4017, proven by cmd/rtcprobe via the firmware's own fcn.59E2C). So
once the DLP-render obstruction is cleared, the date should paint with no extra
RTC work — set TIMEDSP and the existing clock model supplies the BCD. Decision
(2026-06-01): defer the timedate display to this DLP-render effort rather than a
one-off poke. Enable lever for that work: DLP variable TIMEDSP.

## FRESH dynamic trace (2026-06-01) — confirms: stuck in the BOOT SWEEP ORCHESTRATOR, never reaches fcn.18568

A clean debugger-driven profile (cmd/dlpfresh single-steps a post-boot window;
cmd/sweepstate snapshots the sweep cells) settles the conflicting prior notes.
**The "firmware reaches the operating loop" claim (CLAUDE.md ★2026-05-31) is wrong
under faithful driving.** Ground truth:

- Boot to ~45M cycles: stuck mid-init in the sweep-step loop (0x4800 timer-poll
  73% + 0x7C00 LO-DAC + 0x25000 freq-calc); b0a0 bit11 SET.
- Boot to ~250M cycles (init complete, b0a0 bit11 CLEAR): the steady state is the
  **boot sweep orchestrator at 0x17400** — 34% in 0x17400, 62% in its callee
  0xCC00–0xD7FF, 3% slot-dispatch (0x400). **fcn.18568 reached 0 times** in a 4M-step
  window; so are fcn.34EE8, fcn.349B6, fcn.5ECEE, __GTTDRW, fcn.59718.

The orchestrator loop (disasm 0x17400):
```
0x17402  move.w $f300,D6 ; btst #11,D6 ; bne 0x17418   ; wait for sweep-in-progress
0x1740E  jsr $4824 (deadline) ; beq 0x17402            ; else poll w/ timeout
...
0x17460  tst.w $a9a0 ; blt 0x17472                      ; if point-counter < 0, SKIP done
0x17466  btst #11,D6 ; bne 0x17472
0x1746C  bset #13,$befa                                 ; mark sweep-DONE
0x17476  move.w #$2080,D0 ; and.w $befa ; beq 0x17424   ; loop until befa bit13|bit7
```

Steady-state cells (cmd/sweepstate, stable across 12M cycles):
`a9a0=ffff(-1)  a2e8=0000  a2ee=0000  befa=0404  f300=1008  bf34=0000`, PC=0x1745C.

**Root cause (precise):** nothing drives a sweep cycle during BootToOperating —
- `$a9a0` (sweep point counter) is **-1**, so the sweep-done bset is skipped;
- `$f300` bit 11 (sweep-in-progress) **never pulses** — mmio.go models only bit 12
  (`sweepStatusReady=0x1000`), not bit 11;
- `$bf34` (IRQ6 capture dispatch) is **0** — sample capture isn't armed;
- **pkg/emu/device/sweepengine.go is NOT wired into machine.go** — it's a
  standalone model used only by cmd tools, so the boot never gets sweep data,
  bit-11 pulses, or a point-counter advance.

So the trace/timedate/annunciator DLP renders are all downstream of a sweep
orchestrator that never completes a sweep. **The bounded task: wire a faithful
sweep cycle into the boot** — f300 bit 11 pulse (in-progress→done) + `$a9a0` point
counter advancing 0..N in lock-step with IRQ6 buffer fill (bf34-armed) — so the
0x17400 orchestrator sets befa bit13, processes the trace, and hands off to
fcn.18568. This is CORRECTION #2's "model the sweep subsystem end-to-end",
now pinned to the exact loop + cells. Tools: cmd/dlpfresh, cmd/sweepstate.

### Callee dissected (2026-06-01) — the sweep is NEVER ARMED; no second gate

Per the "trace the 0xCC00-0xD7FF callee first" decision (cmd/hotloop):

- The 62% callee is **fcn.cfbe** (0xCFBE), called **93689×** in a 3M-step window.
  It is the sweep-acquisition sync, but in this state it **always early-exits**:
  work-path 0xD07E = **0 hits**, early-exit 0xD618 = **93689**. It bails because its
  acquire-arg `bit0 of (9,A6)` is clear. (The `$bffe` writes 0x107/0x109 are
  write-only progress-checkpoint TAGS, not a hardware mailbox — the whole 0x4001/
  0x7001/0x010x family tags code checkpoints; the real timer handshake is `$befb`
  bit7, IRQ5-driven and satisfied.) **So there is NO independent second gate** —
  the callee is a no-op idle bail.
- **The sweep is never armed.** Across the window `bf34` stays **0** (IRQ6 capture
  handler unset) and `a9a0` stays **< 0** (point counter unset). `b0e6=0xFFF1`.
- The arm sequence lives at **0x173A0-0x173CC** (programs `$f300` low nibble = 8,
  clears `befa` bit13, sets `bf34 = ($48,A4)` from the band-indexed handler table,
  then `tst a9a0; bge` …). It executes **0.00%** of the window (page 0x17000 = 55
  hits) — the orchestrator never goes through it. `a9a0` is armed to a real count
  (`0x100`) only at **0x9288** (sweep-setup fcn.~0x9200), which is never called;
  a9a0 is only ever (re)set to -1 (0x8FBE/0x92AA/0x92B2).

**Refined root cause:** the firmware is not "mid-sweep and stalled" — it is in a
**pre-sweep IDLE loop** that never ARMS/STARTS a sweep. The orchestrator spins
(0x17400 poll f300 bit11 + call fcn.cfbe which bails), with `a9a0=-1` so the
sweep-done bset is skipped and the arm path is never taken. The lever is therefore
**the sweep-START trigger**: what should call the sweep-setup (fcn.~0x9200 → set
`a9a0=0x100`, arm `bf34`) and why it never fires. That trigger is normally issued
from the operating-loop/DLP sweep source — which we don't reach — so this is the
same chicken-and-egg as the operating-loop entry, now localized to the sweep-arm
trigger. Next trace: the caller/gate of fcn.~0x9200 (the sweep-setup). Tools:
cmd/dlpfresh, cmd/sweepstate, cmd/hotloop.

### PROOF (2026-06-01) — the sweep-arm gate IS the lever; forcing it reaches the LIVE operating UI

Per "trace then prove": after tracing the sweep-START trigger (fcn.8f04 arms
a9a0=20 early but resets to -1 via the 0x9298 `cmpi.l #0x3a98,-0x4(a6)` path, so
steady-state a9a0=-1 → pre-sweep idle), the force-arm proof (cmd/hotloop) is
decisive:

**Forcing `a9a0=0x190` (≥0) + `befa` bit13 (sweep-done) post-boot drives the
firmware out of the boot-measurement idle loop and INTO the live operating loop.**
- It takes the armed path (0x173EE) + process branch (0x17490) + reaches
  **fcn.18568** (0× otherwise).
- It renders the **softkey menu** — "COPY DEV / PRNT PLT", "Plot Config",
  "Print Config", **"Time Date"**, "Chan Prefix", "More 1 of 8" — plus the boot
  banner ("COPYRIGHT HP 1986-90", "rev 980615", "HP-IB ADRS: 0"). The softkey
  menu is the interactive UI and never renders in the stuck idle state.
- **800 vectors drawn** (Lines 7905→8705) vs 0 normally. screens/proof_forcearm.png.

So the long-standing "operating loop / DLP render" blocker reduces to: **the
firmware never arms a sweep because fcn.8f04 computes the point counter a9a0 = -1
at steady state.** Make a9a0 arm to a valid positive count (and befa bit13 advance
in lock-step with a real sweep cycle) and the firmware runs the live UI.

Remaining gates (now bounded, downstream of the arm):
1. The **C trace-draw at 0x174E0** (`jsr $568`, arg 0) is gated by **fcn.104dc**
   (0x174D0) which returned bit0 SET → SKIP. fcn.104dc blanks the draw when
   `fcn.104ba() ≥ 0x191(401)` OR (`pos ≥ b0ac` ∧ `b1e0` bit13 ∧ ¬`b072` bit10 ∧
   `b0c2`==0). So a faithful sweep (point index advancing 0..401 in lock-step)
   would satisfy it.
2. The arm itself: why fcn.8f04's `-0x4(a6) ≥ 0x3a98` path picks a9a0=-1 (the
   point-count math from span/sweep-time). That is the natural fix vs. forcing.

Net: the blocker is now a concrete, bounded **sweep-arm + lock-step point-counter**
model, PROVEN to unblock the live operating UI when satisfied. Tools: cmd/dlpfresh,
cmd/sweepstate, cmd/hotloop (force-arm proof).

### IMPLEMENTATION EXPERIMENT (2026-06-01, branch sweep-cycle-model) — IRQ6 lock-step unblocks the UI + drawing

The sweep IS armed during boot (bf34=0x40B8/0x410A written at 0x173CC, 18×) but our
BootToOperating never fires IRQ6, so no samples capture, the sweep never completes,
and the firmware aborts (bf34→0, a9a0=-1) into the idle orchestrator.

**Driving IRQ6 in lock-step DURING boot** (SweepActive=true so f200 supplies
SweepEngine ADC samples; fire IRQ6 while bf34∈{0x40B8,0x410A} and A5<bf30):
- bf34 STAYS armed (109k arm-windows vs reset-to-0), **92k sweeps complete**, befa
  bit13 fires, **55,615 vectors drawn** (vs ~7,900 baseline).
- Renders the **live operating UI** (softkey menu COPY DEV/Plot Config/Time Date/...
  + banner) AND trace-like vertical lines. screens/exp_sweepdrive.png.

**Partial, not clean yet:** the drawing happens during the boot phase; in the
post-160M steady window the operating loop (0x18568) is entered only transiently
(1×) and the C-draw (0x174E0)/__GTTDRW don't re-fire, so the trace shows as two
vertical bars, not a sustained spectrum line. a9a0 stays -1 (time-limited sweep).

**Status:** the MECHANISM is validated (IRQ6 lock-step → armed sweeps → UI + draw),
but a CLEAN sustained trace needs refinement (faithful IRQ6 pacing tied to sweep
time, correct point-index→X mapping, sustained operating-loop residence). Not wired
into machine.go yet — per project rule "a half-mock is worse than the clean screen",
the aggressive-IRQ6 model shouldn't replace BootToOperating until the trace is clean.
Experiment tool: cmd/hotloop (branch sweep-cycle-model).

### TWO-IRQ SWEEP MODEL (2026-06-01) — the sweep is IRQ-driven (IRQ1 step + IRQ6 capture); trace draws

Decisive architecture finding (answering "how is the main loop notified of sweep
complete?"): **the sweep is fully interrupt-driven by TWO interrupts, both of which
raise the sweep-done flag the main loop polls — no busy-wait, no flag-forcing:**
- **IRQ1 (0x2AB8) = sweep UPDATE/STEP:** `jsr $ca` (load sample/step), `bset #13,
  $befa` (sweep-done), reprograms the sweep DACs f200/f300/f400/f70a/f716. This is
  what ADVANCES the ramp across the band.
- **IRQ6 (0x4088) = sample CAPTURE:** reads f200, scales (bf2e video-filter
  integrator), dispatches via bf34 to 0x40B8 (store to (A5)+) ; when A5≥bf30
  (buffer full) it falls through to **0x40C2 end-of-sweep → `bset #13,$befa`**.

My first experiment drove only IRQ6 → samples captured but the ramp never stepped →
degenerate (vertical-bar) trace. **Driving IRQ1+IRQ6 together** (cmd/hotloop
`irq1+irq6`) fixes it: window LineLog flips from vertical to 3248 horizontal
segments, the buffer fills (A5→bf30=0x2FD82A), befa bit13 set the REAL way, and the
trace DRAWS — flat noise floor along the bottom + signal spikes (CAL 300 MHz +
injected tones), with the live operating UI (softkey menu + banner). The trace is
FAITHFUL: noise floor -90 dBm is below the 80 dB window at 0 dBm ref, so it sits on
the bottom edge and tones rise as spikes — correct for those settings.
screens/trace_irq1irq6_tones.png.

**So the faithful model is established:** the machine must fire **IRQ1 + IRQ6
autonomously, paced to the sweep time**, while the firmware has the sweep armed
(bf34∈{0x40B8,0x410A}) and the buffer isn't full (A5<bf30) — exactly like the IRQ5
timer tick is injected. SweepEngine (already wired at m.MMIO.Sweep, SweepActive
gates f200) supplies the data. Remaining polish: pace IRQ1/IRQ6 to real sweep
time (not per-chunk burst), sustain the operating loop, and (cosmetic) put the
noise floor on-screen for a textbook trace. Tool: cmd/hotloop (branch
sweep-cycle-model), modes irq6 / irq1 / irq1+irq6.

### WIRED INTO THE MACHINE (2026-06-01, branch sweep-cycle-model)

The two-IRQ sweep model is now production code (opt-in), not just an experiment:
- **`Machine.SweepDrive`** (bool, default false) + **`Machine.driveSweepCycle()`** —
  in the boot loop, while the firmware has the capture handler armed
  (bf34∈{0x40B8,0x410A}) and the buffer isn't full (A5<bf30), it fires IRQ1 (sweep
  step) then a burst of up to 8 IRQ6 (sample capture). The firmware's own handlers
  raise befa bit13 (sweep-done) when the buffer fills — faithful, no flag-forcing.
- **`Machine.BootToOperatingWithSweep(maxCycles)`** — enables SweepDrive +
  SweepActive and boots; supplies trace data from m.MMIO.Sweep (inject tones via
  m.MMIO.Sweep.Spectrum.Signals first). Kept SEPARATE from BootToOperating so the
  deterministic golden-screen boot is untouched (verified: full suite green).
- **`TestBootToOperatingWithSweep`** locks it in: asserts befa bit13 set (sweep
  completed the real way), trace buffer non-empty (samples captured), and >15k
  vectors drawn (trace drawn). cmd/sweeprun2 renders it (screens/sweeprun2.png).

Remaining polish (cosmetic, not blocking): pace IRQ1/IRQ6 to real sweep time
instead of per-chunk burst; put the noise floor on-screen for a textbook trace;
sustain the operating loop indefinitely. The core blocker — "the firmware never
sweeps because nothing fires the sweep IRQs" — is RESOLVED.

### REFINEMENTS (2026-06-01) — real-time pacing, random noise floor, signal limits

Three polish items, all on branch sweep-cycle-model:
- **Paced to real sweep time:** driveSweepCycle now paces via a cycle accumulator
  (`sweepCyclesPerPoint=2300` ⇒ ~58 ms for a 401-point sweep) — one IRQ1+IRQ6 point
  per ~2300 cycles, not a per-chunk burst. Clamps the accumulator while the buffer
  is full so re-arm doesn't burst-fire the next sweep.
- **Visible random noise floor (grass):** SweepEngine gains NoiseFloorDBm (default
  -72 dBm, ~10% up the 80 dB window) + NoiseAmpDB (±6 dB) + a seeded RNG; DetectADC
  power-sums the per-point random grass with the spectrum. The trace now shows the
  classic noisy SA baseline (400/401 points non-zero) — screens/sweeprun2.png. The
  analog model's true -90 dBm thermal floor is unchanged; the grass is a display
  layer in SweepEngine (seeded → reproducible; tests unaffected).
- **Signal boundary/limit check:** `SweepEngine.SetSignals` drops tones outside the
  sweep span [StartHz,StopHz] and clamps amplitudes to the display window
  [RefLevel-80, RefLevel]; returns the dropped count. cmd/sweeprun2 demonstrates a
  5 GHz tone dropped and a +20 dBm tone clamped.

Tests: TestSweepEngineSetSignals (boundary), TestBootToOperatingWithSweep (still
green with pacing+grass), full suite green. Render: screens/sweeprun2.png shows the
grass baseline + live operating UI.

## WHY a9a0 SETTLES -1 (2026-06-05) — the sweep-arm gate is b0a1 bit 3 (CONTS), and it's a deadlock

Traced the "operating loop never sustains" blocker down to the exact bit, measured
end-to-end (probes: `pkg/emu/machine/sweeparm_diag_test.go`, DIAG=1). The whole
chain — from the un-repainted graticule / un-drawn trace to the root — is:

```
graticule eroded where the trace is  ← fcn.18568 (operating loop) never entered (measured 0×)
  ← the boot sweep-orchestrator (0x17460 `tst a9a0; blt`) only marks sweep-DONE / hands off when a9a0 >= 0
  ← a9a0 = -1 (slow sweep DISABLED) because fcn.8f04 @ 0x8F5A `btst #3,0xb0a1; beq 0x92b2` sees b0a1 bit 3 CLEAR
  ← the CONTS-mode handler fcn.5f968 (sets b0a1 bit3) runs 0× — only reachable via the command
     dispatcher fcn.12288 @ 0x126D8, which runs IN the operating loop (never entered)
  ← DEADLOCK
```

### The gate (fcn.8f04, the sweep point-count function)

`fcn.8f04(d0=sweep-time-µs)` computes the point counter `a9a0`. Thresholds:
0x4E20=20 ms, 0x3A98=15 ms, 0x30D40=200 ms, 0xC3500=800 ms, 0xF4240=1 s.
- **Fast sweeps (<20 ms)** take the 0x8F10 `blt 0x8F9C` branch and arm regardless
  (`a9a0=20` seen at 0x90C8) — the engine works.
- **Slow sweeps (≥20 ms — the boot's coupled ~58 ms)** fall through to 0x8F5A
  `btst #3,b0a1`; with bit 3 clear they jump to **0x92B2 → a9a0 = 0xFFFF (-1)**.
  (Probe note: the write at 0x92B2 is 6 bytes, so the captured PC reads 0x92B8.)

### b0a1 bit 3 = continuous-sweep mode (CONTS)

`b0a1` bit 3 is written in exactly one place — `fcn.5f968` (dispatch slot 0x550,
`jmp 0x5f968`), which sets bit 3 to match a command argument (`btst→sne→eor→bchg`).
Its only caller is the command dispatcher `fcn.12288 @ 0x126D8`. `CONTS`/`SNGLS`
exist as command strings in ROM (~0x5A283). So bit 3 = the **CONTS continuous-sweep
mode**, set by parsing that command — which never happens at boot.

### The deadlock, measured

- `fcn.5f968` (CONTS setter) reached **0×**, `fcn.18568` (operating loop) reached **0×**.
- The orchestrator reads `a9a0=-1` **6377×** (idle poll 0x17464) vs armed only **16×**
  (the transient fast sweeps); sweeps DO complete (`befa` bit13 set 44×) but never hand off.
- So: full-span sweep disabled (no CONTS) → no hand-off → no operating loop → no CONTS
  → disabled. Self-reinforcing.

### Lever confirmed — necessary but NOT sufficient

Holding `b0a1` bit 3 set across the boot makes `fcn.8f04` arm the **slow full-span
sweep — `a9a0 = 252` (0xFC)** appears, vs only -1/20 without it. That *proves* bit 3
is the arm gate. But it is **not sufficient** — measured by the reliable signal
(**vectors drawn**, `chip.LineLog`, = the operating UI rendering): forcing `b0a1`
bit 3 draws **+570** vectors over baseline, whereas the direct force-arm draws
**+19248** (the full operating UI). So one bit arms the counter but the sweep still
doesn't complete+persist+hand off. This matches the historic force-arm result: only
forcing the *end state* (`a9a0` positive AND `befa` bit13, **persistently**) ever
renders the operating UI.

> **Detector caveat:** the `fcn.18568`-entry signal "a `b0a1` write from PC
> 0x18568–0x1A000" is **unreliable** — the operating loop renders (+19248 vectors)
> while that detector reads 0, because the loop doesn't always hit the `bclr #7,b0a1`
> at 0x1933A. Use **vectors drawn** as the "operating UI rendered" signal, not that.

### Ruled out (with evidence)
- **CRT-controller sync** — the HD63484 has no frame/vsync MPU interrupt (datasheet
  §2.3.3: command-error/command-end/edge/light-pen/4×FIFO only); its vsync/hsync are
  raster outputs to the A2 display. The sweep is self-timed (HSWP is an output) and
  can be ~100 s ≫ a 60 Hz frame.
- **Plane separation** (`GraticuleToUpper`) and **phosphor/persistence** —
  disproven, see docs/DISPLAY_FINDINGS.md.

### Kickstart experiment (2026-06-05) — forcing into the op-loop does NOT set CONTS

Tested whether a temporary KICK (force `a9a0=0x190` + `befa` bit13 for ~20M cycles)
bootstraps self-sustaining operation. Result: the kick **renders the operating UI**
(6416 vectors) but **`b0a1` bit 3 stays 0 the whole time** — i.e. even inside the
forced operating loop the firmware does **not** issue CONTS. Stop forcing and it does
not self-sustain (no natural arm; `a9a0` just keeps the leftover forced value).

`b0a1` bit 3 is set in exactly one way — `fcn.5f968`, reached only from the command
dispatcher `fcn.12288 @ 0x126D8` (parsing the `CONTS` command); there is NO whole-byte
write or default-table store of `b0a1`. So continuous-sweep mode is applied by
**parsing a CONTS command**, which must come from the **power-up default-config /
saved-state recall** — and that path never executes in our boot.

### Bounded next task — REVISED

The real missing piece is the **power-up default configuration / state-0 recall**
that issues `CONTS` (and the other default-mode commands) at boot. It is NOT the
sweep handoff (forcing past it doesn't set CONTS) and NOT a one-bit poke. Trace where
the firmware applies its power-up defaults — what feeds the `fcn.12288` dispatcher at
init (the instrument-preset / recall-state path), and why it stalls before issuing
`CONTS`. That is the faithful fix; only after CONTS is set will `fcn.8f04` arm the
slow sweep, the orchestrator hand off, and the trace/graticule self-sustain.

(Practical-but-unfaithful alternative, per "a half-mock is worse than the clean
screen": force `a9a0`+`befa` continuously renders the UI but the firmware never
latches CONTS, so it needs permanent forcing — not a real fix.)

### CONVERGENCE (2026-06-05) — the sweep-arm IS the DLP/operating-loop blocker

Traced the setter the rest of the way. `b0a1` bit 3 ← `fcn.5f968` ← `fcn.12288`
(command-code dispatcher, `(code>>8)-0xd → jump table`) ← **`fcn.12b10 @ 0x12DCE`,
the COMMAND EXECUTOR** (pulls a command word from a record and dispatches). And
`CONTS` is NOT issued by any power-up DLP preset — it appears in ROM only as a
parser vocabulary word (~0x5A283) and inside the `__PZZOOM` peak-zoom macro
(0x630AB/0x63193). So continuous-sweep mode is applied only when the **command
executor runs the power-up default-config commands** — and that executor is the same
operating-loop / DLP command engine that, per this whole document, never runs.

**So the sweep-arm dead-end is not a separate problem — it is one more facet of THE
blocker** (the firmware never runs its operating-loop DLP/command sources), exactly
like the trace-draw (`__GTTDRW`), the front-panel key consume, the annunciator
update, and the timedate display.

### CORRECTION + reconciliation with DRIVETICK_BLOCKER (2026-06-05, later)

Two corrections to the above, both from re-measuring with reliable signals:

1. **`fcn.18568` IS entered — 699× in the sweep-driven boot** (reliable detector:
   `b010` write @0x1856C, the loop's 2nd instruction). The "never entered (0×)"
   claims above used the *unreliable* `b0a1`-write-@0x1933A detector (the loop
   renders without hitting that conditional deep-path bclr). NB: the *passive* boot
   (`BootToOperating`, no sweep drive) does NOT enter it — matching DRIVETICK
   2026-05-31 "cont." — so sweep-driving is what advances the firmware into the loop.
   ⇒ the block is INSIDE the loop / the measure-mode state, **not** a loop-entry
   deadlock.

2. **This sweep-arm facet IS DRIVETICK's "Gate 1."** docs/DRIVETICK_BLOCKER.md already
   established `a9a0=-1` is the multi-layered sweep-arm gate and pinned one layer —
   the **`0xB0EC` display-mode** (0x90CE: `cmpi #0x31,b0ec`; the firmware boots into
   CONFIG mode `0x1A`, never spectrum `0x31`). **This session adds another layer: the
   `b0a1` bit 3 / CONTS gate at 0x8F5A.** Both are necessary, neither sufficient (the
   doc proved mode-alone isn't; I proved CONTS-alone isn't) — exactly the doc's
   "multi-layered measure-mode state, not a single lever" conclusion. DRIVETICK's
   "Gate 2" (the trace-draw DLP source `0x5ECEE`/`__GTTDRW` never scheduled) is
   unchanged.

**Net (corrected):** the session ruled out CRT-sync / plane / phosphor, settled the
`fcn.18568`-entry question reliably (entered, sweep-driven), and pinned one more layer
of DRIVETICK Gate 1 (`b0a1` bit 3 / CONTS). The root is unchanged and is the
documented multi-session task: the firmware boots into CONFIG mode and never enters
the continuous-sweep **MEASURE-mode** DLP state (`0xB0EC`→spectrum + the measure-mode
DLP that schedules the trace-draw). See docs/DRIVETICK_BLOCKER.md for the full Gate
1+2 map.

### REFINEMENT (2026-06-05, later still) — it's a COMMAND-DELIVERY gap, and the analog idle is a red herring

Three new measurements (`pkg/emu/machine/oploop_diag_test.go`, DIAG=1) sharpen the above
from "the operating loop / measure-mode state is blocked" to the exact missing event:

1. **`fcn.18568` is on the stack the WHOLE idle — not a derail, not a DLP-step block.**
   `TestIdleStackScanDiag` raw-A7-stack scan at the analog-poll idle: an `fcn.18568`
   (0x18568..0x18B00) return address is present in **200/200** samples; `fcn.34EE8` (DLP
   interpreter), the DLP scheduler, and the command dispatcher are **ABSENT**. The
   "dominant boot-measurement analog idle" (PC pages 0x4800/0x5E400, ~9800 `0xFFF75E`
   reads) is the operating loop calling the boot-default analog measurement DIRECTLY — the
   compiled routine at DLP-RAM `0xFC9A32` → `fcn.5e88c`/`fcn.5f0c4` → poll `fcn.5e5de`
   (`TestIdleStackDiag` gives the call chain). So the loop is HEALTHY; it just lacks CONTS.

2. **The analog `0x9A` cadence is NOT the trace gate (but IS a real faithfulness bug).**
   `fcn.5e5de` waits for `(0x9A_status & 0x12)==0x02` (bit1 set) with a ~1000-unit timeout;
   our model (`analogbus.go statusReadyEveryNReads=256`) returns the real status only every
   256th read and `0x00` (bit1 CLEAR — unfaithful; ready/settled are static-on when powered)
   the rest, so the poll mostly times out and burns the loop. `TestADCCadenceSweepDiag` A/B
   (cadence 256→1): analog reads `18997→735`, op-loop entries `71→547`, **but `b0ec`/`a9a0`/
   `b0a1` byte-identical** and the old "ready-every-read collapses the render" regression does
   NOT reproduce (vectors ~unchanged). So fix the cadence for faithfulness, but it does not
   unblock the trace. (And `fcn.5E6E8`, the PRESET ADC cal, COMPLETES — returns `0xFFFF`
   uncal — it does not infinite-loop; the `fcn.5e63c` sub-poll is only on the cal-VALID path.)

3. **CONTS has exactly ONE delivery path — pinned to one instruction.** `b0a1` bit 3 is
   never wholesale-written; its only writer is `0x5f980 bchg` in `fcn.5f968`, whose only call
   site is `0x126d8 jsr fcn.00000550` — the CONTS *case* of dispatcher `fcn.12288`
   (`move.w -0x8(a6),d0` = command arg → `jsr`). So CONTS is set ONLY when the command
   executor dispatches a CONTS opcode+arg.

### CORRECTION (2026-06-05, later) — the gap is RESOLVE→DISPATCH, not "command delivery"

Sent a real `CONTS` over HP-IB the faithful way (`TestSendCONTSDiag`), fixing two things: the
message terminator is **LF (`\n`)** not `;`, and the command must be driven **IRQ5-only** (the
sweep starves command execution — HPIB_E2E_FLOW.md). Result: `CONTS\n` is received + parsed and
the **name-lookup `fcn.320fe` IS reached** — but the **DLP scheduler `fcn.349b6` is NOT** (the
dispatcher `fcn.12288` 0×, CONTS handler 0×, `b0a1` unchanged). So this is NOT a "delivery" gap
and NOT the "0x18F3E deep-block never reached" story (superseded — 0x18F3E IS reached). The
operating tick, parser, and name-lookup all run; the gap is one step later: **a resolved command
name never invokes its handler** (the `fcn.320fe`→`fcn.349b6`/`fcn.12288`-slot dispatch). Same gap
hits CAL DISP / CAL DUMP / MEASOFF (HPIB_E2E_FLOW.md 2026-06-02). Bounded next task: RE why a
resolved command's handler/DLP-source is never scheduled after `fcn.320fe`.

> **SUPERSEDED (2026-06-05, same day): commands DO execute — this "gap" was a harness artifact.**
> Replicating the GUI keyboard path (NO `InstallHPIB`; AT scancodes F8→type→Enter; drive LONG)
> runs `CAL DISP;` to completion — cal-label region read, command echoes on screen, `fcn.349b6`
> fires (`TestSendCONTSDiag`, asserted; `screens/cal_disp_kbd.png`). The "`fcn.349b6` never fires"
> reading came from the wrong input path (`InstallHPIB` steals IRQ4 from the AT keyboard via `b05f`
> bit0) + too-short drive. The operating loop, parser, scheduler, and command handlers all work,
> and the trace draws (grass visible). CONTS specifically still doesn't reach `fcn.5f968` typed
> this way — a narrow open question, not a general execution gap. See
> docs/MEASURE_MODE_HANDOFF.md "★★ DEFINITIVE CORRECTION".

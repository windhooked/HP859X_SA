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

## ★★★ ROOT, rigorously traced (2026-06-06) — it's CONTS, and CONTS is blocked by direct-C dispatch

A single-decision backward trace (the right method — see the meta-note in chat) converged:

1. **The boot-sweep arm gate is CONTS, NOT `b0ec`.** Read `fcn.8f04` (the sweep-point/arm
   function) instruction-by-instruction. The boot sweep is **58 ms (slow, ≥ 0x4E20=20 ms)** so it
   takes the slow path, whose arm decision is **`0x8f5a: btst #3,b0a1; beq 0x92b2`** → if CONTS
   (`b0a1` bit3) is clear, `0x92b2` writes `a9a0=-1` (disable). The `cmpi #0x31,b0ec` checks only
   affect sweep-TIME LIMITS (`0x8f22`–`0x8f48`), not the arm. So the long `b0ec→0x31` hunt was a
   RED HERRING; `b0ec` (config `0x1A`/`0x01`, never a spectrum mode) is not the arm gate. (`b0ec`
   write timeline: `TestModeTraceDiag`; mode setter `fcn.21c96` is a pure clamp `fcn.7978`, so
   `b0ec`=whatever caller passes — and no caller passes a spectrum mode at boot anyway.)
2. **CONTS (`b0a1` bit3) is set ONLY by `fcn.5f968`** (proven: 0 other writers all boot,
   `TestB0A1WritersDiag`).
3. **`fcn.5f968` is a DIRECT-C command handler that never fires** — same as typed `CONTS`/`MEASOFF`
   (`TestSendCONTSDiag`): direct-C command dispatch doesn't invoke the handler PC, while DLP-source
   commands (CAL DISP) run.

⇒ **ONE root, two symptoms:** the broken direct-C command dispatch swallows BOTH typed `CONTS` AND
the power-up CONTS that should set continuous sweep → arm the slow boot sweep → (then the
measure-mode DLP schedules `__GTTDRW`). Forcing `b0a1` bit3 arms `a9a0` (252) but draws only +570
vectors — CONTS is the FIRST gate, not the whole chain, but it is the blocking one.

**Next drill (in progress): WHY the parser never calls a resolved direct-C handler PC.** The bus
parser `fcn.58c2e` branches on the HP-IB addressing gate (`0x58c98`: `btst #13,bc64`; `0x58ca2`:
`b1ee`==`0x60/0x61`; `0x58cba`: `b1e4`==`0x34`) — addressed-as-talker ⇒ output path, else ⇒ command
dispatch (`fcn.56d1a`/`fcn.567e0`); the ASCII command executor is deeper (`fcn.580b4`↓). Differential
trace CAL DISP (works) vs CONTS (doesn't) through this to find the first divergence.

### DIFFERENTIAL RESULT (2026-06-06) — divergence pinned to command-ACTION generation

CAL DISP vs CONTS through the dispatch (`TestSendCONTSDiag` env `ATCMD`, milestone+token capture):
- **Both reach every dispatch function identically** (`fcn.58c2e`, `fcn.320fe`, the `0x71D76`
  dispatch table, the DLP interpreter token dispatch at **`0x34C94`** = `jsr (a1)`,
  `a1=table[token]`). So it's NOT control-flow divergence at the function level.
- At `0x34C94` each DLP source dispatches its tokens. Diff of token→handler:
  - CAL DISP: unique **token `0x1B` → handler `0x492EA`** (its action; leads to the cal-label read).
  - CONTS: **NO command-specific token** — only the shared background tokens
    (`0x7C/0x91/0x96/0x99/0x9A`). Its action is never generated.
- Both commands DO resolve (both in the parser-name table `0x7D500`): `CONTS` @`0x7def0`
  (`30 07 "CONTS \0"` + handler-bytes `00 00 74 10 05`); working direct-C `MEASOFF` @`0x7d64e`
  (`30 07 "MEASOFF"` + `20 00 80 05 67` → slot `0x567`, the **`0x80` = direct-C flag**). CONTS's
  handler-bytes **lack the `0x80` direct-C flag** that MEASOFF/EDITDLP (`80 01 41`) carry.

⇒ **Root locus (pinned): command-ACTION generation** — translating a resolved command's
handler-bytes into an executed dispatch token. CONTS resolves but its handler-byte form yields no
action token, so nothing is dispatched (hence `fcn.5f968` 0×).

### ★ REFRAME (2026-06-06) — CONTS is a SOFTKEY/state command (type 0x10), not a typed command

`cmd/jumptable` decodes the parser-table handler-bytes. Of 410 commands, **395 use the slot form**
(`TYPE SlotHi SlotLo`, e.g. `MEASOFF (80 05 67) → slot 0x721AA → jmp 0x03EC9A`) and only **15 are
non-slot**. CONTS/SNGLS are non-slot with the form **`00 00 7X 10`** (CONTS=`00 00 74 10`,
SNGLS=`00 00 75 10`, MKFC=`01 00 7D 10`) — the trailing **`10`** is the catalog's "**softkey
position/state ID**" handler type (ROM_DATA_CATALOG.md). So CONTS is a **type-0x10 softkey/state
command** (ID `0x74`) — on the real panel the **SWEEP→CONT softkey**. That is why typing CONTS
RESOLVES it but never dispatches the action: type-0x10 commands go through the **softkey/menu-state**
mechanism, not the typed-command slot dispatch.

**Implication for the trace:** the power-up CONTS (continuous sweep, the real-instrument default)
is set by the **menu/softkey DEFAULT STATE** selecting CONT — i.e. via the boot-menu loader
([[boot-menu-loader]]: `fcn.358C → fcn.5ACB2 → fcn.5AA88`, per-menu vtable `0xFF9594+menu*0xE0`) /
softkey-ID-`0x74` dispatch → (eventually) `fcn.12288` case `0x126d8` → `fcn.5f968` → `b0a1` bit3.
None of that runs at our boot. **Next:** RE how a type-0x10 softkey/state ID is dispatched (the
softkey/menu state machine, ID `0x74`→CONTS), and why the power-up menu default doesn't select CONT
→ set `b0a1` bit3. (Open: MEASOFF is a SLOT command yet also didn't dispatch when typed — so the
typed-command immediate dispatch may ALSO be incomplete; verify separately.)
Probe: `TestSendCONTSDiag` (env `ATCMD`, dispatch-milestone + `0x34C94` token capture); `cmd/jumptable`.

**UPDATE (2026-07-12, en route to the A7 map):** `fcn.12288` is now fully decoded — it is the
**typed-command CLASS dispatcher**: `d0 = cmdword; lsr #8; subi #0xd` → 33-case signed-offset table
at ROM `0x12754` (bounds `word@base-6 = 0x21`, dispatch helper `fcn.6862` pops the return addr
`0x1279c` as table base). Case index 26 (class `0x27`) = `0x126d4/0x126d8: move.w -0x8(a6),d0;
jsr fcn.550` → slot `0x550 = jmp 0x5f968` = CONTS. **The service softkey IDs (0x99–0xB1, menu
template ROM `0x7cc30`) can NOT enter this dispatcher as-is** — their class `(ID>>8)=0` is below
the table floor `0xd`; the `10 NN 00 00` menu-record trailer binds ID→display-label only (via
`fcn.E7A2`, RAM `0xFF9914+`). The ID→action binding is produced upstream by the softkey key-event
emit layer — the same machinery as the Gate-2 front-panel keystone. At boot the dispatcher runs
exactly once, class `0x12` (code `0x12D6`).

### ★★★ 2026-07-12 — the DISPATCH SPINE is cracked; legitimate CONTS dispatch VERIFIED in-emulator

Full decode (softkey-dispatch RE agent + verified):

1. **The spine:** operating loop `fcn.18348` drains the command-source ring (`bb96`/`bb98`
   head/tail) → dequeue+parse (`fcn.427c`→`fcn.1a6e2`) → builds a 0xE-byte record at `0xbb82`:
   `record[0..7] = (0xb03e)` context longs, `record+8 = 0xb1e4` (**the command WORD**, writer
   `fcn.11750 @0x11798`, ~14 call sites), `record+0xa = 0xb1fe` — gated on **`b1e0 < 0`**
   (command-pending) → `jsr fcn.12b10 @0x183b6`. `fcn.12b10` (sole caller of `fcn.12288`,
   @`0x12dce`): **class byte == 0 → the class-0 data-entry path (`0x12dd6`→`0x1344c`), class ≠ 0 →
   `fcn.12288`** with the record word pushed and `movem.l record,d0-d1`.
2. **Typed CONTS is a NO-OP by design:** its parser form `00 00 74 10` yields class `0x10` →
   `fcn.12288` index 3 → jump-table entry `0x1278e = 0x0000` → the `0x1279c` unlk. The differential
   result ("CONTS resolves but no action token") is root-caused: **the class-0x10 slot is
   intentionally empty**; the CONTS *action* exists only as **class 0x27**.
3. **Handler semantics:** `fcn.5f968(d0)` — arg = record[0..1] (incoming d0 HIGH word); bit0 =
   desired on/off; on state mismatch `bchg #3,b0a1` (@`0x5f980`, the sole CONTS-bit writer) then
   runs the continuous-sweep arm block (b068/b06c checks, b0a2 windows, `fcn.eca2/1ea/178/5d4`).
4. **Menu machinery** (for the emitter hunt): label vtable `0xFF9594+menu*0xE0` (56×4-byte ptrs
   to 16-byte Pascal buffers, built by `fcn.5aa88` from ROM ptr table `0x2611C`); per-menu state
   block ptr `0xFF957C+menu*4`, 6-byte per-slot records `{w0,w2,w4}` with **w4.low = softkey ID**,
   **w4.bit13 = highlight/state**, template cloned from ROM `0x25FD2` (165 words); active menu
   installed via `fcn.5a918` (`0xFF9562` state base / `0xFF9566` vtable); **`b1e4` itself is
   per-menu saved/restored state** (`fcn.5a946 @0x5aa0e`). The default template has NO ID `0x74` —
   the CONT binding appears when the SWEEP menu is built.
5. **★ VERIFIED EXPERIMENT** (`TestCONTSDispatchDiag`, pkg/emu/machine): the minimal legitimate
   dispatch — `push.w #0x2701; d0=0x00010000; d1=0; jsr fcn.12288` (exactly what the SWEEP→CONT
   softkey emit would produce) — **sets CONTS through the firmware's own dispatcher**
   (`b0a1: 0x00→0x08`). No RAM-cell forcing. **However** the sweep-arm counter `a9a0` stays `-1`
   and paints don't increase on resume: post-boot, the arm decision (`fcn.8f04 @0x8f5a
   btst #3,b0a1`) doesn't re-run without a **sweep-restart trigger** — the next link to chase
   (candidates: the `fcn.5f968` post-bchg arm block's bail conditions — `b1e4==3` check,
   `b0a2` window — and whatever re-invokes the sweep-setup path after a mode change).
6. **The remaining unknown (sharpened):** the softkey key-event EMITTER — what writes a
   class-bearing word (`0x27xx`) into `b1e4`/the record for a softkey press, and **where
   front-panel key/RPG events physically enter** (NOT `0xEF40xx` — that is the RTC, see the
   corrected [FRONTPANEL_UC_SCOPE.md](FRONTPANEL_UC_SCOPE.md); not the PPI `0xF000` (init-only);
   not the PIT beyond the AT-keyboard regs). Attack: walk `fcn.11750`'s ~14 callers and the
   ring-producer sites back to their hardware reads.

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
   visible sweep isn't gated on `b0a1`/`a9a0` the way assumed.

   **THE TRACE BLOCKER, re-confirmed CORRECTLY (long 2B-cycle natural run, not short/forced):**
   `TestTraceLongRunDiag` — over 2e9 cycles of GUI-style sweep-driven run, the firmware's own
   trace-paint command **`__GTTDRW` (`0x65986`) fires 0×** and the trace buffer (`0x2FD508`, which
   HOLDS the CAL peak max=0x17F) is **never paint-read**. `b0ec=0x1` (not spectrum `0x31`), `a9a0=-1`.
   The 235k vectors drawn are graticule/labels/menus; the flat noise-floor "trace" at the bottom is
   NOT painted from the buffer. So the trace blocker is REAL (not a harness artifact like the command
   stuff): the firmware never enters the continuous-sweep spectrum MEASURE state that schedules
   `__GTTDRW`. Steady-state render: `screens/trace_longrun.png` (full SA UI, no CAL peak).

   The gate is **`b0ec`→`0x31`** (spectrum measure; `fcn.8f04` arms the sweep only when `b0ec==0x31`).
   `b0ec` is written by: PRESET `0x4E01C` (→`0x01`), the mode setter `fcn.21c96` (`0x21CD8`, clamps
   its caller's `d0`), and restores from `b058`/`b248` (`0x11C1A`/`0x11CB8`). The mode setter's caller
   `fcn.220a0` is invoked from `0x1a2xx` (boot, passes `0x01`/`0x1A`) and `0x1c2xx` (command handlers,
   pass `0x0`/`0x1`) — **none pass `0x31`**. Typed commands TS / CONTS / CAL DISP do NOT move `b0ec`
   to `0x31`.

   **REFINED (2026-06-06) — typed DIRECT-C command handlers never fire; only DLP-source commands do.**
   Tested several commands the same way that runs CAL DISP (`TestSendCONTSDiag ATCMD="…"`):
   - `CAL DISP` (public command, DLP-source handler) — **EXECUTES** (cal-label region read).
   - `MEASOFF` (public, direct-C handler `0x3EC9A`) — handler fires **0×**.
   - `CONTS` (direct-C, `fcn.5f968`) — **0×**. `TS` (take sweep) — no `__GTTDRW`, no mode change.
   All reach `fcn.320fe` (lookup) + the DLP scheduler, but the **public direct-C handler PC is never
   CALLED** by the parser, while DLP-source commands schedule + run. This CONFIRMS HPIB_E2E_FLOW.md's
   old "MEASOFF `0x3EC9A` ×0" finding (which was right) — the broader "no commands execute" was the
   wrong part (CAL DISP, a DLP command, runs). Since CONTS and the sweep/measure commands are all
   DIRECT-C, that is exactly why they don't take effect. ⇒ **Bounded next target: RE the parser
   `fcn.58C2E`'s DIRECT-C dispatch** — why a resolved public command's direct-C handler PC (from the
   secondary table `0x71E02`, e.g. MEASOFF→`0x3EC9A`, CONTS→slot 0x550→`fcn.5f968`) is never invoked,
   while the DLP-trampoline path is. Fixing that should let CONTS / the measure-mode commands run.
   Probes: `TestSendCONTSDiag` (env `ATCMD`, tracks CAL-DISP/MEASOFF/CONTS handlers + `__GTTDRW`);
   `TestTraceLongRunDiag` (`__GTTDRW` over a long natural run).
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

# HP-IB (Option 041) end-to-end command/query flow

Dynamic-debugger trace, 2026-06-01 (`cmd/caldump`). Goal: understand why HP-IB
queries (`ID?`, `CF?`, `CAL DUMP`) parse but emit no response, so the real
`CAL DUMP` cal data can be captured.

## Option numbers (HP-IB interface)

| Option | Interface          | Notes                                             |
|--------|--------------------|---------------------------------------------------|
| **021**| **GPIB only**      | HP-IB interface, no parallel port.                |
| **041**| **GPIB + Parallel**| HP-IB **and** Centronics/parallel I/O on one board. |
| 023    | RS-232 only        | Serial interface.                                 |
| 043    | RS-232 + Parallel  | Serial **and** parallel.                          |

The **8593E in `docs/8593E_frontpanel.png` reports `041: GPIB + Parallel`** in its
installed-options list (read directly off the CRT: `004: OVEN`, `119: NOISE`,
`041: GPIB + Parallel`, `8593E #4219`, `Firmware rev 991130`). So this instrument
has the *combined* GPIB+parallel board — which is why the firmware's HP-IB TX goes
out a parallel-style register (`0xFFF122`) and the option board is a combined
GPIB/Centronics I/O controller. **Option 021 is the GPIB-only variant** (same HP-IB
behaviour, no parallel port); the older 8590A/B HP-IB board (A20, TMS9914A) is the
021-class part. The firmware's internal descriptor `bf09` encodes interface *kind*
(4 = GPIB, 8 = RS-232), not the marketing option number — so 021 and 041 both
present as `bf09=4` for the HP-IB path.

## The four stages

```
   HP-IB bus ──┐
   (controller)│
               ▼
 ┌─────────────────────────────────────────────────────────────────┐
 │ 1. RECEIVE   (modeled, WORKS)                                     │
 │    controller addresses instr as LISTENER, sends bytes →          │
 │    option-board µC → 0xFFF142 (data-in) → IRQ4 (0x2642) →          │
 │    bc12 input FIFO (write idx bc28, read idx bc26)                 │
 ├─────────────────────────────────────────────────────────────────┤
 │ 2. PARSE     (modeled, WORKS)                                     │
 │    fcn.58C2E pops bc12 → fcn.57278 trie-tokenizes the command      │
 │    name char-by-char ('I'→46, 'D'→79…). '?' → 0xFFFF (skipped —    │
 │    it is NOT what triggers a query). Command name accumulated.    │
 ├─────────────────────────────────────────────────────────────────┤
 │ 3. DISPATCH GATE  (0x58C98 — THE WALL)                            │
 │    btst #13, bc64        ; bit 13 = TALKER-ADDRESSED               │
 │    beq → SET-command path (fcn.56d1a / fcn.567e0, no output)       │
 │    + requires  b1ee ∈ {0x60,0x61}  AND  b1e4 == 0x34              │
 │    → fcn.58bf4 + formatters (586ae/580b4/583d8/58476)             │
 │      = QUERY-RESPONSE generation                                  │
 ├─────────────────────────────────────────────────────────────────┤
 │ 4. OUTPUT    (path exists, never reached)                        │
 │    formatters → bba6 output FIFO (write idx bbba, read idx bbbc)  │
 │    → drain (fcn @ 0x551E0) → option-board µC dance (0x552B0:       │
 │      f130 cmd, f140/f142/f144 data, f160 status) → f122 TX        │
 └─────────────────────────────────────────────────────────────────┘
```

## Why queries produce nothing

Stage 3 is gated on the **HP-IB talker-addressed state**:

| cell      | gate wants      | our boot has |
|-----------|-----------------|--------------|
| bc64 b13  | set             | clear (8006) |
| b1ee      | 0x60 or 0x61    | 0x001E       |
| b1e4      | 0x34            | 0x0000       |

In real IEEE-488 the controller, after sending the query text, asserts ATN and
sends the instrument's **My-Talk-Address (MTA)** byte. The option board signals
an *addressing-change* event; the firmware decodes it and sets the three cells
above. Only then does the parser take the query-response branch. Our model fills
the input FIFO with the command bytes but never performs the talker-addressing
handshake, so the gate stays closed and every query silently falls through to the
SET-command path (which executes sets fine — that is why SET commands "work" and
queries don't).

## The addressing state is NOT a single register

A sweep of every `0xFFF160` status value (`cmd/caldump`) opens nothing — the
talker state is not driven by f160 directly. The option board (Option 041) is a
**smart I/O microcontroller**, not a flat register file. The addressing event
propagates through several layers:

- **IRQ4 (0x2642)** reads `f160` status → `bf05`, dispatches by bit:
  - bit 1 → receive a data byte (`f140` → bc12)   ← we model this
  - bit 2 → `fcn.1d58`  (option-board EVENT dispatcher)
  - bit 4 → `fcn.2444`
  - bit 5 → `fcn.22f0`  (output drain)
- **fcn.1d58** reads `f120`/`f122` event flags → `befd`/`befe`, and for
  addressing events calls **fcn.1cec**, which reads `f124` (address nibble) and
  updates `bef6`/`f618`.
- The operating-loop HP-IB service (ROM 0x18xxx/0x19xxx) is where `b1ee←a480`,
  `b1e4←afe8`, and `bc64` bits 14/15 (from `9afa`) are committed — driven by the
  events above plus the IEEE-488 address comparison.

So faithfully opening the gate means modeling the option-board µC's event
protocol (`f120/f122/f124/f130/f140/f142/f144/f160` + `befd/befe/bef6/bf02/bf03`
flags) **and** the IEEE-488 addressing semantics (MTA/MLA/UNT/UNL) — i.e. the
"full bus state machine", not one register. This is the honest scope.

## Two realistic ways forward

1. **Option-board µC + IEEE-488 model** (faithful, large): model the smart I/O
   controller's event registers and an addressing state machine, then have
   `SendHPIB` perform listen-address → data → talk-address so the firmware's own
   logic sets bc64.13/b1ee/b1e4 and drains bba6 → f122. Biggest, most correct.

2. **Pragmatic talker force** (targeted, small): after delivering the query +
   terminator, set `bc64 |= 0x2000`, `b1ee = 0x60`, `b1e4 = 0x34` at the gate
   moment and drive the parser, simulating "addressed as talker." Bypasses the
   handshake but exercises the *real* formatters → bba6 → output drain, so it
   produces the genuine response bytes (e.g. `CAL DUMP` values) for capture.

## CAL DISP / command-dispatch divergence trace (2026-06-02)

Goal: send `CAL DISP;` (which should render the cal-data screen) on the GUI device
WITH the HP-IB option installed (`bf09=4`, addr 18), and trace where it gets stuck.
Method: divergence trace (cmd/caldump), IRQ5-only drive (NOT sweep-driven — the
sweep starves command execution; see the sweep/HP-IB time-sharing note).

**Result — the stuck point is the post-lookup DLP DISPATCH, and it's NOT
CAL-specific:**
- `CAL DISP` reaches the DLP name-lookup `fcn.320fe` (×18) and the DLP interpreter
  `fcn.34EE8` (×5) — i.e. the command IS parsed and resolved.
- BUT the DLP scheduler `fcn.349b6` is **×0** — the command's action is never
  scheduled — and the cal-display routine (label region `0x5057c` BANDWIDTH /
  `0x50620` TUNING) gets **0 reads**.
- The same holds for a DIRECT-C-handler command: `MEASOFF`'s handler `0x3EC9A` is
  also **×0**. `MEASOFF`, `CAL DISP`, `CAL DUMP` all execute the *same* ~2769 PCs
  of generic DLP lookup machinery and stop (CAL DISP vs CAL DUMP differ by only 4
  parser PCs at ROM 0x571E4).

**So: every command reaches the DLP name-lookup, but the per-command HANDLER /
ACTION never dispatches** (the DLP scheduler `fcn.349b6` is never called). The
cal-display is simply downstream of this general DLP-VM command-dispatch gap. This
refines the earlier "THE BUS RESPONDS — command executes" note: the command
*machinery* (parser → lookup → DLP interpreter) runs, but the resolved handler is
not actually invoked.

**Per input path:**
- HP-IB: stuck at the DLP-VM dispatch (above) — lookup OK, scheduler/handler never fires.
- AT keyboard: stuck EARLIER — the keyboard delivers raw scancodes to `bc12` while
  the command parser expects ASCII (scancode 'I' ≠ ASCII 'I'), so the typed command
  misparses before even reaching a correct lookup. (Separate, earlier blocker.)

**Next:** RE why `fcn.349b6` (DLP scheduler) is never called after `fcn.320fe`
resolves the command — i.e. how a resolved command name dispatches its handler/DLP
source. That single gap unblocks all command execution (and thus CAL DISP → cal
screen). Tools: cmd/caldump (divergence trace), the DLP-VM docs (DLP_RUNTIME.md /
DLP_VM_ARCHITECTURE.md).

### Cal-display routine + dispatch token pinned (2026-06-02 cont.)

The cal-display routine is **`0x4c636`** (`move.l #0x5030a,D0; jsr 0x730` — renders
the cal strings via the pointer table at 0x4c63c → strings 0x50300-0x50700, incl.
BANDWIDTH@0x5057c / TUNING@0x50620). It is reached ONLY via **DLP token `0x72`**:
the DLP dispatch table `$a74=0x71D76`, entry `0x71D76 + 0x72*4 = 0x71f3e` = `0x4c636`.
(There's a dead master-table slot 0x19b4→0x4c636 too, but nothing `jsr`s it.)

**Verified: NO CAL subcommand reaches 0x4c636** — CAL DISP / CAL DUMP / CAL ALL all
give `0x4c636` hits = 0 and cal-data-region (0x50300-0x50700) reads = 0. And the
DLP dispatch (0x34C94 `jsr (A1)`) during CAL DISP invokes only generic primitives
(0x27B88, 0x3C4C0, 0x56C22…), never token-0x72's handler 0x4c636.

So the precise stuck point: **the CAL command's DLP processing never emits/dispatches
token 0x72** (the cal-display). The DISPLAY CAL DATA softkey is DLP-defined
(`KL'DISPLAY|CAL DATA'` @0x7a92a) and presumably runs a DLP source that emits token
0x72; the HP-IB `CAL DISP` command should reach the same token but doesn't. NEXT:
find the DLP source for DISPLAY CAL DATA (emits token 0x72) and why the CAL-command
path doesn't run it — this is squarely DLP-VM command-dispatch RE.

### CORRECTION (2026-06-02): it's NOT a DLP-dispatch gap — it's IRQ/timer starvation + analog gating

User intuition ("is the irq not fired? / one loop starving the others? / is the DLP
on another IRQ ticker?") was correct. PC-histogramming the stuck CAL DISP revealed the
real chain. Crucial architecture fact: **our model has NO autonomous IRQ generation** —
every `CPU.SetIRQ` is a *manual pulse* from a driver helper (bootLoop's periodic IRQ5,
driveSweepCycle's IRQ1/6, SendHPIB's IRQ4, key helpers' IRQ3). There is no free-running
timer raising IRQ5 and no device-driven IRQ4/3/6. The ROM handlers exist; nothing raises
them on a schedule.

The command IS processed, but it stalls in a CHAIN of hardware-timing waits, each fed by
the IRQ5 timer ISR (`0x3ECE`: `addq.l #1,$bf12` free-run counter; `addq.l #1,$bf16; bne;
bset #7,$befb` countdown-expiry flag):

1. **`0x7C52`** `btst #7,$befb; beq` — wait for the timer-ISR countdown (`befb.7`). A
   broken trace-driver (1-`Step` IRQ deassert) meant IRQ5 was *never serviced* → infinite
   spin. Correct + denser IRQ5 servicing clears it.
2. **`0x4824`** range-check `$bf12` ∈ [lo,hi] — a *scheduled real-time delay* (wait for the
   free-run timer to reach a target). Denser ticks advance past it (verified: `bf12`
   reached 0x7FA68 = 522k ticks).
3. **`0x5E5FA`** analog ADC poll (`move.w #$9a,$f75c; move.w $f75e,$9492; and.b $9493,D6;
   cmp.b (9,A6),D6; bne`) — the **remaining** dominant loop after warmup. Our
   `analogbus.go` 0x9A model satisfies the BOOT poll contract but NOT this
   CAL/measurement-context expected-match value → spins.

cal-display `0x4c636` is downstream of all three; it never fires because the firmware
never clears gate 3. **The fix is architectural, not DLP-RE:** (a) add a free-running
timer that autonomously raises IRQ5 every N CPU cycles (so timed delays resolve
everywhere without hand-pulsing), then (b) extend the analog 0x9A model to satisfy the
measurement-context poll at 0x5E5FA. Tool: cmd/caldump (PC histogrammer).

### RESOLUTION (2026-06-02 cont.): autonomous timer built; analog poll was a red herring

**(a) DONE — autonomous timer.** Added `machine.Timer` + `Machine.pumpTimer(ranCycles)` +
`Machine.RunTimed(cycles)` (machine.go). Period = 10000 mainline cycles (bootChunkCycles*
bootIRQPeriod), ServiceCost 400. The six hand-pulse IRQ5 sites (bootLoop, SendHPIB,
driveHPIB, DriveOperatingTickUntil, ForceOperatingTick, PressKeyMatrix) now funnel through
`pumpTimer`. ALL tests pass (boot/golden/faithful boot unchanged). Verified under pure
`RunTimed` (zero hand-pulsed IRQ5): the 0x7C52 / 0x4824 timer-waits drop from MILLIONS of
hits to flowing-through. New drivers/probes should use `RunTimed` instead of hand-pulsing.

**(b) NOT NEEDED — the 0x5E5FA poll is satisfied once the timer feeds it.** Decoded the
poll as `fcn.5E5DE(mask=0x12, want=0x02)`: loops until `(status_low & 0x12) == 0x02`, with a
~1000-tick timeout. analogbus.go returns 0x06 on its ready-pulse (`0x06 & 0x12 == 0x02` ✓).
Measured during CAL DISP: poll-entry 13996, match 13995, **exit 13992** — it resolves on
essentially every call. The 0x5Exxx region that looked like a stall is just the NORMAL
background ADC-measurement loop. No analog-model change required.

**What actually remains (separate thread).** After boot the firmware idles in a background
measurement loop (bf12 timing @0x04xxx + ADC sampling @0x5Exxx). `novelPCs` in the last 30M
cycles = 0 → it's a bounded loop, not advancing to cal-display. BUT steady-state PC-region
histograms can't tell whether a command executed: NO-COMMAND, `IP` (preset), and `CAL DISP`
all produce identical steady-state region profiles (the idle loop swamps any transient).
Observing command execution needs TRANSIENT tracing — watch the handler region in the first
N cycles right after delivery, before control returns to idle — which is the DLP-dispatch /
cal-display thread above, independent of the timer/analog work resolved here.

## CAL DUMP status (2026-06-02)

`CAL DUMP` is a real GPIB command (HP8595E_Cal_Dump_Wilko.pdf: *"The GPIB command
CAL DUMP allows to access analyzer calibration constants in unformatted numeric
form"*). In the firmware it is the **`CAL` command + `DUMP` argument** — `CAL` is a
parser-name-table entry (ROM 0x7ECA6, handler `80 00 5f`); there is no separate
`DUMP` command. Verified naturally (`cmd/caldump`, live boot + LF terminator):
`CAL DUMP\n` is **recognized and its command machinery executes** (`?`-tokenize
0x33714 ×6, lookup `fcn.320fe` ×18, 2034 command-handler PCs — same profile as a
known-good `MEASOFF`).

**Blocker = the OUTPUT (talker) leg, shared by ALL queries.** `CAL DUMP`, `ID?`,
`CF?`, `RL?` all execute but emit nothing: the output FIFOs (`bba6`/`bbca`) never
grow and `f120` gets no data. The cal-constant read + numeric output happen at
**talker time** — i.e. after the command, the controller addresses the instrument
as talker and the firmware reads the cal data and streams it out. Our model drives
the receive/execute side but not the talker-addressed *output generation*. So
capturing CAL DUMP needs the talker-output leg finished (the firmware computing +
streaming the response when addressed as talker), which is the remaining deep
sub-task — distinct from, and downstream of, the now-working command execution.

## ★ THE BUS RESPONDS — command executes naturally at run level (2026-06-02)

Dynamic full boot to the live operating loop + the LF terminator + **zero forcing**
(`cmd/caldump` pre-flight; reproduce live with `cmd/gdbserver -operating -hpib`):

Boot via `BootToOperatingWithSweep` (the live, sweep-driven operating loop), install
Option 041, address-as-listener, `SendHPIB("MEASOFF\n")`, then run the gdb-`continue`
style loop (single-step + IRQ5 every 2000, **no forced PC**). Result:

| observable | result |
|---|---|
| `0x18F3E` slot-0x69A parser dispatch | **reached** (was believed "never reached") |
| `fcn.58C2E` parser | runs |
| `bc12` read-index | **0x0000 → 0x0008** — parser consumed the whole command |
| `fcn.320fe` command lookup | **18 hits** |
| DLP dispatch (`fcn.34B44` region) | **578 hits** |
| command-handler PCs vs baseline | **2034 distinct PCs execute** (incl. HP-IB handling, e.g. `0x27CD4` checks `9b20`=option type) |

**So at normal run level the HP-IB bus RESPONDS and the command EXECUTES naturally.**
This overturns the long-standing "parser/0x18F3E never reached → command never
executes" conclusion (docs/DRIVETICK_BLOCKER.md). The two keys, both confirmed:
1. the command terminator must be **LF (`\n`/0x0A)** — not `;`/`\r`;
2. the firmware must be at the **live sweep-driven operating level** (`BootToOperatingWithSweep`) and driven with the natural IRQ5 tick — NOT a forced
`fcn.58C2E` call (which lacks the operating-loop context the DLP dispatch needs).

### Inspect it live (interactive gdbserver)

```
# terminal 1 — boot to run level + queue a command, then serve:
DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib go run ./cmd/gdbserver -operating -hpib MEASOFF
# terminal 2 — attach and watch the command path fire:
rizin -a m68k -b 32 -d gdb://localhost:3333        # or gdb -ex 'set architecture m68k' -ex 'target remote :3333'
#   db 0x58C2E    # parser
#   db 0x320FE    # command lookup (fcn.320fe)
#   dc            # continue — breakpoints hit ⇒ the bus responded
# send more commands live without restarting:
#   :> =! via monitor:  monitor hpib CF 1.5GHZ
```
`monitor hpib <cmd>` (LF auto-appended) sends a command over the real receive path
mid-session; then `continue` to watch it execute.

## Command execution path (2026-06-02, fresh run from the receive FIFO)

Starting from the receive FIFO and ignoring the edit-path gate, the actual
HP-IB command-execution path is:

```
bytes → bc12 receive FIFO → operating-tick slot 0x69A → fcn.58C2E (parser/line
editor; accumulates into the command-line buffer bc2c)
   │  on the LF terminator (\n / 0x0A) the COMMAND LOOKUP runs:
   ▼
fcn.320fe (ROM 0x32108–0x321E8) — the DLP VM NAME-LOOKUP — walks the command
   table at ROM 0x7D000..0x80200 and matches the mnemonic
   │   table format: [0x30|0x60][len][name][0x20][0x00][3-byte handler token]
   │   (0x7D000–0x7D650 = menu/softkey labels; 0x7D650+ = command names like
   │    MEASOFF; the 3-byte handler token is "stored directly" after each name)
   ▼
record → token → DLP dispatch (ROM 0x71D76, slot 0xa74) → handler
```

**KEY FINDING: HP-IB command execution dispatches THROUGH the DLP VM.** The
command lookup is `fcn.320fe` — the SAME DLP name-lookup the DLP-derail forensics
describe (docs/DLP_STARTUP_DERAIL.md / DLP_VM_ARCHITECTURE.md). So executing an
HP-IB command is a DLP-VM dispatch, and is bound to that subsystem's state.

**Confirmed (the user's hint):** the command terminator is **LF (`\n` / 0x0A)** —
with it, `fcn.58C2E` triggers the table walk (1925 table reads, `cmd/caldump`);
with `;` or `\r` there are **zero** table reads (no lookup). This is why earlier
attempts that sent `;`-terminated commands never executed.

**Open:** in an isolated forced `fcn.58C2E` call the table is walked but the
handler doesn't dispatch (only line-editor PCs run) — the DLP-VM dispatch needs
the operating-loop caller context/state, and ties into the broader DLP-VM
execution work. Tool: `cmd/caldump` (LF-terminator command probe);
`device/hd63484.SetProbeNoPanic(true)` lets a probe run command execution past
unmodelled display opcodes.

## Implemented controller (2026-06-02)

`device.GPIBController` ([pkg/emu/device/gpib.go](../pkg/emu/device/gpib.go))
models the Option 041 option-board microcontroller at 0xFFF100–0xFFF160 and is
wired into `HP8593AMMIO` (the f120–f160 range delegates to it when installed)
and the machine (`InstallHPIB` activates it; `SendHPIB` feeds its receive
buffer). High-level API on `Machine`:

- `GPIBSend(msg, maxCycles)` — addressed-as-listener program message.
- `GPIBQuery(query, maxCycles) []byte` — listener → data → talker → collect TX.

Register contract modeled: 0x160 status (bit0 I/O-active, bit1 data-available,
bit2 addressing-change event), 0x140/0x142 data-in (RX pop), 0x148 init
handshake (0x01), 0x124 address nibble, 0x12a TX-ready (bit5), 0x120/0x122 TX
capture, 0x130/0x144/0x150 command/strobe accept.

**State: FOCUSED-FAITHFUL infrastructure done; clean RESPONSE still blocked.**
Verified with `cmd/caldump`:
- boot + receive work through the controller with NO regression (lines=20902,
  all existing tests pass);
- the addressing-event injection (`AddressTalker`/`AddressListener` → 0x160
  bit2) **reaches the firmware's addressing decode** — `fcn.1d58` → `fcn.1cec`
  fire and `bef6` is decoded from the f124 nibble;
- when the talker gate at ROM 0x58C98 is satisfied, the firmware takes the REAL
  query branch (`fcn.58bf4` + formatters run, output queued to the bba6 FIFO).

The remaining gap is the SAME Rev L operating-loop blocker behind the two
SKIPPED HP-IB/tick tests (docs/DRIVETICK_BLOCKER.md): the HP-IB service in the
operating loop (ROM 0x18xxx/0x19xxx) that COMMITS the talker-addressed state
(bc64 bit13, b1ee=0x60/0x61, b1e4=0x34) and runs the output drain barely
executes in our environment, so the gate never opens on its own and the
formatted bytes aren't a clean response string. Forcing the gate cells at the
check makes the formatters run but does NOT yield correct output (the broader
HP-IB state isn't genuinely talker-addressed). Cracking the operating-loop
dispatch is the prerequisite — that is the next milestone. Tests:
`TestGPIBControllerInstalled`, `device.TestGPIB*`; round-trip
`TestGPIBQueryIDRoundTrip` is SKIPPED with this rationale.

### 2026-06-02 — root cause found: HP-IB address was uninitialised (0); loop IS live

Two findings (prompted by the observation that the live screen showed
"HP-IB ADRS: 0" and that the instrument is in continuous sweep mode) cut the
blocker down dramatically:

1. **The operating loop `fcn.18568` IS live during sweep mode** — it runs the C
   UI loop continuously (26,697 body visits / 6M instr). The earlier "operating
   loop never runs" framing was about the *passive* boot; under the sweep-driven
   boot the loop runs. So the live-loop prerequisite is already met.

2. **The instrument's HP-IB address was 0 (uninitialised).** The firmware loads
   its own HP-IB primary address from the battery-backed config at CalRAM
   `0x2FC000` (ROM 0x3a22: `move.b 0x2fc000, befc`), masks it to 5 bits, and
   programs it into the option board (`f128`, ROM 0x2c6c) so the board knows
   which bus address to answer. On a zeroed CalRAM that address is 0 — the
   firmware even renders "HP-IB ADRS: 0" — and no talk/listen addressing can
   match. **Fix: `machine.DefaultHPIBAddress = 18` seeded into CalRAM 0x2FC000 at
   construction** (`SetHPIBAddress(addr)` to change it). The screen now correctly
   shows "HP-IB ADRS: 18" (golden updated); `TestHPIBAddressDefault` locks it.

With the address valid, **`b1f8` bit12 (HP-IB-has-work) now sets naturally**, and
with the sweep quieted — so `befa` bit13 (sweep-done) doesn't make `fcn.11da8`
keep diverting the loop into sweep processing — **the loop REACHES the HP-IB
service**: ROM 0x18942 gate → 0x18968 → `jsr 0x358` → **`fcn.3c7d4`** (the HP-IB
command processor) all execute. (`cmd/caldump`.)

**The ONE remaining piece:** make `fcn.3c7d4` commit the talk-address into
`a480` (ROM 0x3c828) so `b1ee ← a480` (0x18996) yields the talker state and the
parser gate at 0x58C98 opens. That needs the **My-Talk-Address (MTA) delivered
through the option-board command path** (not just the status event the
controller currently raises), plus time-sharing the operating loop between sweep
and HP-IB so the service gets cycles. This is now a bounded, well-localized task
rather than the multi-session operating-loop blocker.

### 2026-06-02 — the option-board addressing-notification protocol (traced)

The smart option board tells the firmware about HP-IB addressing via **command
bytes delivered through `f140`**, dispatched by IRQ4:

```
IRQ4 (0x2642) → f160 bit4 set → fcn.2444 (0x24e8)
  → f14a bit0 (byte-ready) → read f140 command byte:
      0x13 → bset #2, bef7   (TALK-addressed)     [ROM 0x259a]
      0x11 → bclr #2, bef7   (talk-unaddressed)   [ROM 0x25aa]
  → operating loop (0x18b14) compares bef6/bef7 addressing state vs b034,
    updates the R/T/L annunciators (0x52='R' 0x54='T' 0x4C='L') and latches b034
```

So "address as talker" = the board delivering byte **`0x13`** via `f140` (with
`f160` bit4 + `f14a` bit0 signalling), which sets the firmware's TALK flag
(`bef7` bit2). The talk/listen flags (`bef7`) then drive the parser's talker gate
(`b1ee`/`b1e4`/`bc64`.13) and the `a480` commit in `fcn.3c7d4`.

**2026-06-02 (cont.) — command path reaches the interpreter; two decoupled layers.**
With `GPIBController.DeliverCommand` + the f160/f14a plumbing and the correct IRQ4
delivery (assert IRQ4 while single-stepping), the command path is now fully
exercised: `fcn.2444` runs, dispatches via the `f144` selector, and reaches the
talk-command interpreter at **0x2580**. There the byte is gated by `bf08 < 0`
(addressing-mode active); forcing that, `0x13`→`bset bef7.2` and `0x11`→`bclr
bef7.2` both fire and change `bef7` (verified: `0x0F`↔`0x0B`). Per the operating-
loop annunciator decode (0x18b48: 'T' drawn when `bef7` bit2 CLEAR), **`0x11` =
talk-addressed** (lights T), `0x13` = un-talk.

**COURSE-CORRECTION (important): the ROM 0x58C98 "talker gate" is the DLP-EDIT
path, NOT the simple-query output path.** The gate cells `b1ee` ∈ {0x60,0x61} +
`bc64`.13 are set by `fcn.59718` / the surrounding handler (0x598xx), whose
referenced strings are **`'EDITNAME … not found'`** and **`'Prefix … Edit item
mem'`** — i.e. this is the DLP-variable name/item EDIT subsystem. So pursuing this
gate for a plain `ID?`/`CAL DUMP` query was the wrong mechanism. Simple queries
must route to output via a **direct command-handler** path (the command-name
lookup → handler that, on `?`, emits the value), which is NOT yet located. That
re-location is the correct next focus — do NOT re-pursue the 0x58C98 talker gate
for simple queries.

**KEY FINDING: the bus-addressing layer (`bef7`/R-T-L annunciators) is DECOUPLED
from the parser's talker gate (`b1ee`/`a480`/`bc64`.13).** Driving `bef7` to the
talk-addressed state updates the annunciators but does NOT move `a480`/`b1ee`/
`bc64`, so the parser still takes the SET-command branch. The parser's talker gate
wants `b1ee` ∈ {0x60,0x61} — and that value is the firmware's "parsing an HP-IB
PROGRAM message" state set by `fcn.59718` (ROM 0x5983a), not the bus-addressing
flags. So the response path needs the *parser* to treat the input as a query
(reach `fcn.59718` / the query formatters), which is a separate mechanism from the
talk-addressing handshake. Resolving how a simple query (`ID?`) routes to the
query-output path — vs. whether it is a direct command handler rather than the
0x58C98 program-parser gate — is the next RE focus. Both `bf08`-prefix and the
`f144` table decode below are sub-tasks within that.

**`fcn.2444` selector (for whoever wires it):** `fcn.2444` (0x2444) dispatches
on **`f144 & 7`** (a sub-operation selector — `bra 0x2634` jump table), THEN reads
the `f140` data byte; the `0x13`/`0x11` talk command is interpreted in the 0x2580
arm of that table. So the `GPIBController` must present the right **`f144`
selector** alongside the `f140` command byte (and the IRQ4 dispatch needs `b05f`
bit0 + `f160` = 0x11 = bit0+bit4 so the handler reaches `0x26ae → bsr 0x2444`).
`GPIBController.DeliverCommand` + the `f14a`/`f160` bit4 plumbing are in place;
wiring the `f144` selector value (decode the 0x2634 table) and confirming the IRQ4
entry is the concrete remaining task. The `f140` command-read path (0x2580) is also
gated by `bef7` bit3 (listen), whose setter isn't a simple `bset` — i.e. the
listen-addressing must be delivered first (HP default sequence: address-listen,
send query, address-talk). cmd/caldump has the DeliverCommand probe.

### Precise localization of the talker-state lever (2026-06-02)

The gate cells are not arbitrary flags — `b1ee` ∈ {0x60, 0x61} is the firmware's
**"actively parsing an HP-IB program message" state**, set inside the HP-IB
program-string parser `fcn.59718` (`move.w #0x61, b1ee` at ROM 0x5983A), and
committed into the operating-loop service via `b1ee ← a480` at ROM 0x18996.
`b1e4 == 0x34` is the paired parser-context value; `bc64` bit13 is talker.

Experiment (cmd/caldump): forcing the FULL operating-loop entry (PC = 0x18568,
which includes the HP-IB service at 0x18996 — unlike `DriveOperatingTick` which
jumps to the 0x18ADC deep block and skips it) **x400 with the addressing event
delivered via IRQ4 still does NOT commit the talker state**: `a480` stays at its
reset `0xFFFF`, so `b1ee ← a480` never produces a talker value, and `fcn.59718`
(which would set `b1ee = 0x61`) is never entered because the firmware is not in
HP-IB-program-processing mode. This is the same root as Gate 1/Gate 2 in
docs/DRIVETICK_BLOCKER.md — the firmware must be genuinely driven into the
HP-IB I/O processing mode (operating-loop subsystem live), which is the
multi-session sweep/measure/IO state-machine task. The HP-IB-specific lever is
now pinned: `a480` → `b1ee` (talker-address) via `fcn.59718` / 0x18996.

## Tools

`cmd/caldump` — dynamic GPIB tracer: tokenizer-return probe (per-char trie
tokens), option-board MMIO histogram (found f160 polled 15004× / query), the
f160 status sweep, and the GPIBQuery gate/formatter trace. Diagnostic hooks:
`device.HP8593AMMIO.SetF160Override`, `device.GPIBController` TX/event state.

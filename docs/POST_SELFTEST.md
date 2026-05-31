# A16 Power-On Self-Test (POST) — the "FAIL: xxxx" display

Cracked 2026-05-31 using the new GDB watchpoint debugger (`pkg/emu/gdb`,
`cmd/gdbserver`) + `cmd/failcode` + `cmd/post`.

## The display

At boot the firmware renders `FAIL: DF0F 0000000000` on the left of the screen.
This is the **power-on self-test result**, not a RAM word — it is formatted on
the fly from two hardware status latches.

## The reporter (ROM 0x184DE)

```
0184EA  move.b $f610.w, D6    ; read POST result LOW  byte  @ 0xFFF610
0184EE  not.b  D6             ; latches are active-low (set bit = test PASSED)
0184F0  move.b $f612.w, D0    ; read POST result HIGH byte  @ 0xFFF612
0184F4  not.b  D0
0184F6  andi.b #$ec, D0       ; only bits 0xEC of the high byte count as failures
0184FA  or.b   D6, D0
0184FC  beq    $18558         ; (~f610 | (~f612 & 0xEC)) == 0  →  NO failure → skip
...
01851C  jsr    $6ca.w         ; format NOT(f612) as hex  → the "DF"
018526  jsr    $6ca.w         ; format NOT(f610) as hex  → the "0F"
```

So **`FAIL: DF0F` = `NOT(f612):NOT(f610)`**. A clean POST requires:

- `f610 == 0xFF`  (all 8 low-byte tests passed)
- `f612 & 0xEC == 0xEC`  (high-byte bits 2,3,5,6,7 passed; bits 4,1,0 don't count)

## How f610/f612 are built (ROM 0x4998 + 0x4534 analog suite)

The POST clears `f610`/`f612`, then runs a suite of A16 bus/peripheral integrity
tests and `or.b`s a PASS bit per subsystem. `f610`/`f612` are read/write latches
in the MMIO backing store, so the writes stick; the tests fail on our virtual
instrument because they probe hardware readback paths a flat backing store does
not replicate. Each bit:

| latch.bit | test | ROM | what it checks | model |
|-----------|------|-----|----------------|-------|
| f614/f616 strap | "mark all pass" | 0x49A0 | if either status input ≠ 0 → `f610=f612=0xFF` then run detailed suite (which `or.b`s, never clears) | **assert f614=f616=0xFF** (POST-bypass strap) |
| f612.3 | data-path loopback | 0x4A0E | write pattern→`0xFFF700`, read `0xFFF780`, expect echo | **f780↔f700 mirror** (addr bit 7 not decoded) |
| f612.6 | address-decoder latch | 0x4AA0 | write `0xFFF700+i*2`, read `0x320000 & 0x1F`, expect `==i` | **A16AddrLatch @ 0x320000** ← MMIO addrLatch |
| f612.7 | HD63484 VRAM wrap | 0x4B0C→0xD6B2 | write pattern to ACRTC VRAM, read back ×16384, expect match | **NOT YET** — needs the HD63484 RD command |

`bb2c` is the suite-local accumulator (all 27 ROM refs live in 0x4500..0x49E8),
so the bypass strap only affects the POST verdict — safe.

## Status (2026-05-31)

Three faithful hardware models implemented in `pkg/emu/device/mmio.go` +
`pkg/emu/machine/machine.go`:
1. **POST-bypass strap** f614/f616 (constructor).
2. **f700↔f780 data-path mirror** (Read).
3. **A16 write-address latch @ 0x320000** (`addrLatch` + `A16AddrLatch`).

Result: `DF0F → CC00 → C000 → 8000`. **f610 fully clean; f612 = 0x7F** (15/16
bits). The last bit (f612.7) is the HD63484 ACRTC VRAM read-back wrap at ROM
0xD6B2 — write a pattern to VRAM (cmd 0x5800), then a 16384-word read-back loop
(cmd 0x4400, MAR=0x4000:0) comparing each read of `0xFFF5FE` to the pattern. Our
display tracks `vram` + `MAR` for raster *writes*; implementing the inverse RD
path would clear it.

## Note: the status annunciators are SEPARATE

`REF UNLOCK`, `ADC-TIME FAIL`, `OVEN COLD` persist unchanged across all four FAIL
states above — they are **not** driven by the f610/f612 POST word. They have an
independent status source (still to be cracked with the same watchpoint method).

## Annunciator investigation (REF UNLOCK / ADC-TIME FAIL / OVEN COLD) — in progress

Method applied (read-watchpoint the ROM string + backtrace). Established with
`cmd/annunchunt`:
- The 5 status strings live consecutively at ROM 0x2b37f (ADC-TIME FAIL),
  0x2b38b (ADC-GND FAIL), 0x2b39b (ADC-2V FAIL), 0x2b3a7 (OVEN COLD), 0x2b3fd
  (REF UNLOCK).
- They are copied to RAM (e.g. REF UNLOCK→0xFC44D2, ADC-TIME→0xFC43A2) by the
  menu builder **fcn.5AA88** (reached via `jsr fcn.5ACB2` at ROM 0x3A02), which
  copies a whole string table from `[0xCD2]` into per-menu slot vtables at
  0xFF9578 / 0xFF9590 / 0xFF9594+menu*0xE0.
- **All 5 are copied; only 3 are shown** (REF UNLOCK, ADC-TIME FAIL, OVEN COLD)
  — so the draw is status-gated, with ADC-GND/ADC-2V passing but ADC-TIME
  failing (the ADC self-test has per-reference bits: GND/+2V ok, TIME fails).
- Ruled out: NOT the f610/f612 POST word (annunciators persist across all FAIL
  states); the ROM strings are read ONLY by the copy (PC 0x6A48); the RAM copies
  are read ONLY by the builder length-check (PC 0x5AAFE) — **never re-read at
  screen-draw time**. So the graticule glyphs are emitted in the builder's
  one-pass copy/draw (chars in registers) or by a separate status render, gated
  by a status test not yet localized.

Next: instrument the menu render over the 0xFF9594 slot vtable to find the
per-slot status-condition field, OR (more direct) find where each subsystem
POSTS its status (ref-lock detect → REF UNLOCK; ADC timing test → ADC-TIME FAIL;
oven 5-min timer → OVEN COLD) and model that hardware status. Each annunciator
maps to a specific un-modeled analog/timer status, so this dovetails with the
analog model (docs/ANALOG_MODEL_PLAN.md). OVEN COLD is the easiest — a fake
5-minute IRQ5-tick timer with no temp sensor; it self-clears after ~5 min of
modeled runtime.

### Update: the fcn.268aa status-message system is NOT the boot annunciator path

Traced the static gate: ROM 0x26d2e draws a status string (base 0x2b31e) when
`fcn.268aa() != 5`. fcn.268aa reads the annunciator-status indirect bus at
**0xFFF758 (data) / 0xFFF75A (select)** (write select 8 → read 0xFFF758 →
process high byte & 7), gated first by `fcn.2689c` which tests **0xFFBF2A bit 16**
(`move.l $bf2a,D6; not.l; btst #$10` → returns bit0). BUT a boot PC-reach probe
shows **none of 0x26d2e/0x268aa/0x2689c/0x26ede execute during boot** (x0), and
0xFFF758/75A sees **zero** boot traffic. So this is a *separate* message system
(HP-IB / service-mode), sharing the 0x2b31e string base — a red herring for the
graticule annunciators.

**Reframe for next session:** the boot annunciators (REF UNLOCK / ADC-TIME FAIL
/ OVEN COLD) are most likely drawn ONCE as the default power-up state and only
*removed* when each subsystem's status-good path runs (ref-lock acquired, ADC
cal pass, oven-warm timer elapsed) — which never fires with un-modeled hardware.
Productive target = the status-CLEARING path per annunciator, found by either
(a) instrumenting the ACRTC glyph emission (watch 0xFFF5FE writes + backtrace) to
find the actual graticule drawer/clearer, or (b) modeling each subsystem status
(0xFFBF2A is a candidate system-status long worth watching). Not the 0x26Exx /
0xFFF758 path.

### Candidate status words found (for the interactive-debugging follow-up)

The boot status render lives around ROM 0x184B6-0x184DE (just before/with the
FAIL reporter): `jsr fcn.17546; jsr fcn.5B0DA; ... <FAIL reporter 0x184DE>`.
`fcn.5B0DA` is menu-state mgmt that does `move.w $bef6,D6; not.w D6; move.w
D6,$b034` — same read-and-invert idiom as the FAIL reporter. Measured at boot:
- **0xFFBEF6 = 0x000F** (writers: 0x1D0E sets 0x000F; 0x2F7A/0x6BAA clear it) →
  **0xFFB034 = 0xFFF0** (read once at 0x17472, the boot-settle PC).
- **0xFFBF2A**: system-status long, fcn.2689c tests bit 16 (but that path is not
  on the boot annunciator route).

Neither is a clean 3-bit match for the 3 shown annunciators, so the per-slot
condition is elsewhere in the menu render. The render uses table-dispatched
(slot) calls, which defeat A6-frame backtracing — so the productive method is
**interactive**: use the GDB stub (`cmd/gdbserver`) to break in the 0x184B6
render region / fcn.5B0DA and single-step the menu-slot draw, watching which RAM
status word each annunciator's draw tests. Diagnostic tools left in place:
cmd/annunchunt, cmd/befprobe.

**Recommendation:** crack this interactively (break at 0x184BA, step the render)
in a focused session, OR pivot to the M2 spectrum trace (fully mapped, tractable,
high visual impact) and return to the cosmetic annunciators after.

### CRACKED: the annunciator render pipeline (via shadow-stack tracer)

The table-dispatched render that defeated A6-backtracing is solved with
`cmd/rendertrace` (uses the new `CPU.RunUntil` to land exactly on the render
entry, then single-steps tracking call DEPTH through the slot dispatch). The boot
status render `fcn.17546` → slot 0x520 calls a tree of drawers; the annunciator
one is **fcn.11B9A**, which reads the hardware-status source flags and aggregates
them into the packed annunciator display words:

```
fcn.11B9A (ROM 0x11B9A):
  B084 = (B084 & 0x00FF) | (B20E & 0xFF00)
  B08C = (B08C>>15)      | (B1F0<<1)              ; B08C annunciator bits <- B1F0
  B098 = (B098>>15 & 1)  | (B1F6 & 0xFFFE)        ; B098 <- B1F6
  B060 = (B060>>15)      | (B1FA<<2) | ((B1F8>>2)&2)  ; B060 <- B1FA, B1F8
  B068 = B038
  btst #13,B1E0 -> bset #31,B0C2 ; btst #11,B0CE -> B246/B248 swap
  jsr fcn.6B1C(A0=B060, n=0x64) -> B128           ; CHECKSUM (change-detect, not draw)
```

So the annunciator display state lives in **B060/B068/B08C/B098/B0C2** and is fed
by the source-status flags **0xFFB1E0/B1F0/B1F6/B1F8/B1FA** (enumerate writers
with `cmd/befprobe`; each is a complex multi-subsystem word, ~25 writers — e.g.
B1E0=0x1830, B1F0=0x0064, B1F6=0x00A0, B1F8=0x1856 after boot). fcn.6B1C is a
checksum/smoothing helper, not the glyph drawer.

**Remaining (well-defined):** map a specific packed bit (B08C/B098/B060) to each
visible annunciator via the glyph drawer that consumes them, then trace that one
source bit (in B1F0/B1F6/B1FA) back to the subsystem status check that sets it,
and model that hardware as good. The dispatch is no longer a blocker — rendertrace
walks it. NEW reusable infra: `CPU.RunUntil(cycles, stopPC)` + `cmd/rendertrace`.

### Perturbation result: the source flags are multi-purpose (don't bulk-clear)

Two decisive experiments (`cmd/annunctest`):
1. Zeroing the source flags + packed words *post-boot* (40M-cycle window) leaves
   the on-screen annunciators unchanged (`B08C=B098=B060=0000` but REF UNLOCK /
   ADC-TIME / OVEN COLD still drawn) — the boot drew them into VRAM and the
   operating loop does not redraw-clear that region. screens/annunc_cleared.png.
2. Zeroing them *during* boot (before the render) DISRUPTS the boot: the FAIL
   line + HP-IB ADRS vanish, `EMPTY DLP MEM 7` and a NEW `FREQ UNCAL` annunciator
   appear, and the three annunciators STILL show. screens/annunc_boot_cleared.png.

Conclusion: 0xFFB1E0/B1F0/B1F6/B1F8/B1FA are **multi-purpose system-status words**
(frequency-cal, DLP-memory, annunciator bits all interleaved), so the annunciator
bits cannot be bulk-cleared without collateral damage. The aggregation map in
fcn.11B9A is correct (data flow proven), but each visible annunciator must be
cleared by modeling the specific subsystem hardware it reflects (ref-PLL lock →
REF UNLOCK, ADC-timing → ADC-TIME FAIL, oven 5-min timer → OVEN COLD), NOT by
flag-poking. That is analog-model work (docs/ANALOG_MODEL_PLAN.md). The render
dispatch itself is fully cracked (cmd/rendertrace) and no longer a blocker.

### CRACKED: the annunciator add/remove API (the clean abstraction)

The deeply-layered packed-words/aggregation is a red herring for *control*. The
firmware manages annunciators by a simple numeric-code API:
- **fcn.e7f0(D0=code)** — ADD/draw annunciator `code`
- **fcn.e87e(D0=code)** — REMOVE annunciator `code`
- descriptor table at **[0xFF9562]** maps code → string + screen position
  (entry = code*6+2; position from $ba80/$ba82).

`cmd/annuncode` (uses CPU.RunUntil to stop at each call, reads code=D0 + caller
from the linked frame A6+4) maps every code to its **condition site**. Boot ADDs
codes 0x01–0x07 — the visible annunciators — each at a specific status check:

```
code 0x01 @ 0x4E520     code 0x05 @ 0x118EE (annunciator chain)
code 0x02 @ 0x08888     code 0x06 @ 0x26646 / 0x4E51A
code 0x03 @ 0x09670     code 0x07 @ 0x1CFC2
code 0x04 @ 0x07CF2
```
(REMOVE sites for codes 0x0C..0x2A also mapped.) OVEN COLD = code 0x31/0x32,
gated at ROM 0x875E by `btst #13,$b070` (oven-cold hw flag) AND `fcn.79CC < 300`
(elapsed seconds < 5 min); ≥300 → fcn.e87e(0x31/0x32) removes it. fcn.79CC →
fcn.799E reads the power-on time source.

**Now bounded:** for each visible annunciator, disassemble its condition site to
read off the status word/bit it tests, then model that one hardware status as
good — and the firmware itself removes the annunciator via fcn.e87e (no flag-
poking, no VRAM hacks). This is the per-annunciator analog-status modelling task,
one clean site at a time. Tools: cmd/annuncode (the code→site map).

### Key correction: annunciators are BOOT-TIME decisions (not re-evaluated)

`cmd/annunctest` (RunUntil on 0x875E): the oven-status gate executes **0 times
post-boot** — it runs once during boot and is not re-evaluated in the operating
loop. This explains why post-boot flag-poking never clears the annunciators (no
re-check fires fcn.e87e). Also corrected: `fcn.79CC` is NOT an elapsed-seconds
timer — `fcn.799E` reads `$b078` bits[7:4] as a STATE index and fcn.79CC returns
a state-dependent value (idx1→200, idx0→9000, idxF→120000) compared to 300. So
the 0x875E gate is `btst #13,$b070` (hw flag) AND a state-dependent threshold,
decided at boot.

**Therefore the analog status model is structurally identical to the POST fix:**
make each subsystem's hardware status read "good" DURING boot so the boot-time
check never sets the annunciator (vs trying to clear it after). Per annunciator:
find what sets its boot-time gate flag (e.g. B070 bit13 for the 0x875E gate) →
trace to the hardware read → model it good. The annunciator add/remove API
(fcn.e7f0/e87e + cmd/annuncode) localizes each; the gates run at boot. This is
the concrete, bounded analog-status-model task — one boot-time gate per
annunciator, modelled like the POST f610/f612 latches were.

### VERIFIED annunciator code map + the control model (cmd/anndesc)

Descriptor table [0xFF9562] (base 0xFC58E2 after boot), 6 bytes/code, middle word
= string offset from base 0x2b31e. Verified codes:

| code | annunciator | code | annunciator |
|------|-------------|------|-------------|
| 0x0B | ADC-GND FAIL | 0x23 | FREQ UNCAL |
| 0x0D | **OVEN COLD** | 0x28 | **ADC-TIME FAIL** |
| 0x18 | ADC-2V FAIL  | 0x0C/12.. | UNLVL family |

(My earlier guess "OVEN COLD = 0x31/0x32" was WRONG — 0x31/0x32 are handled by the
unrelated 0x875E gate. Lesson: verify code↔string via the descriptor table first.)

**Control model (confirmed):** every status annunciator is ADDED by default at
power-up; the boot REMOVES the ones whose status is good. Each has a handler of
the form `test status-word → fcn.e87e(code) remove if good / fcn.e7f0(code) add
if bad`. Examples found:
- FREQ UNCAL (0x23) @ 0x11EB2: `movem.l $b084; or.l; bne add; e87e(0x23)` → B084==0 ⇒ removed.
- code 0x22 @ 0x11E90: tests $b082. code 0x20 @ 0x9DB2: `cmpi #2,$b1f2`. code 0x11/0x12 @ 0x10498: `cmpi #$12,$b1fe`.

So `cmd/annuncode` (REMOVE scan) confirms the boot removes 0x0C/0x22/0x23/0x27/
0x2A… (good) but NOT 0x0D/0x28 (OVEN COLD/ADC-TIME still bad). **To clear OVEN
COLD / ADC-TIME: find each one's status-word test (same handler pattern) and make
that status read good** — most likely model the ADC-timing self-test (ADC-TIME)
and the oven/ref hardware so the boot's own handler removes the annunciator.
This is the precise, bounded remaining task. Tool: cmd/anndesc (verified code map).

### Architecture clarified: TWO annunciator systems (shown ones use the hard path)

Static scan (cmd/anncodemap, finds every `moveq #code; jsr e7f0/e87e/e7a2`) +
dynamic scan (cmd/annuncode) agree: the code-API (fcn.e7f0 add / fcn.e87e remove,
per-code handlers `test status-word → remove/add`) handles codes 0x06/0x0E/0x0F/
0x10/0x11/0x12/0x1D/0x20/0x22/0x23/0x27/0x2A/0x31/0x32 — i.e. FREQ UNCAL, UNLVL,
RES BW, etc. (the NOT-shown / cleared annunciators).

**The VISIBLE status annunciators — ADC-GND(0x0B), OVEN COLD(0x0D), ADC-2V(0x18),
ADC-TIME(0x28) — have NO code-API handler** (no moveq+jsr site; fcn.e7f0 never
called with them). They are driven by the fcn.11B9A aggregation → packed words
B060/B068/B08C/B098 path instead. cmd/annbits shows those packed words DON'T use
bit==code encoding (set bits 0x12/0x14/0x2C… don't match the visible codes), so
the aggregation drawer applies its own bit→code mapping not yet decoded.

**Net:** the easy-to-clear annunciators (code-API, status-word gated) are already
NOT shown; the ones that ARE shown sit in the aggregation path whose bit→
annunciator→source mapping resisted the quick hypotheses. Clearing them needs
either (a) interactive single-step of the aggregation DRAWER to read its bit→code
table, or (b) recognising they converge with the full analog model — ADC-GND/2V/
TIME are the ADC self-test (model the ADC like POST f610/f612), OVEN COLD the
oven timer/state, REF UNLOCK the A9 ref-lock. The hard RE (architecture, code
map, both systems) is done; what remains is genuinely analog-subsystem modelling.
Verified tools: cmd/anndesc, cmd/anncodemap, cmd/annbits.

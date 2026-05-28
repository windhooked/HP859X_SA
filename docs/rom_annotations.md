# Rev L ROM annotations — PC → meaning

This file is the **canonical map of identified Rev L PCs**. Built incrementally
from emulator probes (`cmd/displayprobe`, `cmd/caltrace`, `cmd/keystate`,
`cmd/abusprobe`, …) plus targeted disassembly via `cmd/disasm` and the full
listing at [docs/rom.asm](rom.asm).

`rom.asm` is a regenerated artefact — do not edit it directly. Add discoveries
here; if a flag would help rizin's listing too, drop a `f <name> @ <addr>` line
into [scripts/rom_analyze.rz](../scripts/rom_analyze.rz) and regenerate.

Format: `PC` is a 6-hex ROM offset, `func` is one-line semantics, `notes` adds
detail (parameters, callers, observed behaviour, gates).

---

## Exception / IRQ vector table — Rev L (longwords at ROM `0x60..0x7F`)

| Vector | Target  | Func                                                  |
|--------|---------|-------------------------------------------------------|
| Reset SP | `0x00FF948A` | Initial stack pointer |
| Reset PC | `0x00001B34` | Reset entry — `movea.l 0.w, A7; bra 0x3998` |
| IRQ1   | `0x002AB8` | Sweep update — writes f200/f300/f400 |
| IRQ2   | `0x003A94` | Noop (rte only) |
| IRQ3   | `0x002B1E` | Front-panel key event — `bset #0, $bc67.w` (key-available flag) |
| IRQ4   | `0x002642` | HP-IB (TMS9914A) — dispatches to fcn.1D58 path |
| IRQ5   | `0x003ECE` | Timer tick — `addq.l #1, $bf12.w`; on `bf16` overflow sets `befb.7` |
| IRQ6   | `0x004088` | Sweep sample capture — `move.w $f200.w, D7`, vectored dispatch via `bf34` |
| IRQ7   | `0x003A9E` | NMI |

---

## Key-event dispatch chain (gate C; see [[rev-l-key-consumer-chain]])

| PC      | Func                                                              |
|---------|-------------------------------------------------------------------|
| `0x002B1E` | **IRQ3 handler** — sets bit 0 of `$bc67.w` (key-available flag), clears `$ef401b.l` (FrontPanel ack) |
| `0x002B26` | The actual `bset #0, $bc67.w` instruction |
| `0x002642` | **IRQ4 handler entry** — reads `$f160.w` into `$bf05.w`, dispatches based on bf05 bits |
| `0x0026A8` | `bsr $1d58` — gated on `bf05.2` (one of several IRQ4 entries into the dispatcher) |
| `0x00277C` | `bset #7, $befd.w` then `bsr $1d58` — the path that sets befd.7 (and thus unlocks the fcn.1B40 reach via fcn.1D58's 0x1F40); reached only when 9b20 select hits this branch of the IRQ4 dispatch table at 0x2834 |
| `0x001D58` | **fcn.1D58** — the dispatch router; ORs `$f120.w` into `$befd.w`, branches based on bit tests. Contains FOUR `bsr $1B40` sites at different conditional points. |
| `0x001B40` | **fcn.1B40** — stack-rts dispatcher. bf03!=0 ⇒ cleanup+return; bf03==0, bf0a!=0 ⇒ jump to (bf0a); bf03==0, bf0a==0 ⇒ `jmp $148` (key consumer) |
| `0x001DAE` | fcn.1B40 call #1 — gated on `bf03 != 0` |
| `0x001E60` | fcn.1B40 call #2 — **always pre-sets `bf0a = 0x3AD0` first**; dispatches to sweep main loop |
| `0x001ED0` | fcn.1B40 call #3 — gated on `befe.6 set` |
| `0x001F40` | fcn.1B40 call #4 — gated on `bf03 != 0 AND bef7.0 set` |
| `0x000148` | Dispatch table entry — `jmp $18568.l` (key consumer entry) |
| `0x018568` | **Key consumer function entry** |
| `0x018F42` | `bclr #0, $bc67.w` — the actual key-flag clear (consumer's first action) |
| `0x0192C8` | **Operating handler** the firmware keeps in `bf0a`; its body ends at `0x1934C  bra $18568` so the key consumer IS reached via this chain when fcn.1B40 dispatches with bf0a still pointing here |

---

## Cal NVRAM checksum (Rev L startup)

| PC      | Func                                                              |
|---------|-------------------------------------------------------------------|
| `0x00454A` | Startup checksum sweep — 8×-unrolled byte adds across all 65536 cal bytes, dual accumulators D2/D3 (even/odd positions). Pass: each ≡ 1 mod 256. Synthesised via `CalNVRAM.SynthesizeRevL()`. |
| `0x0044AA`–`0x0044B8` | CPU integrity test on cal NVRAM offset 0 — `move.l ($200000).l, D6; move.l D6, ($200000).l; cmp.l ($200000).l, D6` |
| `0x0049FE` | `ori.b 0x30, 0xFFF610` — forcibly sets bits 4,5 of f610 (cal-valid bits) regardless of checksum |

---

## Analog-bus accesses (`0xFFF75C` select / `0xFFF75E` data)

Observed from `cmd/abusprobe` (100M post-boot cycles). All PCs in the analog
subsystem code region 0x5E000–0x5F500.

### Writes to select port `0xFFF75C`

| PC      | Select | Count | Func                                                |
|---------|--------|-------|-----------------------------------------------------|
| `0x05E600` | `0x9A` | 247,123 | Operating-loop poll — write `select=0x9A` before reading status |
| `0x05E702` | `0x9A` | 492     | Init/cal stage — write `select=0x9A` |
| `0x05E3B0` | `0x95` | 2       | Send DAC byte 1 (high byte) — `fcn.5E384` |
| `0x05E3C0` | `0x96` | 2       | Send DAC byte 2 (mid byte) — `fcn.5E384` |
| `0x05E3D0` | `0x97` | 2       | Send DAC byte 3 (low byte) — `fcn.5E384` |
| `0x05E732` | `0x90` | 2       | One-shot init |
| `0x05E744` | `0x91` | 2       | One-shot init |
| `0x05E756` | `0x93` | 2       | One-shot init |
| `0x05E340` | `0x20` | 1       | One-shot init |
| `0x05E896` | `0x9A` | 1       | One-shot |
| `0x05E8CC` | `0x9A` | 1       | One-shot |

### Reads from data port `0xFFF75E` (paired with prior select)

| PC      | After select | Count   | Observed values                  |
|---------|--------------|---------|----------------------------------|
| `0x05E604` | `0x9A` (status) | 247,123 | 246,158 × `0x0000`  +  965 × `0x0006` |
| `0x05E706` | `0x9A` (status) | 492     | 490 × `0x0000`  +  2 × `0x0006` |
| `0x05E8A0` | `0x9A` | 1       | `0x0000` |
| `0x05E8D6` | `0x9A` | 1       | `0x0000` |
| `0x05EF96` | `0x9F` (ADC result) | n/a | Range-checked against `[-0x200, 0x1FF]` |
| `0x05EEEA` | `0x9F` | n/a | 3× back-to-back reads — ADC settling pattern |

### Helper functions in the analog-bus subsystem

| PC      | Func                                                              |
|---------|-------------------------------------------------------------------|
| `0x05E5DE` | **`wait_for_adc_match(mask, target)`** — args D0=mask, stack(+8)=target. Loop body at 0x5E5FA-0x5E62E: write select=0x9A, read data, test `(mask & low_byte) == target`. 1000-tick IRQ5 timer. Returns D0 bit 0 set on match, clear on timeout. |
| `0x05E384` | **`send_dac_word(D0)`** — writes 24-bit value as 3 bytes via selects 0x95/0x96/0x97. Stores working copy in `$9492`-`$9494`. |
| `0x05E63C` | **Cal-sweep stage A** — gated on `$94E4 == 0xD2D2`. Calls `wait_for_adc_match(0x12, 0x02)`, sends 3 setup bytes, waits again, sends `0xAF`, then loops 120 times sending `table[i]` from `$948E[]`. **NOT executed in our operating loop** (sentinel never set). |
| `0x05ECDC` | **Cal-init function entry** — only reached via `jmp $5ecdc.l` at the dispatch-table slot 0x00000C4C, AND via internal tail-recursion at 0x5F062. The C4C slot has **no external callers** in ROM — cal-init is intentionally only triggered by a specific user/SCPI command (the front-panel CAL key or `:CAL:` GPIB command), not by automatic boot. The analog-bus model in `pkg/emu/device/analogbus.go` is ready to handle this path if it ever fires (mux+ADC+DAC correlation in place) but the path is dormant in normal operation. |
| `0x05EFAE` | **Cal-sweep main loop** (body of fcn.5ECDC) — writes DAC, waits, reads select=0x9F 3× (ADC settling), range-checks against [-0x200, 0x1FF], loops 120 iterations, eventually sets `$94E4 = 0xD2D2` at 0x5F046 to arm cal-sweep stage A. |

### Comparison sites against analog-bus reads (worth modelling against)

| PC      | Test                                                              |
|---------|-------------------------------------------------------------------|
| `0x05E60E` | `cmp.b (9,A6), D6` — `D6 = mask & low_byte(read)`; compares to stack target. Called from many sites with various mask/target pairs (e.g. mask=0x12 target=0x02, mask=0x02 target=0x02). |
| `0x05E708` | `cmpi.b #6, $9493.w` — exact equality, low byte == 0x06 |
| `0x05E71E` | Same — second site checking ==0x06 |
| `0x05EF9C` | `cmpi.w #0x1FF, (-$16,A6)` — bgt → fail |
| `0x05EFA6` | `cmpi.w #-0x200, (-$16,A6)` — blt → fail |
| `0x05E822` | `cmpi.w #-0x2D2E, $94e4.w` — gate for cal-sweep stage A |
| `0x05E3F6`, `0x05E53C`, `0x05F050` | Other `cmpi.w #-0x2D2E, $94e4.w` sentinel checks |

### Sentinel writes to `$94E4` (cal-state flag)

| PC      | Value  | Func                                                  |
|---------|--------|-------------------------------------------------------|
| `0x05F046` | `0xD2D2` | Sets the cal-init sentinel; **only site** that arms it |
| `0x05E438`, `0x05E46E`, `0x05E4B2`, `0x05E4EE`, `0x05E52C`, `0x05E5AE`, `0x05E5B6`, `0x05E5CC`, `0x05E5D4`, `0x05F002` | `0x0000` | Clears the sentinel |

---

## Other key RAM addresses (Rev L)

| Address    | Func                                                          |
|------------|---------------------------------------------------------------|
| `0xFFBC67` | Key-available flag (bit 0 set by IRQ3, cleared by consumer at 0x18F42) |
| `0xFFBF03` | Event-pending flag (bit 0 = key, others = mode events). `0x81` set at PC 0x731E. Cleared at 0x1BA4. |
| `0xFFBF0A` | Pending-function pointer used by fcn.1B40 stack-rts dispatch. Default in operating loop = `0x000192C8` (the sweep handler). Cleared at 0x1BA8. |
| `0xFFBF12` | IRQ5 timer counter — incremented every IRQ5 tick |
| `0xFFBF16` | Countdown timer — incremented every IRQ5; on overflow sets `befb.7` |
| `0xFFBEFA` | IRQ6 sample-capture working byte (writes at PC 0x40C8 byte=0x24) |
| `0xFFBEFB` | IRQ5/IRQ6 status byte (bit 7 set on `bf16` overflow at PC 0x3EE8) |
| `0xFFBEFD` | Dispatcher state byte — fcn.1D58 ORs `$f120` into it then branches on bits |
| `0xFFBEFE` | Dispatcher mode byte — multiple `bclr` tests in fcn.1D58 body |
| `0xFFBF05` | HP-IB status byte — IRQ4 handler reads `$f160.w` into here, dispatches on bits |
| `0xFF9B20` | Operating-mode byte (0=idle, 3=?, 0xF=?). Source for the IRQ4 dispatch-table index at 0x2778. |
| `0xFF94E4` | Cal-init sentinel (set to `0xD2D2` once, gates cal-sweep stage A) |
| `0xFFBF30` | Sweep trace-buffer end pointer (set to `0x2FD82A` when sweep is armed) |
| `0xFFBF34` | IRQ6 sample-capture vector (`0x40B8` during sweep, `0x40C2` idle) |
| `0xFFBC26` | SCI MOVE X cursor (auto-advances +8 per glyph) |
| `0xFFBC28` | SCI MOVE Y cursor |

---

## TMS9914A IEEE-488 / HP-IB controller (MMIO `0xFFF600`–`0xFFF60F`)

Texas Instruments TMS9914A talker/listener/controller. The firmware
initialises it during boot at PC 0x32A6+:

    move.b #$FF, $f606.w   ; ADR   = 0xFF (address register)
    move.b #$FF, $f604.w   ; AUXCR = 0xFF (auxiliary command)
    move.b #$FA, $f608.w   ; SPMR  = 0xFA (serial-poll mode response)

Then leaves it alone unless HP-IB activity occurs (the operating loop
doesn't touch it). Modelled in [pkg/emu/device/tms9914a.go](../pkg/emu/device/tms9914a.go).

### Register layout (32-byte window, 2-byte stride)

Same address selects DIFFERENT register for read vs write:

| Offset | Read     | Write    | Purpose |
|--------|----------|----------|---------|
| `0x0`  | IS0      | IMR0     | Interrupt Status 0 / Mask 0 |
| `0x2`  | IS1      | IMR1     | Interrupt Status 1 / Mask 1 |
| `0x4`  | ADSR     | AUXCR    | Address Status / Auxiliary Command |
| `0x6`  | BSR      | ADR      | Bus Status / Address |
| `0x8`  | —        | SPMR     | Serial Poll Mode Response |
| `0xA`  | CPTR     | PPR      | Command Pass-Through / Parallel Poll |
| `0xC`  | —        | —        | (unused) |
| `0xE`  | DIR      | CDOR     | Data In / Data Out |

### Interrupt behavior

The chip drives an IRQ line into the M68K's autovector IRQ4 whenever
`(IS0 & IMR0) != 0 OR (IS1 & IMR1) != 0`. Both IS0 and IS1 must be
explicitly set externally (via `TMS9914A.SetIS0` / `SetIS1`) — the
minimal model doesn't simulate the chip's own state-transition logic
that would normally set these bits in response to bus activity.

### IS0 bits (read-side)

| Bit | Name | Meaning |
|-----|------|---------|
| 7   | INT1 | An interrupt is pending in IS1 (cascade indicator) |
| 6   | SRQ  | Service Request — another device on the bus requested attention |
| 5   | MAC  | My Address Change |
| 4   | RLC  | Remote/Local Change |
| 3   | SPAS | Serial Poll Active State |
| 2   | END  | END message received |
| 1   | BO   | Byte Out — controller can send next data byte |
| 0   | BI   | Byte In — received byte ready in DIR |

### IS1 bits (read-side)

| Bit | Name | Meaning |
|-----|------|---------|
| 7   | GET  | Group Execute Trigger received |
| 6   | ERR  | Handshake error |
| 5   | UNC  | Unrecognised command |
| 4   | APT  | Address Pass-Through |
| 3   | DCAS | Device Clear Active State |
| 2   | MA   | My Address received |
| 1   | IFC  | Interface Clear |

### Auxiliary commands (AUXCR writes)

The minimal model handles only one of the chip's ~24 auxiliary
commands:

| Command (bits 0..4) | Name  | Effect with SET bit (7) |
|---------------------|-------|------------------------|
| `0x00`              | swrst | Software Reset — clears IS0 and IS1 |

Other commands (tcs, tca, gts, rtl, etc.) are stored in the AUXCR
register but have no modelled side effect.

### Tests

- `pkg/emu/device/tms9914a_test.go`: 5 unit tests covering read/write
  asymmetry, odd-offset access, swrst, IRQ assertion via IS0 mask,
  IRQ assertion via IS1 mask.
- `pkg/emu/machine/hpib_test.go`: chip-attached integration tests:
  - `TestHPIBChipPresent` — chip survives boot init; firmware leaves
    IMR0/IMR1 at 0/0 so no interrupt is asserted at idle.
  - `TestHPIBReadWriteRoutedThroughChip` — MMIO bus correctly routes
    reads/writes to the chip's read/write registers.
  - `TestHPIBNaturalDispatchReachesFcn1D58` — with key FIFO pushed
    and IS0.BI set + IRQ4 injected, the natural dispatch chain reaches
    `fcn.1D58` (LAYER 1 of the path-A obstruction fix; LAYER 2 still
    in place per the operating-tick early-exit branches).

### HP-IB command receive + dispatch chain

The HP 8593A's HP-IB command flow (Rev L, decoded empirically):

1. **External**: bytes arrive via either the TMS9914A chip (real bus) or
   the front-panel μC's HP-IB-bridge port at MMIO `0xFFF160`/`0xFFF140`.
   Our model: `Machine.SendHPIB(bytes, maxCycles)` pushes bytes via the
   chip's `Push(bytes)` API, which makes the f140 read return queued
   bytes one at a time and f160 read return status `0x03` (bits 0+1
   set) while bytes are pending.

2. **IRQ4 handler** at PC `0x002642`:
   - Tests `b05f.0` — must be set to take the f160 path (`SendHPIB`
     pre-arms this).
   - `move.b $f160.w, $bf05.w` — read HP-IB status into bf05.
   - `btst #0, $bf05.w` AND `btst #1, $bf05.w` — verify "data ready".
   - `move.b #$c, $f130.w` — strobe the front-panel μC.
   - `move.b $f140.w, D0` — read the data byte.
   - `lea $bc12.w, A0; jsr $42f8.w` — push the byte into the parser
     FIFO at `0xFFBC12` (data buffer at `0xFFBDB0`).

3. **Parser FIFO** at `0xFFBC12`:
   - size = 0x52 (82 bytes) at offset `0xE` = `$bc20`
   - data buffer pointer at offset `0x10` = `$bc22` = `0xFFBDB0`
   - read index at offset `0x14` = `$bc26`
   - write index at offset `0x16` = `$bc28`

4. **Parser invocation** at operating-tick PC `0x18F3E`:
   `jsr $69a.w` → slot `0x69A` → `fcn.58C2E` — the HP-IB command parser.

5. **Parser** `fcn.58C2E`:
   - Checks the FIFO via `lea $bc12.w, A0; jsr $4340.w` (count).
   - Pops bytes via `lea $bc12.w, A0; jsr $427c.w` at PC `0x58C6A`.
   - Calls a per-byte state machine `bsr $57278` to recognise
     mnemonics + collect arguments + dispatch handlers.

### `Machine.SendHPIB` API (LAYER 1 fully wired)

```go
// SendHPIB queues `bytes` for the firmware to receive over HP-IB,
// then drives the natural receive path until the chip's input
// buffer is drained. After the call, `bytes` are in the parser FIFO
// at $bc12; call DriveOperatingTick to make the operating tick's
// slot 0x69A consume them via fcn.58C2E.
func (m *Machine) SendHPIB(bytes []byte, maxCycles int) int
```

Empirically verified end-to-end (`TestSendHPIBPlusDriveOperatingTickDrainsParserFIFO`):

  - `SendHPIB("ABCDE", 5M)` returns `pending=0` (chip drained).
  - bc12 FIFO write index `$bc28` advances `0x0000 → 0x0005`.
  - Bytes at `0xFFBDB0..0xFFBDB4` = `'A','B','C','D','E'`.
  - `DriveOperatingTick(20M)` runs the parser via slot 0x69A.
  - bc12 FIFO read index `$bc26` advances `0x0000 → 0x0005` —
    **all 5 bytes consumed by fcn.58C2E**.

The parser ran end-to-end. Per-command HANDLER PCs (e.g. for CF
center-frequency, SP span) and their RAM-side effects are not yet
mapped — that's future work to verify a specific command's
observable side effect (e.g. center-frequency value written to a
RAM cell).

### Architectural status (post-TMS9914A)

The TMS9914A model **unblocks LAYER 1** of the natural-dispatch
obstruction documented earlier: the chip is now present, the firmware
can read non-default status, and external code can trigger IRQ4 in
ways the firmware recognises.

**LAYER 2 remains**: even after the natural dispatcher reaches the
operating tick, the tick's body has state-flag gates (b1e0, b1e4,
b07a, b07c, b0ce) that route execution AWAY from the bclrs under
steady-state RAM. `Machine.DriveOperatingTick` remains the canonical
primitive for forcing the operating tick to execute its deep path.

#### LAYER 2 empirical measurements (`cmd/layer2`)

Probe: wrap RAM with a write-tracer, boot, then run 100 chunks
(5M cycles) of operating loop with IRQ5/IRQ6/IRQ4 injection. Report
which firmware PCs touch each LAYER 2 gating address and how often.

**Naturally inert during operating loop (zero writes in 5M cycles):**

| Address | Symbol | Post-boot value | Implication |
|---------|--------|----------------|-------------|
| `0xFFB010` | sweep status snap | `0x0000` | Only written by `move.w $f300, $b010` inside the operating tick body, which doesn't run. |
| `0xFFB072` | upstream gate | `0xC0A0` | Bits 2+5 (`0x24` mask for slot 0x5C8) stay at init. |
| `0xFFB0AB` | fcn.A250 enable | `0x0000` | Never set → fcn.A250 always early-exits → `b0ce.11` never gets set naturally. |
| `0xFFB0CE` | state (bit 11 deep) | `0x8000` | Bit 11 stays clear because fcn.A250 never runs. |
| `0xFFB1F9` | service-request bits | `0x5600` | Bit 3 stays clear (needed by slot 0x5C8 fcn.40720). |

**Written during operating loop (the firmware IS doing work):**

| Address | Touch sites | Notes |
|---------|------------|-------|
| `0xFFB1E0` | PC `0x09A4E` (val 0x18), PC `0x1D318` (val 0x1820) | Bit 9 (0x0200) NEVER set; firmware writes 0x1820 = bits 5+11+12. |
| `0xFFB1F8` | PC `0x0BFA6`, `0x1D2E8`, `0x1E992`, `0x179F8` | Values 0x56 and 0x1D6 — bits 11+12 set on some paths (0x1D6 has bit 8+7+6+4+2+1+0). |
| `0xFFBEFA` | PC `0x40C8` (IRQ6 handler) ×25, PC `0x9AAE`, PC `0x17346` | IRQ6 keeps writing 0x24 (bit 2+5). Bit 13 (sweep-done) gets set by IRQ6 path 0x4088 then cleared by the sweep-done processor. |
| `0xFFBEFD` | PC `0x1D60`, `0x1D74`, `0x1F16` ×12 each | All in fcn.1D58 body — dispatcher IS running 12× per 100 chunks. |

#### Empirical conclusion

The dispatcher (`fcn.1D58`) runs naturally — it's invoked 12 times per
100 chunks via IRQ4. But the operating tick body (where `b0ab`, `b072`
bits 2+5, `b0ce.11`, `b1f9.3` would be set) never executes. So those
gating bits stay at their boot values forever.

The cascade is concrete: each gate bit's production depends on the
operating tick running its work loops, which requires earlier gates to
be set, which requires the tick to run... a chicken-and-egg
dependency that only resolves if some external stimulus (a key press
modelled end-to-end, an HP-IB command processed naturally, a sweep
completing through the firmware's expected mode handlers) causes one
of the gating bits to flip via a path the firmware DOES execute.

**This is the empirical evidence that LAYER 2 is multi-week**:
modelling enough of the firmware's state machine that ANY one path
produces a gating-bit set without the workaround. The TMS9914A model
landed in `ad73a99` is the first step — the next steps are modelling
the front-panel μC's command/response semantics and the sweep cycle's
state-transition expectations.

#### LAYER 2 investigation summary

Each state-flag gate has its own production site, and the chain is
deep. Findings from `cmd/tickflags` LAYER 2 experiment:

| Gate bit         | Production site PC | Gated on            |
|------------------|-------------------|---------------------|
| `$b0ce.11`       | `0x0A2F2` (bset)  | Inside `fcn.A250`. `fcn.A250` runs only when `$b0ab != 0`. `$b0ab = 0x12` is written at PC `0x1B310` IF dispatch slot `0x5C8` returns D0 bit 0 set. |
| `$b1f8.11`       | `0x3C960` (bset)  | Inside some display/state function. Reached only when D0 > 0 from PC `0x3C952`'s prior computation. |
| `$b1f8.12`       | `0x12290`, `0x12E0E`, `0x15154`, `0x165E8`, `0x31FE4`, `0x34B5A`, `0x34E68`, `0x34EDA`, `0x352C4`, `0x357DA` — 10 sites | Multiple display / sweep / mode-change functions. |
| `$b1e0.9`        | (no direct bset/bclr found — set via word writes to `$b1e0`) | Likely set when a key is queued for processing. |
| `$9afb.2`        | `0x56CC8` (bset), `0x568FC` (bclr) | In some operating-mode handler. |
| `$befa.10` clear | IRQ5 handler-related | Periodically set; needs to be clear when the operating tick checks it. |

The production sites depend on *each other* — many are reached only
when the operating tick has already done some prior work. So
"naturally setting LAYER 2 bits" reduces to "letting the operating
tick run far enough that it sets its own bits via its work loop" —
which is what the natural dispatch is trying to do.

**Workaround that works** (committed in `cmd/tickflags`): force PC to
`0x18ADC` (deep block) WITH the LAYER 2 probe `$b0ab := 0x12` plus
the original DriveOperatingTick pre-arms. The key bclr at `0x18F42`
fires + the sweep-done bclr at `0x17346` fires. Verified end-to-end
in `TestDriveOperatingTickClearsKeyAndSweepFlags`.

**Path to a clean LAYER 2 fix** (multi-week scope; not done):

1. Drive a tick from natural IRQ4 (TMS9914A + key FIFO non-empty —
   already works post-`ad73a99`).
2. Let the operating tick body's first iteration set the gating bits
   via its work loops.
3. The SECOND tick (and subsequent ones) would then take the deep
   path naturally because the gates are now set.
4. The complication: each iteration's state is rolled back partially
   by other handlers (IRQ5 timers, sweep handlers, etc) so subsequent
   iterations may need DIFFERENT state arming.

This is a deep model-of-the-firmware-state-machine task. The TMS9914A
model is the prerequisite that's now in place; the full LAYER 2 fix
is a separate multi-week investigation.

---

## A16 analog-bus select map (decoded via `cmd/abusprobe`, 100M-cycle survey)

Each select value written to `0xFFF75C` addresses a different sub-function
on the A16 analog-control hybrid. The data port `0xFFF75E` is the
bidirectional bus; reads return the addressed quantity and writes set it.

| Select | Direction | Function (decoded) | Evidence |
|--------|-----------|--------------------|----------|
| `0x20` | W (one-shot init) | Unknown — likely a reset / mux-init pulse | written once at PC 0x5E340 during boot AND once again at OP-loop init |
| `0x90` | W | Control register A — observed value `0x0000` | written 2× at PC 0x5E73E, sel preceded by 0x5E732 |
| `0x91` | W | Control register B — observed value `0x0012` | written 2× at PC 0x5E750 |
| `0x93` | W | Control register C — observed value `0x000F` | written 2× at PC 0x5E762 |
| `0x95` | W | DAC byte 1 (high byte of 24-bit DAC word) — observed `0x0000` | written 2× at PC 0x5E3BA inside `fcn.5E384` (send_dac_word) |
| `0x96` | W | DAC byte 2 (mid byte) — observed `0x0000` | written 2× at PC 0x5E3CA inside `fcn.5E384` |
| `0x97` | W | DAC byte 3 (low byte) — observed `0xFF93` boot, `0xFF8D` OP | written 2× at PC 0x5E3DA inside `fcn.5E384` |
| `0x9A` | R | **ADC-ready status register** — bit-mapped flags | read 247,615× at PC 0x5E604 (main poll). Tested against masks `0x12 & x == 0x02` (operating loop) and `x == 0x06` (init stage at PC 0x5E708). Returning `0x06` periodically satisfies both. |
| `0x9F` | R | **ADC result register** (12-bit signed, range `[-0x200, +0x1FF]`) | NOT read in our operating loop (cal-sweep code never runs). Used at PC 0x5EF96 (range-check) and PC 0x5EEEA (3× settling read pattern) inside cal-init `fcn.5EFAE`. |
| `0x9D` | R/W? | Unknown — listed in CLAUDE.md observed selects but not seen in 100M survey | n/a |

Cross-reference to CLIP 5963-2591 chip identification: U47 = 12-bit ADC,
U64 + U201 = 8-channel mux, DAC writes program YIG/LO tune. Select 0x9F's
range `[-0x200, +0x1FF]` matches a 12-bit signed ADC (4096 codes, ±2048
≈ ±0x800, but firmware sanity-checks a tighter ±0x200 = ±512 range).

The mux channel-select probably lives in one of selects 0x90/0x91/0x93 (the
"control register" writes). Specifically:
- `select=0x91, data=0x0012` — bit pattern `0001 0010` could be channel
  number (bits 0–2 = channel ID 2) + enable bit (bit 4)
- `select=0x93, data=0x000F` — bit pattern `0000 1111` could be ADC mode
  bits (all four conversion-control bits set: differential, bipolar, etc.)
- `select=0x90, data=0x0000` — control reset / clear

The 24-bit DAC word is composed of three byte writes:
- `select=0x95` → bits [23:16]
- `select=0x96` → bits [15:8]
- `select=0x97` → bits [7:0]

with the firmware setting initial value `(0,0,0x93)` at boot (= signed `0x000093` ≈ +147) and `(0,0,0x8D)` mid-run (= +141). These are small DAC adjustments — the YIG-tune or LO-trim DAC being nudged for thermal correction.

---

## Sweep / trace render pathway

The IRQ6 sample-capture handler at ROM `0x40C2` (idle mode) detects
end-of-sweep and sets bit 13 of RAM `0xFFBEFA`. The firmware's main
loop is supposed to detect this flag and render the captured samples
as a trace polyline.

### Sweep-done processor

| PC      | Func                                                  |
|---------|-------------------------------------------------------|
| `0x017346` | `fcn.17346` — sweep-done processor. First instruction is `bclr #13, $befa.w` (acknowledges the sweep-done flag). Calls slot `0x43C` (fcn.9A52 = first-stage sweep-done processor) then chains through slots `0x640`, `0x5A4`, `0x15A` for further processing. |
| `0x008A4`  | Dispatch table slot pointing at `fcn.17346` — `jmp $17346.l`. **Has no direct callers**: nothing in ROM does `jsr $8a4.w` or `bsr fcn.17346` via the slot. fcn.17346 is reached only via PC-relative `jsr fcn.00017346(pc)` from the 0x18000-0x18200 range of the **operating tick** body (10+ call sites). |
| `0x019088` / `0x019098` | Operating-tick call sites in `fcn.18568`'s body that lead (transitively, via slots `0x472` / `0x50E`) to the sweep-done processor. |

### The shared architectural gate

The sweep-done processor at `fcn.17346` is only reached when the
operating tick at `fcn.18568` runs FAR ENOUGH to execute PC `0x19088`+
(deep inside the function body, well past the early state-test exits).
Like the key consumer, this is blocked by:

1. The natural dispatch chain doesn't reach `fcn.18568` (path A at
   PC `0x1E60` perpetually redirects via `bf0a = 0x3AD0`).
2. Even when forced (`ForceOperatingTick`), the function exits early
   via one of its many state-flag branches (`b1e0 & 6`, `b1e4 == 0x34`,
   `b07a.b`, `b07c.d`, `b0ce.b`, etc.) before reaching `0x19088`.

**Verified by `cmd/sweeprender`** (Rev L, 80M-cycle pre-tick boot +
10M-cycle forced operating tick with IRQ5 ticks):
  - `befa.13 = 1` confirms the sweep-done flag IS set (IRQ6 fired
    1109 times, samples captured into the trace buffer at
    `0x2FD508`-`0x2FD82A`).
  - After `ForceOperatingTick`: `befa.13` STAYS set, no `lines` /
    `paints` delta — the operating tick exits before reaching either
    the sweep-done bclr at `0x17346` or any trace-drawing code.

So both gate C (key consumer) and the trace render are blocked by the
SAME architectural issue: the operating tick can't be reached or made
to run to completion. Fixing either deliverable requires either:

  - A sophisticated "tick driver" that pre-arms all the operating-tick
    state-flag conditions to take the deep paths.
  - Finding and patching the path-A obstruction at PC `0x1E60` so
    `fcn.1B40` dispatches to `0x148` (the operating tick) rather than
    `0x3AD0` (the sweep handler that never returns).
  - Building the GPIB/TMS9914A command pipe so external commands
    can drive the firmware through paths that bypass this gate.

### Where the trace actually gets drawn (when it does)

Per [docs/research.md](research.md) section 7 + the rev-l-firmware-switch
memory: the trace is NOT drawn via SCI vector commands (`0x8801` ALINE
opcode). The A/B test in `cmd/sweeprun` confirmed 7684 IRQ6 events
produced +378 extra glyphs (annunciator updates) but **zero additional
lines, rects, or dots**. The trace must go through direct HD63484
video-RAM writes via PAINT / WPTN-with-large-count / WRITE-AREA
commands — exactly the kind of raster-write the screen-background
fill at MAR=`0x4000/0x0000` uses, just targeting a different MAR in
the trace area.

### Operating-tick state machine (gate-common to gate C and trace render)

`fcn.18568` is **a continuous loop, not a one-shot function**: PC 0x18A88
is `bra $18568` — a tail-call back to entry. Each iteration tests state
flags, takes one of many branches, and either reaches a deep target
(key-flag bclr at 0x18F42, sweep-done bclr at 0x17346 via slot dispatch)
or loops back to the entry. The dispatcher's stack-rts trick into this
function is what would *start* the loop from interrupt context.

**Early-branch tests in the entry block (PC 0x18568 — 0x185D0):**

| PC      | Test                                | Default value after boot     | Pre-arm to take deep path |
|---------|-------------------------------------|------------------------------|---------------------------|
| 0x18572 | `btst #11, $b010.w` ⇒ exit 0x18ADC  | `b010 = 0x0000` ✓            | (no change needed)        |
| 0x18588 | `(b1e0 & 6) != 0` ⇒ 0x191E0         | `b1e0 = 0x1900` ✓            | `b1e0 := 0x0200`          |
| 0x18592 | `b1e4 == 0x34` ⇒ 0x185AC            | `b1e4 = 0x0000` ✓            | (no change)               |
| 0x1859A | `bc64 bit 13`                       | `bc64 = 0x8006` (bit 13 = 0) | (no change)               |
| 0x185AC | `b07a bit 11` ⇒ exit 0x18ABC        | `b07a = 0x0000` ✓            | (no change)               |
| 0x185B6 | `b07c bit 13` ⇒ exit 0x18ABC        | `b07c = 0x0011` (bit 13 = 0) | (no change)               |
| 0x185C4 | `b0ce bit 11` clear ⇒ 0x18642       | `b0ce = 0x8000` (bit 11 = 0) | `b0ce \|= 0x0800`          |

**Deep-path gates discovered (PC 0x188B6 — 0x18A88 main loop body):**

| PC      | Test                                | After pre-arm     | Needed     |
|---------|-------------------------------------|-------------------|------------|
| 0x188FC | `(b1f8 & 0x1800) == 0x1800`         | `b1f8 = 0x0056`   | `b1f8 \|= 0x1800` |
| 0x18AFC | `b1e0 bit 9` clear ⇒ 0x18FD6 (past key bclr) | `b1e0 = 0x0200` ✓ | (bit 9 set) |
| 0x18B00 | `befa bit 10` set ⇒ 0x18FD6         | befa keeps getting bit 10 set | `befa &= ~0x0400` |

**Empirical findings (`cmd/tickflags`, Rev L, 30M boot + 20M instrumented steps):**

  - WITHOUT pre-arm: 0 instructions executed inside `fcn.18568` body
    (function exits via early branches before doing any work).
  - WITH pre-arm above: **2,062,783 instructions executed inside the
    body**, deepest PC reached `0x18A88` (the loop-back instruction).
    Function ran its full main linear path.
  - PC 0x18F42 (key bclr): NOT REACHED — gated on additional state we
    haven't yet enumerated, somewhere between 0x18B14 and 0x18F30.
  - PC 0x17346 (sweep-done): NOT REACHED — same reason; this is reached
    via slot dispatches from inside the loop body.

**Next step**: enumerate the remaining conditional branches between
PC 0x18B14 and PC 0x18F42 to find the additional state gates. Use
`cmd/disasm` + `cmd/dispatch` to follow branches; pre-arm matching
RAM bits in `cmd/tickflags` and re-measure deepest PC reached.

---

### Trace buffer addresses

| Address    | Func                                                      |
|------------|-----------------------------------------------------------|
| `0x2FD508` | Trace buffer start (A5 initialises here when sweep arms)  |
| `0x2FD82A` | Trace buffer end (= start + 802 = 401 samples × 2 bytes) — held in `RAM[0xFFBF30]` |
| `0x40B8`   | IRQ6 capture handler — A5++ stores samples, compares with `RAM[0xFFBF30]` for end-of-buffer |
| `0x40C2`   | IRQ6 idle/end-of-sweep handler — sets `$befa.w` bit 13 (sweep-done) and bit 11 (=`0x2400`) |

`RAM[0xFFBF34]` holds the active IRQ6 vector: `0x40C2` at idle,
`0x40B8` when actively sweeping. The firmware arms the sweep by
switching this pointer.

---

## CalRAM working buffer (`0x2FC000`–`0x2FFFFF`)

| Offset | Func                                                              |
|--------|-------------------------------------------------------------------|
| `0x000` | Cal data working copy (firmware copies 4082 bytes from cal NVRAM here at boot) |
| `0x013` | IRQ6 sample-capture branch byte — `btst #4, $2fc013.l` at ROM 0x40D4 picks "store sample" vs "end-of-sweep" |
| `0x1508` | Trace-buffer start (A5 initialises here, advances 802 bytes per sweep) |
| `0xDF5` | Highest observed cal-data offset (490+ references in docs/rom.asm) |

---

## Firmware dispatch jump table (ROM `0x000C0`–`0x007B0+`)

The firmware's main organizational structure: a flat, contiguous table of
6-byte JMP instructions starting at ROM offset `0x0C4`. Each entry is
`4EF9 hh hh ll ll` = `jmp $longabs.l`. Entries are addressed by their
offset within the table; the firmware uses `jsr $XXX.w` (16-bit short
addressing) to invoke an entry, executing the JMP and tail-jumping to
the actual handler.

The table starts AFTER the M68K exception/IRQ vector area at
`0x000`–`0x0BF` (which is 48 longwords: initial SP/PC at 0x0/0x4, then
vectors 2..47 each as a 4-byte address). Use `cmd/dispatch` to resolve
any slot quickly — see below.

There is NO marker or boundary inside the table — earlier notes about a
`dc.w 0x0E00` marker at offset 0x200 were wrong (that byte sequence was
the low half of the JMP-target at slot 0x1FC = `jmp $030E00`).

Total entries ≈ 200+; only a small fraction has been mapped to semantics.

### Confirmed dispatch entries (used by mapped code paths)

| Slot      | Target      | Mapped via | Function |
|-----------|-------------|------------|----------|
| `0x00C0`  | `0x001B34`  | (PC-rel)   | Reset vector — also reachable via the table |
| `0x00CA`  | `0x05ECB6`  | rev-l-memo | Indirect handler pointer (RAM-resident var also points here) |
| `0x00D0`  | `0x00ABDE`  | fcn.1B40   | SCI write: `mode=0x0002, data=0x0180` (display init helper) |
| `0x00D6`  | `0x00C470`  | fcn.5E?    | Used by analog-bus init |
| `0x0124`  | `0x00ABD0`  | fcn.1B40   | SCI write: `mode=0x0002, data=0x8100` |
| `0x012A`  | `0x05FAAE`  | n/a        | Cal subsystem helper |
| `0x0148`  | `0x018568`  | **gate C** | **Operating tick (a.k.a. "key consumer" entry)** — see note below. fcn.1B40 dispatches here when bf03==0 AND bf0a==0. |
| `0x014E`  | `0x032522`  | fcn.3AD0   | Sweep handler — called from operating loop |
| `0x015A`  | `0x05ECA2`  | rev-l-memo | Indirect handler (RAM 0xCA dispatches here)|
| `0x02FE`  | `0x02FFF4`  | rev-l-memo | RAM[0x2FE] indirect handler |
| `0x0304`  | `0x05F11A`  | rev-l-memo | RAM[0x304] indirect handler |
| `0x0430`  | `0x059E2C`  | key-cons   | Called by 0x18F4A inside the operating-tick — small wrapper into the 0x59000 (DLP/display state) subsystem |
| `0x043C`  | `0x009A52`  | rev-l-memo | Sweep-done first-stage processor (called immediately after `bclr #D, $befa.w`) |
| `0x04A2`  | `0x0192C8`  | **gate C** | **Operating-loop handler** — what bf0a perpetually points to; its body ends `bra 0x18568` (key consumer) |
| `0x05A4`  | `0x0309C0`  | rev-l-memo | Indirect handler pointer |
| `0x0640`  | `0x030020`  | rev-l-memo | Indirect handler pointer |
| `0x067C`  | `0x05A0E8`  | key-cons   | Called by 0x18F84 / 0x18FA0 inside the operating-tick — DLP-state subsystem |
| `0x069A`  | `0x058C2E`  | key-cons   | Called by 0x18F3E inside the operating-tick — reads `$b05f.w`, processes input state |
| `0x06DC`  | `0x00967A`  | key-cons   | Called by 0x18FAC inside the operating-tick — tests `$b071.w` bit 6 |
| `0x0736`  | `0x059D2A`  | key-cons   | Called by 0x18F54 inside the operating-tick — DLP-state subsystem (sibling of slot 0x430) |
| `0x0C4C`  | `0x05ECDC`  | **dormant**| **Cal-init entry** — no external callers; only via user CAL command |

**Note on slot 0x148 / fcn.18568**: previously called "the key consumer".
It IS where the bclr of `$bc67.0` (the key-available flag) lives — at PC
0x18F42, deep inside the function. But the function itself is the
firmware's **main operating tick**: a long sequence that tests sweep
state (`$f300.w → $b010.w`, then bits 11), display state (`$b07a.w`,
`$b07c.w`, `$b0ce.w`), mode bits (`$b1e0.w & 6`, `$b1e4.w == 0x34`), and
calls many other dispatch slots (`$6fa`, `$640`, `$5af4`, …) to update
sub-states. Processing the key flag is just ONE of many sub-steps.

So the gate-C investigation's framing was misleading: even if fcn.1B40
dispatched here, a huge amount of sweep / display / mode work runs
before (and after) the key-flag clear. The whole function is what
the firmware would run for one "user-facing tick" cycle.

To look up any slot quickly use `cmd/dispatch` — it reads the JMP at the
slot, prints the target, and disassembles the first three instructions
there:
```bash
go run ./cmd/dispatch/ 148       # single slot
go run ./cmd/dispatch/ C4 200    # range scan
```

### Why this table matters

The dispatch table IS the firmware's high-level control flow. When tracing
a runtime decision (e.g. "what does the IRQ4 handler do next?") the answer
is almost always "jsr $XXX.w" through this table. Mapping any slot you see
in disassembly to its target tells you which subsystem you've entered,
without chasing PC arithmetic.

---

## Memory-decode PAL (`U114`, source: `hp8593a_eeproms/PAL_8590-80159.zip`)

The PAL produces chip-selects from the M68K address bus:

| Select  | Range                   | Equation                |
|---------|-------------------------|-------------------------|
| LROM    | `0x000000–0x0FFFFF`    | low addresses, /MA21    |
| LCAL    | `0x200000–0x2FFFFF`    | `/MA23·MA21·/MA20`     |
| LKBD    | `0xEF8000` (256 B)      | PIT region              |
| LRTC    | `0xEF4000` (32 B)       | Front-panel μC          |
| LMMIO   | `0xFFF000–0xFFFFFF`     | top 4 KB                |

This is what told us 0x200000 is CalNVRAM (not RF/IF) and that the cal SRAM
is 64 KB-wide despite the firmware doing 4082-byte structured reads at boot.

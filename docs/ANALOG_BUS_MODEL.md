# A16 Analog-Bus (ADC / mux / DAC) — research & model design

**Status:** design proposal for review. No code changed yet. Implements the
"faithful conversion model" decision for the `fcn.5E63C`/`fcn.5E6E8` boot
stall identified in [DRIVETICK_BLOCKER.md](DRIVETICK_BLOCKER.md) (Path B).

**Goal:** replace the constant-`0x0006`-every-256-reads stub in
[pkg/emu/device/analogbus.go](../pkg/emu/device/analogbus.go) with a coherent
ADC conversion state machine so the firmware's boot-time PRESET ADC
calibration completes and the boot reaches a *live* operating loop
(`fcn.18568`), instead of freezing in the analog poll.

---

## 1. The problem, precisely

Under the 8593 SystemID strap the firmware never reaches the operating loop:
it freezes in `fcn.5E6E8` (called at boot from `0x5E858`), the **PRESET
two-point ADC calibration**. That routine performs the classic ADC read
sequence three times:

```
send_dac_word(cmd)         ; program channel/DAC (selects 0x95/96/97)  → trigger conversion
wait_for_adc_match(1, 1)   ; poll 0x9A status until bit0 (conversion complete)
select 0x9F; read 0xFFF75E ; read the ADC result (clears data-ready)
```

`wait_for_adc_match(mask=0x01, target=0x01)` needs **status bit 0 set**. The
current model returns `0x0006` (bits 1+2, *no bit 0*), so the poll can never
match — confirmed by single-step trace (`cmd/naturalkey -trace`): 0 matches
across 60 751 iterations. A naive "rotate `{0x06,0x07}`" fix unstuck the poll
but derailed the boot (the firmware needs *coherent* status+data across the
read sequence, not blind values), and regressed `TestMachineBootScreen`.

---

## 2. The conflicting `0x9A` status contracts (full set, from `docs/rom.asm`)

Every caller of `wait_for_adc_match` (`fcn.5E5DE`, loop body `0x5E5FA`) plus
the two `cmpi.b #6` sites, with the low-byte contract each imposes:

| Call site(s) | Routine | Contract on `0x9A` low byte | Requires |
|---|---|---|---|
| `0x5E64A`, `0x5E674` | `fcn.5E63C` cal-sweep A | `(0x12 & x) == 0x02` | bit1=1, bit4=0 |
| `0x5E708`, `0x5E71E` | `fcn.5E6E8` init | `x == 0x06` (exact) | bits1,2=1, **bit0=0** |
| `0x5E77E`, `0x5E7C0`, `0x5E800` | `fcn.5E6E8` ADC reads | `(0x01 & x) == 0x01` | **bit0=1** |
| `0x5EED4` | cal-sweep main | `(0x01 & x) == 0x01` | bit0=1 |
| `0x5EF84` | cal-sweep main | `(0x11 & x) == 0x01` | bit0=1, bit4=0 |
| `0x5E87C` (via `0x5E84A`) | GND/2VREF read | `(mask & x) == target`, args vary | parameterised |

`x == 0x06` (bit0 **clear**) versus `(0x01 & x) == 0x01` (bit0 **set**) cannot
both be satisfied by one constant. **A state machine is mandatory** — these
are different points in a conversion lifecycle, not one steady value.

This refines the older survey in [rom_annotations.md](rom_annotations.md)
(§"A16 analog-bus select map"), which only saw the `mask=0x12/target=0x02`
operating-loop poll (which `0x06` satisfies) and the `==0x06` init poll, and
missed the `mask=0x01` conversion-done polls that gate the 8593 boot.

---

## 3. Hardware (service guide `docs/Agilent-HP_8592D` + CLIP parts list)

The A16 analog-control block behind `0xFFF75C` (select) / `0xFFF75E` (data):

- **U47 — "main ADC"** (CLIP part 1826-1522, 12-bit). Input span **0–2 V**
  mapped to **bottom → top graticule** (Ch.5 p.257, Ch.9 p.375). Firmware
  treats the result as **12-bit signed**, range-checked `[-0x200, +0x1FF]`
  (ROM `0x5EF96`/`0x5EFA6`). Conversion start emits `ADC_SYNC` to the card
  cage (Ch.5 p.272); the firmware monitors inter-conversion timing
  (`ADC-TIME FAIL`, Ch.14 p.617). **No CPU-pollable ready/busy bit is
  documented** — the `0x9A` bit meanings below are derived from the firmware
  poll→read pattern (the natural ADC "data-ready" semantics).
- **U64 + U201 — 8-channel input mux** (CLIP part 1826-0609). Selectable
  sources (Ch.9 p.375-376): **ACOM / ground**, **+2 V reference**,
  **VIDEO_IF** (via positive-peak detector *or* bypassed in sample mode),
  **CRD_ANLG_2** (card-cage analog, direct to ADC), CRD_ANLG_1 (via video
  chain). Signal path `U201→U61→U45→U46→ADC` (Ch.4 p.249). Expected ADC
  readings are given only qualitatively: **GND → bottom graticule** (≈ min),
  **+2VREF → top graticule** (≈ full-scale).
- **DACs live on A7, not A16** (Ch.9 p.376-377): YTO/YTF tune are 12-bit
  (0–4095); others 8-bit. The A16 CPU drives them over the I/O bus — the
  `0x95/0x96/0x97` 24-bit byte stream (`send_dac_word`, `fcn.5E384`,
  clamped) is that path.
- The bundled **Calibration Manual has no A16/ADC content** (it's a metrology
  guide); register-level facts are not in any text-searchable repo doc, so
  the model is grounded in the firmware contracts + the qualitative
  signal-flow above.

**Key behavioural anchor (Ch.13 p.561 & p.598):** *"during the preset
routine, the analog-ground and 2 V reference are used to calibrate the main
ADC … if either signal is out of range, the ADC-GND FAIL or ADC-2V FAIL error
message is displayed."* — i.e. `fcn.5E6E8` (where boot stalls) **is** this
PRESET two-point ADC cal. The model must let it both *complete a conversion*
(bit0) and *read in-range GND/2VREF values* (no FAIL).

---

## 4. Register map (empirical, `0xFFF75C`/`0xFFF75E`)

| Select | Dir | Role | Model behaviour |
|---|---|---|---|
| `0x20` | W | one-shot init pulse | store |
| `0x90` | W | control reg A (init `0x0000`) | mux/mode bits (channel select) |
| `0x91` | W | control reg B (init `0x0012`) | mux channel id + enable |
| `0x93` | W | control reg C (init `0x000F`) | ADC mode bits |
| `0x95/0x96/0x97` | W | 24-bit DAC word (hi/mid/lo) | store; **write of `0x97` (last byte) = conversion trigger** |
| `0x9A` | R | **ADC status** | state machine (§5) |
| `0x9D` | R | ADC result, coarse/sign byte | latched conversion result (hi) |
| `0x9F` | R | ADC result (12-bit signed) | latched conversion result; **read clears data-ready** |

---

## 5. Proposed `0x9A` status bit map + conversion state machine

Derived bit semantics (consistent with every contract in §2):

| Bit | Mask | Meaning | When set |
|---|---|---|---|
| 0 | `0x01` | **EOC / data-ready** | a triggered conversion has completed; cleared on result read |
| 1 | `0x02` | **READY** (powered/alive) | always, once initialised |
| 2 | `0x04` | **SETTLED / idle** | when not mid-conversion |
| 4 | `0x10` | BUSY / error | kept clear at match points |

Resulting steady values: **`0x06` = idle** (ready+settled, no pending data),
**`0x07` = data-ready** (idle+EOC). This satisfies all of §2:
`(0x12&0x06)=0x02` ✓, `==0x06` when idle ✓, `(0x01&0x07)=0x01` ✓,
`(0x11&0x07)=0x01` ✓.

**State machine** (per the read sequence in §1):

```
IDLE         status=0x06          (bit0 clear → satisfies the ==0x06 init poll)
  │  write select 0x97 (send_dac_word completes) ─┐  trigger
  ▼                                                ▼
CONVERTING   status=0x06 for the first N reads of 0x9A   (mimics conversion time;
  │                                                       preserves poll cadence)
  │  after N reads (or M IRQ5 ticks)
  ▼
DONE         status=0x07          (bit0 set → satisfies the (mask&x)==... bit0 polls)
  │  read select 0x9F or 0x9D (result)
  ▼
IDLE         status=0x06          (bit0 cleared on result read)
```

`N` (the conversion-time cadence) carries over the role of the current
`statusMatchEveryNReads = 256`: it keeps a realistic "occasionally ready,
mostly busy" rhythm so the firmware still does background work between
conversions (see the render-degradation note in
[CLAUDE.md](../CLAUDE.md) — returning ready *every* read collapsed the
operating-loop render to ~30 pixels). Tunable; start at 256.

**The operating-loop poll** (`mask=0x12`, bit1) only needs bit1, which is
always set — so it never stalls. But because it runs ~247 k times in the
operating loop and gates background-redraw cadence, we must verify the render
doesn't collapse (it keys off the *transition*, not the level). This is the
main tuning risk — see §8.

---

## 6. ADC data model (selects `0x9D`/`0x9F`)

On `DONE`, latch a result from the currently-selected mux channel (decoded
from control reg `0x91` low bits, per the existing `analogbus.go` `adcResult`
logic, extended):

| Mux channel | Source | Latched ADC value |
|---|---|---|
| GND / ACOM | analog ground | ≈ bottom of scale (`-0x200`+margin, e.g. `-0x1F0`) — must read "bottom graticule", in-range |
| +2VREF | +2 V reference | ≈ top of scale (`+0x1F0`) — "top graticule", in-range |
| VIDEO_IF | detected IF (0–2 V) | small positive noise-floor (e.g. `+0x20`) |
| CRD_ANLG_2 / other | card-cage / DAC loopback | track DAC LSBs (sign-extended 9-bit), within `[-0x200,+0x1FF]` |

This passes the PRESET two-point cal (GND→bottom, +2VREF→top, both inside the
firmware's window → no `ADC-GND/2V FAIL`) and the `[-0x200,+0x1FF]` range
checks. Exact endpoints are tunable against what the firmware's window
accepts (the guide gives no numeric limit; derive empirically with
`cmd/naturalkey -trace` watching for the FAIL paths at `0x5EF9C`/`0x5EFA6`).

---

## 7. Reconciliation note (annotation fix)

[rom_annotations.md:104](rom_annotations.md) states the cal subsystem
(`fcn.5ECDC`, dispatch slot `0xC4C`) is "dormant — only triggered by the CAL
key / `:CAL:`". That is correct **for the full user cal** (`fcn.5ECDC`), but
the **PRESET two-point ADC cal `fcn.5E6E8` runs on every boot/preset** and is
where the 8593 boot stalls. The annotation should be updated to distinguish
the two (boot-PRESET ADC cal vs user-triggered full cal). This doc supersedes
the "analog cal is not executed at boot" implication.

---

## 8. Implementation plan (`pkg/emu/device/analogbus.go`)

1. Add conversion state to `analogBus`: `convState (idle/converting/done)`,
   `convReadCount`, `muxChannel`, `latchedADC int16`. Drop the bare
   `statusPending`/`statusReadCount` pulse.
2. `writeData`:
   - selects `0x90/0x91/0x93` → update `muxChannel`/mode (decode `0x91`).
   - select `0x97` (last DAC byte) → **arm conversion**: `convState=converting`,
     `convReadCount=0`, compute+stash `latchedADC` from `muxChannel`+DAC.
3. `readData(0x9A)`: drive the state machine — return `0x06` while idle or
   during the first `N` converting-reads, flip to `DONE`/`0x07` after `N`.
4. `readData(0x9F)` / `readData(0x9D)`: return `latchedADC` (0x9F low/signed,
   0x9D coarse/sign byte) and clear EOC → `idle`.
5. Keep the register-file fallback for unknown selects.

Keep the change behind the same `analogBus` type; no bus/MMIO wiring changes.

---

## 9. Validation plan

- **Boot reachability (primary):** `cmd/naturalkey` — assert the operating-loop
  body `[0x18568,0x18A88]` is visited > 0 times post-boot, and ideally
  `FrontPanel.Consumed()` becomes true after a key (the original Path B goal).
- **No regression:** full suite green, **especially `TestMachineBootScreen`**
  (the canary that the naive fix derailed) and `TestMachineBootFaithful`.
- **No FAIL paths:** trace that `0x5EF9C`/`0x5EFA6` range-fail and the
  `ADC-GND/2V FAIL` branches are not taken during boot.
- **New unit test** `TestAnalogBusConversionLifecycle`: trigger → status
  `0x06` for N reads → `0x07` → result read clears to `0x06`; verify each §2
  contract is eventually satisfiable.
- **Render cadence:** confirm the operating-loop render doesn't collapse to
  ~30 px (compare lit-pixel count vs the current boot banner).

## 10. Open questions / risks

1. **Exact conversion trigger.** Modelled as the `0x97` write (completes
   `send_dac_word`). Alternatives: a control-reg write, or any data-port
   write. Verify by tracing which write precedes each bit0 transition the
   firmware waits on. *(Empirically resolvable with `cmd/naturalkey -trace`.)*
2. **Operating-loop poll cadence** (§5) vs background-redraw — the main tuning
   risk; bit1-always-set could fast-exit. May need bit1/bit2 to also follow
   the conversion rhythm rather than being constant.
3. **`0x9D` vs `0x9F` roles** — assumed coarse/sign vs 12-bit result; confirm
   from `fcn.5E6BC` combine logic if the cal math misbehaves.
4. **Downstream gates** past `fcn.5E6E8`: once the boot clears this routine it
   may hit further analog/IF dependencies (sweep, IRQ1/IRQ6) before a fully
   live UI. This doc unblocks the *first* gate; expect iteration.

---

## 12. Post-implementation result (2026-05-29)

Implemented (`pkg/emu/device/analogbus.go`) + a latent-bug fix
(`pkg/emu/bus/mem.go`: `beRead`/`beWrite` now treat out-of-range bytes as 0
instead of panicking, so wild execution faults cleanly).

**The analog model works.** With the conversion state machine + EOC-decay, the
8593 boot clears BOTH analog gates (`mask=0x01` conversion-done polls AND the
`==0x06` idle poll) and advances from the long-standing `0x5E000` freeze all
the way **into the operating loop / DLP runtime at ~49M cycles** — roughly a
10× advance. Validated with `cmd/naturalkey -derail`/`-trace`.

**New downstream blocker: a DLP-interpreter derail (separate subsystem).**
At ~49.27M cycles the firmware derails at **`0x034C94 jsr (A1)`** — the DLP
opcode dispatch `A1 = ROM[ROM[0xA74] + token*4]`, base `ROM[0xA74]=0x71D76`:

- The DLP VM **spins on token `0x12F`** (handler `0x3A13A`, a string/symbol
  lookup against the RAM table at `$bb54`) at `recPtr=0x71A6D` for ~23
  consecutive C-loop iterations (the operating loop runs one DLP step per
  iteration via `slot 0x72A`), i.e. a multi-iteration/blocking DLP opcode.
- Then `recPtr` jumps to **`0x71D03`** (already past the documented DLP-source
  end `0x71676`, just before the dispatch table) and reads **token `0x2FF`**.
  That indexes to `ROM[0x72972]` = `0x49463B53` = ASCII **"IF;S"** — i.e. the
  lookup ran *past the handler table into DLP source text*. `jsr` to that
  garbage derails.
- **Not caused by the analog model:** making the ADC return DAC-varying values
  (vs constant) leaves the derail byte-for-byte identical. It is pure DLP-VM
  behaviour, reached only because the analog fix let the boot get this far.

**Likely shape:** a DLP `IF`/branch (the "IF" ASCII is suggestive) or the
multi-step opcode `0x12F` advances the DLP program counter (`recPtr`) to a
wrong location after its loop completes. Fixing it needs DLP-bytecode-VM RE:
the DLP record/opcode format, opcode `0x12F`'s multi-iteration semantics
(handler `0x3A13A`), and how `recPtr` is updated between steps. See
[DLP_RUNTIME.md](DLP_RUNTIME.md). Tool: `cmd/naturalkey -derail` (single-steps
the approach and dumps the DLP dispatch trail).

### 12a. DLP-derail follow-up (2026-05-29, cont.)

Two findings from chasing the DLP symbol table:

1. **DLP heap RAM was unmapped — now fixed.** The firmware partitions its DLP
   working-memory heap (`fcn.358C` + the allocator at `0x3380`) into RAM at
   **`0xFC0000`+**: empirically the heap pointer `$bb4e` settles at `0xFC9C12`
   and the symbol-table base `$bb54` at `0xFD8DEC` — both *below* the old RAM
   map (which started at `0xFEC000`), so every DLP symbol-table access hit the
   `OnFault` void. Added a `DLPRAM` region `0xFC0000–0xFEBFFF` in
   [machine.go](../pkg/emu/machine/machine.go) (faithful: the A16 SRAM is a
   contiguous block to `0xFFFFFF`; suite stays green).

2. **But that is NOT the derail cause.** Mapping the heap RAM left the derail
   byte-identical: the DLP VM still spins on token `0x12F` at `recPtr=0x71A6D`
   then advances `recPtr` to garbage `0x71D03`. Token `0x12F`'s handler
   (`0x3A13A`) is the DLP **identifier resolve/define** opcode — it classifies
   the name (`fcn.36166`: digit/`_` check), looks it up in `$bb54`, and on the
   `0x3A28A` path calls a define routine (pointer near the `DLPINIT` strings at
   `0x3D0Fx`). So the derail is in the **DLP bytecode interpreter's PC
   advancement** while executing the factory startup DLP — a layer below the
   symbol table. Next: trace how the persistent DLP PC (in the foreground ring
   state `0xFFA61C`/`$a630`) is updated between `slot 0x72A` steps, and why it
   lands on `0x71D03` (non-opcode-aligned, just before the dispatch table at
   `0x71D76`) instead of the next valid token. `cmd/naturalkey -derail` dumps
   the dispatch trail + DLP ring state.

**Test impact (unresolved):** `TestMachineBootScreen` (200M) and
`TestCalNVRAMBootAccessPattern` (100M faithful) now fail — their budgets run
*past* the 49M derail, so they capture post-derail state; the old goldens
captured the now-obsolete frozen-at-`0x5E000` boot. The other 6 boot tests
pass. These two need re-baselining (cap below 49M + regen) or skipping (with a
pointer here) once the path is chosen — they cannot be cleanly re-based while
the DLP derail stands.

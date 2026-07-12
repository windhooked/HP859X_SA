# 0xFFF728 / 0xFFF72A — the A7 Analog-Interface "I/O bus" (subsystem research, 2026-05-31)

## Question

The post-boot measurement state machine freezes in a loop at `0x22532–0x22826`
that drives an **unmodelled** indirect register pair: write a select word to
**`0xFFF728`**, read data from **`0xFFF72A`**. Before modelling it we needed to
know what physical subsystem it is.

## Answer (high confidence)

`0xFFF728/0xFFF72A` is the CPU's **indirect port onto the A16→A7 "I/O bus"** —
the digital control + status-readback bus that the A16 processor/video board
uses to drive the **A7 analog-interface assembly** (and, through it, the RF / LO
/ IF analog chain). It is a *separate* interface from the already-modelled
`0xFFF75C/0xFFF75E` analog-control hybrid (which is the on-A16 ADC-input mux +
12-bit ADC that digitises video/reference signals). `75C/75E` **reads digitised
analog**; `728/72A` **controls the A7 board and reads its status / frequency
counter back**.

### Evidence

**Service guide (`docs/08590-90316.pdf`):**
- Ch.9 p.362–377: "The A7 analog interface assembly receives digital control
  input on the **I/O bus control lines from the A16 processor/video assembly**
  and produces analog control signals for most of the analyzer functions" — YTO
  tune DACs, MAIN/FM span, sweep ramp, reference-level DAC, bandwidth companding
  DACs, A12 cal-attenuator + step-gain switching, A14 log/lin + linear gains.
- Ch.9 p.374–375 (A16 functions): two distinct uses of the same bus —
  "**Digital control of analyzer assemblies directly over the IO bus**" and
  "**Analog control … via the A7 analog interface assembly**".
- Ch.5 p.271–272: **ADR0–ADR4 = "I/O address lines"**; **ANA_TEST** = A7→A16
  readback test signals. Ch.14 p.632–635: **U18 = I/O-bus address latch
  (ADR0–4)**, **U2/U3 = I/O data-bus buffers (IOB0–15)**.
- A25 Counterlock: "counts the first LO frequency" and "the 21.4 MHz IF" — these
  counts read back to the CPU over this bus.

**Firmware (`docs/rom.asm`):** all real `0xF728/0xF72A` accesses live in one
driver module `0x223CC–0x22660`:
- `fcn.22532` write primitive: `(addr<<8)|data` merged with shadow → `0xF728`,
  then data → `0xF72A`.
- `fcn.223be` nibble-clocking DAC loader (multi-nibble → wide DAC).
- `fcn.22646` read primitive: select → `0xF728`, then `move.w 0xF72A,d0`.
- High-level callers (`0x22830`, `0x2287e`, `0x228c2`, …) are band-switch /
  attenuator / step-gain routines; e.g. `0x228c2` selects register 3 and
  `btst #6,d0` on the readback (a valid/ready gate).

### Select / data scheme

- **Write select to `0xFFF728`:** `(reg_addr << 8 & 0x0FFF) | (shadow $AD7C &
  0xF000)`. High nibble = control/mode bits carried in the RAM shadow at
  **`0x00AD7C`** (maintained by ~54 firmware sites); the next byte = the A7
  register/DAC address.
- **Data via `0xFFF72A`:** write = load the addressed DAC/latch; read = fetch
  the addressed status/counter. Wide DACs loaded by repeated nibble writes;
  multi-byte readbacks by repeated reads of one selected register.
- **Status bit:** readback gated by a valid/busy bit (`btst #6` in the band/gain
  path); the measurement loop also branches on IRQ-set RAM flags `$bf26` bit16
  (helper `fcn.22668`), `$b1e0` bit11, `$b212`/`$b213`, `$ad7d` bit5.

## What the *frozen* loop actually does (measured — `cmd/longrun`)

In the post-boot freeze, the `0x22532` loop **exclusively polls A7 register 3**
(select `0x13xx` = reg 3 + mode nibble `0x1` from `$AD7C`), **909×** in a 40k-
step window, and reads back a **constant `0x72E2`** every time. Constant
readback ⇒ classic "poll a status that never changes": because the A7 bus is
unmodelled, register 3 never updates, so the measurement state machine never
advances to the next sweep phase / `__GTTDRW` trace draw.

Register 3 (mode 1) is in the band-switch / step-gain / status readback group.
The most likely role of the value the loop waits on is an **A7/A25 settle-or-
lock status** (LO/YTO phase-lock or analog-settled) that must assert before the
firmware arms a sweep — consistent with a real analyzer holding off the sweep
until the LO is locked and the analog chain has settled.

## Implication for emulation (next step, not done here)

To advance the state machine toward a drawn trace, model the A7 I/O bus at
`0xFFF728/0xFFF72A`:
1. Latch the select word written to `0xFFF728` (high byte = register address,
   top nibble = mode from `$AD7C`).
2. On reads of `0xFFF72A`, return per-register data for the selected register —
   in particular make **register 3** return a value whose gating bit(s) signal
   "settled / locked / ready" rather than a fixed `0x72E2`, so the measurement
   loop progresses past the poll.
3. Pair this with the IRQ-driven RAM-flag handshake (`$bf26`/`$b1e0`/`$b212`)
   the same loop branches on — i.e. a faithful sweep cycle, not a constant.

Open item: the exact semantics of A7 register 3's readback bits (which bit =
locked/settled, and the expected value) still need confirming — either by OCR'ing
the A16/A7 schematic pages of `docs/8590 CLIP 5963-2591.pdf` (no text layer; needs
OCR) or by tracing what the `0x22646` caller compares the readback against.
**→ RESOLVED below (2026-07-12): reg 3 settle gate = `(x & 0xC0) == 0x80`, strobed
by reg-2 ← `0xE2`; see the register map.**

---

# ★ 2026-07-12 — THE REGISTER→DAC MAP (disassembly-derived, no hardware)

Derived this session from the driver module (`0x22270–0x22960`), the tune/servo
functions (`0x23128–0x24780`), the service-diag menu template (ROM `0x7cc30`), and
the service guide's Ch.13 DAC inventory (`pdftotext` works on
`docs/Agilent-HP_8592D - Service Guide.pdf` = `08590-90316.pdf`, same file).
Method: 4 parallel disasm agents + direct reads; every claim PC-cited in the
per-function notes below. Tags: **[V]** = verified by two independent reads or
direct disasm; **[M]** = medium confidence (single decode, semantics inferred);
**[A]** = asserted/hypothesis.

## Level 1 — the I/O bus is DIRECT-mapped: one word port per address at `0xFFF700+2n`

The YTO coil DACs are **NOT behind the F728/F72A select pair** — they are direct
word ports in the same block. Hypothesis **[A]**: the I/O-bus address lines
ADR0–ADR4 (U18 latch, service guide Ch.5/14) are decoded as `n = (offset−0x700)/2`,
giving 32 word ports `0xF700–0xF73E`; `F728/F72A` (n=0x14/0x15) is then just one
sub-addressed *device* on that bus (the A7 serial select/data pair).

| Port | Shadow | Function | Evidence | Tag |
|---|---|---|---|---|
| `0xF700` | `B1A4` | **YTO FM-coil DAC** (set `0x800` midscale at tune) | `fcn.7ac8`: `31f8 b1a4 f700`; midscale writes `0x241f6/0x24252/0x2447a` | [V] |
| `0xF702` | `B1A6` | **YTO fine-tune DAC** (12-bit) | `fcn.7ac8`: `31f8 b1a6 f702`; computed `0x24200` | [V] |
| `0xF704` | `B1A8` | **YTO main-coil (coarse) DAC** — `clamp((master+0x8000)>>16 + B1A8, 0xFFF)` | `0x24094`, latch `0x7b26`/`0x1d264` | [V] |
| `0xF708` | `B1AE` | tune re-commit latch (cleared at tune `0x23f7e`; ← `b1ae` in `fcn.1ce80`) | agent A + C | [M] |
| `0xF70A` | — | sweep re-arm (known from DRIVETICK RE) | docs/DRIVETICK_BLOCKER.md | [V] |
| `0xF712` | `B204` | sweep/band digital control latch (write gated `b212.13`) | `0x23168` | [M] |
| `0xF716` | `B1B4` | sweep control (`B1B4 & 0xFFF0`; sweep re-arm path writes it) | `0x23f8c`; DRIVETICK | [V] |
| `0xF718` | — | timebase/reference + misc control latch | `fcn.233f2` @ `0x23428` | [M] |
| `0xF728/F72A` | `AD7C` | A7 serial sub-bus select/data (Level 2) | driver module | [V] |

## Level 2 — the F728/F72A serial sub-bus registers

Select word = `[AD7C mode nibble (bits 12–15) | reg (bits 8–11) | data/subindex (bits 0–7)]`.
`F728` gets the select (data bits masked off in the byte-write primitive `fcn.22532`
`0x2255e`); `F72A` carries the full word / returns the read. RAM word `0xAD7C` is
the live mode shadow OR'd into every access. **[V]**

| Reg | Dir | Function | Key evidence | Tag |
|---|---|---|---|---|
| **0** | W | **YTO serial DAC chain** — `fcn.223b6(value=AD60)`: 8 nibble-writes, sub-index in select bits 4–7 (`addi #0x10` per nibble — bits 4–7, NOT the reg field), groups 2+3+3 nibbles; value split `÷3` (variant `b213.4`) or `÷40`, `0x7D0`(2000)-complement; third group constant `0x32`(50) | `0x223be–0x2249c`; two-point lock check loads `0x7C3`/`0x735` (`0x23daa/0x23ddc`) | [V] mech / [M] label |
| **2** | W | **multiplexed control-latch port**: pointer byte `(group<<6)\|0x30\|(sub<<1)` written in bank-3 mode, then 16-bit data as two byte-writes in bank=`group` (AD7C bits 13–14 = bank). Also: **`0xE2` = settle strobe** (`fcn.227f2`), **`0xE0\|(2<<n)` = gain/measure-path select** (`fcn.2287e`). Band-switch seq (`fcn.22830`): grp0,sub1←arg; grp1,sub2←0; grp2,sub2←0xFFFF | `0x225a6/0x2260a/0x2280e/0x228a2` | [V] mech / [M] fields |
| **3** | R | **status + 16-bit readback**: settle gate `(x&0xC0)==0x80` polled after reg2←0xE2 (the boot-freeze poll, `fcn.227f2` `0x2281a`); bit6 = data-invalid; two consecutive reads = hi/lo bytes of a measurement (negated) (`fcn.2287e` `0x228d4/0x228de`) | | [V] |
| **4** | W | latch, only ever cleared to 0 at band-config end (`fcn.227e0`, sole caller `0x22876`) | | [V] |
| **5** | W | **10 MHz TIMEBASE reference DAC** (8-bit): live shadow RAM `0x9ED7`, cal-NVRAM byte `0x2FC037` (checksummed store via `fcn.42f60`, slot `0x3d6`); mixer-bias excluded (band-indexed per ROM strings `"mixer bias B <band>"`) | writer `0x22574` (slot `0x514`); callers `0x141e6/0x160e2/0x42f76/0x435b6/0x4e206` | [V] path / [M] label |
| **6** | W | **analog control/mode latch**: band/mode values `0x41`(variant)/`0x04`, `0x82`, `7`, `1`; AD7C bit3 mirrored = YTO-lock strobe (two-point check toggles it, `fcn.23d30`); AD7C bit5 = main/FM span mode (`fcn.23362`) | writer `fcn.224c0`; callers `0x22b98…0x23d5c` | [V] mech / [M] bits |
| **7** | R(/W strobe) | **status/ID**: bit0 → annunciator `0x2b` (`fcn.22784`); **bit1 = lock-error** (two-point YTO check); bit2 = status (`fcn.227ba`); **bit3 = hardware variant** → `b213.4` + scale consts `AD88–AD8E` (`fcn.22342`); dual-mode read after `reg7←7` strobe (`fcn.2273c`) | | [V] |

No writes to regs 1, 3, 8–15 exist outside the above (reg-field histogram over all
`fcn.22532/22646` callers — all callers are inside `0x22334–0x228e0`; the only
external interface is the trampoline API below). `fcn.225de` writes `0x800|AD7C`
to F728 alone (select-only strobe, AD7C bit12 = flag) during band/mode setup. **[V]**

## The tune data flow (who computes what)

- **`fcn.23e56`** (slot `0xb60/0xb62`) — frequency→DAC master math (packed-float lib,
  inline-operand convention — `jsr` followed by operand address words; this is why
  rizin desyncs there). Master value = f(input, band scale, coeff `9F44` × coeff
  `AD76`); split → `B1A8` (coarse) + `B1A6` (fine) + `B1A4`=0x800 (FM) → direct
  ports. Band scale table ROM `0x22d90`: 7 × 32-bit = 9·10ⁿ Hz (n=0–6), indexed by
  `B0EE`. Variant consts: `0x5B4`(1460) vs `0x96`(150). **[V]**
- **`fcn.23128`** (slot `0x5aa`) — **open-loop** serial-chain tune: computes `AD60`
  from FP cal slope `AD98` (loaded from `0x24AD6/0x24ADE/0x24AE6` or per-band cal)
  + model consts (`0x22C82/8A/92`, keyed on `bfee`=IDNUM `0x2190`), calls
  `fcn.223b6(AD60, reg 0)`. NOT a counter servo — zero A7 reads; counterlock IRQ
  flag `bf26.16` only gates entry. Residual → `AD52`/`AD56` (sign → AD7C bit15),
  DACS display via `fcn.22274`. **[V]**
- **`fcn.23362`** (slot `0xba0/0xb9e`) — main-coil ↔ FM-coil span-mode switch:
  AD7C bit5 selects `AD76 ← 9F48` (main) or `← 9F4C` (FM), reg-6 mode write,
  retune on change. Manual: FM coil drives LO spans ≤ 10 MHz. **[V] mech / [M] labels**
- **`fcn.23d2a`** — two-point YTO lock check: chain-load `0x7C3`(1987) → strobe
  (AD7C bit3 via reg 6) → check reg7 bit1; `0x735`(1845) → check; restore `AD60`;
  annunciator `0x2f` on failure. **[V]**
- **`fcn.2269e`** (slot `0x7f4`) — per-band tune setup: band cal struct at
  `0xADC8+band*8` / `0xAEB8+band*4`, calls `fcn.23128` + `fcn.23e56`. **[V]**

## RAM shadow cells (the A7 state the firmware owns)

`AD7C` mode shadow (bit3 lock strobe, bit5 FM/main span mode, bit12 strobe flag,
bits13–14 reg-2 bank, bit15 tune-value sign) · `AD60` serial-chain value ·
`AD52/AD56` tune residual (display, signed) · `AD5E` count+1 (FM-span/ramp) ·
`AD76` active slope ← `9F48|9F4C` · `9F44` master coeff · `AD98` FP slope ·
`AD88/8A/8C/8E` variant full-scale (4000/100/4090/5 vs 4095/0/3900/250) ·
`B1A4/B1A6/B1A8` FM/fine/coarse DAC shadows · `B1B4`→F716 · `9ED7` timebase live ·
`2FC037` timebase cal byte · `bf26.16` counterlock IRQ flag · `B0EE` band ·
`b213.4` hardware variant.

## The A7 API surface (Path-5 gold)

The low-memory dispatch at `0x502–0xd00` is a **6-byte-stride `jmp abs.l` array**
— effectively the firmware's A7 driver API. Slots: `0x514`=set timebase DAC ·
`0xc34`=load YTO serial chain · `0x5aa`=retune (`fcn.23128`) · `0xb60`=set
frequency (`fcn.23e56`) · `0x7f4`=per-band tune setup · `0xba0`=span-mode switch ·
`0x718`=read variant/init scale · `0x9a6`=reg-7 status · `0x3d6`=persist timebase.
A Path-5 minimal monitor can drive the analog hardware by calling these slots —
no DLP/UI needed. **[V]**

## Still open (ordered by value)

1. **reg-0 chain physical label** — which A7 DACs the 2+3+3 nibble groups load
   (interpolation pair + fixed 50); the ÷3/÷40 split semantics. Schematic OCR or
   the component-level binder would settle it. [M]
2. **reg-2 group/sub field map** — band-switch bit meanings; step-gain/cal-atten
   attribution (`SET ATTN ERROR`, `STP GAIN ZERO` handlers not yet bound). [M]
3. **Direct-map hypothesis** `F700+2n = ADR n` — consistent with U18/ADR0–4 but
   unproven. [A]
4. **Softkey ID→handler binding** (service IDs `0x99–0xB1` from menu template ROM
   `0x7cc30`; `0x99`=CAL TIMEBASE … `0xAB`=COARSE TUNE DAC … `0xB1`=−10V REF):
   the `10 NN 00 00` trailer is label-only; the action binding lives in the
   softkey key-event emit layer (same machinery as Gate 2). Bonus decoded en
   route: **`fcn.12288` is the typed-command CLASS dispatcher** — class =
   `(cmdword>>8)−0xd`, 33-case offset table at `0x12754`, class `0x27` → slot
   `0x550` → CONTS `fcn.5f968`; boot fires only class `0x12` (code `0x12D6`).
5. **Mixer-bias / 3rd-conv (ref-level) DAC** location — band-indexed, not found
   on the sub-bus yet; likely behind reg 2 or a direct port. [A]
6. **Emulator freeze note**: `fcn.223b6`'s tail busy-waits on the tick counter
   `0xbf12` (deadline +3 ticks, poll loop `0x224aa`) — if the emulator's IRQ5
   timer doesn't advance `0xbf12`, every chain load hangs. Relevant to
   [[no-autonomous-irq-generation]].

## Sources

Service guide `docs/08590-90316.pdf` (Ch.5/9/13/14; Ch.13 = the service softkey
DAC inventory: DACS four = span/YTO coarse/fine/FM, 0–4095; timebase/mixer-bias
0–255; MAIN/FM COIL DR + SWEEP TIME DAC are test-point *monitor* keys); firmware
`docs/rom.asm` + raw `/tmp/rom_gold.bin` hand-decodes (`0x22270–0x22960`,
`0x23128–0x24780`, float lib `0x610c–0x66xx`, menu template `0x7cc30`); measured
by `cmd/longrun`; PAL `hp8593a_eeproms/PAL_8590-80159.zip` is only the coarse
RAM/ROM/CAL decode (MA14–MA23) and does not resolve the on-board `0xFFF7xx` fine
decode.

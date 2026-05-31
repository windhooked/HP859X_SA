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

## Sources

Service guide `docs/08590-90316.pdf` (Ch.5/9/14); firmware `docs/rom.asm`
(`0x223CC–0x22660`, `0x22830`+ callers); measured by `cmd/longrun`; PAL
`hp8593a_eeproms/PAL_8590-80159.zip` is only the coarse RAM/ROM/CAL decode
(MA14–MA23) and does not resolve the on-board `0xFFF7xx` fine decode.

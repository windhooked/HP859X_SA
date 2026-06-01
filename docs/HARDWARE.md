# HP 8593A — Emulated Hardware Reference

Full register-level reference for the hardware modeled by this emulator, derived
from the Go device code (`pkg/emu/...`). Each device/register is marked:

- **MODELED** — faithful behavior (real state machine / data path).
- **STUB** — constant / always-ready override / backing-store-only (enough to keep
  the firmware moving, not faithful).
- **GAP** — unmodeled / known-incomplete (reads return 0 or a placeholder).

Cross-references: the real A16 board is documented in
[rom_analysis.md](rom_analysis.md); calibration/self-test architecture in
[POST_SELFTEST.md](POST_SELFTEST.md); display geometry in
[CRT_GEOMETRY_DIAGNOSIS.md](CRT_GEOMETRY_DIAGNOSIS.md); the trace blocker in
[DRIVETICK_BLOCKER.md](DRIVETICK_BLOCKER.md).

---

## 1. System overview

- **CPU:** Motorola 68000, 16 MHz, **big-endian**, 24-bit address bus (`AddrMask =
  0xFFFFFF`; upper byte ignored). Firmware: **Rev L 98.06.15**, 1 MB across 4×27C020.
- **Reset vector** (image offset 0): `SP = 0x00FF948A`, `PC = 0x00001B34`.
- **Emulation cores** (`pkg/emu/cpu`, behind the `cpu.CPU` interface):
  - **Musashi** (`cpu/musashi`, cgo, MAME-proven) — **PRIMARY**. Handles `ORI #imm,SR`
    and clean autovector IRQ injection the firmware needs. Instruction-hook enabled
    (`RunUntil(cycles, stopPC)` stops at a PC); `M68K_EMULATE_ADDRESS_ERROR` ON
    (firmware recovers from malformed-DLP dispatch via the address-error exception).
  - **Unicorn** (`cpu/unicorn`, QEMU-backed) — **ORACLE** for differential tests only;
    cannot execute `ORI #imm,SR`.
- **Bus** (`pkg/emu/bus`): 24-bit address-decode mapper; non-overlapping device ranges;
  big-endian RAM/ROM. Unmapped accesses → `OnFault` (returns **0** here, not all-ones,
  to match flat-memory boot behavior). `OnRead`/`OnWrite` diagnostic hooks (used by the
  `cmd/` probes); instruction fetches route through `Bus.Read` (so fetch addresses are
  observable via `OnRead`).

---

## 2. Memory map (24-bit, `machine.New8593A`)

| Range | Size | Device | Go type | Status |
|---|---|---|---|---|
| `0x000000–0x0FFFFF` | 1 MB | **ROM** (Rev L firmware, read-only) | `bus.ROM` | MODELED |
| `0x200000–0x20FFFF` | 64 KB | **CalNVRAM** (A16A1 battery-backed cal SRAM; U114 PAL `LCAL`) | `device.CalNVRAM` | MODELED (blank+checksum) |
| `0x2FC000–0x2FFFFF` | 16 KB | **CalRAM** (cal-data working buffer / scratch) | `bus.RAM` | MODELED (RAM) |
| `0x320000–0x32000F` | 16 B | **A16 write-address latch** (POST address-decoder readback) | `mmio.AddrLatch()` | MODELED |
| `0xEF4000–0xEF401F` | 32 B | **FrontPanel** µC (keys + RPG; PAL `LRTC`; IRQ3) | `device.FrontPanel` | partial (see §6) |
| `0xEF8000–0xEF80FF` | 256 B | **PIT stub** (MC68230; PAL `LKBD`; IRQ4 touches PGCR/PSRR) | `bus.RAM` (zeroed) | STUB |
| `0xFC0000–0xFEBFFF` | 176 KB | **DLPRAM** (DLP heap + symbol table; `$bb4e`/`$bb54`) | `bus.RAM` | MODELED (RAM) |
| `0xFEC000–0xFEFFFF` | 16 KB | **TestRAM** (lower half of the march-test range) | `bus.RAM` | MODELED (RAM) |
| `0xFF0000–0xFFEFFF` | 60 KB | **RAM** (stack `SP=0xFF948A` + firmware variables) | `bus.RAM` | MODELED (RAM) |
| `0xFFF000–0xFFFFFF` | 4 KB | **MMIO** window (§5) | `device.HP8593AMMIO` | mixed |
| `0x310000` | — | sweep-generator output latch (write-only) | *unmapped* | GAP (OnFault→0) |
| everything else | — | unmapped → `OnFault` returns **0** | — | — |

Notes: the firmware's march RAM test sweeps `0xFEC000–0xFFC000` (TestRAM + lower main
RAM). The real A16 SRAM is one contiguous block `0xFC0000–0xFFFFFF`; the split into
DLPRAM/TestRAM/RAM is an emulator convenience (all `bus.RAM`).

---

## 3. Interrupts / vector table (Rev L)

Autovectored IRQs; handler addresses are ROM longwords at offsets `0x60–0x7C`.

| Level | Vector → handler | Role | Notes |
|---|---|---|---|
| IRQ1 | `0x002AB8` | sweep update | writes f200/f300/f400; loads sample via `jsr` to RAM 0xCA |
| IRQ2 | `0x003A94` | rte-only (noop) | |
| IRQ3 | `0x002B1E` | front-panel | key/RPG event; sets RAM `bc67.0` / `bd77.0` |
| IRQ4 | `0x002642` | HP-IB | TMS9914A service |
| IRQ5 | `0x003ECE` | timer tick | increments RAM timer counter (`$bfca`); **must be injected periodically** by tests/tools or timer-wait loops never exit |
| IRQ6 | `0x004088` | sweep sample capture | reads ADC (`$f200`) → store-to-`(A5)+` (capture, dispatch `0x40B8`) or end-of-sweep (`0x40C2`), vectored via `$bf34` |
| IRQ7 | `0x003A9E` | NMI | |

**IRQ injection protocol:** `CPU.SetIRQ(level)` → run ~400 cycles to service → `CPU.SetIRQ(0)`.
The device models do **not** assert IRQs themselves; the run loop drives them (e.g.
FrontPanel raises IRQ3 while `Pending()`; TMS9914A exposes `IRQAsserted()` as a predicate).

---

## 4. Memory devices

### ROM `0x000000–0x0FFFFF` — MODELED
Read-only; loaded by `romloader.LoadDir("hp8593a_eeproms")` (interleaves the four Intel-
HEX chip dumps MSB/LSB per bank → 1 MB image). Writes dropped.

### CalNVRAM `0x200000–0x20FFFF` — `device.CalNVRAM`, MODELED
64 KB battery-backed cal SRAM; implements `bus.Device` directly.
- **Read/Write:** big-endian backing store; out-of-range (`addr+sz > 0x10000`) → 0/drop.
- **`NewCalNVRAM()`** = all-zero ("dead battery" → firmware uses ROM defaults).
- **`Synthesize()` / `SynthesizeRevL()`:** zeros all, then `b[0]=0x01`, `b[1]=0x01` — the
  even/odd-byte checksum anchors (Σ even ≡ 1, Σ odd ≡ 1 mod 256) that satisfy the Rev L
  startup checksum sweep at ROM `0x454A`, clearing "CAL: USING DEFAULTS". **GAP:** real
  per-band correction tables are all-zero (defaults substituted).
- **Boot access pattern (faithful, measured by `cmd/caltrace`):** firmware reads all 65536
  bytes once (checksum sweep) + reads offset 0 three more times as a longword (CPU integrity
  test ROM `0x44AA–0x44B8`); offset 0 is the only byte written. **No magic gate byte** —
  Rev L validity is checksum-based.
- **`Trace` hook:** `TraceFunc(off,sz,val,write)` on every access (diagnostic; `cmd/caltrace`).

### CalRAM `0x2FC000–0x2FFFFF`, DLPRAM, TestRAM, RAM — `bus.RAM`, MODELED
Plain big-endian RAM. CalRAM is the cal-data working buffer (the firmware copies cal NVRAM
here at boot; IRQ6 handler tests `btst #4,$2fc013`). DLPRAM holds the DLP heap (`$bb4e`→
`0xFC9C12`) + symbol table (`$bb54`→`0xFD8DEC`).

### A16 write-address latch `0x320000` — MODELED
Returns the index (`(addr&0x7F)>>1`) of the last write into the MMIO `0xFFF700`-block, for
the POST address-decoder self-test (ROM `0x4AA0`). Backed by `HP8593AMMIO.addrLatch`.

---

## 5. MMIO window `0xFFF000–0xFFFFFF` (`device.HP8593AMMIO`)

4 KB RAM-backed (`b [0x1000]byte`); specific offsets overridden on read/write. Offsets below
are relative to `0xFFF000`. Multi-chip: 82C55A PPI, sweep registers, HD63484 ACRTC, TMS9914A
HP-IB, the two indirect analog buses, and the A16 strap/POST registers.

### 5.1 Sweep / detector

| Offset | Name | Read | Write | Status |
|---|---|---|---|---|
| `0x200` | sweep-start latch / **video-ADC** | word, **only when `SweepActive`** → `Sweep.DetectADC()` (§6 SweepEngine); else backing store | backing store; bit13 = sweep active/trigger | MODELED (when active) |
| `0x300` | **sweep-status** | word read OR'd with `sweepStatusReady = 0x1000` (**bit12 = sweep-hardware-ready**, always set) | backing store | STUB (bit12 forced) |
| `0x300` bit11 | sweep-**complete** gate | **NOT forced** — reads backing store | — | GAP (the trace-completion gate; see DRIVETICK_BLOCKER) |
| `0x400` | ADC / sweep-DAC output | backing store | written by IRQ5 handler | STUB |
| `0x634` | timer interrupt ack | backing store | write 1 to ack IRQ5 | STUB |
| `0x716` | sweep DAC / FP (boot init) | backing store | backing store | STUB |

### 5.2 HD63484 ACRTC display (offsets `0x5FC–0x5FF`) — see §7

| Offset | Name | Read | Write | Status |
|---|---|---|---|---|
| `0x5FC` | command register | — | word → `Display.WriteCmd` | MODELED |
| `0x5FD` | **status byte** | overridden to `sciStatusReady = 0x27` (bits 0,1,2,5 all "ready") | re-asserts ready bit | STUB (always ready) |
| `0x5FE` | data / FIFO port | word → `Display.ReadData()` (block read-back; §7) | word → `Display.WriteData`: AR=0 → command/parameter FIFO; AR 0xC8..0xCF → display-address register (RAR1/MWR1/SAR1, page-flip — §7.1) | MODELED |

### 5.3 TMS9914A HP-IB (offsets `0x600–0x60F` + FP data path) — §6

| Offset | Read (R regs) | Write (W regs) | Status |
|---|---|---|---|
| `0x600` | IS0 | IMR0 | STUB |
| `0x602` | IS1 | IMR1 | STUB |
| `0x604` | ADSR | AUXCR (only `swrst` honored) | STUB |
| `0x606` | BSR | ADR | STUB |
| `0x608` | — | SPMR | STUB |
| `0x60A` | CPTR | PPR | STUB |
| `0x60E` | **DIR** (drains 1 byte from input buf; clears IS0.BI) | CDOR (not transmitted) | MODELED in / GAP out |
| `0x160` | `0x03` if `PendingInput()>0` else `0` (FP-port HP-IB status) | — | MODELED |
| `0x140` | `HPIB.ReadByte(0xE)` (pops DIR; FP-port HP-IB data) | — | MODELED |

Byte-width only; odd offsets / idx≥8 ignored. `0x610–0x61F` not routed.

### 5.4 Indirect analog ADC bus `0x75C` (select) / `0x75E` (data) — §6 analogBus

Word-only. `0x75C` write → `abus.writeSelect`; `0x75E` read → `abus.readData`; `0x75E`
write → `abus.writeData`. Select `0x9A`=ADC status (conversion state machine), `0x97`=DAC-low
(triggers conversion), `0x9F`=12-bit result. **MODELED** (ADC conversion state machine);
+2VREF channel is a GAP.

### 5.5 A16→A7 analog-interface I/O bus `0x728` (select) / `0x72A` (data) — §6 a7IOBus

Word-only. `0x728` write → `a7bus.writeSelect`; `0x72A` read → `a7bus.readData`; `0x72A`
write → `a7bus.writeData`. 16-register file; **reg 3 reports live "settled/locked" status**
(`(regs[3]&^0xC0)|0x80`); others are last-written latches. STUB except reg 3.

### 5.6 A16 system-ID / strap registers — §6 SystemID

| Offset | Const | Value | Status |
|---|---|---|---|
| `0x73C` | `SystemIDWord73C` | `0x0018` → board_id 3 → **IDNUM 0x2191 (8593)** | MODELED (IDNUM only) |
| `0x73E` | `SystemIDWord73E` | `0x0000` | MODELED |
| `0x77C` | `SystemIDWord77C` | `0x0000` (LONGWORD B; option bits untraced) | STUB |
| `0x77E` | `SystemIDWord77E` | `0x0000` | STUB |

**GAP:** installed-option bits (Opt 026/027/041/043) and the I/O-board descriptor at
`0xFFF110`/`0x11E` (HP-IB presence) are **not wired** — option-detection reads them absent.
See [option-detection memory note] / `cmd/optprobe`.

### 5.7 POST / self-test hardware models

| Offset | Behavior | Status |
|---|---|---|
| `0x614`,`0x616` | preset to `0xFF` at construction — POST-bypass strap (ROM `0x49A0` "mark all pass" branch) | STUB (faithful bypass) |
| `0x700–0x77F` | writes update `addrLatch` (read back at `0x320000`) | MODELED |
| `0x780–0x7FF` | read **mirrors** `0x700–0x77F` (low addr bit 7 undecoded) — the f700↔f780 data-path loopback the POST `0x4A0E` test verifies | MODELED |
| `0x000–0x00F` | 82C55A PPI (front-panel I/O; control at `0x007`) | STUB (backing store) |

POST result word = `NOT(0xFFF612):NOT(0xFFF610)`; with the bypass strap + loopback mirror +
address latch + the HD63484 read-back (§7), the result is **`0x0000`** (clean — no FAIL line).
Full RE: [POST_SELFTEST.md](POST_SELFTEST.md).

---

## 6. Peripheral device models (detail)

### 6.1 analogBus — A16 analog-control hybrid (`analogbus.go`, embedded in MMIO)
U47 12-bit ADC + 8-ch mux + DACs. Select-then-data via `0x75C`/`0x75E`.

| Select (`sel&0xFF`) | Read | Write | Status |
|---|---|---|---|
| `0x9A` ADC status | ready-pulse: `0x0000` busy except every **256th** read → `0x06` idle (ready bit1 + settled bit2) / `0x07` when conversion done (EOC bit0, self-clears after one pulse). A converting sample → done after **8** status reads. | stored | MODELED |
| `0x9F` ADC result | `uint16(latchedADC)&0x1FFF`; consumes conversion | stored | MODELED |
| `0x9D` ADC hi | `0x0000` (so 32-bit value = `0x9F` word); consumes conversion | stored | STUB |
| `0x97` DAC[7:0] | reg passthrough | store + **trigger conversion** (`sampleADC`) | MODELED |
| `0x96`/`0x95` DAC[15:8]/[23:16] | reg passthrough | store | MODELED (store) |
| `0x91` ctrl (mux ch) | reg passthrough | store; low 3 bits = mux channel | STUB |
| `0x90`/`0x93`/`0x20` ctrl/init | reg passthrough | store | STUB |
| other | `regs[sel&0xFF]` | store | STUB |

`sampleADC()` per mux channel (`regs[0x91]&7`): ch0 CRD_ANLG_2→0, ch1 VIDEO_IF→32, ch2
+2VREF→`0x100`, default→`int16(dac&0x1FF)`. **GAP:** ch2 +2VREF transfer function is a
placeholder — PRESET ADC cal (`fcn.5E6E8`) not fully satisfied → ADC-TIME/GND/2V annunciators
remain cosmetically. See [ANALOG_BUS_MODEL.md](ANALOG_BUS_MODEL.md).

### 6.2 a7IOBus — A16→A7 analog I/O bus (`a7iobus.go`, embedded in MMIO)
Programs the A7 assembly (YTO/LO tune, band switch, sweep ramp, ref-level DAC, BW companding)
and reads A7/A25 status. Select word `(reg<<8 & 0x0FFF) | ($AD7C & 0xF000)`; reg = `(sel>>8)&0xF`.
- **Reg 3:** live status `(regs[3]&^0x00C0)|0x80` — bit7=1 settled/locked (firmware spins at ROM
  `0x22818` until `(readback&0xC0)==0x80`). **MODELED.**
- **Regs 0–2,4–15:** last-written latches. **STUB.** **GAP:** A25 Counterlock frequency readback
  not modeled. Diagnostics: `A7ReadHist`/`A7WriteHist` when env `A7_LOG` set.
- Full RE: [A7_ANALOG_IO_BUS.md](A7_ANALOG_IO_BUS.md).

### 6.3 TMS9914A — HP-IB controller (`tms9914a.go`, `NewTMS9914A()`)
8 registers (`wregs[8]`/`rregs[8]`), `inputBuf`. **MODELED:** input drain (`DIR`/offset `0x60E`
pops one byte, clears IS0.BI), `Push([]byte)` (sets IS0.BI), `PendingInput()`, `IRQAsserted()`
= `(IS0&IMR0)|(IS1&IMR1) != 0`. **AUXCR** (offset `0x604`): only `swrst` (cmd 0, set bit) honored
(clears IS0/IS1); other aux commands stored, not executed. **GAP:** talker/listener state machine,
handshake, faithful IS0/IS1/ADSR/BSR semantics, CDOR transmit.

### 6.4 FrontPanel — front-panel µC (`frontpanel.go`, `NewFrontPanel()`, implements `bus.Device`)
Keys + RPG; IRQ3 on event. `regs[0x20]`, `pending`/`consumed` flags. Offset = `addr & 0x1F`.

| Offset | Read | Write | Status |
|---|---|---|---|
| `0x1B` status | `regs[0x1B] &^ 0x02` — always clears busy bit1 ("data ready") | store | STUB |
| `0x17` | `regs[0x17]`; **read sets `consumed=true`** (run loop drops IRQ3) | store | MODELED |
| `0x01,0x03…0x15,0x17` | matrix nibble regs | store | MODELED |
| `0x1D`/`0x1F` | control strobes | store | STUB |

API: `InjectMatrix([6]byte)` packs the 6-byte physical matrix into the 12 nibble regs (mirrors
ROM `0x3AB52`), sets `pending`; `SetBit(byteIdx,bit)` presses one bit; `Release()` clears regs
`0x01–0x17`; `Pending()`=`pending && !consumed`. **GAP:** the semantic key-code map (which matrix
bit = which physical key) is not decoded — injection is by raw bitmap.

### 6.5 SweepEngine — video-ADC sweep model (`sweepengine.go`, `NewSweepEngine()`)
Feeds `0xFFF200` (when `SweepActive`). Faithful semi-physical analog via `pkg/emu/analog`
(`SpectrumModel` = thermal noise + 300 MHz CAL + injected tones; `Detector` = dBm→count).
Defaults: span **0..2.9 GHz**, **401 points**, ref **0 dBm**, **1 MHz RBW**, CAL on, full-scale
`videoADCFull = 0x1FF`.
- `levelToADC(dBm)` = clamp(`(dBm-(RefLevel-80))/80 * 0x1FF`, 0, 0x1FF) — 80 dB display window.
- `bucketPeakDBm(p)` sub-samples 32 points across each point's frequency bucket (catches narrow CW).
- `DetectADC()` returns the count for the current point and advances (wraps → continuous repeat).
All **MODELED**; `cmd/tracedemo` renders it as an overlay (the firmware's own trace-draw is
DLP-blocked — §8).

### 6.6 SystemID — strap constants (`systemid.go`)
Four `uint16` constants read at `0x73C/73E/77C/77E` (§5.6). MODELED for IDNUM=8593 only;
option bits / I/O-board descriptor are GAP (not wired).

---

## 7. HD63484 ACRTC display (`pkg/emu/device/hd63484/`)

The display controller is the **HD63484 "ACRTC"** (U301 = `1820-6351`; U305/U306 = 2×256-Kbit
SRAM = 64 KB VRAM). The legacy name "SCI" persists in the code; the protocol at `0xFFF5FC/5FE`
IS the HD63484 command set.

### 7.1 Display geometry (from firmware init table ROM `0xA95E`) — MODELED
- VRAM 1024×512 bits (`PaintRowPixels=1024`, `PaintHeight=512`, 64 KB).
- **Displayed raster = 512×256** (MWR1=`0x40` → 512 px/line; VWW=`0x100` → 256 lines; ZFR=0,
  no zoom; single base layer). `VisibleWidth=512`, `VisibleHeight=256`.
- **Output framebuffer = 512×384** (`DisplayWidth=512`, `DisplayHeight=384`): the 256 raster
  lines are upscaled ×1.5 vertically — the analog CRT's 4:3 stretch. Off-screen VRAM (rows ≥256,
  e.g. the `0x4400` back-buffer fill at MAR `0x4000`) is not displayed.
- **Display-start / page-flip:** RenderFrame scans VRAM from `displayStartRow()` = `RAR1/MWR1`
  (the base raster-read address, decoded via the AR protocol — §7.2). The firmware sets RAR1=0
  in the boot and never page-flips, so the display-start is static at row 0 (front buffer) and the
  render is unchanged; if the firmware ever re-points RAR1 at the back-buffer the displayed buffer
  follows. There is **no `CPY` / back-buffer→front copy** in the firmware stream — the back-buffer
  content is prepared but never selected. MODELED (static here); locked by `TestDisplayStartPageFlip`.
- **Y-axis flip:** the firmware draws **Y-up** (Cartesian, bottom-left origin: REF/AT at large Y,
  CENTER/SPAN at small/negative Y). The pixel-write path flips it to raster Y-down:
  `vramY = drawYOrigin - firmwareY` (`drawYOrigin=219`), mapping firmware Y[-22,~205] into the
  visible window with both annotation blocks on-screen. So REF/AT renders at the top, CENTER/SPAN
  at the bottom — matching the real instrument.

### 7.2 Command port `0x5FC` / status `0x5FD` / data `0x5FE`
- **Status `0x5FD`** = constant `0x27` (bits 0,1,2,5 ready) — STUB (instant-complete chip).
- **Command/data** parsed by `decoder` (`parser.go`) — a FIFO state machine. Modeled commands:

| Opcode | Cmd | Params | Status |
|---|---|---|---|
| `0x0000` | ORG (origin) | 2 | parsed; **not applied** to coords (GAP) |
| `0x08RR` | WPR (write param reg RR) | 1 | MODELED |
| `0x0CRR` | RPR | 0 | consumed |
| `0x1800` | WPTN — glyph (count `0x000A`) or pattern-RAM | 15 / 1+N | MODELED (glyph blit) |
| `0x8000/0x8400` | AMOVE/RMOVE | 2 | MODELED |
| `0x8800/0x8C00` | ALINE/RLINE | 2 | MODELED (Bresenham) |
| `0x9000/0x9400` | ARCT/RRCT | 2 | MODELED (outline) |
| `0xA000/0xA400` | AFRCT/RFRCT | 2 | MODELED (filled) |
| `0x9800/0x9C00` | APLL/RPLL (polyline) | **1+2N** (count-prefixed) | framed, **consume-only** (drawing needs ORG — GAP) |
| `0x5800` | block-fill (POST RAM test) | 3 | MODELED (`dmem` fill) |
| `0x5C00` | SCLR (selective clear) | 3 | framed consume-only (GAP) |
| `0xCC00` | DOT | 0 | MODELED |
| `0xC000` | CRCL | 1 | MODELED |
| `0xF000/0xF400` | CLR/SCLR | 3/1 | MODELED |
| `0xE000` | PAINT | 1 | no-op (GAP) |
| other | unknown | — | counted (`UnknownCmdHist`); ~141/boot residual |

  Parser desync was reduced 11285→141/boot by framing the count-prefixed polyline family.

- **Block read-back (`0x5FE` read):** the POST display-RAM test (ROM `0xD6B2`) block-fills VRAM
  (`0x5800`), rewinds the read pointer (MAR `0x4000/0x0000`), then issues RD commands and reads
  `0x5FE` to verify. Modeled by `dmem` (32768-word display memory) + `blockFill` + `ReadData` —
  this closes the 16th POST bit (`f612.7`). **MODELED.**

### 7.3 Rendering (`render.go`, `draw.go`, `wptn.go`) — MODELED
- `vram` (foreground: graticule, glyphs, dots) + `bgVram` (background `0x4400` dot texture, routed
  off-screen). `RenderFrame()` samples the 512×256 visible window, ×1.5 vertical stretch → 512×384
  RGBA. Lit = amber `fgColor`; background dim.
- **GAP:** ORG drawing-origin not applied; dotted line-style attribute not modeled (grid renders
  solid); the **spectrum trace is not drawn** (firmware DLP-blocked — §8).

---

## 8. Known gaps (summary)

| Area | Status | Note |
|---|---|---|
| Spectrum **trace draw** | GAP | The deep DLP blocker. Sweep mechanics + completion flag (`befa` bit13) work, but the firmware never enters continuous-sweep MEASURE mode (`0xB0EC`≠spectrum), so the sweep-trace DLP source (→ slot `0x12CA`→`0x5ECEE`→`__GTTDRW`) is never scheduled. Visual via `cmd/tracedemo` overlay. Full map: [DRIVETICK_BLOCKER.md](DRIVETICK_BLOCKER.md). |
| ADC +2VREF cal | GAP | `analogBus` ch2 placeholder → ADC-TIME/2V/GND annunciators cosmetic. |
| A25 Counterlock freq | GAP | `a7IOBus` non-status registers are latches only → FREQ-related status approximate. |
| Installed options / I/O-board | GAP | SystemID option bits + `0xFFF110` HP-IB descriptor not wired → boot banner shows model only. |
| HP-IB bus protocol | GAP | TMS9914A is input-drain only; no talker/listener state machine or transmit. |
| Front-panel key-code map | GAP | Matrix injection by raw bitmap; semantic key→bit map not decoded. |
| PIT / PPI / sweep DAC regs | STUB | Backing-store only; enough to pass boot polls. |
| Display ORG | GAP | Drawing-origin (ORG command) not applied; minor left-margin X offset. |
| Dotted grid | MODELED | Per-line WPTN stipple applied in drawLine (0x1111 dotted minor lines, frame solid). |

---

## 9. Boot & test entry points

- **`Machine.New8593A(romImage)`** wires the map above; **`CPU.Reset()`** then
  **`Machine.BootToOperating(maxCycles)`** (LoopBreaker + periodic IRQ5) is the canonical boot
  (~5.7M cycles to the operating region). **`BootToOperatingFaithful`** runs the real
  ROM-checksum + march-RAM test with no LoopBreaker.
- Phase gates (in `internal/emutest` + `pkg/emu/machine`): DiffCores (Musashi==Unicorn prologue),
  boot milestones, `TestMachineBootScreen` (pixel-compares the 512×384 golden),
  `TestPOSTSelfTestPasses` (asserts POST `0x0000`).
- Diagnostic tools in `cmd/`: `crtdiag` (CRT geometry), `dlpsched` (DLP/trace blocker),
  `optprobe` (option straps), `caltrace` (cal NVRAM), `dispstream` (HD63484 command stream),
  `tracedemo` (analog-trace overlay), `post`/`failcode` (POST), `gdbserver` (GDB remote stub).

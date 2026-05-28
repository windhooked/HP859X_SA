# HP 8593A Firmware — Structural Analysis (M68K)

> **⚠ STALE — pending Rev L re-analysis (2026-05-28).** This document was
> written against the **17.12.90** development build (now archived under
> `hp8593a_eeproms/legacy_17.12.90/`). The canonical firmware is now **Rev L
> 98.06.15 Opt-027** (1 MB image; reset PC=0x1B34, SP=0x00FF948A). Specific
> PCs below (sweep gate 0xF768, cal selector 0x11630, IRQ handlers 0x1520/
> 0x1B58 etc.) belong to 17.12.90 and **will move** in Rev L; their meaning
> and the structural framing (subsystems, dispatch table, MMIO map) still
> hold, but every PC needs re-verification against the regenerated
> [`rom.asm`](rom.asm). Hardware-facing facts (MMIO addresses, CalNVRAM at
> 0x200000, IRQ vectors 0x64–0x7C, U114 PAL decode) are firmware-independent
> and remain correct. Sections marked **(stable)** below survive the
> revision change; sections marked **(needs Rev L)** must be re-derived.

Companion to the full disassembly listing [`rom.asm`](rom.asm). This documents
the firmware's structure (memory map, vectors, dispatch, main loop, subsystems)
recovered from static analysis (rizin) cross-checked against runtime tracing of
the emulator. For the decoded SCI **display** and front-panel **keyboard**
protocols see [`research.md`](research.md) §7 and §8.

## How this was produced (reproducible)

```bash
go run ./cmd/dumprom/ /tmp/rom_gold.bin        # canonical image from the four Rev L Intel-HEX dumps
rizin -a m68k -b 32 -e cfg.bigendian=true -B 0 \
      -i scripts/rom_analyze.rz /tmp/rom_gold.bin
# listing: pD 0x100000 @ 0  (whole 1 MB Rev L ROM, linear disasm, analysis-annotated) -> docs/rom.asm
```

`cmd/dumprom` reconstructs the image from the four Rev L EEPROM HEX dumps via
`romloader` (the gold source — **not** any committed `rom.bin`).
`scripts/rom_analyze.rz` configures the arch, flags hardware-fixed addresses,
seeds the reset vector, and runs `aa/aap/aac/aar`. Firmware-specific PC seeds
(IRQ handlers, main loop) were removed from the script during the Rev L switch
because most of them have moved; they will be re-flagged here as they are
located in the regenerated disassembly.

**Reliability caveats (rizin m68k backend):** instruction decoding, the vector
tables, the dispatch table, and **call/jump xrefs are reliable**. Automatic
**function-boundary detection is not** — the firmware's jump-table tail-call
style makes rizin chain functions together (reported sizes are nonsense, e.g.
240 KB). Treat the recovered function *entry points* as real and the
*boundaries* as approximate; the linear listing is authoritative for bytes.

## Image facts

- **Rev L 98.06.15 Opt-027** (canonical). 1 MB (1048576 B), 4 × 256 KB 27C020 EEPROMs, M68000 big-endian.
- Reset vector (offset 0): **SP = 0x00FF948A**, **PC = 0x00001B34**.
- Bank layout (PCB-dependent): lower bank = U24 (MSB) + U7 (LSB); upper = U23 (MSB) + U6 (LSB). See `pkg/emu/romloader/romloader.go`.
- Legacy 17.12.90 build (archived): 512 KB (4 × 128 KB 27C010); reset SP=0x00FF88BE, PC=0x00000B3E. Banks: lower = U23+U6, upper = U24+U7 (note the bank swap vs Rev L).

## Memory map (24-bit bus) — **(stable across revisions)**

| Range | Region | Notes |
|---|---|---|
| `0x000000–0x0FFFFF` | ROM | 1 MB read-only (Rev L); the 17.12.90 build only used 0x000000–0x07FFFF |
| `0x200000–0x20FFFF` | **CalNVRAM** (A16A1 battery-backed SRAM) | 64 KB; selected by U114 PAL's `LCAL` (`/MA23·MA21·/MA20`); see `pkg/emu/device/calnvram.go`. Stores cal constants, model/serial, DLPs. Blank → "CAL: USING DEFAULT DATA". (17.12.90 framed this as "RF/IF hardware" — the PAL decode in 2026-05-28 disproved that.) |
| `0xEF4000–0xEF401F` | Front-panel µC | keys + RPG + LED readout; IRQ3 (§8). PAL `LRTC` select. |
| `0xEF8000–0xEF80FF` | MC68230 PIT | HP-IB/serial; IRQ4 reads `0xEF8002` (PSRR). PAL `LKBD` select. |
| `0xFEC000–0xFEFFFF` | RAM (march-test lower) | 16 KB; also vector 17 handler at `0xFEC006` |
| `0xFF0000–0xFFEFFF` | RAM | stack (Rev L SP `0xFF948A`; 17.12.90 was `0xFF88BE`) + variables |
| `0xFFF000–0xFFFFFF` | MMIO | PPI, sweep, SCI display (HD63484 ACRTC), TMS9914A HP-IB |

### MMIO register map (from runtime probing + listing flags)

| Addr | Function |
|---|---|
| `0xFFF000–00F` | 82C55A PPI (front-panel I/O; control `0xFFF007`) |
| `0xFFF110` | HP-IB status (bits 8–11 masked `0xF00` in IRQ4) |
| `0xFFF200` | sweep-start latch (IRQ1/6 write) |
| `0xFFF300` | sweep-status (**bit 12** = sweep ready — polled at `0xF608`) |
| `0xFFF400` | sweep/ADC DAC output (IRQ5 writes) |
| `0xFFF5FC/5FD/5FE` | SCI display: command / status / data FIFO (§7) |
| `0xFFF600–61F` | TMS9914A HP-IB (2-byte stride) |
| `0xFFF634` | timer-tick ack (IRQ5 writes 1) |
| `0xFFF70A–718`, `0xFFF750–766` | sweep/IF DAC + control latches |

## Exception & interrupt vector table (`0x000–0x0FF`)

`0x19D4` is the shared default handler (`movem.w (A7),D0-D3; movem.w D0-D3,$FEC000; …`).

| Vec | Addr | Target | Meaning |
|---|---|---|---|
| 0/1 | `0x00/04` | SP=`FF88BE`, PC=`0B3E` | reset |
| 2–4 | `0x08–10` | `0x19D4` | bus/addr/illegal → default |
| 5 | `0x14` | `0x197E` | (rte) |
| 17 | `0x44` | `0x00FEC006` | **RAM-installed** handler (runtime) |
| 18–23 | `0x48–5C` | `0x1B5E,1B6A,1BB2,1BD6,1BF2,1C18` | **vectored** sweep-sample handlers (IRQ6 family, selectable mode) |
| 25 | `0x64` | `0x1520` | **IRQ1** — sweep update (writes f200/f300/f400/f70a) |
| 26 | `0x68` | `0x197E` | **IRQ2** — rte (unused) |
| 27 | `0x6C` | `0x1582` | **IRQ3** — front panel (sets `bd77.0`, acks `0xEF401B`) |
| 28 | `0x70` | `0x1248` | **IRQ4** — HP-IB (reads `0xEF8002`) |
| 29 | `0x74` | `0x19E2` | **IRQ5** — timer tick (`bfca++`, `bfce++`, `bfb9.7`) |
| 30 | `0x78` | `0x1B58` | **IRQ6** — sweep sample capture (RAM-dispatch via `bfea`) |
| 31 | `0x7C` | `0x1980` | **IRQ7** — NMI/reset path |

### TRAP vectors (`0x80–0xBF`)

Real handlers: TRAP#1=`0x135A`, #2=`0x1410`, #3=`0x14DA`, #4=`0x12C8`,
#5=`0x19A4`, #6=`0x1248` (= the HP-IB handler). #0 and #7–#15 → default.

## Dispatch jump table (`0x200–0x3FF`) — the firmware "syscall" table

Low memory above the TRAP vectors holds a table of 6-byte `jmp $XXXXXX.l`
entries, invoked as `jsr $0200+6n .w`. This is the firmware's vectored
subroutine-dispatch mechanism (and what the project's "227 TRAPs" note really
refers to — most are these dispatched calls, not M68K `TRAP` ops). rizin's xrefs
make the callers explicit. Notable entries:

| Entry | → Handler | Role (from callers/behaviour) |
|---|---|---|
| `0x214` | `0x3AE1E` | marker/readout accessor (many `0xA9xx/0xE9xx/0xF3xx` callers) |
| `0x220` | `0x3A9EC` | front-panel related (called from main `0x10456/0x10896`) |
| `0x226` | `0x39378` | per-cycle service (called from main `0x103A6`) |
| `0x244` | `0x1EFDE` | softkey/menu dispatch (14+ callers) |
| `0x274` | `0x3AB52` | **front-panel key read** (callers: main `0x108A2`, accessor `0xA82E`) |
| `0x2A4` | `0x18B60` | trace/marker math (7 callers in `0xD5xx`) |
| `0x2CE` | `0x16688` | common math/format (13 callers) |

## Code structure & main loop

The reset path `0x0B3E` opens into one enormous routine (`fcn.00000b3e`) that
rizin cannot cleanly segment — it contains init, the main loop, and most
per-cycle service inline. Key landmarks inside it:

- **Main loop spin: `0x51B0`** — `btst #7,$bfb9.w; beq 0x51B0`. Waits for the
  timer flag `bfb9` bit 7, set by the **IRQ5** handler (`0x19EC`) when the
  sub-counter `bfce` wraps. On the flag it does one service pass then re-spins.
- **Per-cycle sweep/display service: `0x10300`** (hot) — drives `0xFFF300`
  sweep state, calls dispatch `0x226`/`0x36A`, reads timer `bfca`.
- **Key consumer: `0x108A2`** — `bclr #0,$bd77; jsr $274` (read key matrix) then
  `movem.l D0-D1,$8f1e`. *Gated*: not reached in the operating state the
  emulator currently reaches (see §8 / Open questions).

Hot pages in the idle loop (runtime PC histogram): `0x5100` (dominant),
`0x10300`, `0xF700`, `0xB000`, `0x7B00–7F00`, `0x4500`, `0x3C00/3F00`.

## Subsystems

- **Timer / scheduler** — IRQ5 (`0x19E2`) increments `bfca`/`bfce` 32-bit
  counters, decrements `bfd2`/`bfda`, sets `bfb9.7` on `bfce` wrap (the main-loop
  heartbeat), and pushes the sweep DAC (`f400`). Emulator drives this by injecting
  periodic IRQ5 (`Machine.BootToOperating`).
- **Display** — SCI command/data at `0xFFF5FC/5FE`; in-band MOVE/glyph protocol.
  Driver: send-word poll `0x7378/0x7394`, glyph blit `0x7390`, colour decode
  `0x73EA`, init `0x73C2`. Full decode: research.md §7; model: `device.SCIDisplay`.
- **Sweep** — IRQ1 (`0x1520`) updates sweep latches each tick; IRQ6 (`0x1B58`
  + vectored variants `0x1B5E…0x1C18`) captures samples (RAM-dispatched via
  `bfea`). Status bit 12 of `0xFFF300` gates the operating-loop entry at `0xF608`.
- **Front panel / keys** — IRQ3 (`0x1582`) latches `bd77.0`; reader `0x3AB52`
  (via dispatch `0x274`) reads the `0xEF4000` nibble register file into the
  `0x8F1E` key-matrix bitmap. Full decode: research.md §8; model: `device.FrontPanel`.
- **HP-IB** — IRQ4 (`0x1248`, also TRAP#6) services the TMS9914A (`0xFFF600`) and
  MC68230 PIT serial (`0xEF8000/8002`).

## Boot-state baseline & peripheral mock status

`machine.New8593A` + `Machine.BootToOperatingFaithful` reaches the operating
loop and renders the screen **with no LoopBreaker** — the ROM checksum (~5M
cycles), march RAM test (~8M), and calibration delay run to completion against
the real ROM/RAM, driven only by the injected IRQ5 timer tick (~20M cycles to
the operating loop). So the loops are not hardware-mock gaps; the LoopBreaker
(`BootToOperating`) is only a test-speed shortcut. This is the "boot state" we
build peripherals on. Locked in by `TestMachineBootFaithful`.

Peripheral mocks, by fidelity — the ledger for "implement as we go":

| Peripheral | Addr | Model | Fidelity | Gaps |
|---|---|---|---|---|
| ROM | `0x000000` | `bus.ROM` | exact (gold image) | — |
| RAM + TestRAM | `0xFF0000`,`0xFEC000` | `bus.RAM` | exact | — |
| Timer | (IRQ5) | injected tick | functional | not a real chip; cadence approximate |
| SCI display | `0xFFF5Fx` | `device.SCIDisplay` | text decode real; status hard-"ready" | palette colours; vector/graticule opcodes |
| Sweep/IF | `0xFFF200/300/400` | hard override (bit 12 ready) | **stub** | no real sweep clock or ADC/video trace data |
| 82C55 PPI | `0xFFF000–007` | RAM-backed | passive | no key-matrix scan semantics |
| Front panel | `0xEF4000` | `device.FrontPanel` | read protocol real; IRQ3 verified | consume gate; key-code map |
| MC68230 PIT | `0xEF8000` | zeroed RAM | stub | HP-IB serial handshake not modelled |
| TMS9914A HP-IB | `0xFFF600` | RAM-backed | passive | no GPIB bus behaviour |
| RF/IF status | `0x200000` | OnFault → 0 | **none** | sweep/ADC status reads return 0 |

Candidate next peripherals (each unlocks visible behaviour): **sweep/ADC** →
trace + graticule on screen; **front-panel consume gate** → key input;
**HP-IB** → remote (SCPI) control.

### Sweep / trace path — findings & plan (in progress)

Goal: get the trace + graticule drawn. Acquisition mechanism (decoded):

- **IRQ1** (`0x1520`) steps the sweep DACs each tick (`98d8`→`f200`, status→`f300`,
  LO/IF DACs `f70a/f716/f400`).
- **IRQ6** (`0x1B58`, *vectored* — modes via vectors `0x48–0x5C`: done/sample/
  pos-peak/sample2/neg-peak; `bfea` mirrors the current mode) captures the
  detector sample from `0xFFF200` into the trace buffer `A5++` until `A5 >= bfe6`.
- The sweep is armed by setting `bfe6` (buffer end, e.g. `0xFFA44A`) and the mode
  handler; the idle/done state is `bfea=0x1B6A`, `bfe6=0` (what a between-sweeps
  snapshot shows).

**The gate (the missing peripheral):** the per-cycle sweep run at `0xF768` does
`cmpi.w #1,$200a3c.l; ble skip`. **`0x200A3C` is the RF/IF hardware "points
acquired" counter** (compared against `0x191`=401; used as count/index at
`0x5AAC`, `0x9B7E`, `0xE7BE`, `0xE7EC`, …). It lives in the **`0x200000` RF/IF
region, currently unmapped → reads 0 → every sweep is skipped** (hence the clean
status screen, no trace).

**Progress (cmd/sweepdrive prototype):** mapping a mock at `0x200000` advances
the firmware into its **power-on self-test (POST)**. With `0x200A3C`=1 the screen
shows the full POST diagnostic: `FAIL: DF2F 1FFFFFFF`, `CAL: USING DEFAULT DATA`,
`rev 17.12.90`, `COPYRIGHT HP 1986-90`, and the ADC cal failures **`ADC-TIME FAIL`
/ `ADC-GND FAIL` / `ADC-2V FAIL`** (message-pointer table at `0x189D0`; strings
at `0x1C29F`). So the firmware is a **Dec-1990 build** stuck on the POST screen;
the spectrum display comes only after the ADC self-cal passes.

`0x200A3C` is **not a simple counter** — it is a *multiplexed* RF/IF board-ID /
status register (the firmware writes a select at `0x116FA` then reads; values map
to model codes 0x2190/2192/2193 at `0x11630`). A constant mock therefore cannot
satisfy it: the board-ID read, the sweep-points read, and the ADC-timing read all
go through the same address and expect different responses — which is exactly why
the constant=401 mock fails ADC-TIME.

**Trace render path (decoded):** `0x13D0E` (called from the sweep path at
`0xF7AC`) processes/draws the trace. It loops `0x979a` (display point index) up
to `0x200a3c` (points acquired), reading per-point arrays in low RAM
(`(idx*4 − 0x43be)` ≈ `0xFF95xx`, `(idx*4 − 0x6a3c)`, `(idx*8 − 0x6afc)`) and
emitting MOVE + **LINE (`0x8801`)** to draw the trace polyline — i.e. the trace
uses the *same* line opcode now rendered by `SCIDisplay`. Gated on `0x9502`
bit 15 (new-data flag). So the render side is ready; only the data is missing.

**The coupled gate (confirmed):** the trace needs BOTH conditions, which conflict
under a naive mock:
- `0x200A3C` must be `>1` or the sweep run at `0xF768` skips (no acquisition, no
  trace) — this is the clean "status screen" state (`0x200A3C`=0).
- but once `0x200A3C`>1 the firmware runs the **ADC self-cal**, which fails
  (ADC-TIME/GND/2V) under a constant mock and keeps it on the POST screen.

So acquisition + render are understood; the remaining work is a faithful
`0x200000` RF/IF + ADC model:
1. `0x200A3C` as a sweep-position counter that ramps 0→401 paced to the
   programmed sweep time (firmware times it via the `bfca` counter — ADC-TIME).
2. ADC reference reads (`0xFFF200` and/or `0x200xxx`) returning correct counts
   for the ground and 2V references — clears ADC-GND / ADC-2V.
3. Detector samples on `0xFFF200` during the sweep (noise floor + injectable
   signal), captured by IRQ6 into the trace buffer → the polyline draws.
   (IRQ6 is *vectored* — the capture handlers are selected by the sweep hardware
   supplying vectors 0x12–0x17; the Musashi adapter currently delivers IRQ6 only
   as the autovector → `bfea` dispatch, so the firmware must arm `bfea`/buffer.)

`cmd/sweepdrive` (constant `0x200A3C` mock) reaches the POST screen but cannot
satisfy this coupled timing/reference behaviour; the device is not wired into
`machine` until it can.

## Self-test / auto-cal hardware interface (mapped via cmd/hwprobe)

To reach the operating (run) level the firmware must pass its power-on self-test
and auto-calibration. Mapping every hardware read/write from reset through the
self-test (real `HP8593AMMIO` so boot proceeds; `0x200A3C`=1 mock so the test
runs) shows what must be satisfied:

- **`0x200000` region — 64 KB linear scan, reads return 0.** The self-test reads
  the *entire* 64 KB option/RF region (every address, ~3×, no writes). Returning
  0 ⇒ "absent/blank" ⇒ the self-test FAIL bitmask (`FAIL: DF2F 1FFFFFFF`, i.e.
  ~all 29 bits set) and `CAL: USING DEFAULT DATA`. This is the big blocker: the
  region is a hardware presence / cal-data / option-ROM space that on a real unit
  holds checksummed data we do not have.
- **ADC self-cal** (ADC-TIME/GND/2V): needs `0x200A3C` to ramp paced to sweep
  time and ADC reference reads to return correct counts (still failing).
- **MMIO registers the cal/sweep drive** (`0xFFF000`): sweep DACs/latches at
  `0xFFF700–73E` (IF/LO tune), `0xFFF200` (sweep DAC / ADC sample, R/W),
  `0xFFF300` (sweep status, polled ~10K×), `0xFFF400` (DAC), `0xFFF716` (IF gain),
  82C55 PPI `0xFFF001–007`, TMS9914 `0xFFF600–61F`. `0xEF8002` (PIT serial)
  polled ~4K×.

**Progress toward run level (option: reach run level, UNCAL tolerated):**

- **Run level is reached** — with the RF/IF region mapped the firmware *loops in
  its operating code* (page 0x5100 dominant), it is NOT halted on self-test. The
  POST/FAIL text is drawn by the operating loop, not a halt screen.
- **Self-test FAIL screen gate:** the `FAIL: <hex> <hex>` display (routine
  0x10040) is shown only when `~[0xFFF610] | (~[0xFFF612] & 0xEC) != 0` (0x1005A
  `beq` skips it). Forcing `0xFFF610`=0xFF, `0xFFF612`⊇0xEC skips it — but that's
  a bypass.
- **A16 chip inventory (from CLIP 08590-90256, parts list in `docs/8590 CLIP 5963-2591.pdf` pp.172–184):**
  - **U301 = `1820-6351` IC63484-8S ACRTC** — **Hitachi HD63484 ACRTC**, with U305/U306 = 2 × 256-Kbit SRAM = **64 KB video RAM**. The display is **the HD63484** mapped at `0xFFF5FC`/`0xFFF5FE`; what was called the "SCI in-band protocol" (`0x8000`=MOVE, `0x8801`=LINE, glyph block, `0x9000`, `0xCC00`, `0x08RR` register writes) is in fact the **HD63484 command set**, fully documented in the Hitachi HD63484 datasheet (research.md §5). Vector / box / circle / paint commands can be implemented properly from the datasheet, not just empirically.
  - **U25 = SHUNT-DIP 8 POSITION** — the **board-ID / options DIP switch**, the likely source of the `0xFFF618` serial config (8 bits → model + installed options).
  - **U64, U201 = `1826-0609` ANALOG MULTIPLEXER 8 CHNL** — the **ADC input mux** (selects GND / 2V-REF / signal for cal).
  - **U47 = `1826-1522` A/D 12-DGT** — the **house ADC** (12-bit hybrid, used for cal-reference measurements).
  - **U57 = `1820-3832` MC68230 PIT** — confirms the PIT at `0xEF8000`.
  - **U56 = `1820-4675` PRIORITY 8-TO-3 ENCODER** — the IRQ-level encoder.
  - **U12 = `1820-6245` 16-bit MPU 16 MHz** — M68000.
  - **U40 / U504 = `1820-5108` / `1820-4669` PROGRAMMABLE INTERVAL TIMER 8 MHz CMOS** — additional timers.
  - **U117 = 14.7423-MHz crystal** — system clock.
- The A16 schematic foldout is **not in this CLIP** — that's in the 08590-90316 assembly-level service guide. Without it, the DIP-switch bit map and ADC-mux control register are TBD (iterate or source the schematic).

### **Major reframing from the U114 memory-decode PAL** (`hp8593a_eeproms/PAL_8590-80159.zip`)

The 22V10 memory-decode PAL source (`HP80159.PLD`, dumped from a real 8591A by Patrick Schäfer) gives the **definitive A16 address map**. Outputs (active-LOW):

| Signal | Equation | Address region | What we'd called it |
|---|---|---|---|
| `LRM0`/`LRM1` | `/MA23 * /MA21 * MA18 * RLW` / `* /MA18 *` | `0x000000–0x07FFFF` (ROM, mirrored to 2 MB) | ROM ✓ |
| **`LCAL`** | `/MA23 * MA21 * /MA20` | **`0x200000–0x2FFFFF`** | **CAL NVRAM** (not "RF/IF hardware"!) |
| `LCAB`/`LIOA`/`LSTB` | low-half MA23=0 control regions | `0x300000`+ | I/O bus (mapping TBD) |
| `LRTC` | `MA23 * /MA20 * /MA15 * MA14` | `~0xEF4000` region | **front-panel processor** (`device.FrontPanel`) |
| `LKBD` | `MA23 * /MA20 * MA15 * /MA14` | `~0xEF8000` region | **MC68230 PIT** |
| `LUSER` | `MA23 * MA20 * (/MA16 + /MA15 + /MA14)` | user/option memory | TBD |

**The PAL signal names are misleading** (chosen for an earlier design): `LRTC` actually selects the front-panel chip, `LKBD` selects the MC68230 PIT. And the MMIO at `0xFFF000` is **not decoded by this PAL** — there's a second decoder PAL (likely U115 `08590-80239`, not yet sourced) for the I/O bus / HD63484 / sweep DACs etc.

**This *completely* reframes the trace work.** Previously I'd been treating `0x200000` as an "RF/IF hardware register region" with `0x200A3C` as a "points-acquired counter". Wrong on both counts:

- **`0x200000–0x2FFFFF` is the calibration NVRAM** — the A16A1 battery-backed SRAM holding cal constants, DLPs, model/serial, error-correction data.
- **`0x200A3C` is a byte offset INTO the cal NVRAM** — a stored cal/sweep-config value. The `cmpi.w #1, $200a3c.l` "sweep gate" at `0xF768` is testing **cal-data**, not hardware state. Returns 0 because the NVRAM reads blank → "no cal" → skip sweep → `CAL: USING DEFAULT DATA`.
- So "ADC self-cal" with mux-aware ADC hardware isn't the trace gate — **a valid cal-data image is**. With one, the firmware would use the cal constants, accept the sweep config, and proceed to operate.

**Path to a live trace** (now properly understood):
1. **Get a valid cal-data NVRAM image** (64 KB) from a real instrument — or synthesise one with valid checksums + cal-table values.
2. Map the cal NVRAM with that data at `0x200000`.
3. Existing IRQ-driven sweep acquisition + detector synth should then render the trace polyline (via the already-implemented LINE/DOT commands).

The cal-data file format is bounded RE: the firmware reads structured offsets (0xA3C, etc.), so the layout can be reverse-engineered from the read sites. The earlier mikrocontroller.net thread mentions **firmware archives at Rev C through Rev L** (RAR files in `hp8593a_eeproms/Firmwares/`, can't extract without `unrar`/`7z`); some forum users had cal-data dumps.

**Firmware ROM facts (from `Firmwares/08590-90324_Firmware Note.pdf`):** datecodes are `DD.MM.YY` for revisions `27.10.92` and earlier, `YYMMDD` after. Listed revisions start at **Rev A = 20.03.90**; **our rev is `17.12.90`, so it predates Rev A — an early development build**. Standard ROM sets are 4×1Mb (our case, U6/U7/U23/U24) or 2×2Mb (later, U7+U24 only).

### From the 08590-90316 service guide (`docs/Agilent-HP_8592D - Service Guide.pdf`)

- **A16U25 is a service-mode bus jumper, NOT the board-ID source** (p.250 free-run procedure: removing U25 + grounding A16TP1 pin 7 puts the M68K in free-run mode, reading MOVEQ as a NOP and stepping address lines A1..A23). So U25 jumpers data-bus lines for diagnostics; the board-ID/option-config read at `0xFFF618` comes from somewhere else (likely the card-cage IOB bus or a custom HP interface chip such as U106 = `1820-5384`).
- **A16 ADC input multiplexer channel map (Chapter 9, p.375 — definitive):** the mux on A16 selects:
  - Card-cage analog (`CRD_ANLG_2`)
  - `VIDEO_IF` — the detected 21.4 MHz IF signal from A14, *via* the positive-peak detector OR bypassed in sample mode (the firmware switches per-detector-mode).
  - **+2 V reference** — for ADC cal "graticule at top screen" (this is the `ADC-2V` check).
  - **ACOM (analog ground)** — for ADC cal "graticule at bottom screen" (this is the `ADC-GND` check).
- **The 2VREF and GNDREF references originate on A16** (NOT from other boards). The service guide's "**2V REF DETECTOR**" and "**GND REF**" service-diag routines (Chapter 4 p.240–241) are the ADC cal: they select the corresponding mux channel and run a sweep; if the captured trace is at top (for 2V) / bottom (for GND), pass — else ADC-2V/ADC-GND FAIL.
- **System I/O fabric** (Chapter 5 A15 motherboard mnemonics, p.273+): A16 talks to other boards over a 16-bit **IOB0–IOB15** bus with **LBIO** (low bottom-box) / **LTIO** (low top-box) strobes. Notable system signals routed through the A15 motherboard:
  - `VIDEO_IF` (A14 detector → A16 mux), `CRD_ANLG_2` (card-cage → A16 mux), `DISCRIM` (A25 → A7 via A16) — analog inputs.
  - `HSWP` (sweep gate, TTL high = sweeping), `EXT_HSWP` (external sweep), `LINE_TRIG`, `SWEEP_RAMP` (0→+10V).
  - `WRUP` (power-up sync), `LPWRON`.
- This pins the ADC-cal model exactly: pick a mux-channel control register, return ~0 when channel=GND/ACOM, return near-max-count when channel=+2VREF, return real detector data when channel=VIDEO_IF.

- **Board-config / presence detection (the proper mechanism, 0x23A0):** the
  firmware does NOT take `0xFFF610`/`0xFFF612` as inputs — it *computes* them.
  At 0x23A0 it **serially shifts in a config word from `0xFFF618` bit 0**
  (`st f618`; loop 0x23B2 reads bit 0 five times → D6 bits 15,11,10,9,8;
  `lsr`/`ori #$80`/write-back shifts the serial line), reads status `0xFFF614`
  and `0xFFF616` (→ `bc76` bits 13/12), then drives `0xFFF610`/`0xFFF612` from
  `bc76`. So the **honest fix is to model `0xFFF618` as a serial board-ID/option
  register returning the correct config word** (+ f614/f616 status) so the
  firmware sees the installed boards as present; f610/f612 then follow. The exact
  config bits map to installed boards/options (would come from the CLIP schematic
  or by iteration).
- **Sweep arms at `0x200A3C` ≥ 2:** the firmware sets `bfea`=0x1B5E (sample
  capture mode), buffer `A5`=0xFFA128..`bfe6`=0xFFA44A (401 words). Injecting
  IRQ1 (step) + IRQ6 (sample) with detector data on `0xFFF200` advances A5
  (samples captured) and grows the trace (Lines 15→41). **ADC-TIME clears** when
  the sweep is driven.
- **Remaining gates:** `ADC-GND` / `ADC-2V` (ADC reads at ground / 2V references
  — need mux-aware detector values), then `FREQ UNCAL` / `MEAS UNCAL`.
  `cmd/runlevel` is the iteration harness.
- **Limit of black-box injection (important):** the ADC annunciators are
  *actively-evaluated cal results*, sensitive to sweep timing — ramping
  `0x200A3C` + IRQ6 re-broke ADC-TIME. And the trace will not draw from injected
  captures alone: the raw IRQ6 buffer (`A5`) must be *processed* into the display
  array (~`0xFF95xx`) and the render (0x13D0E) is gated on `0x9502` bit 15
  (sweep-complete). Producing a clean live trace therefore needs the ADC-cal
  routine RE'd properly (reference-select register + expected values + timing)
  and the capture→process→display-array→render chain understood — not parameter
  tuning. That is the concrete next effort.

**Honest scope:** "fully clean cal" means (a) the `0x200000` region returning
valid checksummed data for every self-test bit, (b) the ADC returning correct
reference/timing for ADC-cal, and (c) the auto-cal routines (CAL FREQ/AMP/YTF/…,
strings at `0x3EE4E`+) each measuring "in-spec" hardware. That is the largest
subsystem in the firmware and likely needs the real instrument's cal-data image
or extensive per-check reverse-engineering. Tooling: `cmd/hwprobe` maps it.

## Key polling — current status (2026-05-27)

The front-panel **read protocol is fully decoded and modelled** (`device.FrontPanel`
at `0xEF4000`; IRQ3 delivery verified; matrix reconstruction round-trips in unit
tests). What does **not** yet work end-to-end: in the operating state the emulator
reaches, the firmware never *consumes* a key. Evidence:

- IRQ3 fires the handler (`0x1582`), which latches `bd77` bit 0 — but `bd77`
  reads `0x05` (bit 0 already set) and stays latched, because the consumer
  `0x108A2` (`bclr #0,$bd77; jsr $274; movem.l D0-D1,$8f1e`) is never executed.
- A 3M-step PC histogram of the idle loop shows it living in the sweep/display
  service (`0x5100`/`0x10300`/`0xF700`/`0xB000`); the `0x10880` block containing
  `0x108A2` is never entered. The path into it is gated by a condition not met in
  this state (likely the sweep-complete / `bfce`-wrap cadence or a mode flag).

So `Machine.PressKeyMatrix` correctly injects the matrix + delivers IRQ3 but
returns `false` (key not consumed); `TestFrontPanelKeyReadChain` is skipped.
Resolving the gate is deferred — see Open question #1. This is the authoritative
status; older notes in research.md §8 and the memory say the same.

## Open questions / next steps

1. **Key-poll gate.** The key consumer `0x108A2` is never reached in the operating
   state the emulator reaches (`bd77.0` stays latched at `0x05`; main loop stays in
   the `0x51B0`/`0x10300` sweep-service path). Need to find the condition that
   routes the main loop into the `0x10880` block. Likely tied to the sweep-complete
   / `bfce`-wrap cadence or a mode flag. Blocks `Machine.PressKeyMatrix` end-to-end.
2. **Function boundaries.** rizin over-merges; a Go recursive-descent pass (the
   fallback option) following the dispatch table with proper tail-call/`rts`
   handling would yield clean per-function segmentation if needed.
3. **`0x200000` RF/IF region** — modelled as 0 via OnFault; sweep/ADC reads there
   may need a stub for the trace/graticule path.
4. **SCI palette + vector opcodes** — glyph colours (palette via `0x08xx` regs)
   and the graticule/trace line-draw opcodes are not yet decoded (research.md §7).

## Tooling

- `scripts/rom_analyze.rz` — rizin analysis script (arch, flags, seeds, analysis).
- `cmd/dumprom` — write the gold image from `*top*.bin` for external tools.
- `cmd/disasm <from> <to>` — Musashi-based range disasm (no color, grep-friendly).
- `cmd/findref <word>` — find/disassemble references to a 16-bit value (e.g. an MMIO addr).
- `cmd/displayprobe`, `cmd/keyprobe` — runtime MMIO write/read histograms + SCI stream.
- `cmd/tracestall`, `cmd/renderframe` — boot-stall trace, display render to PNG.

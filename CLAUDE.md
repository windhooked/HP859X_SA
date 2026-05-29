# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A reverse-engineering and emulation project for the **HP/Agilent 859X series spectrum analyzers** (8590/8592/8593/8595/8596). The instruments run **Motorola 68000 (M68K, big-endian)** firmware stored across four byte-wide EEPROMs. The long-term goal is a full virtual HP 8593A instrument that boots the real firmware to its operating UI.

## Commands

```bash
make build                                 # go build ./... (libraries + cgo Musashi)
make tools                                 # build every cmd/<name> into bin/ (see below)
make test                                  # all tests with the macOS DYLD path set
go build ./...                             # build everything (includes cgo Musashi)
DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib go test ./...   # all tests (macOS: libunicorn rpath)
go run ./cmd/dumprom/ /tmp/rom_gold.bin    # write the 1 MB Rev L ROM image from the four .HEX dumps
go test -v ./internal/emutest/             # Phase-0 DiffCores gate (Musashi == Unicorn)
go test -v ./pkg/emu/cpu/musashi/          # Musashi adapter tests (no DYLD needed — pure cgo)
go test -v ./pkg/emu/cpu/unicorn/          # Unicorn adapter tests (needs DYLD on macOS)
```

**`cmd/` tool binaries go in `bin/` (git-ignored).** Build them with `make
tools` (runs `go build -o bin/ ./cmd/...`), then run e.g. `./bin/naturalkey
-trace 2000000`. Do **not** `go build ./cmd/x` from the repo root — that drops
a stray binary in the working dir; use `make tools` or `go build -o bin/
./cmd/x` instead. For one-off invocations `go run ./cmd/x` is still fine (it
builds to a temp dir, not the root).

Two cgo dependencies:
- **Musashi** (vendored in `third_party/musashi/`) — built by `go build` via unity build in `pkg/emu/cpu/musashi/musashi_core.c`; no separate install needed.
- **Unicorn** — dynamically linked native library; install with `brew install unicorn` on macOS; set `DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib` when running Unicorn-linked tests.

## Firmware

**Canonical image: Rev L 98.06.15 Opt-027** (1 MB, 4 × 27C020 EEPROMs). Sourced from `hp8593a_eeproms/*.HEX` (Intel HEX format, parsed on the fly by [pkg/emu/romloader/](pkg/emu/romloader/)). Y2K-compliant; ships in the 08590-60422 firmware kit.

The earlier **17.12.90** development build (4 × 27C010 = 512 KB raw .bin) is archived under [hp8593a_eeproms/legacy_17.12.90/](hp8593a_eeproms/legacy_17.12.90/). All Rev L-specific firmware facts (PCs, IRQ handler addresses) differ from 17.12.90 — see the "Stale 17.12.90 PCs" notes below.

### ROM reconstruction ([pkg/emu/romloader/](pkg/emu/romloader/))

The 16-bit M68K data bus is split across two byte-wide EEPROMs per bank, two banks. **Rev L's bank ordering** (empirically derived from the reset vector — only one ordering gives plausible SP/PC):
- Lower bank (offset 0): `U24 (MSB)` + `U7 (LSB)` — contains the reset vector
- Upper bank (offset 0x80000): `U23 (MSB)` + `U6 (LSB)`

This is **swapped** vs the 17.12.90 build (lower was U23+U6, upper was U24+U7). The chip-position-to-bank mapping on the A16 PCB changed at some point between the two revisions.

`romloader.LoadDir("hp8593a_eeproms")` parses the four .HEX files, interleaves MSB/LSB byte-by-byte per bank, concatenates both banks → 1 MB image. Intel HEX parser ([pkg/emu/romloader/ihex.go](pkg/emu/romloader/ihex.go)) handles extended-linear-address (type 04) and extended-segment (type 02) prefixes; unprogrammed bytes default to 0xFF.

### Emulator layer ([pkg/emu/](pkg/emu/))

Goal: full virtual HP 8593A — CPU + bus + memory-mapped peripherals.

```
pkg/emu/
  bus/          Bus + Device interfaces; 24-bit address-decode mapper; big-endian RAM/ROM
  cpu/          cpu.CPU interface (Reset, Step, Reg, SetReg, SetIRQ)
    musashi/    PRIMARY — cgo adapter over third_party/musashi; autovector IRQ support
    unicorn/    ORACLE — Unicorn-backed adapter for differential comparison only
  device/       timer, intc, keyboard, display, hpib, CalNVRAM at 0x200000
  machine/      machine.New8593A() — wires ROM/RAM/MMIO + devices
  romloader/    Intel-HEX parser + bank interleaver
third_party/musashi/    vendored Musashi M68K C sources
internal/emutest/       DiffCores, LoopBreaker, RunUntilPC harness helpers
```

**CPU core choice:** Musashi (MAME-proven) is the primary core; Unicorn is the differential oracle. The firmware uses heavy TRAPs and autovectored IRQs — requires clean IRQ injection that only Musashi provides.

**Phase-0 gate** (`internal/emutest`): `TestDiffCores_BootPrologue` runs 4 pre-`ORI SR` boot instructions identically through both cores and asserts register-exact agreement. Rev L's prologue: `0x1B34 MOVEA.L (0).w,A7 → 0x1B38 MOVEA.L A7,A6 → 0x1B3A MOVEA.L A6,A5 → 0x1B3C BRA 0x3998` then `0x3998 ORI #0x700,SR`. Unicorn's QEMU-based M68K cannot execute the `ORI #imm, SR` (a known Unicorn limitation); Musashi handles it correctly.

**Phase-1 gate** (`pkg/emu/machine`): `TestMachineBootMilestone_50` executes 50 boot instructions and asserts (a) no Step error, (b) PC stayed in the ROM body, (c) it moved past the reset vector. The original PC-target form was retired in the Rev L switch because exact landing PCs are fragile across firmware revisions; the test now functions as a smoke test for the bus-backed boot prologue. `TestMachineBootDeep` runs 1000 steps without error.

**Phase-2 gate** (`pkg/emu/machine`): `TestMachineBootBulk` runs 20M cycles (with LoopBreaker + periodic IRQ5 injection) and asserts PC ≥ 0xB000. On Rev L this passes in ~1.6M cycles (reaching PC=0xD6CC, the display-init region).

**Phase-4 gate**: `TestMachineBootScreen` boots to the operating loop at 30M cycles and pixel-matches a deterministic golden PNG (Rev L renders ~30 lit pixels — the top status banner; the centre graticule and data labels need sweep/RF emulation that's a separate task). `TestMachineBootFaithful` runs the same boot at 100M cycles **without LoopBreaker** — the real ROM checksum (1 MB) and march-RAM test execute against the actual ROM/RAM, driven only by IRQ5 ticks, and still render the boot banner.

**Cal NVRAM boot access pattern (Rev L, measured via [cmd/caltrace](cmd/caltrace/main.go)):** during a 100M-cycle faithful boot the firmware reads every byte of cal NVRAM **exactly once** (the byte-checksum sweep at ROM 0x454A) and re-reads **offset 0** three more times for the CPU integrity test at ROM 0x44AA–0x44B8 (`move.l ($200000).l, D6; move.l D6, ($200000).l; cmp.l ($200000).l, D6`). Only offset 0 is ever **written**. **No other offset is polled or compared against a constant** — Rev L has no boot-time "gate byte" of the kind the 17.12.90 build exposed at 0x200A3C. The measurement is locked in by `TestCalNVRAMBootAccessPattern` (`pkg/emu/machine`). `CalNVRAM.Synthesize()` is therefore a no-op under Rev L; cal data matters only AFTER boot (frequency sweeps, mode switches, correction tables — paths not yet modelled).

**IRQ5 timer tick injection:** The IRQ5 handler increments a timer counter in RAM (17.12.90: handler at 0x19E2 with `addq.l #1, $bfca.w`; Rev L equivalent at the autovector-IRQ5 entry from the vector table — not chased down explicitly, but the test boot reaches operating-loop range so the handler is functional). Tests and `cmd/tracestall` inject periodic IRQ5 ticks (every N execution chunks) or timer-wait loops never exit. After `CPU.SetIRQ(5)`, run ~400 cycles to service the handler, then `CPU.SetIRQ(0)`.

**Known IRQ handlers (17.12.90 — STALE; Rev L equivalents pending):** IRQ1@0x1520 (sweep update), IRQ2@0x197E (rte only), IRQ3@0x1582 (front-panel), IRQ4@0x1248 (HP-IB), IRQ5@0x19E2 (timer tick), IRQ6@0x1B58 (sweep sample capture), IRQ7@0x1980 (NMI). The autovector table at ROM offsets 0x64..0x7C still exists in Rev L; addresses there are the canonical source — re-derive from [docs/rom.asm](docs/rom.asm) when needed.

**Memory map (24-bit bus — hardware-fixed, stable across firmware revisions):**
- ROM `0x000000–0x0FFFFF` (1 MB read-only — Rev L; 17.12.90 only used 0x000000–0x07FFFF)
- **CalNVRAM** `0x200000–0x20FFFF` (64 KB battery-backed cal SRAM; selected by U114 PAL's `LCAL` = `/MA23·MA21·/MA20`; [pkg/emu/device/calnvram.go](pkg/emu/device/calnvram.go))
- **CalRAM** `0x2FC000–0x2FFFFF` (16 KB scratch RAM; firmware copies cal NVRAM here at boot then uses it as a working buffer — 490 firmware references in [docs/rom.asm](docs/rom.asm) with offsets in 0x000–0xDF5. With this mapped, the trace-buffer pointer A5 properly initialises into the region at `0x2FD508`; without it, A5 stays at a placeholder and the IRQ6 sample-capture handler at ROM 0x4088 can never store samples. The IRQ6 handler also tests `btst #4, $2fc013.l` to pick between "store sample" and "end-of-sweep" dispatch paths.)
- FrontPanel `0xEF4000–0xEF401F` (front-panel μC; PAL `LRTC` select)
- PIT stub `0xEF8000–0xEF80FF` (256 B; MC68230 PIT zeroed RAM; PAL `LKBD` select; IRQ4 reads/writes here)
- TestRAM `0xFEC000–0xFEFFFF` (16 KB; march-test target)
- RAM `0xFF0000–0xFFEFFF` (60 KB; Rev L stack SP=`0xFF948A`)
- MMIO `0xFFF000–0xFFFFFF` (4 KB; 82C55A PPI at +0x000, sweep registers at +0x200/+0x300, SCI/HD63484 at +0x5FC, TMS9914A HP-IB at +0x600, timer-ack at +0x634, **indirect analog-bus at +0x75C/+0x75E** — see below)

**Indirect analog-control bus at 0xFFF75C/0xFFF75E (Rev L):** classic register-select + data port pattern. The firmware writes selects to `0xFFF75C` (observed values: 0x20, 0x90, 0x91, 0x93, 0x95, 0x96, 0x97, 0x9A, 0x9D, 0x9F) then reads/writes the addressed register through `0xFFF75E` — the A16's analog-control hybrid (mux + DAC + ADC readback). In the operating loop, the firmware writes 0x9A to 0xFFF75C and polls 0xFFF75E in a tight loop at ROM 0x5E5FA. The poll body computes `(stack(-1,A6) & low_byte(read)) == stack(+9,A6)`; with instrumented stack values `0x12` and `0x02` respectively, the natural-match low byte is `0x02`. Returning the match value EVERY read fast-exits the loop and the firmware skips its background annunciator-redraw work (degrades render to ~30 lit pixels); returning it NEVER means the firmware never enters the sweep-arm code path (also stalls). **Solution**: model 0xFFF75E as a periodic-match register — return `0x0002` every `indirectMatchEveryNReads` reads (currently 256), so the firmware experiences the same "occasionally ready, mostly busy" cadence a real ADC produces. With this in place, between 30M and 100M cycles the firmware armed the sweep (`FFBF30 = 0x2FD82A`, the trace buffer end at 401 samples; `FFBF34 = 0x40B8`, the IRQ6 capture handler). Injecting IRQ6 after that point successfully fills the trace buffer (verified: A5 advances by exactly 802 bytes = 401 samples per sweep). Documented in [pkg/emu/device/mmio.go](pkg/emu/device/mmio.go). **UPDATE (2026-05-29):** the periodic-match stub was replaced by a faithful **ADC conversion state machine** in [pkg/emu/device/analogbus.go](pkg/emu/device/analogbus.go) — `0x9A` is now a status register (bit0 EOC/data-ready set after a conversion is triggered by the `0x97` DAC-low write, cleared on the `0x9F` result read; bit1 ready, bit2 settled ⇒ `0x06` idle / `0x07` data-ready). This was required because the 8593 boot's PRESET ADC cal (`fcn.5E6E8`) has **conflicting** `0x9A` poll contracts (init poll wants exactly `0x06`; conversion-done polls want bit0 set) that no constant satisfies — see [docs/ANALOG_BUS_MODEL.md](docs/ANALOG_BUS_MODEL.md). With it, the boot clears the analog gate and advances ~10× (to ~49M cycles) into the **startup-DLP execution** (`WININIT`/`WINOPEN`/`MENU` scripts; renders "EMPTY DLP MEM"), where it now derails on a **DLP-interpreter symbol dispatch** at `0x34C94` (token indexes past the table at `ROM[0xA74]=0x71D76` into DLP source text) — the next blocker, in DLP-runtime init (`$bb54` symbol table, set at `0x33AE`). See ANALOG_BUS_MODEL.md §12. `TestMachineBootScreen` + `TestCalNVRAMBootAccessPattern` are SKIPPED pending re-baseline (their budgets run past the derail); `cmd/naturalkey -derail`/`-trace` is the probe.

**Rev L IRQ vector handlers** (from ROM longwords at 0x60–0x7C):
- IRQ1 → `0x002AB8` — sweep update (writes f200/f300/f400; loads sample via jsr to RAM 0xCA)
- IRQ2 → `0x003A94` — rte-only (noop)
- IRQ3 → `0x002B1E` — front-panel
- IRQ4 → `0x002642` — HP-IB
- IRQ5 → `0x003ECE` — timer tick (Rev L equivalent of 17.12.90's 0x19E2)
- IRQ6 → `0x004088` — sweep sample capture: `move.w $f200.w, D7` (read ADC) → optional scaling → vectored dispatch via `move.l $bf34.w, -(A7); rts`. The dispatch pointer at `0xFFBF34` selects between "store sample to (A5)+ until A5≥FFBF30" (capture mode at 0x40B8) and "end-of-sweep flag set" (idle at 0x40C2). After boot, FFBF34=0x40C2 (idle handler); the firmware swaps it to 0x40B8 only when actively sweeping.
- IRQ7 → `0x003A9E` — NMI

**Musashi memory routing:** Musashi's required-extern `m68k_read/write_memory_*` are Go `//export` functions in `bus_callbacks.go` that dispatch through `activeBus` (a package-level `*bus.Bus` set by `New()`). The file has no cgo preamble (cgo forbids preamble + `//export` in the same file). Disassembler stubs in `bridge.c` forward to these same callbacks. Unmapped bus reads return `0` (OnFault handler) to match flat-memory boot behaviour.

**Musashi integration:** Musashi is compiled as a unity build (`musashi_core.c` includes all `.c` TUs). `m68kconf.h` is modified in-tree (vendored — we own it): `M68K_INSTRUCTION_HOOK M68K_OPT_SPECIFY_HANDLER`, `M68K_EMULATE_PMMU M68K_OPT_OFF`. `RESET_CYCLES` drain and `SR` normalisation to `0x2700` are required after `m68k_pulse_reset()`. `m68k_execute(1)` loop for single-step (any M68K instruction takes ≥4 cycles; 1-cycle budget guarantees exactly one instruction).

**Musashi bulk execution and disassembly:** `CPU.Run(cycles int) int` calls `m68k_execute(cycles)` directly (single cgo call vs N calls for N instructions — essential for multi-million-cycle boot tests). `CPU.Disasm(addr uint32) (string, uint32)` calls `m68k_disassemble` through the active bus — used in `cmd/tracestall` to analyse stall points without needing a text disassembly file.

**LoopBreaker** (`internal/emutest`): breaks known firmware delay loops without corrupting functional loops. Exact PC-range based with hysteresis. Rev L entries:
- ROM checksum inner: `0x454A–0x456A` → D0=0 (8× unrolled byte adds + `dbra D0`; outer segment loop at `0x458A` bounds total work)
- March RAM test: `0x4784–0x47F6` → A2=0xFFBFFE (two sub-loops calling shared check body via `jmp (A3)`; A1=0xFFC000)

A calibration delay equivalent to 17.12.90's `0x2420` has not been needed: with the two breakers above plus periodic IRQ5 injection, the firmware reaches the display init at PC=0xD6CC in ~1.6M cycles and the operating loop at ~30M.

**Canonical boot procedure:** `Machine.BootToOperating(maxCycles)` is the single source of truth for booting the image — it runs the chunked `Run()` loop with the LoopBreaker and periodic IRQ5 injection. Tests and all `cmd/` tools call it rather than re-deriving the loop. Call `CPU.Reset()` first.

**Phase-4 (display) gate** (`pkg/emu/machine`): `TestMachineBootScreen` boots to the operating loop and pixel-compares the SCI display framebuffer against a committed golden PNG (`pkg/emu/machine/testdata/boot_screen.png`). The render is deterministic; regenerate intentionally with `UPDATE_GOLDEN=1 go test ./pkg/emu/machine/ -run BootScreen`. **Currently skipped pending Rev L LoopBreaker tuning** (the firmware never reaches the operating loop with stale breakers).

**Display is the HD63484 ACRTC** ([pkg/emu/device/display.go](pkg/emu/device/display.go)). Confirmed from CLIP 5963-2591 (A16 parts list): **U301 = `1820-6351` IC63484-8S ACRTC**, with U305/U306 = 2×256-Kbit SRAM = 64 KB video RAM. The "SCI" naming in the code is a *misnomer carried over from initial RE*; the protocol decoded at `0xFFF5FC`/`0xFFF5FE` IS the **HD63484 ACRTC command set**. `HP8593AMMIO` forwards word writes at offsets 0x5FC/0x5FE to an attached `*SCIDisplay` (name retained for now), which decodes the command stream into an `image.RGBA` framebuffer.

**Decoded HD63484 commands:**
- `0x8000` AMOVE / `0x8400` RMOVE / `0x8801` ALINE / `0x8C00` RLINE / `0x9000` ARCT / `0x9400` RRCT / `0xCC00` DOT
- `0x1800` WPTN (write pattern): with count `0x000A` → glyph packet (2 colour words + 8 bitmap rows + 4-word post-glyph state)
- `0x08RR` WPR (write parameter register): captured per-register; specific values trigger raster-write mode
- **Raster-write mode** (added 2025-05-28): the firmware writes `WPR 0x0C = 0x4000` followed by `WPR 0x0D = 0x0000` to arm video-RAM-write at MAR = 0x4000. The next 16,384 data words are 1bpp pixel data covering a 64×256-cell = 1024×256-pixel area (cell count derived from the `0x003F` / `0x00FF` parameter words the firmware emits earlier). Modelled by a 32 KB `vram` field on `SCIDisplay`; `RenderFrame` composites it into the RGBA framebuffer underneath any glyph/vector overlay. The dominant fill word `0x4400` produces the firmware's screen-background dot pattern; without this decoder the screen rendered as ~136 lit pixels (text only), with it ~20,600.

**Pen colour quirk**: fg/bg words inside a glyph packet (`0x0000` / `0xFFFF`) are palette/pen selectors set up by `0x08RR` register writes, NOT literal RGB565 — until the palette is decoded, lit glyph pixels render in a fixed amber (`fgColor`).

**A16 hardware architecture (from official docs):** the parts list (`docs/8590 CLIP 5963-2591.pdf` pp.172–184) + the 08590-90316 service guide (`docs/Agilent-HP_8592D - Service Guide.pdf`, 674 pp, text-searchable; mislabeled — it's actually the 8590-series guide) document the A16 board. Key chips: **U12** M68000 16 MHz, **U301** HD63484 ACRTC, **U57** MC68230 PIT, **U47** 12-bit ADC (hybrid), **U64+U201** 8-ch analog mux (the ADC input mux), **U25** is a service-mode bus jumper (free-run mode — NOT a board-ID strap), **U114** 22V10 PAL (`08590-80159.JED/.PLD` in [hp8593a_eeproms/PAL_8590-80159.zip](hp8593a_eeproms/PAL_8590-80159.zip)) is the memory-decode PAL — its equations are what told us 0x200000 is CalNVRAM, not RF/IF. ADC input mux selects **CRD_ANLG_2 / VIDEO_IF / +2VREF / ACOM** (service guide Ch.9 p.375); the `ADC-2V`/`ADC-GND` annunciators come from the firmware's `2V REF DETECTOR` / `GND REF` service-diag routines (Ch.4 p.240–241). Full details + status: [docs/rom_analysis.md](docs/rom_analysis.md).

**Diagnostic commands** (`cmd/`): `dumprom [out.bin]` writes the canonical 1 MB Rev L image via `romloader.LoadDir` for external tools; `tracestall [cycles]` boots and disassembles the stall region + IRQ handlers; `disasm <from> <to>` disassembles a ROM range from the binary; `findref <word>` finds/disassembles references to a 16-bit value (e.g. an MMIO addr); `displayprobe [cycles]` histograms MMIO writes and dumps the ordered SCI stream; `keyprobe [cycles]` histograms MMIO reads + unmapped accesses; `renderframe [cycles] [out.png] [--synth]` renders the display to PNG (`--synth` populates CalNVRAM via `CalNVRAM.Synthesize()`); `caltrace [cycles] [top] [--faithful]` instruments `CalNVRAM.Trace` and dumps a histogram of which cal-NVRAM offsets the firmware reads/writes during boot (the tool that established Rev L has no boot-time cal gate byte); `tickprobe` boots, injects IRQ3, then runs the operating tick with fine-grained PC sampling — established that PC 0x18F42/0x18F3E (the deep-block key-flag bclr + parser jsr inside fcn.18568) are NEVER REACHED on Rev L under the 17.12.90-tuned `DriveOperatingTick` pre-arms; `jumptable` extracts the master + secondary dispatch tables and decodes the parser-name table (363 of 410 entries resolve after the secondary table at ROM 0x71E02 was identified — see [docs/DLP_RUNTIME.md](docs/DLP_RUNTIME.md)); `naturalkey [-boot N] [-run N] [-nokey] [-trace N]` is the **Path B** probe — boots naturally, injects a real front-panel key (no forced PC/gate bits), and either histograms post-boot PC regions or (`-trace`) single-steps the analog poll. It established that the **8593-strap boot never reaches the operating loop `fcn.18568`**: it freezes in the `fcn.5E63C` analog `0x9A` status poll, which needs status **bit 0** set but the model returns `0x0006` (no bit 0) — see [docs/DRIVETICK_BLOCKER.md](docs/DRIVETICK_BLOCKER.md) (the current "boot banner" is the firmware *frozen* at this poll, not a live UI).

**Operating loop is C; DLP runtime is its dominant tenant.** `fcn.18568` is the C-coded operating loop (entered via `slot 0x148`/post-boot fall-through; `bra.w fcn.18568` at PC 0x18A88 closes the loop). Inside each iteration it calls `slot 0x72A → fcn.34EE8` (the DLP interpreter step) twice — once on the foreground ring at `0xFFA61C`, once on the alt queue at `0xFFBBA6`. The 297 `jsr fcn.00000d18` (DLP scheduler) call sites are ALL in PC range `0x5FBEA..0x71676` (the DLP runtime + DLP source region); no C code outside that region schedules DLP. Public HP-IB commands resolve to direct C handlers (e.g. `MEASOFF → 0x3EC9A`); the 298 `__`-prefixed DLP-internal commands resolve to 16-byte trampolines (e.g. `__GTMNK → 0x614C6`) that load a ROM source pointer and call the scheduler. Full chain: [docs/DLP_RUNTIME.md](docs/DLP_RUNTIME.md). Both `pkg/emu/machine` tests `TestDriveOperatingTickClearsKeyAndSweepFlags` and `TestSendHPIBPlusDriveOperatingTickDrainsParserFIFO` are SKIPPED under Rev L — empirically (cmd/tickprobe, 1B cycles) the deep-block key-flag bclr at PC 0x18F42 and the parser jsr at PC 0x18F3E are NEVER reached because `fcn.568F6 → fcn.11DF4` enters a 31+ deep nested annunciator/checksum chain that doesn't unwind, and pre-arm cells (`9afb` bit 2, `b1e4`) are actively overwritten by `fcn.568F6` and `fcn.11750`. **Full empirical writeup + three realistic fix paths**: [docs/DRIVETICK_BLOCKER.md](docs/DRIVETICK_BLOCKER.md). The `Machine.DriveOperatingTickUntil(pred, maxCycles)` predicate-driven helper is in place for whichever fix path is chosen.

**Full disassembly + analysis** ([docs/rom.asm](docs/rom.asm), [docs/rom_analysis.md](docs/rom_analysis.md)): regenerate with `go run ./cmd/dumprom/ /tmp/rom_gold.bin` then `rizin -a m68k -b 32 -e cfg.bigendian=true -B 0 -i scripts/rom_analyze.rz -q -c 'e scr.color=0; pD 0x100000 @ 0' /tmp/rom_gold.bin > docs/rom.asm` (needs `brew install rizin`). The listing is a whole-ROM linear disasm (`pD`), analysis-annotated. rizin's m68k entry-point/xref recovery is reliable (vector tables, the `0x200` dispatch/"syscall" jump table); its **function-boundary detection is not** (jump-table tail-call chaining inflates sizes). rom_analysis.md has the memory map, vector/TRAP/dispatch tables, main-loop structure, and per-subsystem entry points.

**Firmware facts (Rev L):**
- Reset vector (image offset 0): SP = `0x00FF948A`, PC = `0x00001B34`
- Memory map: ROM `0x000000–0x0FFFFF`; CalNVRAM `0x200000–0x20FFFF`; RAM `~0xFF0000–0xFFEFFF`; MMIO `0xFFF000–0xFFFFFF`
- Source files: load via `romloader.LoadDir("hp8593a_eeproms")` which parses the four `*.HEX` files. Never load the legacy `legacy_17.12.90/rom.bin` or any other generated artefact as authoritative.

### Generated vs. source artifacts

In `hp8593a_eeproms/`:
- **Source** (Rev L): the four `*.HEX` files (`U6/U7/U23/U24-98-06-15.HEX`).
- **Source** (PALs): `PAL_8590-80094.zip`, `PAL_8590-80159.zip` (memory-decode PAL — keep, this is what told us 0x200000 = CalNVRAM).
- **Source** (firmware archive): `Firmwares/` — the full set of revision .rar archives + extracted HEX files for Rev C/H/J/K/L (incl. Opt-027) + 08590-90324_Firmware Note.pdf.
- **Archive** (legacy 17.12.90): `legacy_17.12.90/` — the four pre-Rev-A `*top*.bin` chip dumps plus their generated `rom.bin`/`rom.hex`/`rom_dump.hex`. No code path loads these.

The reference disassembly lives at [docs/rom.asm](docs/rom.asm) (regenerated from the gold image via rizin — see the "Full disassembly + analysis" note above), with the structural writeup in [docs/rom_analysis.md](docs/rom_analysis.md). Both currently carry a "stale 17.12.90 PCs" banner pending re-derivation against Rev L.

**Live dump** ([pkg/859x/dump.py](pkg/859x/dump.py)) — PyVISA script that reads instrument memory over USB/GPIB via `ZSETADDR`/`ZRDWR?` commands; not part of the Go build.

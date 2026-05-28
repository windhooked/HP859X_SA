# Diagnostic and instrumentation tools

This directory contains every cmd/ binary that ships with the emulator.
Most are throwaway probes built during RE; a few are general-purpose.
Tools are grouped by what they help you do.

All commands assume the working directory is the repo root and that
`hp8593a_eeproms/` is present.

## Run / render

| Tool | What it does |
|------|--------------|
| [renderframe](renderframe/main.go) | Boots, renders the display to PNG. The fastest way to see the current screen output. `go run ./cmd/renderframe/ [cycles] [out.png] [--synth]` |
| [bootnatural](bootnatural/main.go) | Boots without the LoopBreaker — validates the firmware's own RAM/ROM tests pass against the real model. |
| [sweeprun](sweeprun/main.go) | Boots with IRQ6 sample injection once the sweep is armed. Useful for testing the sweep-capture pipeline. |
| [sweeprender](sweeprender/main.go) | Like sweeprun, but calls `Machine.DriveOperatingTick` after the pre-tick budget to drive the operating tick into its trace-render path. Produces the actual spectrum-on-screen demo. |
| [runlevel](runlevel/main.go) | Runs the firmware to its operating loop and reports run-level state. |

## Disassemble

| Tool | What it does |
|------|--------------|
| [disasm](disasm/main.go) | Range disassembly of the ROM image at any PC. `go run ./cmd/disasm/ FROM TO` (hex). |
| [dispatch](dispatch/main.go) | Resolves any firmware dispatch-table slot to its target + first three instructions. Use this whenever you see `jsr $XXX.w` in disassembly. `go run ./cmd/dispatch/ SLOT_HEX [END_SLOT_HEX]`. |
| [findref](findref/main.go) | Finds and disassembles ROM sites that reference a 16-bit value (an MMIO address, a constant, etc). |

## Probe MMIO / display

| Tool | What it does |
|------|--------------|
| [displayprobe](displayprobe/main.go) | Records every MMIO write during a run, histogrammed by address + value. The first stop for finding which MMIO offset a peripheral lives at. |
| [abusprobe](abusprobe/main.go) | Specialised tracer for the A16 analog-bus pair (0xFFF75C select / 0xFFF75E data). Captures boot + operating-loop accesses, grouped by `(phase, PC, select)`. Built the analog-bus model. |
| [scianalyze](scianalyze/main.go) | Decodes the SCI / HD63484 command stream emitted to 0xFFF5FC/E. |
| [hwprobe](hwprobe/main.go) | Generic hardware-port reads/writes scan during boot. |

## Probe analog / sweep

| Tool | What it does |
|------|--------------|
| [adcprobe](adcprobe/main.go) | Captures ADC-related accesses (0xFFF200 sweep ADC + the analog-bus). |
| [sweepprobe](sweepprobe/main.go) | Captures sweep-state register activity (0xFFF200/300/400/634, FFBF34, etc). |
| [sweepdrive](sweepdrive/main.go) | Drives a synthesised sweep trigger; useful when investigating where the firmware reads sweep data. |

## Probe key / front panel

| Tool | What it does |
|------|--------------|
| [keyprobe](keyprobe/main.go) | Reads the front-panel μC at 0xEF4000 and dumps its state. |
| [keystate](keystate/main.go) | Wraps the main RAM with a tracer so every write to bf03 / bf0a / bc67 / befd / befe is recorded with the PC that issued it. Built the key-consumer-chain investigation. |
| [keyfifo](keyfifo/main.go) | Dumps the key FIFO at `$bba6` (read-idx = bbba, write-idx = bbbc). Experiments with manually pushing entries to test the dispatcher's path A vs path B. |

## Probe boot / cal

| Tool | What it does |
|------|--------------|
| [tracestall](tracestall/main.go) | Boots and dumps the PC ranges where the firmware spends most time + disassembles IRQ handlers. |
| [caltrace](caltrace/main.go) | Instruments `CalNVRAM.Trace` to record every cal-NVRAM offset read/written during boot. Built the "Rev L has no boot-time cal gate byte" finding. |
| [tickflags](tickflags/main.go) | Dumps every RAM state flag that `fcn.18568` (the operating tick) tests in its early branches. Pre-arms flags + force-runs the tick + reports how deep PC reaches. Built the `Machine.DriveOperatingTick` primitive. |
| [dumprom](dumprom/main.go) | Writes the 1 MB Rev L ROM image from the four `*.HEX` dumps. Output is `/tmp/rom_gold.bin` by default; used as input to external tools (rizin, etc). |

## Quick-pick by question

- "What does this firmware do at PC 0x1234?" → `cmd/disasm 1230 1260` (or `cmd/dispatch 1234` if you suspect a slot)
- "Where is MMIO offset 0x600 used in ROM?" → `cmd/findref 0600`
- "What's on screen right now?" → `cmd/renderframe`
- "Is the sweep firing? How many samples?" → `cmd/sweeprun` then look at the IRQ6 count
- "Does my analog-bus change affect anything?" → `cmd/abusprobe` before and after
- "Why isn't the operating tick reaching PC 0x18F42?" → `cmd/tickflags` to see which state-flag branch routes execution away
- "Can I actually get the trace on screen?" → `cmd/sweeprender` (uses `DriveOperatingTick`)

## Naming convention

- `*probe` — captures a stream of accesses, dumps a histogram
- `*run` — runs the emulator with periodic IRQ injection, produces an output PNG
- `*state` / `*flags` — dumps and possibly pre-arms RAM state
- `disasm` / `dispatch` / `findref` / `dumprom` — static ROM tools, don't run the emulator

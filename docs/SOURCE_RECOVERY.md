# Source Recovery — Best-Effort Reconstruction

This document captures what we can responsibly say about the HP 8593A
firmware's **likely original source organization**, based on the
machine-code patterns we've observed. It's not a full decompilation —
that would be weeks of work for 1990 functions — but it does give a
defensible high-level picture of the compilation units and shows what
the original C source likely looked like for a handful of key handlers.

## Confidence Levels

| Recovery level | What we can say |
|---|---|
| **High** | Compiler family + general source-file boundaries |
| **Medium** | What each module probably does + module count |
| **Lower** | Specific source-file names (we're guessing based on HP conventions) |
| **Lowest** | Exact original variable names, struct layouts, comments |

## High-confidence findings

- **Compiler**: late-1980s commercial M68K C compiler (most likely
  Microtec MCC68K or Greenhills MULTI). Evidence in `docs/COMPILER.md`
  (or section in `INVESTIGATION.md`).
- **1964 functions** identified in `docs/rom.asm` (extracted via the
  rizin `┌ fcn.NNNNNNNN()` markers).
- **1303 of those (66%)** use standard C-style `link.w a6, #N` /
  `unlk a6` prologue/epilogue; the rest are leaf helpers + hand-asm.
- **112 hypothesized compilation units** when functions are grouped
  by ROM-address proximity (gap > 1KB = new module). C compilers emit
  functions in source order within a file, and the linker places
  object files contiguously — so address clustering is a reasonable
  proxy for source-file boundaries.
- **Hand-written assembly modules** identifiable by region:
  - boot prologue (`0x1B34..0x2000`)
  - IRQ handler bodies (`0x2642`, `0x2B1E`, etc.)
  - boot probes that don't use `link.w a6` (some helpers in
    `0x2000-0x3000`)

## Hypothesized source file layout

`cmd/modulemap` walks the function list, clusters by proximity, and
labels each cluster with a likely purpose based on:
1. Functions we've actually reverse-engineered in this project
2. Standard HP instrument firmware conventions for this era (mid-1990
  development; same firmware structure used in 8568/8566/4195)

Top-level structure (subset shown; run `cmd/modulemap` for full list):

```
vector.s         ROM 0x000000..0x0000C4  M68K exception/vector table
dispatch.s       ROM 0x0000C4..0x001B34  1128-slot master jump table (linker-emitted)
boot.s           ROM 0x001B34..0x002000  reset_pc + boot prologue (hand asm)
irq.c            ROM 0x002000..0x002800  IRQ handler bodies
boot_probe.c     ROM 0x002800..0x003000  SystemID + model probes
math.c           ROM 0x003000..0x004000  multiply/divide/modulo helpers
rom_check.c      ROM 0x004000..0x008000  ROM checksum + RAM march test
uc_io.c          ROM 0x008000..0x00C000  front-panel μC handshake
display_text.c   ROM 0x00C000..0x010000  text/string rendering helpers
var_dispatch.c   ROM 0x010000..0x018000  scalar value-getter dispatch
operating.c      ROM 0x018000..0x01A000  operating tick body
options.c        ROM 0x01A000..0x020000  model + option detection
hpib_fmt.c       ROM 0x020000..0x028000  HP-IB output formatters
cal_data.c       ROM 0x028000..0x030000  calibration tables
recall.c         ROM 0x030000..0x036000  command interpreter + ring consumer
trace.c          ROM 0x036000..0x040000  trace processing, marker math
sweep.c          ROM 0x040000..0x048000  frequency / sweep control
userfn.c         ROM 0x048000..0x04E000  user-function compiler + executor
preset.c         ROM 0x04D000..0x04E000  Initial Preset handler (fcn.520 body)
capture.c        ROM 0x04E000..0x055000  video/sweep capture, IRQ6 path
parser.c         ROM 0x055000..0x05A000  HP-IB parser + command dispatch
softkey.c        ROM 0x05A000..0x060000  softkey label dispatch
dlp.bin          ROM 0x060000..0x070000  compiled DLP source (DATA)
dlp_runtime.c    ROM 0x070000..0x07C000  DLP runtime + interpreter
labels.bin       ROM 0x07C000..0x080000  softkey labels + parser-name tables (DATA)
option_027.c     ROM 0x080000..0x100000  Option 027 (26.5 GHz) extension
```

The `.s` suffix marks hand-assembly modules; `.c` is C; `.bin` is pure
data tables.

## Sample C pseudocode for key functions

These are best-guess C reconstructions of three functions we know well.
They're meant to illustrate what the original source likely looked like,
not literal recoveries — variable names, struct layouts, and comments
are educated guesses.

### preset.c — `IP` (Initial Preset) handler at fcn.520 / fcn.4DF72

The IP handler clears 15+ specific state cells and re-initializes
several mode variables. Original source likely:

```c
/* preset.c — Initial Preset (IP HP-IB command, PRESET hardkey) */

#include "spec_anal.h"

void preset_initial(void) {
    /* clear sweep/trace running flags */
    g_state.sweep_count = 0;
    g_state.trace_running = 0;
    g_state.sweep_arm = 0;
    g_state.sweep_done = 0;

    /* clear all marker state */
    g_markers.active = 0;
    g_markers.delta = 0;
    g_markers.tracking = 0;

    /* reset display routing */
    g_display.refresh_pending = 0;
    g_display.mode = 0;

    /* reset detector / averaging */
    g_detector.mode = DET_POS_PEAK;
    g_detector.video_avg = 0;

    /* clear cal-related transient state */
    g_cal.scale_factor = 0;
    g_cal.amp_offset = 0;

    /* re-init front-panel state machine */
    g_panel.entry_mode = 0;
    g_panel.units_pending = 0;
}

/* called from the master dispatch slot 0x520:
 *   slot_0520:  JMP preset_initial  ; 4EF9 0004 DF72
 */
```

The 15 specific RAM cells `fcn.520` clears
(`0xAD6C/6A/74/72/6E/70, 0xA9AC, 0xB0EC, 0xB058, 0xAD64, 0xB20E,
0xBA5E, 0xA5D4, 0xBF01, 0xBAF8`) are exactly the kind of global state
flags an IP handler would zero in real instrument firmware.

### irq.c — IRQ4 handler at fcn.2642

The IRQ4 handler in M68K assembly we read at PC `0x2642`:

```
0x2642  movem.l d0/a0-a2, -(a7)
0x2646  link.w a6, #0
0x264A  move.w #0x4001, 0xBFFE.w           ; enter-trace marker
0x2650  btst.b #0, 0xB05F.w                  ; is the bridge gated?
0x2656  beq.w 0x26DC                         ;   no → PIT path
0x265A  move.b 0xF160.w, 0xBF05.w           ; latch μC status
0x2660  btst.b #0, 0xBF05.w                  ; data ready?
...
```

Almost certainly compiled from C source like:

```c
/* irq.c — HP-IB / external keyboard interrupt service */

#include "spec_anal.h"

void __attribute__((interrupt)) irq4_handler(void) {
    BFFE = 0x4001;                            /* trace marker */
    
    if (g_state.bridge_armed) {
        unsigned char status = MMIO_F160;     /* μC status byte */
        BF05 = status;                         /* save for handlers */
        
        if (status & 0x01) {                   /* I/O active */
            if (status & 0x02) {               /* data byte ready */
                if (fifo_free(&bc12_fifo) > 1) {
                    MMIO_F130 = 0x0C;          /* μC: ack data byte */
                    fifo_push(&bc12_fifo, MMIO_F140);
                }
            } else if (status & 0x04) {        /* dispatcher request */
                key_dispatcher();
            } else if (status & 0x10) {        /* front-panel data */
                fp_data_reader();
            } else if (status & 0x20) {        /* break received */
                MMIO_F150 = 0;
                hpib_break_handler();
            }
        }
    } else {
        /* PIT keyboard path */
        if ((PIT_8002 & 0x02) && PIT_8002 != 0xFF) {
            if (fifo_free(&bc12_fifo) > 1) {
                fifo_push(&bc12_fifo, PIT_8000);
            }
            /* dispatch on 0x9b20 mode byte */
            switch (g_state.input_mode) {
            case 0: key_dispatcher(); break;
            case 1: keyboard_pit_reader(); break;
            case 2: hpib_secondary_reader(); break;
            }
        }
    }
    
    BFFE = 0x4002;                             /* exit-trace marker */
}
```

### options.c — model detection at fcn.1A3E0

The model dispatcher we found:

```c
/* options.c — boot-time model identification */

#include "spec_anal.h"

#define MODEL_8590  0x218E
#define MODEL_8591  0x218F
#define MODEL_8592  0x2190
#define MODEL_8593  0x2191
#define MODEL_8594  0x2192
#define MODEL_8595  0x2193
#define MODEL_8596  0x2194

void detect_model(void) {
    unsigned long sysid_long;
    unsigned char board_id;
    
    /* clear the 'out of range' fault flag */
    g_state.b212 &= ~B212_OUT_OF_RANGE;
    
    /* extract 3-bit board strap from system-ID MMIO words */
    sysid_long = *(unsigned long *)0xFFBF26;
    board_id = (sysid_long >> 19) & 0x07;
    g_state.board_strap = board_id;
    
    /* model-ID dispatch — CalNVRAM override wins, else use strap */
    if (CAL_RAM_AT(0x2FCB40) == 3) {
        g_state.idnum = MODEL_8596;
    } else if (CAL_RAM_AT(0x2FCB40) == 2) {
        g_state.idnum = MODEL_8595;
    } else {
        switch (board_id) {
        case 3:
            g_state.idnum = MODEL_8593;
            break;
        case 2:
            g_state.idnum = MODEL_8594;
            break;
        case 1:
            g_state.idnum = MODEL_8591;
            break;
        case 0:
            g_state.idnum = MODEL_8590;
            break;
        default:
            g_state.idnum = MODEL_8595;       /* default fallback */
            g_state.b212 |= B212_OUT_OF_RANGE;
            break;
        }
    }
    
    /* further dispatch based on the resolved model goes here ... */
}
```

The C code maps cleanly to the disassembly via:
- `link.w a6, #0` ↔ entry of `detect_model`
- `move.l 0xBF26.w, d6` + `lsr.l #0x13, d6` ↔ `sysid_long >> 19`
- `cmpi.w #3, 0x2FCB40.l` ↔ `CAL_RAM_AT(0x2FCB40) == 3`
- The `bra 0x1A484` (jsr fcn.6862) ↔ the C `switch` statement
- The inline word table at PC 0x1A47A..0x1A480 is the compiler-
  generated case-target table the switch jumps through

## Where this stops

A **proper full source recovery** would need to:

1. Track every global variable through every use-site to recover its
   type and meaning (e.g., is `bc67` a bit-field struct, a flag byte,
   or a packed enum?)
2. Identify call graphs for each function and inline-trace parameter
   types
3. Recover the `*.h` headers — struct/union layouts, enum constants,
   typedef chains
4. Reconstruct comments based on naming patterns + behavior
5. Validate by recompiling and bit-comparing — proves the recovery is
   sound

This is the work of a dedicated decompiler (Ghidra, Hex-Rays) plus
weeks of human review per major subsystem. For 1990 functions across
1 MB of ROM, full recovery is realistically several person-months.

What we HAVE produced at this point — module clustering, key-function
pseudocode, complete dispatch-table map, full receive-chain semantics —
is enough to:

- Document the architecture for future contributors
- Write specific test cases that target known behaviors
- Iterate on the emulator's modeling without needing to re-derive each
  piece from scratch

That's a reasonable resting point for "reverse to likely original
source files" without claiming we've actually decompiled the firmware.

## Tooling

| Tool | Purpose |
|---|---|
| `cmd/modulemap` | Clusters the 1964 firmware functions into 112 hypothesized compilation units with purpose labels |
| `cmd/jumptable` | Extracts the 1128-slot master dispatch table |
| `cmd/softkeys` | Extracts the 113-entry softkey label table (= `labels.bin` data) |
| `cmd/probeoptions` | Verifies the model-detection chain in `options.c` |
| `cmd/dispatch` | Resolves any dispatch slot PC to its handler |

Plus the full `docs/rom.asm` (235k-line rizin disassembly with PC-level
annotations) as the canonical reference for any function not yet
mapped.

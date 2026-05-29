# DLP Runtime — Architectural Role

The HP 8593A firmware is conventionally described as "an HP-IB instrument
that also runs DLP". This document shows that the relationship is the
other way around: the C operating loop is real and infinite, but its
**dominant per-iteration work is draining a DLP execution ring**. Most
"internal" firmware procedures live in ROM as DLP source text and run
under interpreter control. Public HP-IB commands split into two camps
— direct C handlers (e.g. `MEASOFF`), and DLP trampolines that schedule
a ROM-resident DLP script.

This was verified empirically against the Rev L 98.06.15 disassembly
(`docs/rom.asm`); every PC cited below is from that listing.

## TL;DR

| Path | Where |
|---|---|
| Boot init | C, `reset_pc @ 0x1B34` → `fcn.5ACB2` (menu init) → `slot 0x118 → 0x184A2` (post-init) → falls through to operating loop |
| Operating loop | C, `fcn.18568`, infinite (`bra.w fcn.18568` at `0x18A88`) |
| DLP executor entry | `slot 0x72A` (PC `0x72A → 4EF9 0003 4EE8`), called twice per loop iteration |
| DLP scheduler entry | `slot 0xD18` (PC `0xD18 → 4EF9 0003 49B6`), reachable via the secondary parser-name table |
| Public-command path | parser → secondary table (`0x71E02`) → direct C handler (e.g. `MEASOFF → 0x3EC9A`) |
| DLP-internal path | parser → secondary table → trampoline (e.g. `__GTMNK → 0x614C6`) → `fcn.349B6` → DLP source pointer queued for the operating loop |

## The operating loop is C, the DLP runtime is its tenant

`fcn.18568` is reached at boot via `slot 0x118 → 0x184A2`. The body of
the loop polls DLP-ring state in two RAM ranges and runs one DLP step
each iteration before branching back to the top:

```
0x18A54   move.w 0xa630, d0          ; d0 = main DLP ring head
0x18A58   cmp.w  0xa632, d0           ; compare against ring end
0x18A5C   bne 0x18a64                ; if not at end, drain
0x18A5E   tst.w  0xa634               ; DLP-active count > 0?
0x18A62   ble 0x18a72                ; if not, take alternate path
0x18A64   lea.l  0xa61c, a0           ; a0 = main DLP runtime state block
0x18A68   jsr    fcn.0000072a         ; ← run ONE step of DLP execution
0x18A6C   jsr    fcn.00000706         ; finalize step

0x18A72   move.w 0xbbba, d6           ; alt-queue head
0x18A76   cmp.w  0xbbbc, d6           ; alt-queue end
0x18A7A   beq 0x18a84                 ; nothing to do, skip
0x18A7C   lea.l  0xbba6, a0           ; a0 = alt DLP runtime state block
0x18A80   jsr    fcn.0000072a         ; ← run ONE step of DLP execution

0x18A84   bsr.w  fcn.000183e8         ; per-tick housekeeping
0x18A88   bra.w  fcn.00018568         ; ← infinite loop
```

So there are **two parallel DLP execution rings** drained per operating
tick:

- **Foreground ring** at RAM `0xFFA61C` with state in `0xFFA62A..0xFFA634`
  (head / tail / source-pointer / active-count). Set up by the DLP
  scheduler `fcn.349B6` whenever something schedules a DLP procedure.
- **Background queue** at RAM `0xFFBBA6` with head/end indices in
  `0xFFBBBA`/`0xFFBBBC`. Format and intended use not yet decoded — most
  likely sweep/marker/measurement script slots.

## The DLP executor at `fcn.34EE8`

Slot `0x72A → 0x34EE8` is the DLP interpreter step. It is the only
function called by the operating loop's drain paths above; everything
else inside DLP execution recursively chains through it. Direct
callers in the disassembly:

- `reset_pc @ 0x72A`: master-table slot that JMPs to `0x34EE8`
- `fcn.349B6 @ +0x3a` (PC `0x349F0`): from inside the scheduler itself
  (kick-off after queueing)
- `fcn.349A6` (PC `0x349A6 + 0x14`): an alternate entry that bypasses
  the scheduler

No other call sites. The DLP interpreter is reached exclusively
through these three paths, all converging on the per-tick operating
loop drain.

## The DLP scheduler at `fcn.349B6`

`fcn.349B6` is the queue-a-DLP-procedure entry point. It:

1. Saves the count argument at `0xA62A`
2. Saves the source pointer at `0xA62C`
3. Initializes `0xA630 = 0` (head)
4. Sets `0xA632 = arg` (end)
5. Clears `0xA896`
6. Loads `a0 = 0xA61C` (state block base)
7. Calls `fcn.34EE8` to bootstrap interpretation
8. Calls `fcn.34690` for finalize/cleanup

It is reached via `slot 0xD18 → 0x349B6`. The grep for `jsr
fcn.00000d18` in `docs/rom.asm` returns **297 call sites — every single
one in the PC range `0x5FBEA..0x71676`**, i.e., entirely inside the
DLP runtime/source region. No C code outside that region ever
schedules a DLP procedure directly.

What that means: every DLP-procedure invocation chains *through other
DLP procedures*. Cross-call between DLP procedures is the dominant
form of "function call" in the firmware. The seed scheduling — getting
the first DLP into the ring from a fresh boot or a fresh keypress —
comes from the parser path described next.

## Public vs internal command handlers

The parser-name table at `ROM 0x07D500..0x080100` encodes each command
as `(tag, name, NUL, handler-bytes)`. The handler-byte decoding is in
`docs/ROM_DATA_CATALOG.md` (resolved item 1). Slot indices ≥ `0x47D`
resolve through the **secondary DLP runtime dispatch table at ROM
`0x71E02`** (resolved item 4). The handler PC found there is *not
necessarily a DLP trampoline* — it can be either:

### Public commands: direct C

`MEASOFF (80 05 67)` → slot `0x567` → table entry `0x721AA` → handler
PC `0x3EC9A`. Body:

```
0x3EC9A  link.w a6, -4
0x3EC9E  move.l d0, (a7)
0x3ECA0  cmpi.w #1, 0x8(a6)         ; arg-count check
0x3ECA8  pea.l  -0xa(a6)            ; param-parse buffer
0x3ECAE  jsr    fcn.0002F2          ; parse arg
0x3ECB2  ...                        ; do the work
0x3ECEA  move.w #0xFFFF, (a0, a4.w) ; store result
0x3ECFA  unlk a6 ; rts
```

Pure C, no DLP. The handler runs to completion inside the parser's
call. No ring queueing.

### DLP-internal commands: trampoline → scheduler

`__GTMNK (80 05 C5)` → slot `0x5C5` → table entry `0x72322` → handler
PC `0x614C6`. Body:

```
0x614C6  link.w a6, 0
0x614CA  move.w #0x42, -(a7)         ; arg = 0x42
0x614CE  lea.l  0x61484(pc), a0      ; a0 = ROM pointer to DLP source TEXT
0x614D2  jsr    fcn.00000D18         ; → fcn.349B6 (DLP scheduler)
0x614D6  unlk a6 ; rts
```

Three useful lines: load the DLP source address, push the arg, call
the scheduler. **The DLP source for `__GTMNK` is at ROM `0x61484`** —
that's where the compiled DLP text for the procedure body lives. It
gets queued into the foreground ring, then drained by the operating
loop over many subsequent ticks.

This is what makes the architecture interesting: each `__`-prefixed
DLP-internal command is a 16-byte ROM trampoline plus an arbitrarily
long DLP source block. The DLP source region at `0x60000..0x70000` is
not "DLP scripts that the user can edit" — it's the **bulk of the
firmware's user-visible logic**, compiled into a higher-level
representation than M68K and interpreted by the operating loop.

## Reconstructed dispatch path

```
PS/2 byte arrives
        │
        ▼
IRQ4 handler (fcn.2642)
        │  reads MMIO 0xFFEF8000 / 0xF160
        ▼
scancode → ASCII (translator at ROM 0x55C28)
        │
        ▼
push byte to command-line buffer (RAM 0xBC2C+, indices 0xBC32/34/36)
        │
        ▼  (next operating-loop tick drains the buffer)
operating loop fcn.18568
        │
        ▼
parser fcn.58C2E   ← (per char)
        │  on terminator (";", LF), look up the mnemonic in the
        │  parser-name table at ROM 0x07D500..0x080100
        ▼
parser-name handler bytes  →  decoded slot_idx
        │
   ┌────┴────┐
   │         │
slot < 0x468 slot ≥ 0x47D
master table secondary table
(0xC4)       (0x71E02)
   │         │
   ▼         ▼
handler PC   handler PC
   │         │
   ▼         ▼
direct C    ┌── direct C   (public DLP-commands like MEASOFF)
            │
            └── DLP trampoline (16 bytes; loads ROM DLP-source addr +
                                arg, calls slot 0xD18 → fcn.349B6)
                          │
                          ▼
                fcn.349B6 — DLP scheduler
                          │  populates RAM 0xA62A..0xA634
                          │  (source ptr, length, active count)
                          │
                          ▼
                queued in foreground DLP ring at 0xA61C
                          │
                          ▼  (drained by operating loop, one step/tick)
                fcn.34EE8 — DLP interpreter step
                          │
                          ▼
                interprets DLP source text bytes at 0x60000..0x70000
                          │  — may schedule further DLP procedures
                          │  (each contributes more 0xD18 jsr's; this
                          │   is why all 297 `jsr fcn.00000d18` callers
                          │   are in 0x5FBEA..0x71676)
                          ▼
                eventually exits — operating loop continues
```

## What this does NOT mean

The user-facing hypothesis "the device boots the DLP very early and
drives all initialisation and user interaction via DLP" turns out to
be **half right**:

- ✗ The top-level loop is NOT a DLP program. It's the C function
  `fcn.18568`, with `bra.w fcn.18568` as its tail.
- ✗ Boot init is NOT a DLP script. `reset_pc → fcn.5ACB2 → slot 0x118
  → ...` is all C, and it FALLS THROUGH into the operating loop
  without ever calling `fcn.349B6`.
- ✓ But once the loop starts, **every iteration runs DLP code**, and
  the substantial firmware logic (the 298 `__`-prefixed procedures
  and most of what user keypresses trigger) lives in ROM as DLP
  source text and runs under the interpreter.

So a more accurate framing: the firmware is a **C-coded interpreter
host** (boot + IRQs + parser + operating loop) for a **large DLP
program** (the 60 KB of source text at `0x60000..0x70000`) that
encodes most of the instrument's behaviour above the hardware-touching
layer.

## Open follow-ups

- Decode the DLP bytecode/text format. The region `0x60000..0x70000`
  is mostly ASCII, with a sprinkling of byte-pair opcodes — `fcn.34EE8`
  is the interpreter and the format reveals itself there.
- Decode the alternate ring at `0xFFBBA6` — what scripts use this
  queue? Likely sweep-update and marker-math background work.
- Map the 297 `jsr fcn.00000d18` call sites back to which DLP
  procedure each belongs to (their containing 16-byte trampoline) —
  this gives a complete DLP-procedure call graph.
- Decode the 256-byte `(arg, type)` byte-pair table at `0x71D00..
  0x71E00` that sits immediately before the secondary dispatch table.
  Format suggests per-slot prologue selection (arg + dispatch-style
  flag). Without this, the 47 `cmd/jumptable` non-slot entries
  (compound mnemonics + DLP-only `FFXXFFXX` placeholders) stay
  unresolved.

## Cross-references

- `docs/ROM_DATA_CATALOG.md` — open item 4 documents the secondary
  dispatch table at `0x71E02`
- `docs/rom_annotations.md` — boot-menu loader chain (the preceding
  investigation)
- `cmd/jumptable` — implements the master + secondary table lookup
- `docs/rom.asm` — full disassembly with all PCs cited above

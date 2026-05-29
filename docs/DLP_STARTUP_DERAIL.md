# DLP startup-execution derail — DLP-VM instruction index runs out of range

**Status:** mechanism isolated; fix pending. This is the blocker *after* the
A16 analog gate ([ANALOG_BUS_MODEL.md](ANALOG_BUS_MODEL.md)). With the analog
conversion model in place the 8593 boot advances ~10× — into the **startup-DLP
execution** — and derails at ~49M cycles. Probe: `cmd/naturalkey -derail`.

> **Correction (supersedes an earlier draft of this doc):** an earlier version
> claimed the root cause was `$a02 = 0xFFFFFFFF` ("empty DLP" sentinel). That was
> a **misread address**: the firmware's `$a02`/`$a50`/`$a74` are absolute-SHORT
> `0x0Axx` operands → **ROM `0x00000Axx`** (dispatch-table slot longwords reused
> as data pointers), not RAM `0xFFAxx`. Reading RAM `0xFFA02` returned the ROM's
> own *unprogrammed* tail byte `0xFF` (`0xFFA02` as a Go literal is ROM offset
> `0xFFA02`, near the 1 MB ROM end). The real values are valid ROM constants:
> `$a02=0x727CA`, `$a50=0x71682`, `$a74=0x71D76`. So this is **not** an empty-DLP
> / NVRAM problem — the DLP source/tables are ROM-resident factory data.

## Mechanism (one line)

The DLP interpreter executes a **ROM-resident factory DLP** via
`recPtr = $a50 + offsetTable[$a02 + idx*2]`, dispatching `token = word[recPtr]`
through the table at `$a74=0x71D76`. The VM's **instruction index `idx` advances
out of range** of the factory DLP's offset table/records, so `recPtr` lands in
the `ff 02` filler before the dispatch table (`0x71D03`), the token there
(`0x2FF`) indexes *past* the dispatch table into DLP source text
(`ROM[0x72972]="IF;S"=0x49463B53`), and `jsr (garbage)` derails.

## The chain (all PCs from docs/rom.asm, Rev L)

1. **Operating loop** `fcn.18568` drains the foreground DLP ring each iteration
   via `slot 0x72A → fcn.34EE8` (see [DLP_RUNTIME.md](DLP_RUNTIME.md)). State
   block `0xFFA61C`: char-source ring base `$a62c=0x727CA` (= `$a02`), size
   `$a62a=0x4e`, head `$a630=0xd`, tail `$a632=0x4d`.

2. **Instruction exec** `fcn.34B44` (`execInstr`) runs one DLP record. At
   `0x34B6C` it computes the record pointer via `fcn.331cc(idx, &recPtr, $a02)`
   and stores it in `-0x1e(A6)`.

3. **`fcn.331cc`** computes `recPtr = $a50 + word[ $a02 + (idx-1)*2 ]`:
   ```
   0331D8  movea.l ($8,A6), A4        ; A4 = $a02 = 0x727CA  (offset table base, ROM)
   0331DC  move.w  (A4,D0.l), …        ; offset = word[0x727CA + (idx-1)*2]
   0331FE  movea.l $a50.w, A2          ; A2 = $a50 = 0x71682  (record-data base, ROM)
   033202  adda.l  A1, A2              ; A2 = 0x71682 + offset
   033208  move.l  A2, (A3)            ; recPtr
   ```
   Observed: a valid `idx` → offset `0x3EB` → `recPtr=0x71A6D` (token `0x12F`).
   Then `idx` advances → offset `0x681` → `recPtr=0x71D03` (token `0x2FF`,
   invalid). `0x681` is past the real records, into the `ff 02` filler region.

4. **Token dispatch** `0x34C7C–0x34C94`: reads the 16-bit token (byte-assembled)
   from `recPtr`, indexes the dispatch table `$a74=0x71D76` (`A1 = ROM[0x71D76 +
   token*4]`), and `jsr (A1)`. `token 0x12F` → handler `0x3A13A` (the DLP
   **identifier resolve/define** opcode: classifier `fcn.36166` digit/`_`; define
   path `0x3A28A`). The VM spins on `0x12F` ~23 operating-loop iterations, then
   `idx` advances to the out-of-range value → `token 0x2FF` → `ROM[0x72972]`
   (ASCII `"IF;S"`) → `jsr` garbage → **derail**.

## Evidence

- `$a02/$a50/$a74 = 0x727CA / 0x71682 / 0x71D76` are stable ROM constants (the
  jmp-target longwords of dispatch slots `0xA00`/`0xA4E`/`0x71D76`'s slot), read
  correctly at `0x0A02`/`0x0A50`/`0x0A74`.
- Mapping the DLP heap RAM (`DLPRAM`, where `$bb4e=0xFC9C12`/`$bb54=0xFD8DEC`
  live) left the derail byte-identical — necessary but not the cause.
- DAC-varying ADC values left the derail byte-identical.
- The storage subsystem is not involved (see below) — the DLP is ROM-resident.

## Storage-subsystem hypothesis — tested, NOT the cause

The 8590 has a real mass-storage abstraction — **`MSI`** ("Mass Storage Is")
toggling **`INT`** (battery SRAM) vs **`CARD`** (removable SRAM/PCMCIA memory
card via the 08590-60396 reader board): ROM has `MSI CARD|INT`, `HAVE(CARD)`,
`CAT *,CARD`, `NO CARD`, `SAVRCL … STATE/DLP`. We model none of it (only cal
NVRAM). Natural hypothesis: a phantom card / unmapped storage probe makes the
firmware queue a bogus DLP to run.

**Tested with `cmd/naturalkey -faults`** (wraps `Bus.OnFault`, histograms
unmapped accesses during boot). Result: the boot does **NOT** heavily probe any
unmapped storage region — accesses are sparse and explained:
- `0x320000` (32 reads, firstPC `0x4ab4`) + `0x310000` (9 writes, `0x491c`) —
  from the **boot RAM-test/sizing** phase, not a card catalog.
- `0xF00000`/`0xF80000`/`0xFB0000` (≤6 accesses each, firstPC `0x33xx`/`0x34xx`)
  — from the DLP **partition allocator**; sparse, likely garbage-pointer
  artifacts of the bad `$a02` state (extending RAM/DLPRAM down to `0xF00000`
  left the derail byte-identical).

**Conclusion:** the derail is **not** a missing storage device — the boot isn't
reaching for one, and (after the address-misread correction above) the DLP
source/tables are ROM-resident factory data, so NVRAM/CARD storage is not
involved either. It is an internal **DLP-VM instruction-index** bug.

## Open questions / next steps

1. **Why does `idx` advance out of range?** `recPtr = $a50 + word[$a02 +
   (idx-1)*2]`; a valid `idx` gives offset `0x3EB` (`recPtr=0x71A6D`, token
   `0x12F`), but `idx` then advances to a value whose offset (`0x681`) points
   into filler past the records. Trace how `idx` (the `D0` arg to `fcn.331cc`)
   is produced/advanced — it is the DLP VM's program counter into the factory
   DLP's offset table at `$a02=0x727CA`. Either the table's valid length is
   being exceeded (missing end-of-program terminator handling) or `idx` is
   mis-incremented after the `0x12F` (resolve/define) opcode's ~23-iteration
   spin.
2. **Is the factory DLP at `$a50=0x71682` / table `$a02=0x727CA` the one that
   *should* be running here?** Confirm the VM selected the right program (the
   char-ring base `$a62c` also = `0x727CA`). If the wrong program/region was
   selected, the index walks off a shorter-than-expected table.
3. **What is the `0x12F` spin waiting on?** It re-dispatches ~23× before `idx`
   moves; identify the condition (`fcn.36166`/`0x3A28A` symbol lookup against
   `$bb54`) that finally lets it advance, and whether that advance is correct.

## Key addresses

| Symbol | Addr | Meaning |
|---|---|---|
| `$a02` | ROM `0x0A02` = **`0x727CA`** | DLP offset-table base (idx → record offset) |
| `$a50` | ROM `0x0A50` = **`0x71682`** | DLP record-data base (recPtr = `$a50` + offset) |
| `$a61c` | `0xFFA61C` | foreground DLP ring state block (head `+0x14`, tail `+0x16`, src base `+0x10`) |
| `fcn.34EE8` | `0x34EE8` | DLP step (slot `0x72A`); char-ring parser |
| `fcn.34B44` | `0x34B44` | execInstr — runs one DLP record |
| `fcn.331cc` | `0x331CC` | computes recPtr from `$a02` table + `$a50` |
| dispatch tbl | `0x71D76` | `ROM[0xA74]`; `handler = tbl[token*4]` |
| char-read | `fcn.4258`/`0x427C` | reads source char from ring `state[0x10]+head` |

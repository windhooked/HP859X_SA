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

## Where `idx` comes from — DLP name resolution (hash lookup)

`idx` is **not** a sequential program counter — it is the **return value of a
hash-table name lookup**. The menu/window executor (function around `0x35540`,
caller of `execInstr` at `0x35802`) computes:

```
035566  pea     $a7da.w          ; key: source-text cursor ($a7da = 0x5f5f = "__")
03556A  move.l  $a68.w, -(A7)    ; $a68 = ROM 0x8001E (name-string table)
03556E  move.l  $a02.w, -(A7)    ; $a02 = ROM 0x727CA (hash-bucket table, 32 buckets)
035572  move.w  A4, D0           ; A4 = $a896 (item count)
035574  bsr     fcn.320fe        ; D0 = idx = lookup(name)
035578  move.l  D0, (-$c,A6)     ; idx
03557C  ble     $35f04           ; idx<=0 → "not found"; idx>0 → "found"
```

`fcn.320fe` hashes the name (sum of words, `& 0x1f` → 32 buckets, `*4`), indexes
the bucket table at `$a02=0x727CA`, and walks the `$a68=0x8001E` string table.
At the derail it returns **`idx=0x6787`** (treated as "found", since >0) for a
`__`-prefixed DLP variable name. `recPtr = $a50 + word[$a02 + (idx-1)*2]` with
`idx=0x6787` reads the offset word at `0x7F6D6` (inside the parser-table region
`0x7C800–0x80000`) — clearly past the intended bucket table — giving a bogus
offset → `recPtr` in filler → invalid token `0x2FF` → derail.

So the derail is in **DLP `__`-variable name resolution**: the hash lookup
returns a positive-but-wrong index. The `0x12F` opcode (identifier
resolve/define, handler `0x3A13A`, uses `$bb54`) spins ~23× before `idx` moves,
consistent with repeatedly resolving/defining the same name.

## Open questions / next steps

1. **Is `idx=0x6787` a genuine match or a hash-lookup miss returning garbage?**
   RE `fcn.320fe`'s no-match path: does it return the item count / a sentinel
   that the caller wrongly treats as "found" (`idx>0`)? Capture `$a896` (count)
   and the name at `$a7da` at the derail and compare.
2. **Is the `$a02=0x727CA` hash table the right one for this name?** It's a ROM
   static table; confirm the `__`-name being resolved is actually present in it
   (hash the name by hand and check the bucket), vs the name being a *runtime*
   `__WN_*` variable that should resolve via the RAM symbol table `$bb54`
   instead (wrong table selected).
3. **Does the resolution depend on RAM state** (`$bb54`, `$a896`, `$a7da`) that
   our boot hasn't set up correctly — i.e. the startup DLP defines `__WN_*`
   vars before referencing them, and our execution order/state diverges?

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

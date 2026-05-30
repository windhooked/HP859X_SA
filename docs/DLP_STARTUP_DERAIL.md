# DLP startup-execution derail — RESOLVED (68000 address-error emulation)

> ## ✅ RESOLVED (2026-05-30) — `M68K_EMULATE_ADDRESS_ERROR` was OFF
>
> **Root cause:** the DLP interpreter dispatches token-handlers via `jsr (A1)`
> with NO bounds check (`0x34C90`), *deliberately relying on the 68000
> address-error exception* to catch a malformed token. When the startup DLP
> resolves a global routine (`__PKIP`) whose record offset points past the
> populated records, the token (`0x2FF`) indexes past the dispatch table and
> `A1` becomes a garbage **odd** address (`0x49463b53`). On real hardware
> `jsr (odd)` faults → address-error vector 3 (ROM `0x3B16`, a full
> exception-dispatch table) → the firmware aborts the bad DLP step and
> continues. Our Musashi build had `M68K_EMULATE_ADDRESS_ERROR M68K_OPT_OFF`
> (an early-bring-up simplification), so the bad `jsr` executed garbage →
> "derail". *That* is why every ROM constant matched real hardware yet only we
> crashed (the long contradiction documented below).
>
> **Fix:** `third_party/musashi/m68kconf.h` → `M68K_EMULATE_ADDRESS_ERROR
> M68K_OPT_ON`. The boot no longer derails; it runs the full startup DLP and
> **renders the operating UI** (status annunciators, ref-level/atten fields,
> graticule — see `screens/boot_operating_ui.png`) and processes the
> front-panel key flag (`bc67` set+cleared). Full suite green incl. the
> Musashi↔Unicorn DiffCores gate. `TestMachineBootScreen` revived with a new
> golden. The chain below is kept as the (correct) RE record that led here.

**Original status (superseded):** mechanism isolated; fix pending. The blocker
*after* the A16 analog gate ([ANALOG_BUS_MODEL.md](ANALOG_BUS_MODEL.md)) — the
boot advances into the **startup-DLP execution** and derailed at ~49M cycles.
Probe: `cmd/naturalkey -derail`/`-dlptrace`.

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

## Lookup mechanism works — the bug is upstream in name extraction

`fcn.320fe` is sound: it returns **`-1` on no-match**, or **`D6/2`** (positive)
on a real length+string match. The name-table entries it resolves are valid ROM
records of the form `[len][offset][0x3006][name-chars…]`, e.g.:

- `0x7F6D4`: offset `0x3EB`, name `"SR"` → `recPtr 0x71A6D` → token `0x12F` (OK).
- `0x7EDF4`: offset `0x681`, name `"__"` → `recPtr 0x71D03` → token `0x2FF`.

Capturing the lookup **inputs** (`D0`=name length, `$a7da`=key) over the run
shows the lookup is mostly fed **valid DLP names** — `"VARD"` (len 6), `"__PK"`
(len 6), etc. — so the mechanism is fine. But the derail-causing calls are fed
a **malformed key**: `$a7da = 0x3b41035f = ";A._"` (len 3) — it contains a `;`
(DLP statement separator) and a `0x03` control byte. The name extraction
**overran a statement boundary**, capturing `;`+garbage as a "name", which then
hash-resolves to a bad idx → bad `recPtr` → token `0x2FF` → derail.

So the root is **upstream of `fcn.320fe`**: the routine that copies the next
token/name from the DLP source (the char-ring at `0x727CA`, parsed by
`fcn.34EE8`) into `$a7da` produces a malformed name at the point of the derail.

## Deepest finding: `__WN_VARDEF` variable-definition loop, malformed name prefix

The char-ring source (base `0x727CA`) is a list of startup-DLP global decls:
`__VCOM;__PKIP;__SOONIP;__PZREMCMDS;__FFTONIP;__ACPPWRUP;__GTGDRVT;__WN_VARDEF; …`
then code `MIF(HN+1);IF(…`. The derail is inside **`__WN_VARDEF`** — a DLP
routine that **defines window variables in a loop**. Dumping the lookup-name
buffer `$a7da` per call shows the loop (note the incrementing `X/Y/Z`):

```
len=3 ";A.__X"   len=3 "VRD  X"   len=6 "VARDEF"
len=3 ";A.__Y"   len=3 "VRD  Y"   len=6 "VARDEF"
len=3 ";A.__Z"   len=3 "VRD  Z"   len=6 "VARDEF"   …
```

The constant/clean names (`"VARDEF"`, `"__PKIP"`) sit at buffer offset 0 and
resolve fine. But the loop-defined names carry a malformed **`";A" + 0x03`
prefix** (bytes `3b 41 03`) before the real `__X`. With `len=3` the hash in
`fcn.320fe` digests the garbage prefix (`";A" 0x03`) instead of `"__X"`,
returning a bad idx → bad `recPtr` (filler `0x71D03`) → token `0x2FF` → derail.

So the real fault is in how `__WN_VARDEF` builds the per-iteration name into
`$a7da`: it prepends a `;`/type/length record header (`; A 0x03`) that the
lookup should skip but hashes verbatim — or the name pointer/length passed to
the lookup is off by the 3-byte header.

## Open questions / next steps

1. **Decode the `";A" + 0x03 + name` record format** that `__WN_VARDEF` builds,
   and find where the name pointer/length handed to `fcn.320fe` should skip the
   3-byte `; type len` header but doesn't (or where our VM state makes it
   diverge). The working lookups pass the bare name; the loop ones pass the
   header — that delta is the bug.
2. **Confirm whether this is our-emulation divergence or faithful-but-unhandled:**
   does real hardware's `__WN_VARDEF` produce the same `";A.__X"` buffer (then
   the lookup is *meant* to skip the header), or does our VM state corrupt the
   buffer? The incrementing `X/Y/Z` shows the loop itself runs.

## Attempted fix: `fcn.331cc`/`$a50` global-base — NEGATIVE (it's flow/state, not the base)

Verified `fcn.331cc`'s base: `movea.l $a50.w` is `2478 0a50` → absolute-short
`0x0A50` → **ROM constant `0x71682`** (not RAM, not type-selected — the symbol
type `D6` is only *returned* to the caller; the base is always `$a50`). So:

- `$a50` (`0x71682`), `__PKIP`'s name-table offset (`0x681`), the name table,
  and the dispatch table are **all ROM constants** — byte-identical on real
  hardware. The firmware on a real 8593A computes the **same** `recPtr =
  0x71d03` / token `0x2FF` → it would derail identically.
- A real unit does **not** crash here, so the divergence is **not** in
  `fcn.331cc`/`$a50`. It must be that our emulated **flow/state** reaches
  "resolve `__PKIP` and execute its record" where real hardware's flow does
  not — `__PKIP` should be *declared* (not executed), or the scheduler should
  have branched elsewhere after `__WN_VARDEF`.

**Conclusion:** no base-swap fix exists; that would be a blind hack (cf. the
reverted rotating-status attempt). The genuine fix is the upstream flow/state
divergence — the shared `fcn.1B40` scheduler root (define-vs-execute ordering /
ring state). Pinning *which* state differs almost certainly needs the
real-hardware RAM oracle (a `pkg/859x/dump.py` snapshot at this boot point, once
the GPIB cable is available) to compare our DLP-scheduler RAM against correct.

## FINAL MECHANISM (resolved-idx capture)

Logging each lookup's *result* idx (`fcn.320fe` return) vs the idx the dispatch
uses pinned it exactly:

- After `__Z`, `__WN_VARDEF` finishes and the source context switches back to the
  outer global list (`base 0x727ca`, `__PKIP`). The `";A.__Z"` define-context
  lookups correctly resolve to **idx = -1 (not found)** — handled fine.
- `"__PKIP"` then resolves (correctly!) to its real name-table entry, **idx
  `0x6317`** (the entry at `0x7EDF0`: name `…PKIP`, **offset `0x0681`**, type
  `0x3006`). So the hash lookup is right.
- But `recPtr = $a50(0x71682) + 0x681 = 0x71d03`, which is in the **`ff 02`
  filler past the valid record region** (cf. `"SR"` offset `0x3eb` → `0x71a6d`,
  a valid record). So **`__PKIP`'s record at `$a50+offset` is unpopulated** when
  it is dispatched → token `0x2FF` → past the dispatch table → derail.

`__VCOM;__PKIP;__SOONIP;…__WN_VARDEF` are the startup DLP's **global routines**
(the `WININIT`/window scripts). Each must have its record (its DLP code/value)
associated *before* it is executed. In our state `__PKIP`'s record offset
(`0x681`) points past the populated records into filler — i.e. either the
record region for outer globals isn't built yet (a define-before-execute /
scheduler-ordering problem, back to `fcn.1B40`), or `$a50=0x71682` is the wrong
record base for global (vs local `VRD __X`) symbols and a different base should
be selected by the entry's type (`0x3006`).

**Fix locus:** in `fcn.331cc`, how `recPtr` is computed for a global symbol —
specifically whether `$a50` should differ by symbol type, and/or whether the
global records (`__VCOM…`) are populated before execution. The `-dlptrace`
probe (now logging resolved idx + dispatch idx) is the tool.

## RESOLVED MECHANISM (tracer, `cmd/naturalkey -dlptrace`)

The DLP tracer (per-step log of tokenizer name + source cursor + lookup idx +
dispatch token) made the bug unambiguous:

1. The startup DLP runs a fixed ROM source list at **`0x5FB0E`**:
   `VRD __A;VRD __B; … ;VRD __Z;NV` — it declares **all 26 vars `__A`–`__Z`**,
   correctly, one per loop iteration (`VRD __X` → `VARDEF` → dispatch token
   `0x12F`). Source `tail = 0xd0` points at the **`NV`** routine-terminator.
2. After `__Z`, the source buffer empties (`head == tail == 0xd0`). The
   char-reader `fcn.427C` issues **`TRAP #1`** (source-empty → refill).
3. The TRAP #1 handler (`0x286C`) routes the **foreground** ring (`$a61c`) to
   `0x2912`, which is just **`bsr fcn.1B40; rte`** — `fcn.1B40` is the DLP
   **dispatcher** (the same one from the original operating-loop blocker). It is
   supposed to advance the DLP to the next statement and refill the source.
4. **In our state `fcn.1B40` does not refill** — the source base stays at the
   `VRD` list (`0x5FB0E`), so the parser re-reads **past `tail`/`NV`** into the
   trailing bytecode (`3f3c 00d0 41fa…` = `"?<..A."`), tokenizes a garbage 76th
   name, the hash returns a bad idx → `recPtr` in filler → token `0x2FF` →
   dispatch past the table → **derail**.

**So the DLP-startup derail and the original key-dispatch blocker share a root
cause: `fcn.1B40` / the DLP scheduler not advancing.** Fixing the scheduler's
ring/dispatch state (bf03/bf0a/the foreground+alt rings — see
[DRIVETICK_BLOCKER.md](DRIVETICK_BLOCKER.md) and the
`rev-l-key-consumer-chain` memory) is likely to unblock **both** the startup
DLP and front-panel/HP-IB key dispatch.

**Fix locus:** the TRAP #1 → `fcn.1B40` path (`0x2912`) and the DLP scheduler
state it reads. Next: trace `fcn.1B40` when entered from TRAP #1 with the
foreground source empty — determine what ring/queue state it needs to advance
to the next DLP statement (and why it currently falls through without
refilling). The `-dlptrace` probe + a breakpoint at `0x2912`/`0x1B40` is the
tool.

## Tokenizer detail (fcn.33940) — names are length-prefixed

The name tokenizer is `fcn.33940`: it reads source chars (`fcn.4258`), tracks
`[] () ,` and space/`?` delimiters with bracket/paren depth, and at `0x33A52`
writes a **length byte** (`D6 = $a898 - $a896`, the token length) ahead of the
token chars. So DLP names are stored **length-prefixed: `[len][chars]`**. The
`0x03` byte I earlier called "garbage" is in fact the **length** for `"__X"`
(3 chars) — not corruption.

The genuine anomaly: the `__WN_VARDEF` loop-variable tokens are preceded by an
extra `";A"` (`0x3b 0x41`) *before* the length byte, which the simple-name path
(`"VARDEF"`, `"__PKIP"`, stored bare at offset 0) does not have. `fcn.320fe`
hashes the key from `$a7da[0]` in both cases, so for the loop names it hashes
`";A"+len+"__X"` instead of `"__X"` → wrong bucket → wrong idx → derail.

(Correction to an earlier note: in `fcn.320fe` the args are `($8,A6)=$a02`
string table, `($c,A6)=$a68=0x8001E` hash buckets, `($10,A6)=&$a7da` key — the
`$a02`/`$a68` roles were stated swapped earlier; the chain conclusion is
unchanged.)

**Precise next step:** determine where the `";A"` prefix comes from — is it a
2-byte scope/type record header that `__WN_VARDEF` legitimately emits and the
lookup is *meant* to skip (so our bug is the key pointer not being advanced past
it), or is it stale adjacent-buffer data from a prior token that a correct parse
would have overwritten? Decode the `__WN_VARDEF` record layout to decide.

## Strategic note

This is ~8 layers into the DLP bytecode VM. The chain from the analog gate to
here is fully mapped, but fixing it is a focused RE of `__WN_VARDEF`'s record
format + the lookup's header handling — a deep, self-contained task. The analog
model (which got the boot *into* the startup DLP) was the session's large win;
this DLP-startup execution is a separate multi-step subsystem.

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

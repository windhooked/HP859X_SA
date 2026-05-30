# DLP startup-execution derail — RESOLVED: corrupt Opt-027 firmware dump

> ## ✅ ROOT CAUSE (2026-05-30, final) — the Opt-027 EEPROM dump is incomplete
>
> After fully reverse-engineering the DLP VM (see
> [DLP_VM_ARCHITECTURE.md](DLP_VM_ARCHITECTURE.md)), the derail traced to one
> undefined routine, `__PKIP`, whose record body is `0x02FF` padding. Comparing
> the archived firmware revisions (`cmd/reinittrace` + a HEX diff) showed the
> record region `0x71682–0x71D76` differs by 443 bytes between our **Opt-027**
> dump and **plain Rev L 98.06.15** — and in Opt-027 the **U24 (MSB) chip is
> ~half unprogrammed (`0xFF`): 385/768 bytes** in `0x71700+`. Plain Rev L has
> real data there (1/768 `0xFF`). So our **Opt-027 image is a bad/incomplete
> EEPROM dump**, not a firmware that genuinely lacks `__PKIP`.
>
> **Proof:** booting **plain Rev L 98.06.15** (`ROM_DIR=…/Rev. L 98.06.15`):
> `cmd/reinittrace` runs 40M single-steps with **no derail** (final PC `0x2281A`,
> operating code); `cmd/sweeprun` 200M cycles shows **no reboot loop** —
> `corruptAt=-1`, `marchHitsPostArm=0`, `checksumHits=0`, the sweep state stays
> valid (`bf30=0x2FD82A bf34=0x40B8`), `A5=0x301FF2` is a valid trace-buffer
> pointer, and the top PC pages are the real operating loop (`0x22x/0x32x`), not
> the POST/boot churn. Opt-027 derails at `0x34C90` as before.
>
> **So the entire "reboot loop" / sweep-keys-UI blocker was a corrupt firmware
> dump.** Fix: use a complete image — plain Rev L 98.06.15 (in the tree), or
> re-dump the Opt-027 U24 chip from the real instrument. The address-error
> emulation (`M68K_EMULATE_ADDRESS_ERROR`) is correct and stays; with a good
> image there is no derail to recover from. The VM/derail analysis below remains
> the (correct) RE record that led here.

---

# (historical) DLP startup-execution derail — reboot loop on the bad dump

> ## ⚠️ CORRECTION (2026-05-30, later) — the derail is NOT resolved; it reboots
>
> A subsequent instruction-level trace (`cmd/reinittrace`, single-stepping from
> the armed-sweep state) proved the address-error change **did not fix the DLP
> derail — it converted it into a full instrument REBOOT LOOP.** The exact
> captured path:
>
> ```
> 034C94  jsr (A1)            ; A1 = garbage 0x49463B53 ("IF;S" = DLP source text)
> 003B18  (addr-error vec 3)  ; jsr to a non-existent/odd long → address error
> 003BA6 → 002B3A             ; bus/addr-error dispatch + check
> 003DA4  move.w #$b902,$bff8  ; recovery: set restart-magic
> 003DB8  bra 0x3998          ; ← REBOOT (boot prologue)
> 003998 … 0039BC  jsr $43ba  ; → POST: destructive RAM test fills 0xFEC000–0xFFC000
> ```
>
> So the firmware's *own* address-error handler **reboots the instrument** on
> the bad DLP `jsr`. Every ~19M cycles the startup DLP re-derails → address
> error → reboot → POST re-runs the destructive march RAM test over live RAM →
> the sweep/trace state (`$bf30/$bf34/$befa`, the IRQ6 write pointer `A5`) is
> wiped. The "operating UI" in `screens/boot_operating_ui.png` is genuine but
> **re-rendered fresh on each boot pass** before the next derail — it is a
> boot loop, not stable operation. This also explains the unstable UI, the
> flashing FAIL annunciators, and why the sweep can never fill a trace.
>
> **The address-error emulation is still correct and kept** (`m68kconf.h`
> `M68K_EMULATE_ADDRESS_ERROR M68K_OPT_ON` — it is faithful HW behaviour). But
> the real bug below — the DLP VM's `idx` advancing past the factory-DLP
> program — remains the open blocker. Fixing it is what makes the instrument
> stay in stable operation (and unblocks sweep/trace + front-panel keys).
>
> _Superseded banner (kept for history):_ the earlier text claimed "the boot no
> longer derails; renders the operating UI … processes the front-panel key
> flag." That observation was real but mis-interpreted as stable operation; it
> is actually one pass of the reboot loop.

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

## Source-include stack + unresolved-global root (2026-05-30, `cmd/reinittrace`)

Single-stepping the dispatch trajectory into the derail (`cmd/reinittrace`)
pinned the *semantic* cause — it is an **unresolved DLP global (`__PKIP`)**, not
a raw pointer bug:

- The DLP runtime keeps a **source-include stack**: depth at `$a634`, 10-byte
  entries `(size, base, head, tail)` at `0xFFA636 + 10·n`. The pop is at ROM
  `0x34690`–`0x346C4` (`head==tail` ⇒ `subq.w #1,$a634`; reload `$a62a..$a632`
  from the parent entry).
- At the derail the stack is: **n=1** = `base=0x05FB0E head=0xCB tail=0xD0`
  (the `VRD __A;…VRD __Z;NV` source), **n=0** = `base=0x727CA head=0x06
  tail=0x4D` (the **outer** factory startup source, ASCII `"__VCOM;_PKI…"`).
- Trajectory: the outer source (`0x727CA`) **includes** the `VRD __A..__Z;NV`
  source (`0x5FB0E`); that include declares `__A..__Z` (the token-`0x12F`
  identifier-resolve handler runs ~23× with a constant `idx=0x6787` while head
  walks `0x1F→0xCF`), is consumed (`head→tail=0xD0`), and **pops** back to the
  outer source. The outer source's next token then resolves the global
  **`__PKIP`** to record `idx=0x6317` → offset `0x681` → `recPtr=0x71D03` (ROM
  filler) → token `0x2FF` → garbage `jsr`.
- So `__PKIP` is **referenced but its record body is empty**. Comparing the ROM
  records confirms it:
  - `__VCOM` (name @`0x7F044`) → record-offset `0x479` → `recPtr=0x71AFB`, whose
    words are real tokens `01 57 / 01 58 / 01 59 / 01 ff` → handlers
    `0x158→0x5FBDE`, `0x159→0x5FC9A` (valid; `__VCOM` executes).
  - `__PKIP` (name @`0x7EDEE`) → record-offset `0x681` → `recPtr=0x71D03`, which
    is inside a **`0x02FF` filler gap** (`ff 02 ff 02 …`, i.e. MSB chip `0xFF`
    unprogrammed / LSB chip `0x02`) that pads up to the dispatch table at
    `$a74=0x71D76`. Token `0x2FF` → `dispatch[0x71D76 + 0x2FF*4]=0x72972` →
    `0x49463B53` (off the table, into source text).
- **Confirmed root:** the factory power-up routines listed in the outer startup
  source (`__PKIP;__SOONIP;__PZREMCMDS;__FFTONIP;__ACPPWRUP;__GTGDRV;__WN_VARDEF;…`)
  have **names in the ROM table but empty (filler) record bodies**. They are
  meant to resolve through a **RAM DLP symbol table populated during boot**; the
  screen literally shows **"EMPTY DLP MEM"**, so that table is empty and the
  name lookup (`fcn.320fe`) falls back to the ROM name table → the filler record
  → token `0x2FF` → derail → reboot. `__VCOM` works only because its record
  happens to live in the populated part of the ROM table.
- **Refinement (`fcn.320fe` lookup instrumentation):** the name lookup uses two
  tables — a RAM symbol table at `0xFFBF66` (user vars `__A..__Z`, count grows as
  they declare) and a ROM built-in table at `0x08001E`. `__PKIP` is looked up in
  the **ROM built-in table and IS found** (`fcn.320fe` returns `-1` only on
  miss; we get `idx=0x6317`, not `-1`). Its record offset `0x681` is programmed
  in ROM (at `0x7EDF6`) but points into the **intentional `0x02FF` padding** that
  fills the record region's tail up to the dispatch table `0x71D76`. So
  `__PKIP`'s *name + offset* are in ROM, but its *body* is **not** in the ROM
  record region (base `$a50=0x71682`) — only `__VCOM`'s (and the earlier
  routines') bodies are.
- **Ironclad conclusion:** this is all fixed ROM, so real hardware resolves
  `__PKIP` to the same blank record and would take the same address error — and
  the recovery reboot is deterministic (`0x2B3A` returns `ROM[0xB8]=0x7FFF0000`,
  always `≥0` → `bra 0x3998`). Therefore **real HW does not reach this `__PKIP`
  dispatch with the blank record.** Either (a) the factory routine *bodies* are
  loaded/compiled into the record region (or a RAM shadow of `$a50`) by a boot
  step we don't model — leaving ours blank; or (b) execution diverges before
  `__PKIP` (e.g. `__VCOM` does more than declare vars). The `$a50`/`$a02` bases
  are ROM constants in *our* run, so (a) implies a RAM-relocated base on real HW.
- **Next step (needs fresh trace or the real-HW oracle):** trace `__VCOM`'s body
  (handlers `0x5FBDE`/`0x5FC9A`) to see whether it compiles/loads the following
  routines, and check whether `$a50` is meant to be RAM-relocated for built-ins.
  The GPIB oracle (`pkg/859x/dump.py`, cable ~3 weeks out) would settle it by
  dumping the live record region. (The earlier `M68K_EMULATE_ADDRESS_ERROR`
  change only changed the *outcome* of the blank-routine `jsr` from "execute
  garbage" to "address-error → reboot loop"; see the correction banner.)

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

# DLP bytecode VM — architecture (Rev L), reverse-engineered

This is the structural model of the HP 8593A's **DLP interpreter** — the
token-threaded bytecode VM that runs the factory power-up program and all
`__`-prefixed internal routines. Derived by instruction-level tracing
(`cmd/reinittrace`) + static analysis. It is the substrate the startup-DLP
derail ([DLP_STARTUP_DERAIL.md](DLP_STARTUP_DERAIL.md)) lives in.

## Core data structures (ROM constants, read at runtime)

The VM is parameterised by three pointers stored as longwords in the low-ROM
dispatch/pointer table (the "`$0Axx` slots", reused as data pointers):

| name  | slot     | value     | role |
|-------|----------|-----------|------|
| `$a02`| ROM 0x0A02 | `0x727CA` | **source / name table** — ASCII routine-name list + per-name record offsets |
| `$a50`| ROM 0x0A50 | `0x71682` | **record region** — variable-length sequences of word *tokens* (the compiled bodies) |
| `$a74`| ROM 0x0A74 | `0x71D76` | **dispatch table** — `long[$a74 + token*4]` = handler/trampoline address |

These are **fixed ROM values at runtime** (verified: `recPtr = $a50 + offset`
back-solves to `$a50=0x71682`; `offset` read-addr back-solves to `$a02=0x727CA`).
No RAM relocation of these bases was observed.

## Execution pipeline (one routine call)

Parsing the source text (e.g. `"__VCOM;__PKIP;…"`) calls each `;`-separated
routine. For each name:

1. **Name lookup** — `fcn.320fe(name, table)` hashes the name and searches a
   symbol table, returning a record index `idx` (or `-1` on miss, at `0x321B2`).
   Two tables are used: a **RAM symbol table** at `0xFFBF66` (user variables —
   `__A..__Z` get added here, its count at `+0x80` grows as they declare) and a
   **ROM built-in table** at `0x08001E` (commands like `VRD`, and `__` routines).
2. **Record resolve** — `fcn.331cc(idx)`:
   `offset = word[$a02 + (idx-1)*2]` (the per-name offset, **sign-extended** via
   `movea.w` — negative offsets reach below `$a50`), then
   `recPtr = $a50 + offset`. (`fcn.34B44` is the caller; stores `recPtr` at
   `-0x1e(A6)`.)
3. **Dispatch** — `fcn.34B44` @ `0x34C7C`–`0x34C94`:
   `token = word[recPtr]`; `A1 = long[$a74 + token*4]`; `jsr (A1)`.
   **No bounds check** — an out-of-range token deliberately faults into the
   68000 address-error vector (the VM relies on the exception; see the derail
   doc).

## `__` routines are trampolines (the key pattern)

A built-in `__` routine's record holds a token whose dispatch-table entry points
at a **16-byte trampoline** of the form:

```
link    A6,#0
move.w  #<size>,-(A7)      ; push the source length
lea     (<disp>,PC),A0     ; A0 = pointer to this routine's DLP *source*
jsr     $d18.w             ; jsr 0x349B6 = the DLP scheduler
unlk    A6
rts
```

Examples (verified): `__VCOM` → token `0x158` → `0x5FBDE`, which schedules the
source `0x5FB0E` = `"VRD __A;…VRD __Z;NV"` (size `0xD0`). `__GTMNK` → `0x614C6`,
scheduling source `0x61484` (size `0x42`). So a `__` routine **includes its own
DLP source** by pushing it onto the source stack and re-entering the scheduler.

## Source-include stack (nesting)

The scheduler maintains a **source-include stack** so routines can include other
sources:

- depth `$a634`; entries are 10 bytes `(size, base, head, tail)` at
  `0xFFA636 + 10·n`. The active source's `(size,head,tail)` live at
  `$a62a/$a630/$a632`, base at `$a62c`.
- **push** = a trampoline's `jsr $d18` (a nested source becomes active).
- **pop** = `0x34690`–`0x346C4`: when the active source is consumed
  (`head==tail`), `subq.w #1,$a634` and reload `$a62a..$a632` from the parent
  entry. Loops while still `head==tail` (pops multiple empty levels).

Traced example: outer source `0x727CA` runs `__VCOM` → push `0x5FB0E`
(`VRD __A..__Z;NV`) → that source declares 26 vars (the `0x12F`
identifier-resolve handler runs ~23× as `head` walks the source) → consumed →
**pop** back to `0x727CA` → next routine `__PKIP`.

## The startup-derail anomaly (open)

`__PKIP` (and the routines after it in the power-up list:
`__SOONIP;__PZREMCMDS;__FFTONIP;__ACPPWRUP;__GTGDRV;__WN_VARDEF;…`) are **found**
in the ROM built-in table but their record offsets point into the **`0x02FF`
padding** that fills the tail of the record region (`recPtr=0x71D03` for
`__PKIP`) — token `0x2FF` is past the dispatch table → `jsr` to source text →
address error → **reboot** (the recovery at `0x3DB8 bra 0x3998` is deterministic;
`0x2B3A` returns `ROM[0xB8]=0x7FFF0000 ≥ 0`). Crucially `__PKIP` has **no body
source in ROM** (only 2 occurrences: the startup list + the name table) — it is
**undefined in this Rev L Opt-027 build**. `__VCOM` is the one with a real body.

So the startup source references routines that aren't implemented in this
firmware, and calling one deterministically reboots. Since that is fixed ROM,
**real hardware must not execute this path** — the divergence is upstream.
Leading hypotheses, in order:

1. **The startup source is config-gated** (most likely). `0x727CA` (referenced
   only by its own `$a02` slot) may be a factory *default/template* that runs
   only — or runs unfiltered — on a blank/uncalibrated instrument; a configured
   unit may run a different startup DLP (from cal NVRAM / option config) that
   lists only the routines implemented in this option. → Trace what first
   schedules `0x727CA` at boot (the initial `jsr $d18` that sets `$a62c=0x727CA`)
   and under what condition.
2. **Undefined-routine graceful skip.** The VM may be expected to detect an
   unresolved/blank-record `__` routine and skip it; our reboot would then be a
   mis-modelled recovery. But note the dispatch has no bounds check and the
   address-error recovery is deterministic, so a "skip" would have to happen
   *before* dispatch (in the lookup/resolve), not in the fault handler.

> **Refuted lead — `NV` is not a terminator.** The notes called the `NV` at the
> end of `VRD __A;…VRD __Z;NV` a terminator routine. It is not: the VRD source is
> `0xD0` bytes and ends exactly at `0x5FBDE`, where the **`__VCOM` trampoline
> code** begins — and `0x4E56` ("NV") is simply the `link A6,#imm` opcode of that
> trampoline (`4e 56 … 41 fa ff 26 = lea $5FB0E … 4e b8 0d 18 = jsr $d18`).
> Source text and trampoline code are adjacent in ROM. The source has no explicit
> terminator; it runs to `tail`, so the outer source genuinely continues into the
> undefined `__PKIP`. This *strengthens* hypotheses 1–2.

The fastest disambiguation is the **GPIB oracle** (`pkg/859x/dump.py`): dump the
live source stack / `$a634` / the record region on a running instrument and
compare. Statically, hypothesis (1) is the cleanest next probe.

## Tooling

`cmd/reinittrace` single-steps from the armed-sweep state with a ring buffer and
instruments: the dispatch trajectory (`idx/offset/token/head/tail`), the
source-stack contents, head/tail repoint writes, and `fcn.320fe` lookups
(table + name). Chunk-sampling (`cmd/sweeprun`) cannot catch the brief
exception/reboot transitions — instruction-level stepping was required.

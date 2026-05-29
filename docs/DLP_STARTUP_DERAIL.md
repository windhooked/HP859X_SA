# DLP startup-execution derail — root cause: `$a02 = -1` (empty DLP) executed

**Status:** root cause isolated; fix pending. This is the blocker *after* the
A16 analog gate ([ANALOG_BUS_MODEL.md](ANALOG_BUS_MODEL.md)). With the analog
conversion model in place the 8593 boot advances ~10× — into the **startup-DLP
execution** — and derails at ~49M cycles. Probe: `cmd/naturalkey -derail`.

## Root cause (one line)

**`$a02` (RAM `0xFFA02`, the DLP record-table base) = `0xFFFFFFFF`** — the
firmware's "empty DLP memory" sentinel — and the DLP interpreter uses it as a
table base anyway, computing a garbage record pointer that walks off into ROM
data. This is the **"EMPTY DLP MEM"** state the user observed: the analyzer has
no downloaded programs, yet our boot reaches DLP-record *execution* instead of
skipping it.

## The chain (all PCs from docs/rom.asm, Rev L)

1. **Operating loop** `fcn.18568` drains the foreground DLP ring each iteration
   via `slot 0x72A → fcn.34EE8` (see [DLP_RUNTIME.md](DLP_RUNTIME.md)). The ring
   (state block at `0xFFA61C`) has head `$a630=0xd` ≠ tail `$a632=0x4d`, so it
   thinks there is work to run. Its source-char ring is valid: base
   `$a62c=0x727CA`, size `$a62a=0x4e`.

2. **Instruction exec** `fcn.34B44` (call it `execInstr`) runs one DLP record.
   At `0x34B6C` it computes the record pointer via
   `fcn.331cc(index, &recPtr, $a02)` and stores it in `-0x1e(A6)`.

3. **`fcn.331cc`** computes `recPtr = $a50 + word[ $a02 + (index-1)*2 ]`:
   ```
   0331D8  movea.l ($8,A6), A4        ; A4 = $a02  ← the empty sentinel 0xFFFFFFFF
   0331DC  move.w  (A4,D0.l), …        ; read offset from $a02 table → GARBAGE (wraps to ROM)
   0331FE  movea.l $a50.w, A2          ; A2 = $a50 (record-data base)
   033202  adda.l  A1, A2              ; A2 = $a50 + garbage offset
   033208  move.l  A2, (A3)            ; recPtr = garbage
   ```
   With `$a02 = 0xFFFFFFFF`, `(A4,D0.l)` reads `ROM[0xFFFFFFFF + D0 & 0xFFFFFF]`
   = arbitrary ROM, so `recPtr` is garbage (observed `0x71A6D`, then `0x71D03`).

4. **Token dispatch** `0x34C7C–0x34C94`: reads a 16-bit token (byte-assembled)
   from `recPtr`, indexes the DLP dispatch table at `ROM[0xA74]=0x71D76`
   (`A1 = ROM[0x71D76 + token*4]`), and `jsr (A1)`. `recPtr=0x71A6D` gives token
   `0x12F` → handler `0x3A13A` (the DLP **identifier resolve/define** opcode:
   classifier `fcn.36166` checks digit/`_`; define path `0x3A28A`). The VM spins
   here ~23 operating-loop iterations, then `recPtr` becomes `0x71D03` (in
   `ff 02` filler before the dispatch table) → token `0x2FF` → indexes past the
   table into DLP source text (`ROM[0x72972]="IF;S"=0x49463B53`) → `jsr` to
   garbage → **derail**.

## Evidence it is `$a02`, not the analog model or RAM map

- `$a02 = 0xFFFFFFFF` is stable from early boot (20M cycles on), not corruption.
- Mapping the DLP heap RAM (`DLPRAM 0xFC0000–0xFEBFFF`, where `$bb4e=0xFC9C12` /
  `$bb54=0xFD8DEC` live — a real missing region, now fixed) left the derail
  byte-identical.
- Making the ADC return DAC-varying values left the derail byte-identical.
- The char-source ring (`$a62c=0x727CA`) is valid; only the `$a02`-based record
  pointer is garbage.

## Open questions / next steps

1. **Where is `$a02` set to `-1`?** No *absolute* write to `0xFFA02` appears in
   rom.asm, so it is written via a register-indirect path (likely a bulk
   "clear DLP directory to -1" during DLP init). Find it and the matching
   **empty-guard**: the consumer (`execInstr`/the scheduler) presumably should
   test `$a02 == -1` and skip record execution when the DLP is empty.
2. **Why is the foreground ring non-empty** (`$a630≠$a632`) with an entry that
   triggers record execution, given the DLP is empty? Trace what queued it
   (the DLP scheduler `fcn.349B6` / `slot 0xD18`) — the bogus queued step is
   what drives `execInstr` with `$a02=-1`.
3. Candidate fixes once understood: (a) ensure the empty-DLP guard fires, or
   (b) initialize `$a02` to a valid empty record table — but (a) is more likely
   correct since the firmware deliberately uses `-1` as "empty".

## Key addresses

| Symbol | Addr | Meaning |
|---|---|---|
| `$a02` | `0xFFA02` | DLP record-table base — **`0xFFFFFFFF` = empty (the bug)** |
| `$a50` | `0xFFA50` | DLP record-data base (recPtr = `$a50` + offset) |
| `$a61c` | `0xFFA61C` | foreground DLP ring state block (head `+0x14`, tail `+0x16`, src base `+0x10`) |
| `fcn.34EE8` | `0x34EE8` | DLP step (slot `0x72A`); char-ring parser |
| `fcn.34B44` | `0x34B44` | execInstr — runs one DLP record |
| `fcn.331cc` | `0x331CC` | computes recPtr from `$a02` table + `$a50` |
| dispatch tbl | `0x71D76` | `ROM[0xA74]`; `handler = tbl[token*4]` |
| char-read | `fcn.4258`/`0x427C` | reads source char from ring `state[0x10]+head` |

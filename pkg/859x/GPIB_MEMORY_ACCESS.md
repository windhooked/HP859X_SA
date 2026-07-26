# 859x GPIB memory/cal access — what's actually supported (firmware-verified)

Reviewed the Rev L firmware parser (via `cmd/jumptable`, the reliable name-table
decoder) + the HP 8590 E-series Programmer's Guide, after the `ZSETADDR`/`ZRDWR`
approach returned **UNDEFINED COMMAND** on the real 8593E.

## Headline
- **`ZSETADDR` / `ZRDWR` do NOT exist in the 859x firmware.** They were an
  **8563-family** service command (the old `dump.py` targeted an `HP8563E`). The
  859x parser has no equivalent — confirmed by decoding the full command table.
  Instrument responds `UNDEFINED COMMAND` on the real 8593E.
- **The 859x has NO command to read an arbitrary memory address over GPIB.** Data
  comes out only through **structured** commands. The DLP macro language has no
  arbitrary-memory peek either (its only memory-ish function, `MSBIT(loc,bit)`,
  reads fixed status-flag locations, not arbitrary addresses).

### The read-mnemonic family is NOT memory access (RESOLVED 2026-07-25 — disassembled)
The ROM *does* contain the suggestive strings `MRD MRDB MBRD MWR MWRB MBWR BRD
BWR LRD LWR HRD` (in the parser command table ~0x7D982+). They look like
Memory-ReaD / Memory-WRite / Block, but each one's firmware handler was resolved
(via the type-byte record form `cmd/jumptable` skips) and disassembled — they are
**Marker / Band** commands, not memory:

| Mnemonic | Handler | What the code actually does |
|---|---|---|
| `MRD`  | 0x0348D2 | reads fixed **marker** RAM vars `$a62c/$a62e/$a630/$a632` |
| `MRDB` | 0x03E3A0 | formats/outputs a **marker** value |
| `MBRD` | 0x04063A | a 0..0xF `moveq #N,D1; jmp` **band/mode index** dispatch |
| `BRD`  | 0x05ED70 | 2-instr stub returning constant band code `0x7D00` |

So "M/B + RD" = **Marker/Band ReaD**, not Memory. Confirmed no `PEEK POKE PEEKB
PEEKW RDMEM MEMRD GETMEM` string exists anywhere in the 1 MB ROM.

### Independent confirmation — the KE5FX GPIB Toolkit (the proven-working tool)
`ke5fx.com/gpib` (John Miles' toolkit, works on the user's Windows setup) has **no
memory/EEPROM/cal dumper for the 8590 family**. Its 8590-relevant tools are
`7470.EXE` (HP-7470A **plotter-emulation screen capture** — the standard way to
grab an 8590 screen), `SATRACE.EXE` (**trace** capture), and `BINQUERY.EXE` (send
a query, grab the returned binary block — e.g. `CAL DUMP;` or `TRA?;`). Its cal
state save/load (`VNA.EXE`) is **only** for the 8753/8510/8720 VNAs, not the 8590.
Data leaves an 8590 over GPIB only through structured queries — never a raw peek.

## Goal A — restorable cal/state backup: SUPPORTED (documented commands)
These read back ASCII you can store and later re-send to restore. Address 7:

| Command | Read (query) | Restore | What it captures |
|---|---|---|---|
| **AMPCOR** | `AMPCOR?;` → freq,amp pairs | `AMPCOR f,a, f,a, …;` | USER amplitude-correction table |
| **SER** | `SER?;` | `SERSET <n>;` | serial number |
| **State** | `SAVES <n>;` (to internal reg) / to card | `RCLS <n>;` | full instrument state (settings) |
| **Card** | `MSI` + `STOR`/`SAVED`/`SAVET` | `MSI` + `RCLS`/`LOAD` | state + traces + DLPs to a RAM card |
| **CORREK** | `CORREK?;` (on/off) | `CORREK ON/OFF;` | correction-factors enable |
| **Trace** | `TRA?;`/`TRB?;` (+ `TDF`) | — | current trace data |

### The CAL correction factors ARE GPIB-readable (CORRECTION 2026-07-25)
The factory/self-cal correction factors (frequency + amplitude corrections in the
A16A1 NVRAM) **can** be read and restored over GPIB via the `CAL` command family
(verified on the real 8593E — `CAL DUMP` returned the data):

| Command | Direction | What it does |
|---|---|---|
| **`CAL DUMP`** | **read → controller** | **returns the correction factors over GPIB** (the backup) |
| `CAL DISP` | screen | displays *some* factors on the analyzer screen (subset) |
| `CAL STORE` | working → NVRAM | saves working corrections to the power-on area (only if valid; else SRQ 110) |
| `CAL FETCH` | NVRAM → working | recalls stored corrections into working RAM |
| `CAL INIT` | reset | sets cal data to defaults (needs CF −37 Hz first; re-run `CAL YTF` after) |
| `CAL ALL`/`FREQ`/`AMP`/`YTF` | run | self-cal routines (need CAL OUT / COMB OUT cabled) |

Notes: the guide warns `CAL DISP` **and** `CAL DUMP` may not return *every* factor
(historically a screen-char limit) — capture `CAL DUMP` output and check it looks
complete. Restore workflow: get the corrections back into working RAM, then
`CAL STORE` to commit to NVRAM. `AMPCOR?` (user amplitude corrections) is a
SEPARATE, additional dataset — back that up too.

## Goal B — memory dump over GPIB: USER MEMORY *is* dumpable (USTATE?) — BENCH-VERIFIED

**CORRECTION (2026-07-26): there IS a GPIB command that dumps a whole memory region — `USTATE?`.**
Running the read-only probe on the real 8593E surfaced it:

| Query | Bench response | Meaning |
|---|---|---|
| `USTATE?` | `#A` + ~5244 bytes | **entire user-memory region** (downloaded DLPs, user variables, saved traces) as an HP `#A` binary block |
| `TRSTAT?` | `CLRW A;BLANK B;BLANK C;` | trace display state, as re-sendable commands |
| `IDNUM?` | `8593` | numeric model code |
| `GSTAT?`/`DATASTAT?`/`BUFSTAT?`/`EVNTCNT?` | `1`/`2`/`0`/`0` | subsystem status scalars |
| `WINSTAT?` | `-9999` | window status sentinel (no active window) |

**Why `USTATE?` works** (firmware): `USTATE` is a **DLP array variable** — special-token
class `type=0xc0`, array ID **12**, sitting immediately after the trace arrays
`TRA=7 TRB=8 TRC=9 TRD=10`. The trace arrays are read/write (`TRA?` reads, `MOV TRA,…`
writes); `USTATE` uses the **same generic array handler (slot 0x3F)**, so:
- **Backup:** `USTATE?` → capture the `#A` block = full user-memory image.
- **Restore (very likely):** re-send the block (bare `USTATE <#A block>` or via `MOV`) —
  symmetric with the trace arrays. **Test on the bench** (it does NOT touch cal NVRAM;
  worst case corrupts user DLP memory, which `USTATE?` itself just backed up).

This is the "memory dump over GPIB" the 8563's `ZRDWR` was wrongly sought for — the 859x
exposes it as a **named array variable**, not a raw peek. It covers the *writable/volatile*
memory worth backing up; ROM we already have, and cal lives in NVRAM (use `CAL DUMP;`).

### Byte-exact NVRAM/RAM image (arbitrary address): still NO GPIB PATH
`USTATE?` gives the user-memory region, not arbitrary addresses. For a byte-exact
NVRAM/RAM image of any address:
1. **Physically read the battery-backed SRAM.** The cal/state NVRAM is on the
   removable A16A1 memory card (or the SRAM chips U5/U22). Read it with an
   external reader (research.md §6: Molex 15-92-2050 breakout + Arduino/MCU, or a
   PLCC/DIP SRAM reader). This is the ONLY way to get the factory-cal NVRAM image
   and matches what the emulator's `CalNVRAM` models (0x200000, active data
   0..0x303F).
2. **ROM** we already have (the Rev L `*.HEX` dumps → `/tmp/rom_gold.bin`); no
   need to read it over GPIB.
3. A service/factory GPIB command MIGHT exist behind an undocumented mode, but
   none was found in the parser table and the instrument rejects `ZSETADDR` — so
   without a service reference this is a dead end.

## Bottom line for the two goals
- **Restorable cal backup (A):** do it over GPIB now — `AMPCOR?` + `SER?` +
  a state save to a RAM card. Fully supported; captures the user-restorable data.
- **Raw memory / factory-cal image (B):** GPIB cannot do it on the 859x. Use the
  physical SRAM-card read. (The earlier `dump859x.py` + Windows prompt are built
  on the 8563 `ZSETADDR` protocol and will NOT work here — supersede them with
  the AMPCOR/state commands above for A, and the physical route for B.)

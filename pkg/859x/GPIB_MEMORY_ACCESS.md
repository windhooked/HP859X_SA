# 859x GPIB memory/cal access — what's actually supported (firmware-verified)

Reviewed the Rev L firmware parser (via `cmd/jumptable`, the reliable name-table
decoder) + the HP 8590 E-series Programmer's Guide, after the `ZSETADDR`/`ZRDWR`
approach returned **UNDEFINED COMMAND** on the real 8593E.

## Headline
- **`ZSETADDR` / `ZRDWR` do NOT exist in the 859x firmware.** They were an
  **8563-family** service command (the old `dump.py` targeted an `HP8563E`). The
  859x parser has no equivalent — confirmed by decoding the full command table
  (no `ZSETADDR`, `ZRDWR`, `MRD`, `MWR`, `MRDB`, `MBRD`, `PEEK`, `POKE`; the
  ASCII strings `MRD`/`MWR`/etc. in the ROM are DLP variable names, not commands).
- **The 859x has NO command to read an arbitrary memory address over GPIB.** Data
  comes out only through **structured** commands. The DLP macro language has no
  arbitrary-memory peek either (its only memory-ish function, `MSBIT(loc,bit)`,
  reads fixed status-flag locations, not arbitrary addresses).

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

**Important limitation:** `AMPCOR` is only the **user** corrections. The
**factory cal constants** (flatness, step-gain, timebase, log-amp — the battery-
backed A16A1 NVRAM at CPU `0x200000`) are **NOT exposed by any GPIB read
command**. They're produced by the internal self-cal (`CAL`) routines and live
only in NVRAM. So over GPIB you can back up user corrections + state, and you can
re-run self-cal (`CAL ALL`), but you **cannot read the raw factory cal bytes**.

## Goal B — raw memory image (NVRAM/RAM byte-exact): NO GPIB PATH
There is no service command on the 859x to dump raw memory over GPIB. Options for
a byte-exact image:
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

# 859x undocumented GPIB/DLP command set (firmware-extracted)

Extracted from the Rev L parser command table (ROM `0x7C800..0x80000`) and diffed
against the HP 8590 E-series Programmer's Guide. **The parser recognizes 802
command mnemonics; only ~323 are documented → ~599 are undocumented.** Of those,
~300 are ordinary service/factory commands and ~299 are `__`-prefixed DLP-internal
trampolines. This is the "automated fault-finding / commissioning" command surface.

## How this was derived
- `cmd/jumptable` + a byte-level table decoder walk the parser name table. Each
  record is `<tag 0x30/40/10/20> <sub> <NAME> <type-byte> <handler>`. jumptable's
  published list only shows entries whose type-byte is NUL; the type-byte forms
  (0x01/0x02/0xff — e.g. the `M*RD` family) were decoded separately here.
- Handlers resolve via the master dispatch table (`0xC4 + slot*6`, `4EF9 <target>`)
  or the secondary DLP table (`0x71E02 + (slot-0x47D)*4`).
- Documented set = mnemonics with a dotted-leader TOC entry or "The XXX command"
  prose in the guide. (Imperfect — a few DLP math keywords like `SIN/COS/ATAN/AND/
  OR/IF/THEN/ELSE` leak into the "undocumented" list; ignore those.)

## Bottom line for memory dump (the recurring question)
**There is still NO general memory peek/poke — even in the undocumented set.** The
read/write-looking mnemonics were each disassembled and are subsystem-specific, not
address-generic:
- `MRD/MRDB/MBRD/BRD` = **Marker/Band** read (fixed marker RAM vars, band index).
- `RDREAL/RDDBL/RDBCDL` + `STORREAL/STORDBL/STORBCDL` = DLP-source **trampolines**
  (`jsr $d18` run a macro) — typed value move to/from DLP variables, not memory.
- `OUTDAC`, `YTFDATAR/W` = DLP trampolines too.
- `BADDR` = a fixed-point accumulator adjust (`$b1a6/$b1a8`), not an address latch.
- `VRD`/`ADCXFR` = write the **ACRTC display port** `0xFFF5FC/5FE` (graphics ops).
- `DDGET` = read a clamped counter (`$b0a2`).
- `CALMXRDATA` = **real** code, writes a byte into the mixer-cal table at `$ab82+idx`.

**Why no peek exists:** commissioning an 859x doesn't need arbitrary memory access —
it needs *subsystem* access (set a DAC, read an ADC, read/write a specific cal
table, force a count, read status). All of that IS exposed (below). HP had no
reason to ship a raw peek, and didn't (unlike the 8563's undocumented `ZRDWR`).
For a byte-exact NVRAM image the only route remains a physical SRAM read; for a
*functional* cal backup use `CAL DUMP;` + the cal-table commands below.

## Service command families (undocumented)

### Cal / factory alignment (read + WRITE — the commissioning core)
| Cmd | Likely meaning | Notes |
|---|---|---|
| `CALMXRDATA` `CALMXRBIAS` `INITMXRDATA` | mixer cal data / bias | `CALMXRDATA` writes a byte into `$ab82`-indexed cal table (disassembled) |
| `CALFLTDATA` `CALFLTDATAX` `CALATNDATA` `CALATN` | IF-filter / attenuator cal data | real-code handlers (`0x28436`, `0x3D4C2`) |
| `CALTGX` `CAMPCOR` | tracking-gen / amplitude-corr cal | |
| `YTFDATAR` `YTFDATAW` `YTFDAC` `DSPYTF` `DSPYTFI` | YIG-tuned-filter cal read/write/DAC | `YTFDATAR/W` = DLP macros |
| `GAINTWEAK` `OFSTTWEAK` `USEROFST` `ZTGAOFST` `FMGAIN8` `QPGAIN` `KFACT` | gain/offset alignment tweaks | **WRITE — de-calibrates if misused** |
| `DSPBIAS` `FACTSET` | bias set / factory preset | `FACTSET` likely a factory default |

### Direct hardware — DAC / ADC
| Cmd | Meaning |
|---|---|
| `OUTDAC` `VGDAC` `NBWDAC` `PRSDAC2` `YTFDAC` | write specific DACs (video-gain, bandwidth, YTF…) |
| `ADCSHOW` `ADCSETUP` `ADCPBKT` `ADCXFR` `CTMADC` | ADC readback / setup / transfer |

### Diagnostics / status (READ — safe to try)
| Cmd | Meaning |
|---|---|
| `GSTAT` `DATASTAT` `TRSTAT` `BUFSTAT` `DDSTAT` `TVTSTAT` `WINSTAT` `WINSTATE` `PARSTAT` | subsystem status queries |
| `SHOWOPT` `IDNUM` `USTATE` `PWRUPOB` `PWRUPTIME` | option / id / state / power-up info |
| `FORCECNT` `EVNTCNT` `CTMADC` | force/read counters |

### Synth / PLL / lock
`SETPLL` `PHASELOCK` `NPHASE` `HNLOCK` `HNUNLK` `EXTOED` `SYNCMODE`

### "DD" direct-data channel & typed move
`DDCMD DDGET DDREAD DDSEND DDWRT DDRST DDSTAT DDRECS DDMEAS` — data-display/direct
channel (counter/measurement values, clamped; not raw memory).
`RDREAL RDDBL RDBCDL STORREAL STORDBL STORBCDL VRD WRNM WRNO` — typed value move
(DLP variables / display), via DLP trampolines.

### Full 300-entry service list
See the parser dump; the complete list (minus `__`-internal) is regenerable with the
one-liner in this file's git history / `cmd/jumptable`. Key ones are tabled above.

## Safe experimentation protocol (for the bench 8593E)
You have the real instrument on working GPIB — you can probe these directly. But:

**SAFE (queries / reads — no state change):** append `?` where it's a query, or send
and read the response. Try first:
`SHOWOPT;` `IDNUM?;` `GSTAT?;` `DATASTAT?;` `TRSTAT?;` `ADCSHOW?;` `USTATE?;`
`PWRUPOB?;` `SER?;` `CAL DUMP;` `AMPCOR?;`
Capture each response — this is a rich, non-destructive diagnostic dump.

**DANGEROUS (writes — can de-calibrate / brick alignment):** do **not** send casually:
`CALMXRDATA CALFLTDATA CALATNDATA GAINTWEAK OFSTTWEAK OUTDAC *DAC FACTSET
CAL INIT CALTGX YTFDATAW SERSET`. These write cal tables / DACs. If you experiment,
**`CAL FETCH;` first isn't a backup** — do a full `CAL DUMP;` + `CAL STORE` state
capture and be prepared to restore, and ideally know the factory value before writing.

## Relation to the backup goal
For a **restorable functional backup**, combine the documented + newly-found readers:
`CAL DUMP;` (correction factors, confirmed working) + `AMPCOR?;` (user amp corr) +
`SER?;` + a `SAVES`/state save. The `*DATA` cal readers (`CALFLTDATA`, `CALATNDATA`,
`YTFDATAR`, `CALMXRDATA` in its query form) may expose finer-grained factory tables —
worth capturing on the bench and comparing against `CAL DUMP` for completeness.
A **byte-exact memory image** still requires physically reading the A16A1 SRAM.

# HP 8593E GPIB backup — findings & handoff

**Instrument:** HP 8593E, serial **155**, firmware **REV 940822** (1994-08-22), GPIB **address 7**.
**Adapter:** NI GPIB-USB-HS, `USB\VID_3923&PID_709B&REV_0101`, serial 02170F4A,
NI driver 15.5.0.49152, ni4882.dll 15.5.0f0, visa32.dll 5.7.520.0 (IVI).
**Host:** Windows, Python 3.14, `pyvisa` + `pyvisa-py`. NI-VISA backend is `@ivi`.

---

## TL;DR for the next agent

1. **The cal data IS backed up** and verified stable — `dump_out\cal_dump.txt` (446
   correction values via `CAL DUMP`). Two reads on 2026-07-25 and one on 2026-07-26
   are byte-identical. This is the irreplaceable data; it's safe.
2. Several **undocumented status queries do work** (see table) — useful for RE, but none
   is an address peek.

---

## Critical gotcha: the value-stack echo

This firmware **silently ignores unknown commands** — it does NOT report an error.
`ERR?` stays `0,0,0,0`. An unrecognized query returns whatever number is currently on
the parser's value stack (the last numeric argument/result seen). So **a plausible-looking
numeric reply proves nothing.**

**Always verify a command with the fake-command control** before trusting its output:
```
python probe_cmd.py --cmd "SOMECMD?"
```
It interleaves `ZQZXNOTACMD?` (a command that cannot exist). If your command returns the
same value as that control → not recognized. If different → genuinely recognized.

---

## Hardware gotcha: adapter comes up not Controller-In-Charge

The GPIB-USB-HS initialises as System Controller (`IbaSC=1`) but does **not** assert IFC,
so it is not CIC. Symptom: **every** address 1–30 fails with `VI_ERROR_BERR` on open
(looks like "wrong address" but a wrong address would *time out*, not bus-error).

Fix (once per replug / instrument power-cycle):
```
python -c "import ctypes; ctypes.windll.LoadLibrary('ni4882.dll').SendIFC(0)"
```
After this, `GPIB0::7::INSTR` opens normally. It has stayed CIC across all sessions since.
This not-CIC-after-attach behaviour may be the same defect that hangs linux-gpib on this
REV_0101 hardware (reports init status 0x15) — worth checking before a USBPcap trace.

---

## HARD SAFETY RULES (do not violate)

- **Read-only.** Never send: `CAL INIT`, `CAL STORE`, `CAL FETCH`, `DEFAULT CAL DATA`,
  `FACTSET`, `INIT FLAT`/`INIT FLT`, `STORE FLATNESS`, `SET ATTN ERROR`.
- **Interlock in our favour:** `CAL INIT` only executes if center frequency is set to
  **−37 Hz** first. Leave CF alone → the destructive reinitialise stays blocked.
  (Several service *reload* procedures deliberately set −37 Hz / −2001 Hz to unlock writes.)
- `CAL DUMP` is verified read-only and safe. `probe_cmd.py` refuses destructive names and
  gates `--arg` (which puts a value on the stack a write could consume) behind an explicit flag.
- If `ID?` ever stops containing `8593`, STOP — the cal map/procedures are 859x-only.

---

## Working undocumented commands (control-verified 2026-07-26)

| Command | Returns | Meaning |
|---|---|---|
| `IDNUM?` | `8593` | numeric model code |
| `GSTAT?` | `1` | general status |
| `DATASTAT?` | `2` | data status |
| `TRSTAT?` | `CLRW A;BLANK B;BLANK C;` | trace status (structured string) |
| `BUFSTAT?` | `0` | buffer status |
| `WINSTAT?` | `-9999` | window status (sentinel = none) |
| `EVNTCNT?` | `0` | event counter |
| `USTATE?` | `#A` + ~5,244 B | **user memory** (DLPs/vars/traces), A-block format |

NOT recognized (empty / matched control): `*IDN?` (no SCPI), `ADCSHOW?`, `PWRUPOB?`.
`SHOWOPT` is a display action, not a query.

Documented queries that work: `ID?`→HP8593E, `SER?`→155, `REV?`→940822,
`AMPCOR?`→`0,0` (empty user amp-corr table), `CORREK?`→`1` (corrections ON).

---

## The cal data is small & semantic (not a 64 KB image)

Per service guide Ch.3, the irreplaceable constants are: flatness/frequency-response
points, five A12 step-attenuator errors (1/2/4/8/16 dB), one timebase constant, plus
CALTGX slope/offset (Option 010/011 only, printed on an A7A1 board label). `CAL DUMP`
returns 446 values covering all five flatness bands; each band's point count checks out
via `((stop−start)/step)+1`. See `CAL_BACKUP_859x.md` for the full decode + HP Ch.3 verbatim.

**Open item:** exactly which 5 header values are the A12 step-atten errors is not yet
pinned down (candidates flagged in `CAL_BACKUP_859x.md`); confirm against front-panel
`DISPLAY CAL DATA` before using them for a reload.

---

## If a true "rd addr" is still needed

No directly-accessible command provides it. Realistic routes, in order:
1. **Title-execution service namespace.** Commands like `FACTSET`/`CALMXRDATA`/`CALTGX`
   are invisible to direct GPIB — entered as a screen title then run via front-panel
   `CAL, More, More, SERVICE CAL, EXECUTE TITLE`. A memory-peek primitive, if it exists on
   any 859x, most likely lives here. Testing needs someone at the front panel.
2. **Physical ROM extraction.** ROM is on the A16 processor/video board; pull & read in a
   programmer for a complete, trustworthy firmware dump. Board access is in the service guide.
3. If firmware RE yields the real mnemonic + arg format:
   `python probe_cmd.py --cmd "REALCMD?" --arg 200000 --i-understand-writes-are-possible`

---

## Scripts in this folder

| File | Purpose |
|---|---|
| `probe859x.py` | READ-ONLY sweep: id/cal/state/diag/trace. `--only cal` for just cal backup. Fixed for Windows console. |
| `probe_cmd.py` | Test whether any command is recognized, with fake-command control. Use before trusting any undocumented command. |
| `cal_dump.py` | Retrieve `CAL DUMP` correction factors; verifies state before/after. |
| `parse_cal_dump.py` | Decode/verify the CAL DUMP band structure → `dump_out\cal_constants_decoded.{txt,json}`. |
| `dump859x.py` | Original byte-dumper. **Its ZSETADDR/ZRDWR premise is invalid on this unit** — kept only for its echo-guard; do not expect real bytes from it. |
| `CAL_BACKUP_859x.md` | Full cal-backup reference + HP service-guide Ch.3 verbatim + danger list. |

### Typical invocation
```
python -c "import ctypes; ctypes.windll.LoadLibrary('ni4882.dll').SendIFC(0)"   # only if BERR on open
python probe859x.py --resource GPIB0::7::INSTR --id           # sanity check
python probe859x.py --resource GPIB0::7::INSTR                # full read-only sweep
python probe859x.py --resource GPIB0::7::INSTR --only id,cal,state,diag   # strictly read-only (skips TDF P)
```
Note: the trace group sends `TDF P;` which sets the trace *output format* — harmless and
non-cal, but technically a write. Use `--only …` above to stay strictly read-only.

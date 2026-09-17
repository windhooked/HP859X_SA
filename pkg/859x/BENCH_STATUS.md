# 8593E bench status — GPIB host, adapter, and what is captured

Living status page for the **real instrument** work (as opposed to the emulator).
Start here; the detail lives in the documents linked below.

| | |
|---|---|
| **Instrument** | HP 8593E, serial **155**, firmware **REV 940822** (1994-08-22), HP-IB address **7** |
| **Adapter** | NI GPIB-USB-HS, `USB\VID_3923&PID_709B&REV_0101`, serial `02170F4A` |
| **Windows host** | WORKS end-to-end (NI-488.2 / NI-VISA `@ivi`, pyvisa) — all captures to date came from here |
| **Linux host** | `yoda`, kernel 6.8.0-136, linux-gpib **4.3.7** from source — attaches and addresses, **data transfer still fails** |

## Status as of 2026-08-14

**Captured and safe** (in `bench/data/`, byte-identical across three reads):

- `data/cal/cal_dump.txt` + `cal_dump_run{1,2}.txt` — **446 `CAL DUMP` correction
  values**, the irreplaceable data. Decoded structure in
  `data/cal/cal_constants_decoded.{txt,json}`; full reference in
  [bench/CAL_BACKUP_859x.md](bench/CAL_BACKUP_859x.md).
- `data/probe_20260726_064711/` — the full read-only sweep: identity, cal,
  settings, the undocumented status queries, and
  **`27_diag_USTATE.txt` = the `USTATE?` user-memory block** (5,250 B on the
  wire: `#A`, 2-byte length `0x147C`, 5,244 B of payload). This is the bench
  proof behind the memory-dump finding in
  [GPIB_MEMORY_ACCESS.md](GPIB_MEMORY_ACCESS.md).

**Blocked:** running any of this from Linux. See "Linux status" below.

## Two gotchas that each cost a session

### 1. The adapter comes up NOT Controller-In-Charge

The GPIB-USB-HS initialises as System Controller but **never asserts IFC**, so
it is not CIC and no addressing works. The symptom is misleading: *every*
address 1–30 fails — `VI_ERROR_BERR` on Windows, `ENOL` on Linux — which reads
like "wrong address", but a wrong address would **time out** instead.

```
Windows: python -c "import ctypes; ctypes.windll.LoadLibrary('ni4882.dll').SendIFC(0)"
Linux:   ibrsc(board, 1) then ibsic(board)      # gpibprobe_linux.py --setup does this
```

Once per replug or instrument power-cycle. Persists after that.

### 2. The firmware silently ignores unknown commands

`ERR?` stays `0,0,0,0` and the reply is **whatever number is on the parser's
value stack** (the last numeric argument or result). A plausible numeric answer
therefore proves nothing. Always interleave the impossible control command
`ZQZXNOTACMD?`; same reply as the control ⇒ not recognised. Both
[bench/probe_cmd.py](bench/probe_cmd.py) and
[gpibprobe_linux.py](gpibprobe_linux.py) do this automatically.

This is why `docs/command_notes_empirical.json` overrides the disassembly-derived
notes: bench truth beats inference, but only when the control says the command
is real.

## Safety rules (do not violate)

- **Read-only.** Never send `CAL INIT`, `CAL STORE`, `CAL FETCH`,
  `DEFAULT CAL DATA`, `FACTSET`, `INIT FLAT`/`INIT FLT`, `STORE FLATNESS`,
  `SET ATTN ERROR`. Both probe scripts refuse these by denylist.
- **Interlock in our favour:** `CAL INIT` only executes if centre frequency is
  first set to **−37 Hz**. Leave CF alone and the destructive reinitialise stays
  blocked.
- Never pass an argument to an unverified command — a value left on the parser
  stack is exactly what a real WRITE would consume.
- If `ID?` ever stops containing `8593`, **stop**: the cal map is 859x-only.

## Linux status (yoda) — precisely where it fails

The historical blocker is **gone**, and the real one is one layer further in.

- **Attach is clean.** No `-110`, no register-write failure, and no `0x15`
  "unexpected data" warning: `probe succeeded` → `attached to gpib0`. The
  failure analysed in [LINUX_GPIB_NI_HS.md](LINUX_GPIB_NI_HS.md) no longer
  reproduces on 4.3.7 / kernel 6.8.
- **Addressing works** once IFC is pulsed — `ibln` found the 8593E at pad 7.
- **Data transfer fails**: `ibwrt("ID?;")` → `EDVR`, traced to
  `ni_usb_gpib.c:816` `case NIUSB_ADDRESSING_ERROR: retval = -ENXIO`. The USB
  bulk transfer *succeeds* and **the adapter firmware itself** reports an
  addressing error. Not USB, not endpoints, not autosuspend.
- **Kernel version is not the answer**: `ni_usb_write` / `ni_usb_send_bulk_msg`
  are byte-identical between 4.3.7 and torvalds/master, and neither accepts
  `0x15` at `buffer[6]`. Moving to the in-tree `drivers/gpib` cannot fix it.

Full reply-to-handoff: [bench/LINUX_RESULTS.md](bench/LINUX_RESULTS.md).
The remaining fix path is the USBPcap trace of NI-VISA doing open + `viWrite` +
`viRead` on Windows, diffed against `ni_usb_command`/`ni_usb_write` — recipe in
[bench/LINUX_GPIB_HANDOFF.md](bench/LINUX_GPIB_HANDOFF.md) §6 and
[LINUX_GPIB_NI_HS.md](LINUX_GPIB_NI_HS.md) path 1.

### Session log 2026-08-13/14

Re-probed yoda from scratch over ssh. Confirmed: adapter attaches clean (kernel
log 18:50 and the 18:57 `gpib_capture.sh` re-attach), `/etc/gpib.conf` is the
`ni_usb_b` board with device pad 7, board reaches **CIC** after `ibrsc`+`ibsic`
(`ibsta=0x0178` = CMPL|REM|CIC|ATN|TACS).

`iblines` on this adapter reports only a subset of the bus lines as valid, so it
is a weak presence test: raw `0x50ff` (REN + ATN valid) on 2026-08-13, raw
`0x52ff` (REN + ATN + **NDAC valid and asserted**) on 2026-08-14. Tempting to
read the NDAC bit as "a device is out there", but the two readings were taken in
different board states, so it does not reliably distinguish a live device from a
state artifact — **the `ibln` scan is the test that counts.**

Then the bus went quiet: a full `ibln` scan of pads 1–30 at T3s returned
`{iberr 2 (ENOL): 30}` — no acceptor handshake at *any* address, where the same
setup had found pad 7 on 2026-07-26. That is a physical-layer state (analyzer
powered down or cable unseated), **not** the driver bug, and it is a different
and earlier failure than the `EDVR`/`NIUSB_ADDRESSING_ERROR` above. The
instrument was reported back up on 2026-08-14, but **the re-scan still finds
nothing**: `--scan --id` from the synced tooling on yoda returned
`30x iberr=2 (ENOL)` again, and `ID?`/`SER?`/`REV?`/`IDNUM?` at pad 7 all failed
the same way.

So with the analyzer powered on, the bus is still silent — which points at the
**cable/connection** rather than instrument power. Note we are failing *earlier*
than the known Linux blocker: the 2026-07-26 session got as far as `EDVR` /
`NIUSB_ADDRESSING_ERROR` on the write, which means the adapter *can* address this
instrument when the physical path is good. **Until `--scan` shows pad 7 again,
nothing about the driver bug can be re-tested.**

Tooling gap closed on the way: yoda's system python has neither `pyvisa` nor the
linux-gpib bindings (`~/gpib859x/.venv` has pyvisa 1.16.2 but no `gpib` module),
so `probe859x.py` cannot run there at all. [gpibprobe_linux.py](gpibprobe_linux.py)
now talks to `libgpib.so.0` directly through ctypes — python3 and a configured
board are the only requirements.

## Next actions (on yoda)

```bash
cd /bigdata/src/HP859X_SA/pkg/859x
python3 gpibprobe_linux.py --scan                  # is the 8593E listening at pad 7 again?
python3 gpibprobe_linux.py --id                    # does data transfer still give EDVR?
```

1. **Re-run the scan** after reseating the GPIB cable at both ends (run
   2026-08-14 with the analyzer powered: still ENOL on all 30 addresses).
2. **Re-test data transfer.** If `ID?` still returns `EDVR`, the
   `NIUSB_ADDRESSING_ERROR` blocker is unchanged and the USBPcap trace is the
   only way forward. If it now *works*, the Linux path is open — go straight to
   the full sweep and re-capture `USTATE?` for the restore experiment.
3. **Open bench question, unrelated to the adapter:** which 5 header values in
   `CAL DUMP` are the A12 step-attenuator errors. Confirm against front-panel
   `DISPLAY CAL DATA` before using them for any reload
   ([bench/CAL_BACKUP_859x.md](bench/CAL_BACKUP_859x.md)).

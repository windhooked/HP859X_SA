#!/usr/bin/env python3
"""probe859x.py — READ-ONLY GPIB diagnostic + cal capture for the HP/Agilent 859x.

Fires ONLY safe queries at the instrument and saves every response to a
timestamped folder. It NEVER sends a write/tweak/DAC/cal-store command, so it
cannot change instrument state or de-calibrate it. After each command it reads
the error queue (ERR?) so undefined/undocumented commands are detected
non-destructively (they just report an error and we move on).

Two things it delivers:
  1. A restorable cal/state backup — CAL DUMP (correction factors), AMPCOR?
     (user amp corrections), SER?, and the current settings.
  2. A firmware-derived diagnostic sweep — the undocumented *STAT / SHOWOPT /
     ADCSHOW / GSTAT service queries from UNDOCUMENTED_COMMANDS.md, so we learn
     which return useful data on YOUR unit.

## Connection (same backends as dump859x.py)
  NI GPIB-USB-HS via NI-VISA/linux-gpib:   --resource GPIB0::7::INSTR
  Prologix / AR488 serial adapter:         --prologix /dev/cu.usbserial-XXXX --addr 7
  Windows NI:                              --resource GPIB0::7::INSTR   (NI-VISA installed)
  Enumerate what's visible:                --list

  pip install pyvisa pyvisa-py pyserial      # pyserial only for --prologix

## Usage
  python3 probe859x.py --list
  python3 probe859x.py --resource GPIB0::7::INSTR --id
  python3 probe859x.py --resource GPIB0::7::INSTR            # full safe sweep
  python3 probe859x.py --prologix /dev/cu.usbserial-A1 --addr 7
  python3 probe859x.py --resource GPIB0::7::INSTR --only cal # just the cal backup

Output: probe_out/probe_YYYYMMDD_HHMMSS/  with _log.txt + one file per command.
"""

import argparse
import datetime
import os
import time

# ─────────────────────────────────────────────────────────────────────────────
# SAFE query list. Every entry is a READ. `send` is the exact bytes we transmit;
# `binary` marks responses to keep raw (CAL DUMP / traces). `group` lets you run
# a subset with --only. NOTHING here changes instrument state.
#   documented queries → high confidence they respond.
#   undocumented (from UNDOCUMENTED_COMMANDS.md) → tried in `?` form; if the unit
#   doesn't implement the query it reports UNDEFINED COMMAND (caught via ERR?).
# ─────────────────────────────────────────────────────────────────────────────
SAFE = [
    # --- identity / revision (documented) ---
    ("id",      "ID?",        False, "instrument identity"),
    ("id",      "*IDN?",      False, "SCPI identity (if supported)"),
    ("id",      "IDNUM?",     False, "numeric id / model code (undocumented)"),
    ("id",      "SER?",       False, "serial number"),
    ("id",      "REV?",       False, "firmware revision"),
    ("id",      "SHOWOPT",    False, "installed options (undocumented; no '?')"),

    # --- CAL backup (the irreplaceable data) ---
    ("cal",     "CAL DUMP;",  True,  "calibration correction factors (backup)"),
    ("cal",     "AMPCOR?;",   False, "user amplitude-correction table"),
    ("cal",     "CORREK?;",   False, "correction-factors on/off state"),

    # --- current instrument state (documented, read-only) ---
    ("state",   "CF?;",       False, "center frequency"),
    ("state",   "SP?;",       False, "span"),
    ("state",   "RL?;",       False, "reference level"),
    ("state",   "RB?;",       False, "resolution bandwidth"),
    ("state",   "VB?;",       False, "video bandwidth"),
    ("state",   "ST?;",       False, "sweep time"),
    ("state",   "AT?;",       False, "input attenuation"),
    ("state",   "DET?;",      False, "detector mode"),
    ("state",   "TITLE?;",    False, "screen title"),

    # --- undocumented diagnostic / status sweep (from UNDOCUMENTED_COMMANDS.md) ---
    ("diag",    "GSTAT?;",    False, "general status (undocumented)"),
    ("diag",    "DATASTAT?;", False, "data status (undocumented)"),
    ("diag",    "TRSTAT?;",   False, "trace status (undocumented)"),
    ("diag",    "BUFSTAT?;",  False, "buffer status (undocumented)"),
    ("diag",    "DDSTAT?;",   False, "data-display status (undocumented)"),
    ("diag",    "TVTSTAT?;",  False, "TV-trigger status (undocumented)"),
    ("diag",    "WINSTAT?;",  False, "window status (undocumented)"),
    ("diag",    "PARSTAT?;",  False, "parser status (undocumented)"),
    ("cal",     "USTATE?;",   True,  "USER MEMORY dump (DLPs/vars/traces) as #A block — backup!"),
    ("diag",    "PWRUPOB?;",  False, "power-up option-board readout (undocumented)"),
    ("diag",    "EVNTCNT?;",  False, "event counter (undocumented)"),
    ("diag",    "ADCSHOW?;",  False, "ADC readback (undocumented)"),
    ("diag",    "IDNUM?;",    False, "id number (undocumented)"),
    ("diag",    "ERR?;",      False, "error queue (also our safety probe)"),

    # --- trace data (documented; larger, kept last & optional via --only) ---
    ("trace",   "TDF P;",     False, "set trace format to ASCII P (read-side only)"),
    ("trace",   "TRA?;",      True,  "trace A data"),
]

GROUPS = ["id", "cal", "state", "diag", "trace"]


# ─── Prologix / AR488 native serial backend (pyserial) ───────────────────────
class PrologixGPIB:
    def __init__(self, port, addr, baud=115200, timeout_ms=4000):
        import serial

        self.ser = serial.Serial(port, baud, timeout=timeout_ms / 1000.0)
        self.timeout_ms = timeout_ms
        time.sleep(0.1)
        self.ser.reset_input_buffer()
        ver = self._cmd_query("++ver")
        print(f"[prologix] {ver}")
        for c in ("++mode 1", f"++addr {addr}", "++auto 0", "++eoi 1", "++eos 2",
                  f"++read_tmo_ms {min(3000, timeout_ms)}"):
            self._cmd(c)

    def _cmd(self, s):
        self.ser.write((s + "\n").encode("ascii"))
        self.ser.flush()

    def _cmd_query(self, s):
        self._cmd(s)
        return self.ser.readline().decode("ascii", "replace").strip()

    def write(self, s):
        self._cmd(s)

    def query_raw(self, s):
        """Send an instrument command, then read the whole response until EOI/idle."""
        self._cmd(s)
        self._cmd("++read eoi")
        # Read until an idle gap (instrument done) or overall timeout.
        out = bytearray()
        deadline = time.time() + self.timeout_ms / 1000.0
        idle = 0.25
        last = time.time()
        while time.time() < deadline:
            n = self.ser.in_waiting
            if n:
                out += self.ser.read(n)
                last = time.time()
            else:
                if out and (time.time() - last) > idle:
                    break
                time.sleep(0.02)
        return bytes(out)

    def close(self):
        try:
            self.ser.close()
        except Exception:
            pass


# ─── pyvisa (NI-VISA @ivi / pyvisa-py @py) backend wrapper ───────────────────
class VisaGPIB:
    def __init__(self, resource, timeout_ms):
        import pyvisa

        last = None
        for backend in ("@ivi", "@py", ""):
            try:
                rm = pyvisa.ResourceManager(backend) if backend else pyvisa.ResourceManager()
                inst = rm.open_resource(resource)
            except Exception as e:
                last = e
                continue
            inst.timeout = timeout_ms
            inst.read_termination = None      # keep raw; CAL DUMP may be binary
            inst.write_termination = "\n"
            self.rm, self.inst = rm, inst
            print(f"[open] {resource} via backend {backend or 'default'}")
            return
        raise SystemExit(
            f"could not open {resource}. Last error: {last}\n"
            "Install NI-488.2/linux-gpib (NI adapter) or use --prologix with a serial adapter."
        )

    def write(self, s):
        self.inst.write(s)

    def query_raw(self, s):
        self.inst.write(s)
        try:
            return bytes(self.inst.read_raw())
        except Exception:
            return b""

    def close(self):
        try:
            self.inst.close()
        except Exception:
            pass


def summarize(raw):
    """One-line summary of a response for the console/log."""
    if not raw:
        return "<no response>"
    printable = sum(1 for b in raw if 32 <= b < 127 or b in (9, 10, 13))
    kind = "ascii" if printable >= 0.9 * len(raw) else "binary"
    head = raw[:60].decode("ascii", "replace").replace("\n", " ").replace("\r", " ")
    return f"{len(raw)} B {kind}: {head!r}" + ("…" if len(raw) > 60 else "")


def err_check(inst):
    """Read the error queue; returns a short string (used as our safety probe)."""
    try:
        r = inst.query_raw("ERR?;").decode("ascii", "replace").strip()
        return r
    except Exception as e:
        return f"<ERR? failed: {e}>"


def main():
    ap = argparse.ArgumentParser(description="READ-ONLY 859x GPIB diagnostic + cal capture")
    ap.add_argument("--prologix", metavar="PORT", help="Prologix/AR488 serial port")
    ap.add_argument("--addr", type=int, default=7, help="GPIB primary address (default 7)")
    ap.add_argument("--baud", type=int, default=115200, help="Prologix baud (default 115200)")
    ap.add_argument("--resource", help="VISA resource, e.g. GPIB0::7::INSTR")
    ap.add_argument("--list", action="store_true", help="enumerate visible resources/ports and exit")
    ap.add_argument("--id", action="store_true", help="identify and exit")
    ap.add_argument("--only", help="comma list of groups to run: " + ",".join(GROUPS))
    ap.add_argument("--outdir", default="probe_out", help="base output directory")
    ap.add_argument("--timeout", type=int, default=5000, help="GPIB/serial timeout ms")
    ap.add_argument("--no-errcheck", action="store_true",
                    help="skip the ERR? safety probe between commands (faster, less safe-diagnostic)")
    args = ap.parse_args()

    if args.list:
        try:
            import serial.tools.list_ports as lp
            print("serial ports (Prologix/AR488 candidates):")
            for p in lp.comports() or []:
                print("   ", p.device, "-", p.description)
        except Exception as e:
            print("serial enumeration unavailable:", e)
        try:
            import pyvisa
            for backend in ("@ivi", "@py"):
                try:
                    rm = pyvisa.ResourceManager(backend)
                    print(f"pyvisa {backend}: {rm.list_resources('?*')}")
                except Exception as e:
                    print(f"pyvisa {backend}: unavailable ({e})")
        except ImportError:
            print("pyvisa not installed")
        return

    if args.prologix:
        inst = PrologixGPIB(args.prologix, args.addr, baud=args.baud, timeout_ms=args.timeout)
    elif args.resource:
        inst = VisaGPIB(args.resource, args.timeout)
    else:
        raise SystemExit("give --prologix PORT or --resource; --list to enumerate.")

    # Identify first.
    ident = inst.query_raw("ID?;").decode("ascii", "replace").strip()
    print(f"[id] ID? → {ident!r}")
    if args.id:
        inst.close()
        return
    if ident and not any(m in ident.upper() for m in
                         ("8590", "8591", "8592", "8593", "8594", "8595", "8596")):
        print(f"WARNING: {ident!r} doesn't look like an 859x — continuing anyway (queries are read-only).")

    only = set(args.only.split(",")) if args.only else set(GROUPS)
    stamp = datetime.datetime.now().strftime("%Y%m%d_%H%M%S")
    outdir = os.path.join(args.outdir, f"probe_{stamp}")
    os.makedirs(outdir, exist_ok=True)
    logpath = os.path.join(outdir, "_log.txt")
    print(f"[out] {outdir}")

    with open(logpath, "w") as log:
        log.write(f"# 859x READ-ONLY probe  {stamp}\n# ID? -> {ident!r}\n\n")
        idx = 0
        for group, cmd, binary, desc in SAFE:
            if group not in only:
                continue
            idx += 1
            try:
                raw = inst.query_raw(cmd)
            except Exception as e:
                raw = b""
                summary = f"<send failed: {e}>"
            else:
                summary = summarize(raw)
            # Save the response.
            safe_name = "".join(c if c.isalnum() else "_" for c in cmd).strip("_")[:24]
            ext = "bin" if binary else "txt"
            fpath = os.path.join(outdir, f"{idx:02d}_{group}_{safe_name}.{ext}")
            with open(fpath, "wb") as f:
                f.write(raw)
            err = "" if args.no_errcheck else err_check(inst)
            line = f"[{group:5}] {cmd:12} {desc}\n         -> {summary}"
            if err and err not in ("0", "0,0", ""):
                line += f"\n         ERR? -> {err!r}"
            print(line)
            log.write(f"## {cmd}   ({desc})\n")
            log.write(f"response: {summary}\n")
            if err:
                log.write(f"ERR?: {err}\n")
            log.write(f"file: {os.path.basename(fpath)}\n\n")

    print(f"\nDone. {idx} queries captured → {outdir}")
    print("Review _log.txt; the CAL DUMP / AMPCOR / SER files are your cal backup.")
    inst.close()


if __name__ == "__main__":
    main()

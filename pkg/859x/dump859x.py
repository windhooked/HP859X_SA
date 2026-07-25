#!/usr/bin/env python3
"""dump859x.py — GPIB memory + calibration backup for the HP/Agilent 859x.

Reads instrument memory over GPIB using the service-mode ZSETADDR/ZRDWR
protocol (undocumented; discovered via firmware disassembly — see
docs/rom_analysis.md). Resumable, region-driven, backend-flexible.

## What it does
  1. IDs the instrument (ID?) and refuses to run on a non-859x.
  2. Backs up the CALIBRATION data first (the only truly irreplaceable bytes:
     the battery-backed CalNVRAM + the working CalRAM), then optional RAM and a
     sampled ROM verify.
  3. Every region is written byte-by-byte to its own file AND is RESUMABLE — if
     interrupted, rerun and it continues from the last saved byte.
  4. Also captures the human-readable CAL constants via the documented output
     path when available (see --cal-ascii), a second independent backup.

## Requirements (this is the blocker on the current Mac)
  A pyvisa backend that can actually drive the adapter:
    - NI GPIB-USB-HS  → needs NI-488.2 (Linux: linux-gpib + gpib-ctypes;
      Windows/older-macOS: NI-VISA). Apple-Silicon macOS has NO NI GPIB driver.
    - Prologix / AR488 (USB-serial) → works everywhere via pyvisa-py
      (ASRL/PRLGX resource); pass --resource with the serial port.
  Install:  pip install pyvisa pyvisa-py pyserial
  Pick backend automatically (tries NI-VISA '@ivi' then pyvisa-py '@py').

## Usage
  python3 dump859x.py --list                 # enumerate visible resources
  python3 dump859x.py --resource GPIB0::18::INSTR --id      # just identify
  python3 dump859x.py --resource GPIB0::18::INSTR           # CAL backup (default)
  python3 dump859x.py --resource GPIB0::18::INSTR --regions cal,ram
  python3 dump859x.py --resource GPIB0::18::INSTR --regions all   # everything
  python3 dump859x.py --resource ASRL/dev/cu.usbserial::INSTR ... # Prologix

Every read is one GPIB round-trip, so byte-wise dumps are slow (order of
100–300 bytes/s). CAL (80 KB) ≈ 5–15 min; RAM (60 KB) ≈ 5–10 min; full ROM
(1 MB) is hours — use --regions rom-sample to spot-verify instead.
"""

import argparse
import os
import time

# 859x memory map (24-bit bus). Verified 2026-07-25 against (a) the U114 PAL
# LCAL decode → 0x200000, (b) the emulator memory map (machine.go /
# calnvram.go), and (c) the firmware's OWN boot access: the cal-checksum sweep
# touches cal-NVRAM 0x200000..0x20303F (~12 KB active cal data) — measured by
# TestCalExtentDiag. The 0x200000 window is a BOARD-level decode (A16A1 memory
# board), shared across the A16 family incl. the 8593E. We dump the full 64 KB
# window: if the physical SRAM is smaller it aliases within the window (captured
# harmlessly; the repeat period reveals the true size). end is EXCLUSIVE.
REGIONS = {
    # name:        (start,    end,       description)
    "calnvram":   (0x200000, 0x210000, "battery-backed cal NVRAM (64 KB window; active data 0..0x303F)"),
    "calram":     (0x2FC000, 0x300000, "working cal scratch RAM (16 KB)"),
    "ram":        (0xFF0000, 0xFFF000, "main working RAM (60 KB)"),
    "rom":        (0x000000, 0x100000, "firmware ROM (1 MB) — long!"),
    "rom-sample": (0x000000, 0x000200, "ROM first 512 B — reset vector / revision spot-check"),
    "cal-active": (0x200000, 0x203100, "just the boot-touched active cal data (~12 KB, quick)"),
}
# Named groups for --regions.
GROUPS = {
    "cal": ["calnvram", "calram"],
    "all": ["calnvram", "calram", "ram", "rom"],
}


# ─────────────────────────────────────────────────────────────────────────────
# Prologix GPIB-USB / AR488 native serial backend (pyserial). Chosen for a
# reliable byte-exact backup: we drive the ++command protocol directly rather
# than rely on pyvisa-py's experimental Prologix wrapper. Presents the same
# .query()/.write() surface the rest of the script uses.
# ─────────────────────────────────────────────────────────────────────────────
class PrologixGPIB:
    def __init__(self, port, addr, baud=115200, timeout_ms=3000):
        import serial  # pyserial

        self.ser = serial.Serial(port, baud, timeout=timeout_ms / 1000.0)
        time.sleep(0.1)
        self.ser.reset_input_buffer()
        ver = self._cmd_query("++ver")
        if "prologix" not in ver.lower() and "gpib" not in ver.lower():
            print(f"WARNING: ++ver → {ver!r} — not obviously a Prologix/AR488 adapter")
        else:
            print(f"[prologix] {ver}")
        # Controller mode, target address, manual read, assert EOI, LF-terminate
        # instrument commands, generous read timeout.
        for c in ("++mode 1", f"++addr {addr}", "++auto 0", "++eoi 1", "++eos 2",
                  f"++read_tmo_ms {min(3000, timeout_ms)}"):
            self._cmd(c)
        self.timeout = timeout_ms  # for API parity; pyserial already set

    def _cmd(self, s):
        self.ser.write((s + "\n").encode("ascii"))
        self.ser.flush()

    def _cmd_query(self, s):
        self._cmd(s)
        return self.ser.readline().decode("ascii", "replace").strip()

    def write(self, s):
        # An instrument command (no ++): Prologix forwards it, appending EOS(LF).
        self._cmd(s)

    def query(self, s):
        # Instrument query: send it, then explicitly read the response until EOI.
        self._cmd(s)
        self._cmd("++read eoi")
        return self.ser.readline().decode("ascii", "replace").strip()

    def close(self):
        try:
            self.ser.close()
        except Exception:
            pass


def open_instr(resource, timeout_ms):
    import pyvisa

    last = None
    for backend in ("@ivi", "@py", ""):
        try:
            rm = pyvisa.ResourceManager(backend) if backend else pyvisa.ResourceManager()
        except Exception as e:  # backend not installed
            last = e
            continue
        if resource is None:
            return rm, None
        try:
            inst = rm.open_resource(resource)
            inst.timeout = timeout_ms
            # 859x line terminators: commands end with ';' or newline; ID? returns
            # a line. Keep read/write terms permissive.
            inst.read_termination = "\n"
            inst.write_termination = "\n"
            print(f"[open] {resource} via backend {backend or 'default'}")
            return rm, inst
        except Exception as e:
            last = e
            continue
    raise SystemExit(
        f"could not open {resource}. Last error: {last}\n"
        "No working GPIB backend. Install NI-488.2/linux-gpib (NI adapter) or use "
        "a Prologix/AR488 serial adapter (pyvisa-py handles it)."
    )


def identify(inst):
    for q in ("ID?", "*IDN?"):
        try:
            r = inst.query(q).strip()
            if r:
                return q, r
        except Exception:
            continue
    return None, None


def read_byte(inst, addr):
    """One ZSETADDR/ZRDWR round-trip → an int 0..255."""
    inst.write(f"ZSETADDR {addr:06X}")
    return int(inst.query("ZRDWR?").strip()) & 0xFF


def dump_region(inst, name, start, end, outdir, flush_every=256):
    path = os.path.join(outdir, f"{name}_{start:06X}_{end:06X}.bin")
    total = end - start
    # Resume: continue from the current file size.
    done = os.path.getsize(path) if os.path.exists(path) else 0
    if done >= total:
        print(f"[{name}] already complete ({total} bytes) — {path}")
        return path
    mode = "ab" if done else "wb"
    print(f"[{name}] {start:06X}..{end:06X} ({total} B); resuming at +{done}")
    t0 = time.time()
    with open(path, mode, buffering=0) as f:
        buf = bytearray()
        for addr in range(start + done, end):
            buf.append(read_byte(inst, addr))
            if len(buf) >= flush_every:
                f.write(buf)
                buf.clear()
                n = addr - start + 1
                rate = n / max(1e-6, time.time() - t0)
                eta = (total - n) / max(1e-6, rate)
                print(f"  {addr:06X}  {n}/{total}  {rate:5.0f} B/s  ETA {eta/60:5.1f} min",
                      end="\r", flush=True)
        if buf:
            f.write(buf)
    print(f"\n[{name}] done → {path}  ({time.time()-t0:.0f}s)")
    return path


def cal_ascii(inst, outdir):
    """Independent, human-readable cal backup via documented output commands.
    Tries known cal-dump mnemonics; writes whatever responds. Non-fatal."""
    path = os.path.join(outdir, "cal_constants.txt")
    got = 0
    with open(path, "w") as f:
        for cmd in ("CAL?", "ERR?", "SER?", "REV?", "MODEL?", "IDN?",
                    "CAL DUMP?", "SAVES?", "SPAN?", "CF?", "RL?"):
            try:
                r = inst.query(cmd).strip()
            except Exception as e:
                r = f"<no response: {e}>"
            if r and not r.startswith("<no response"):
                got += 1
            f.write(f"# {cmd}\n{r}\n\n")
    print(f"[cal-ascii] {got} responsive queries → {path}")
    return path


def main():
    ap = argparse.ArgumentParser(description="859x GPIB memory + cal backup")
    ap.add_argument("--prologix", metavar="PORT",
                    help="Prologix/AR488 serial port (e.g. /dev/cu.usbserial-XXXX) — native driver")
    ap.add_argument("--addr", type=int, default=7, help="GPIB primary address (default 7)")
    ap.add_argument("--baud", type=int, default=115200, help="Prologix serial baud (default 115200)")
    ap.add_argument("--resource", help="VISA resource (NI/pyvisa path: e.g. GPIB0::7::INSTR)")
    ap.add_argument("--list", action="store_true", help="enumerate visible resources / serial ports and exit")
    ap.add_argument("--id", action="store_true", help="identify the instrument and exit")
    ap.add_argument("--regions", default="cal",
                    help="comma list of region/group names (default: cal). "
                         f"regions={list(REGIONS)}  groups={list(GROUPS)}")
    ap.add_argument("--outdir", default="dump_out", help="output directory")
    ap.add_argument("--timeout", type=int, default=5000, help="GPIB/serial timeout ms")
    ap.add_argument("--cal-ascii", action="store_true", help="also capture documented cal-constant queries")
    args = ap.parse_args()

    if args.list:
        try:
            import serial.tools.list_ports as lp
            ports = [f"{p.device}  ({p.description})" for p in lp.comports()]
            print("serial ports (Prologix/AR488 candidates):")
            for p in ports or ["  <none>"]:
                print("  ", p)
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
            print("pyvisa not installed (only needed for the NI/VISA path)")
        return

    if args.prologix:
        inst = PrologixGPIB(args.prologix, args.addr, baud=args.baud, timeout_ms=args.timeout)
    elif args.resource:
        _, inst = open_instr(args.resource, args.timeout)
    else:
        raise SystemExit("give --prologix PORT (serial adapter) or --resource (VISA); --list to enumerate.")

    q, ident = identify(inst)
    print(f"[id] {q} → {ident!r}")
    if args.id:
        return
    if ident and not any(m in ident.upper() for m in ("8590", "8591", "8592", "8593", "8594", "8595", "8596")):
        print(f"WARNING: {ident!r} does not look like an 859x — the ZSETADDR/"
              "ZRDWR map and region addresses may not apply. Aborting; pass the "
              "instrument's own service-manual map if this is intended.")
        raise SystemExit(2)

    os.makedirs(args.outdir, exist_ok=True)
    # Expand region/group names, de-dup, preserve order.
    want = []
    for tok in args.regions.split(","):
        tok = tok.strip()
        if tok in GROUPS:
            want += GROUPS[tok]
        elif tok in REGIONS:
            want.append(tok)
        else:
            raise SystemExit(f"unknown region {tok!r}; regions={list(REGIONS)} groups={list(GROUPS)}")
    seen, order = set(), []
    for r in want:
        if r not in seen:
            seen.add(r); order.append(r)

    if args.cal_ascii:
        cal_ascii(inst, args.outdir)
    for name in order:
        start, end, desc = REGIONS[name]
        print(f"\n=== {name}: {desc} ===")
        dump_region(inst, name, start, end, args.outdir)

    print("\nAll requested regions complete. Files in", args.outdir)


if __name__ == "__main__":
    main()

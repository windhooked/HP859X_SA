#!/usr/bin/env python3
"""gpibprobe_linux.py — READ-ONLY 859x GPIB probe for Linux, straight on libgpib.

Why this exists: on the Linux bench host (yoda) neither `pyvisa` nor the
linux-gpib python bindings are installed for the system interpreter, so
`probe859x.py` cannot run there. This talks to `/usr/local/lib/libgpib.so.0`
through ctypes, so it needs **nothing but python3** and a configured board.

It also encodes the two bench gotchas that cost a session each:

  1. **The adapter comes up NOT Controller-In-Charge.** The NI GPIB-USB-HS
     (REV_0101) initialises as System Controller but never asserts IFC, so no
     addressing works until you do `ibrsc(1)` + `ibsic()`. Symptom without it:
     EVERY address 1-30 fails - ENOL on Linux, VI_ERROR_BERR on Windows - which
     reads like "wrong address" but a wrong address would time out instead.
     `--setup` (on by default) does this for you.

  2. **The firmware silently ignores unknown commands** (ERR? stays 0,0,0,0) and
     returns whatever number is on the parser's value stack. A plausible numeric
     reply therefore proves NOTHING. `--cmd` always interleaves the impossible
     control command ZQZXNOTACMD? - if your command returns the same thing as
     the control, it is not recognised. See bench/FINDINGS.md.

USAGE
  python3 gpibprobe_linux.py --lines              # bus line states (REN/ATN only on this adapter)
  python3 gpibprobe_linux.py --scan               # ibln listener scan, pads 1..30
  python3 gpibprobe_linux.py --id                 # ID? / SER? / REV? at --pad
  python3 gpibprobe_linux.py --cmd "GSTAT?" --cmd "USTATE?"
  python3 gpibprobe_linux.py --cmd "USTATE?" --binary --out ustate.bin

Read-only by construction: it sends only what you pass, refuses the destructive
denylist outright, and never appends an argument (an argument left on the value
stack is what a real WRITE command would consume).
"""
import argparse
import ctypes
import sys

LIB = "/usr/local/lib/libgpib.so.0"
FAKE = "ZQZXNOTACMD?"
# Same denylist as bench/probe_cmd.py - these reinitialise or overwrite the
# irreplaceable cal constants. CAL INIT additionally needs CF = -37 Hz to arm,
# so leaving centre frequency alone is a second interlock in our favour.
DENY = ("CAL INIT", "CALINIT", "CAL STORE", "CALSTORE", "CAL FETCH", "CALFETCH",
        "DEFAULT", "FACTSET", "INIT FLAT", "INITFLAT", "CAL STOR", "STORE FLAT",
        "SET ATTN", "DISPOSE", "ERASE")

IBERR = {0: "EDVR (driver/OS)", 1: "ECIC (not CIC)", 2: "ENOL (no listener)",
         3: "EADR (addressing)", 4: "EARG", 5: "ESAC", 6: "EABO (timeout)",
         7: "ENEB (no board)", 11: "ECAP", 14: "EBUS", 15: "ESTB", 16: "ESRQ"}
T3s, T10s = 12, 13


class Gpib:
    def __init__(self, board="gpib0"):
        self.lib = ctypes.CDLL(LIB)
        self.lib.ibfind.restype = ctypes.c_int
        self.lib.ibdev.restype = ctypes.c_int
        self._sta = ctypes.c_int.in_dll(self.lib, "ibsta")
        self._err = ctypes.c_int.in_dll(self.lib, "iberr")
        self._cnt = ctypes.c_long.in_dll(self.lib, "ibcntl")
        self.bd = self.lib.ibfind(board.encode())
        if self.bd < 0:
            sys.exit(f"ibfind({board}) failed - is the board configured? "
                     f"run: sudo gpib_config --minor 0")

    sta = property(lambda self: self._sta.value)
    err = property(lambda self: self._err.value)
    cnt = property(lambda self: self._cnt.value)

    def decode(self):
        s, bits = self.sta, []
        for name, m in (("ERR", 0x8000), ("TIMO", 0x4000), ("END", 0x2000),
                        ("SRQI", 0x1000), ("CMPL", 0x100), ("LOK", 0x80),
                        ("REM", 0x40), ("CIC", 0x20), ("ATN", 0x10),
                        ("TACS", 0x08), ("LACS", 0x04)):
            if s & m:
                bits.append(name)
        out = f"ibsta=0x{s:04x} [{'|'.join(bits)}]"
        if s & 0x8000:
            out += f" iberr={self.err} {IBERR.get(self.err, '?')}"
        return out

    def setup(self):
        """Take System Control and pulse IFC - gotcha #1. Idempotent."""
        self.lib.ibrsc(self.bd, 1)
        self.lib.ibsic(self.bd)
        self.lib.ibtmo(self.bd, T3s)
        return bool(self.sta & 0x20)

    def lines(self):
        v = ctypes.c_short(0)
        self.lib.iblines(self.bd, ctypes.byref(v))
        raw = v.value & 0xFFFF
        print(f"iblines raw=0x{raw:04x}  {self.decode()}")
        for name, m in (("DAV", 0x01), ("NDAC", 0x02), ("NRFD", 0x04),
                        ("IFC", 0x08), ("REN", 0x10), ("SRQ", 0x20),
                        ("ATN", 0x40), ("EOI", 0x80)):
            if raw & (m << 8):          # validity bit set -> state is meaningful
                print(f"  {name:<4} asserted={1 if raw & m else 0}")
            else:
                print(f"  {name:<4} not reported by this adapter")

    def scan(self, lo=1, hi=30):
        found, errs = [], {}
        res = ctypes.c_short(0)
        for pad in range(lo, hi + 1):
            res.value = 0
            self.lib.ibln(self.bd, pad, 0, ctypes.byref(res))
            if self.sta & 0x8000:
                errs[self.err] = errs.get(self.err, 0) + 1
            elif res.value:
                print(f"  pad {pad:2d}: LISTENER")
                found.append(pad)
        if not found:
            print("  no listeners")
            for e, n in sorted(errs.items()):
                print(f"  {n}x iberr={e} {IBERR.get(e, '?')}")
            if errs.get(2):
                print("  ENOL on every address = nothing on the bus is "
                      "handshaking:\n    check the analyzer is powered on and "
                      "the GPIB cable is seated at both ends.")
        return found

    def open(self, pad):
        ud = self.lib.ibdev(0, pad, 0, T10s, 1, 0)
        if ud < 0:
            sys.exit(f"ibdev(pad={pad}) failed")
        return ud

    def ask(self, ud, cmd, binary=False):
        """Write a query, read the reply. Returns bytes, or None on error."""
        b = cmd.encode() if isinstance(cmd, str) else cmd
        self.lib.ibwrt(ud, b, len(b))
        if self.sta & 0x8000:
            print(f"  {cmd:<14} WRITE FAILED  {self.decode()}")
            return None
        buf = ctypes.create_string_buffer(65536)
        self.lib.ibrd(ud, buf, 65535)
        if self.sta & 0x8000:
            print(f"  {cmd:<14} READ FAILED   {self.decode()}")
            return None
        data = buf.raw[:self.cnt]
        shown = repr(data[:70]) + (" ..." if len(data) > 70 else "")
        print(f"  {cmd:<14} {len(data):6d} B  {shown if not binary else shown}")
        return data


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--board", default="gpib0")
    ap.add_argument("--pad", type=int, default=7, help="instrument address (8593E = 7)")
    ap.add_argument("--no-setup", action="store_true",
                    help="skip the ibrsc/ibsic IFC bring-up (you almost never want this)")
    ap.add_argument("--lines", action="store_true")
    ap.add_argument("--scan", action="store_true")
    ap.add_argument("--id", action="store_true", help="ID? / SER? / REV? / IDNUM?")
    ap.add_argument("--cmd", action="append", default=[],
                    help="query to test, with fake-command control; repeatable")
    ap.add_argument("--binary", action="store_true", help="--cmd replies are binary")
    ap.add_argument("--out", help="write the LAST --cmd reply to this file")
    a = ap.parse_args()

    for c in a.cmd:
        up = c.upper().replace("  ", " ")
        for d in DENY:
            if d in up:
                sys.exit(f"refusing: {c!r} matches destructive denylist entry {d!r}")

    g = Gpib(a.board)
    if not a.no_setup:
        cic = g.setup()
        print(f"board bring-up: ibrsc(1)+ibsic -> {g.decode()}")
        if not cic:
            print("  WARNING: still not CIC - addressing will fail")

    if a.lines:
        g.lines()
    if a.scan:
        print("ibln listener scan, pads 1-30:")
        g.scan()
    if not (a.id or a.cmd):
        return

    ud = g.open(a.pad)
    if a.id:
        print(f"identity at pad {a.pad}:")
        for c in ("ID?;", "SER?;", "REV?;", "IDNUM?;"):
            g.ask(ud, c)
    last = None
    if a.cmd:
        print(f"queries at pad {a.pad} (control = {FAKE}):")
        control = g.ask(ud, FAKE)
        for c in a.cmd:
            last = g.ask(ud, c if c.endswith(";") else c + ";", a.binary)
            if last is not None and last == control:
                print("       ^ SAME AS CONTROL - command is NOT recognised "
                      "(value-stack echo)")
    if a.out and last:
        with open(a.out, "wb") as f:
            f.write(last)
        print(f"wrote {len(last)} B -> {a.out}")
    g.lib.ibonl(ud, 0)


if __name__ == "__main__":
    main()

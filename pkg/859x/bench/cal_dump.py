#!/usr/bin/env python3
"""cal_dump.py - retrieve 859x correction factors via the documented CAL DUMP command.

CAL DUMP is documented in the HP 8590 E-Series Programmer's Guide (p5-87) as
"returns correction factors to the controller" (ASCII). It is read-only.

NEVER sent by this script: CAL INIT, CAL STORE, CAL FETCH, DEFAULT CAL DATA,
INIT FLAT, or "CF -37HZ" (the -37 Hz center frequency is the interlock that arms
CAL INIT -- leaving CF alone keeps the destructive reinitialise blocked).

Records instrument state before and after and reports any drift.
"""
import os, sys

OUT = os.path.join("dump_out", "cal_dump.txt")
STATE = ("CF?", "SPAN?", "RL?", "AT?")   # read-only settings, used as a tripwire


def main():
    import pyvisa
    rm = pyvisa.ResourceManager("@ivi")
    inst = rm.open_resource("GPIB0::7::INSTR")
    inst.timeout = 8000
    inst.read_termination = "\n"
    inst.write_termination = "\n"

    def q(cmd):
        try:
            return inst.query(cmd).strip()
        except Exception as e:
            return f"<ERR {type(e).__name__}>"

    ident = q("ID?")
    print(f"ID?  -> {ident!r}")
    if "8593" not in ident.upper():
        sys.exit(f"not an 8593E ({ident!r}) - stopping")

    print("\n=== state BEFORE ===")
    before = {c: q(c) for c in STATE}
    before["ERR?"] = q("ERR?")
    for k, v in before.items():
        print(f"  {k:7s} {v}")

    print("\n=== CAL DUMP ===")
    lines = []
    try:
        inst.write("CAL DUMP")
        while True:                      # collect until the analyzer stops talking
            try:
                ln = inst.read().strip()
            except Exception:
                break
            if ln == "":
                break
            lines.append(ln)
            print(f"  {ln}")
            if len(lines) > 4000:
                break
    except Exception as e:
        print(f"  write/read failed: {type(e).__name__}: {e}")

    if not lines:
        print("  (no data returned)")

    print("\n=== state AFTER ===")
    after = {c: q(c) for c in STATE}
    after["ERR?"] = q("ERR?")
    drift = []
    for k, v in after.items():
        flag = ""
        if before.get(k) != v:
            flag = f"   <-- CHANGED (was {before.get(k)})"
            drift.append(k)
        print(f"  {k:7s} {v}{flag}")

    os.makedirs("dump_out", exist_ok=True)
    with open(OUT, "w", encoding="utf-8") as f:
        f.write(f"# CAL DUMP from {ident}\n")
        f.write(f"# state before: {before}\n")
        f.write(f"# state after:  {after}\n")
        f.write(f"# lines returned: {len(lines)}\n\n")
        f.write("\n".join(lines) + "\n")

    print(f"\n{len(lines)} line(s) -> {OUT}")
    if drift:
        print(f"WARNING: instrument state changed: {drift}")
    else:
        print("Instrument state unchanged. Error queue clean." if
              after.get("ERR?", "").startswith("0,0,0,0") else
              "Instrument state unchanged; check ERR? above.")
    inst.close()


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""probe_service_mode.py - read-only verification that ZSETADDR/ZRDWR? actually work.

Run this AFTER putting the 8593E into service mode, BEFORE any real dump.

Sends only: ID?, ERR?, ZSETADDR <hex>, ZRDWR?, and a deliberately nonexistent
query used as a control. NEVER sends a bare `ZRDWR <value>` (that would write).

The control test matters: in normal mode this analyzer silently ignores unknown
tokens, parses a bare number onto its value stack, and returns that stale number
for ANY unrecognized query. That makes a broken read look like plausible data.
If a command we know is fake returns the same value as ZRDWR?, the protocol is
not working -- no matter how reasonable the numbers look.
"""
import sys

FAKE = "ZQQNOTACMD?"      # cannot exist; used as the control


def main():
    import pyvisa
    rm = pyvisa.ResourceManager("@ivi")
    inst = rm.open_resource("GPIB0::7::INSTR")
    inst.timeout = 4000
    inst.read_termination = "\n"
    inst.write_termination = "\n"

    def q(cmd):
        try:
            return inst.query(cmd).strip()
        except Exception as e:
            return f"<ERR {type(e).__name__}>"

    def setaddr(a):
        inst.write(f"ZSETADDR {a:06X}")

    def rd(a):
        setaddr(a)
        return q("ZRDWR?")

    print("=== identity / health ===")
    ident = q("ID?")
    print(f"  ID?  -> {ident!r}")
    print(f"  ERR? -> {q('ERR?')!r}")
    if "8593" not in ident.upper():
        sys.exit(f"not an 8593E ({ident!r}) - stopping")

    print("\n=== control: value returned by a command that does not exist ===")
    ctrl_before = q(FAKE)
    print(f"  {FAKE} -> {ctrl_before!r}")

    print("\n=== ZRDWR? across addresses ===")
    addrs = [0x200000, 0x200001, 0x200002, 0x200003, 0x200000, 0x203040, 0x2FC000]
    vals = {}
    for a in addrs:
        v = rd(a)
        vals.setdefault(a, []).append(v)
        print(f"  {a:06X} -> {v!r}")

    print("\n=== control again, immediately after a ZRDWR? ===")
    setaddr(0x200000)
    live = q("ZRDWR?")
    ctrl_after = q(FAKE)
    print(f"  ZRDWR? -> {live!r}")
    print(f"  {FAKE} -> {ctrl_after!r}")

    # ---- verdict ----
    print("\n=== VERDICT ===")
    fails = []

    if ctrl_after == live:
        fails.append(f"fake command returns the SAME value as ZRDWR? ({live!r}) "
                     "-> ZRDWR? is not reading memory, just echoing the value stack")

    def as_int(s):
        try:
            return int(float(s.replace("E", "e")))
        except Exception:
            return None

    ints = {a: as_int(v[0]) for a, v in vals.items()}
    bad_range = {f"{a:06X}": i for a, i in ints.items()
                 if i is not None and not (0 <= i <= 255)}
    if bad_range:
        fails.append(f"values outside a byte range 0..255: {bad_range}")

    echo = {f"{a:06X}": i for a, i in ints.items()
            if i is not None and i == int(f"{a:06X}") if f"{a:06X}".isdigit()}
    if echo:
        fails.append(f"value equals the address argument (echo): {echo}")

    reps = vals.get(0x200000, [])
    if len(reps) > 1 and len(set(reps)) > 1:
        fails.append(f"same address read twice gave different values: {reps} "
                     "-> unstable/not real memory")

    distinct = {i for i in ints.values() if i is not None}
    if len(distinct) == 1:
        fails.append(f"every address returned the identical value {distinct.pop()!r} "
                     "-> constant, not real data")

    if fails:
        print("  PROTOCOL NOT WORKING - do NOT dump. Reasons:")
        for f in fails:
            print(f"    - {f}")
        print("\n  Any dump run now would record fabricated bytes.")
        sys.exit(1)

    print("  Looks like genuine byte reads:")
    print("    - fake command returns a DIFFERENT value than ZRDWR?")
    print("    - all values within 0..255")
    print("    - repeat reads of one address are stable")
    print("    - values do not echo the address argument")
    print("\n  Safe to proceed with:")
    print("    python dump859x.py --resource GPIB0::7::INSTR --regions cal-active")
    print("  (start with cal-active: ~12 KB, quick, and it is the data that matters)")
    inst.close()


if __name__ == "__main__":
    main()

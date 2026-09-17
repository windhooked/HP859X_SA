#!/usr/bin/env python3
"""probe_cmd.py - safely determine whether the 859x firmware RECOGNIZES a command.

Motivation: this analyzer's parser silently ignores unknown tokens, leaves any
bare number it parsed on its value stack, and returns that stale number for ANY
unrecognized query. So a "reasonable-looking" reply proves nothing. The only
reliable test is a control: compare against a command that CANNOT exist. If your
candidate query returns the same value as the fake one, it is not recognized.

This is the tool to use while reverse-engineering a memory-read primitive: it
tells you whether a candidate command name is real BEFORE you trust its output.

SAFETY
------
- By default this sends only queries (name ending in '?') plus the fake control.
  Query-only probing cannot write instrument state.
- --arg lets you append an argument (e.g. an address). A bare argument is left on
  the value stack; harmless on its own, but a real WRITE command would consume it.
  So --arg is gated behind --i-understand-writes-are-possible.
- It NEVER sends: CAL INIT, CAL STORE, CAL FETCH, DEFAULT CAL DATA, FACTSET,
  INIT FLAT, or anything matching a destructive denylist.
- Records CF?/SPAN?/RL?/ERR? before and after and flags any drift.

USAGE
-----
  python probe_cmd.py --cmd "ZRDWR?"                 # is ZRDWR? recognized?
  python probe_cmd.py --cmd "RDMEM?" --cmd "PEEK?"   # test several
  python probe_cmd.py --cmd "ZRDWR?" --arg 200000 --i-understand-writes-are-possible
"""
import argparse, sys

FAKE = "ZQZXNOTACMD?"          # control: cannot exist
DENY = ("CAL INIT", "CALINIT", "CAL STORE", "CALSTORE", "CAL FETCH", "CALFETCH",
        "DEFAULT", "FACTSET", "INIT FLAT", "INITFLAT", "CAL STOR", "STORE FLAT",
        "SET ATTN", "DISPOSE", "ERASE")
STATE = ("CF?", "SPAN?", "RL?", "ERR?")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--resource", default="GPIB0::7::INSTR")
    ap.add_argument("--cmd", action="append", required=True,
                    help="candidate command to test; repeatable")
    ap.add_argument("--arg", default=None,
                    help="argument to append (e.g. an address). Requires the "
                         "--i-understand-writes-are-possible flag.")
    ap.add_argument("--i-understand-writes-are-possible", action="store_true")
    ap.add_argument("--timeout", type=int, default=4000)
    args = ap.parse_args()

    for c in args.cmd:
        up = c.upper().replace("  ", " ")
        for d in DENY:
            if d in up:
                sys.exit(f"refusing: {c!r} matches destructive denylist entry {d!r}")
    if args.arg is not None and not args.i_understand_writes_are_possible:
        sys.exit("--arg can leave a value on the parser stack that a real WRITE "
                 "command would consume. Re-run with "
                 "--i-understand-writes-are-possible if that is intended.")

    import pyvisa
    rm = pyvisa.ResourceManager("@ivi")
    inst = rm.open_resource(args.resource)
    inst.timeout = args.timeout
    inst.read_termination = "\n"
    inst.write_termination = "\n"

    def q(cmd):
        try:
            return inst.query(cmd).strip()
        except Exception as e:
            return f"<ERR {type(e).__name__}>"

    ident = q("ID?")
    print(f"ID? -> {ident!r}")
    if "8593" not in ident.upper():
        sys.exit(f"not the expected 8593E ({ident!r})")

    before = {c: q(c) for c in STATE}

    def ctrl():
        return q(FAKE)

    print(f"\ncontrol {FAKE} -> {ctrl()!r}  (this value means 'not recognized')")
    print("-" * 64)

    for c in args.cmd:
        send = c if args.arg is None else f"{c.rstrip('?')} {args.arg}" + ("?" if c.endswith("?") else "")
        base = ctrl()                      # fresh control just before
        # If arg given and command is a query, set arg then query:
        if args.arg is not None and c.endswith("?"):
            inst.write(f"{c[:-1]} {args.arg}")
            resp = q(c)                    # some designs: set then re-query bare
        else:
            resp = q(send)
        after_ctrl = ctrl()
        recognized = (resp != base) and (resp != after_ctrl) and not resp.startswith("<ERR")
        verdict = "RECOGNIZED (different from control)" if recognized else \
                  "not recognized (matches control / echo / error)"
        print(f"{c!r:16s} sent={send!r}")
        print(f"    response      = {resp!r}")
        print(f"    control before= {base!r}   after= {after_ctrl!r}")
        print(f"    => {verdict}")
        print()

    after = {c: q(c) for c in STATE}
    drift = [k for k in STATE if before.get(k) != after.get(k)]
    print("-" * 64)
    if drift:
        print(f"WARNING: state changed: "
              + ", ".join(f"{k} {before[k]!r}->{after[k]!r}" for k in drift))
    else:
        print(f"state unchanged; ERR? = {after.get('ERR?')!r}")
    inst.close()


if __name__ == "__main__":
    main()

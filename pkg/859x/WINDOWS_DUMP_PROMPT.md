# Task: back up an HP/Agilent 8593E spectrum analyzer's calibration data + memory over GPIB (Windows)

You are helping me pull a calibration/memory backup off a real **HP/Agilent 8593E**
spectrum analyzer. The instrument is connected to **this Windows PC** through a
**National Instruments GPIB-USB-HS** adapter, and **NI-488.2 / NI-VISA is installed
and working** (the adapter already talks to the instrument via NI's driver). The
GPIB primary address is **7** (confirm; HP default is 18).

Guide me step by step, and where you can run commands, do so. Explain what each
step does before running it.

## Hard safety rules (do not violate)
- This is a **read-only** backup. The only instrument commands you may send are
  `ID?`, `ZSETADDR <6-hex-address>`, and `ZRDWR?` (the query form). **Never** send
  a bare `ZRDWR <value>` or any other write/command — `ZRDWR` without `?` WRITES
  memory and could corrupt the instrument's calibration. Reads never harm it.
- If `ID?` does not return a string containing `8593`/`8590`/`8591`/`8595` (an
  859x), STOP and tell me — the memory map below is for the 859x family only.
- Do not power-cycle, preset, or change instrument settings.

## Verified facts (from reverse-engineering the 859x firmware; trust these)
- The service-mode protocol is: send `ZSETADDR AAAAAA` (6 uppercase hex digits =
  a 24-bit CPU address), then `ZRDWR?` returns **one byte as a decimal integer**.
  One byte per GPIB round-trip (so it's slow: ~100–300 B/s).
- Memory map (24-bit bus; `end` is EXCLUSIVE), verified against the firmware's own
  boot access and the A16A1 memory-board PAL decode — board-level, applies to the
  8593E:
  - `0x200000–0x210000` **CalNVRAM** — battery-backed calibration NVRAM, 64 KB
    window. The **active cal data lives in `0x200000–0x203040`** (~12 KB); the rest
    of the 64 KB is dumped too (if the physical SRAM is smaller it just aliases —
    harmless, and the repeat pattern reveals its true size). **This is the
    irreplaceable data — back it up first.**
  - `0x2FC000–0x300000` **CalRAM** — working cal scratch RAM, 16 KB.
  - `0xFF0000–0xFFF000` **RAM** — main working RAM, 60 KB (live state).
  - `0x000000–0x100000` **ROM** — 1 MB firmware (optional verify; hours byte-wise).

## What to build and run
Create `dump859x.py` (full source below), then run, in order:

```
pip install pyvisa
python dump859x.py --resource GPIB0::7::INSTR --id           # confirm it's the 8593E
python dump859x.py --resource GPIB0::7::INSTR --cal-ascii     # CAL backup: 64KB NVRAM + 16KB CalRAM + ASCII cal constants
python dump859x.py --resource GPIB0::7::INSTR --regions ram   # then the 60KB working RAM
```

Files land in `dump_out\`. Each region is a separate file and is **resumable** —
if a run is interrupted, rerun the same command and it continues from the last
saved byte. If NI-MAX shows the analyzer at a different address, change `::7::`
(e.g. `GPIB0::18::INSTR`); the `--id` step reports immediately if it's wrong.

After the CAL dump, show me:
- the sizes of `dump_out\calnvram_*.bin` and `dump_out\calram_*.bin`,
- a hexdump of the **first 256 bytes** of the calnvram file (that's where the
  active cal constants + checksum live),
- and the contents of `dump_out\cal_constants.txt`.
I will validate those against a known-good reference.

## dump859x.py (complete, self-contained)

```python
#!/usr/bin/env python3
"""dump859x.py - read-only GPIB memory + calibration backup for the HP/Agilent 859x.
Sends only ID? / ZSETADDR / ZRDWR? . Resumable per-region byte dump over any
pyvisa backend (NI-VISA is auto-selected on Windows)."""
import argparse, os, time

# end is EXCLUSIVE. Addresses verified against the 859x firmware + A16A1 PAL decode.
REGIONS = {
    "calnvram":   (0x200000, 0x210000, "battery-backed cal NVRAM (64 KB; active data 0..0x303F)"),
    "calram":     (0x2FC000, 0x300000, "working cal scratch RAM (16 KB)"),
    "ram":        (0xFF0000, 0xFFF000, "main working RAM (60 KB)"),
    "rom":        (0x000000, 0x100000, "firmware ROM (1 MB) - long!"),
    "cal-active": (0x200000, 0x203100, "just the active cal data (~12 KB, quick)"),
}
GROUPS = {"cal": ["calnvram", "calram"], "all": ["calnvram", "calram", "ram", "rom"]}

def open_instr(resource, timeout_ms):
    import pyvisa
    last = None
    for backend in ("@ivi", "@py", ""):   # @ivi = installed NI-VISA (Windows)
        try:
            rm = pyvisa.ResourceManager(backend) if backend else pyvisa.ResourceManager()
        except Exception as e:
            last = e; continue
        if resource is None:
            return rm, None
        try:
            inst = rm.open_resource(resource)
            inst.timeout = timeout_ms
            inst.read_termination = "\n"
            inst.write_termination = "\n"
            print(f"[open] {resource} via backend {backend or 'default'}")
            return rm, inst
        except Exception as e:
            last = e; continue
    raise SystemExit(f"could not open {resource}. Last error: {last}")

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
    inst.write(f"ZSETADDR {addr:06X}")   # set address (no response)
    return int(inst.query("ZRDWR?").strip()) & 0xFF   # read one byte, decimal

def dump_region(inst, name, start, end, outdir, flush_every=256):
    path = os.path.join(outdir, f"{name}_{start:06X}_{end:06X}.bin")
    total = end - start
    done = os.path.getsize(path) if os.path.exists(path) else 0
    if done >= total:
        print(f"[{name}] already complete ({total} bytes) - {path}"); return path
    mode = "ab" if done else "wb"
    print(f"[{name}] {start:06X}..{end:06X} ({total} B); resuming at +{done}")
    t0 = time.time()
    with open(path, mode, buffering=0) as f:
        buf = bytearray()
        for addr in range(start + done, end):
            buf.append(read_byte(inst, addr))
            if len(buf) >= flush_every:
                f.write(buf); buf.clear()
                n = addr - start + 1
                rate = n / max(1e-6, time.time() - t0)
                eta = (total - n) / max(1e-6, rate)
                print(f"  {addr:06X}  {n}/{total}  {rate:5.0f} B/s  ETA {eta/60:5.1f} min",
                      end="\r", flush=True)
        if buf:
            f.write(buf)
    print(f"\n[{name}] done -> {path}  ({time.time()-t0:.0f}s)")
    return path

def cal_ascii(inst, outdir):
    path = os.path.join(outdir, "cal_constants.txt"); got = 0
    with open(path, "w") as f:
        for cmd in ("CAL?", "ERR?", "SER?", "REV?", "MODEL?", "IDN?",
                    "SPAN?", "CF?", "RL?"):
            try:
                r = inst.query(cmd).strip()
            except Exception as e:
                r = f"<no response: {e}>"
            if r and not r.startswith("<no response"):
                got += 1
            f.write(f"# {cmd}\n{r}\n\n")
    print(f"[cal-ascii] {got} responsive queries -> {path}")

def main():
    ap = argparse.ArgumentParser(description="859x read-only GPIB memory + cal backup")
    ap.add_argument("--resource", help="VISA resource, e.g. GPIB0::7::INSTR")
    ap.add_argument("--list", action="store_true")
    ap.add_argument("--id", action="store_true")
    ap.add_argument("--regions", default="cal",
                    help=f"comma list. regions={list(REGIONS)} groups={list(GROUPS)}")
    ap.add_argument("--outdir", default="dump_out")
    ap.add_argument("--timeout", type=int, default=5000)
    ap.add_argument("--cal-ascii", action="store_true")
    args = ap.parse_args()

    if args.list:
        import pyvisa
        for backend in ("@ivi", "@py"):
            try:
                print(f"{backend}: {pyvisa.ResourceManager(backend).list_resources('?*')}")
            except Exception as e:
                print(f"{backend}: unavailable ({e})")
        return

    if not args.resource:
        raise SystemExit("give --resource (e.g. GPIB0::7::INSTR); --list to enumerate.")
    rm, inst = open_instr(args.resource, args.timeout)
    q, ident = identify(inst)
    print(f"[id] {q} -> {ident!r}")
    if args.id:
        return
    if ident and not any(m in ident.upper() for m in
                         ("8590","8591","8592","8593","8594","8595","8596")):
        raise SystemExit(f"{ident!r} is not an 859x - aborting (map would not apply).")

    os.makedirs(args.outdir, exist_ok=True)
    want = []
    for tok in args.regions.split(","):
        tok = tok.strip()
        if tok in GROUPS: want += GROUPS[tok]
        elif tok in REGIONS: want.append(tok)
        else: raise SystemExit(f"unknown region {tok!r}")
    seen, order = set(), []
    for r in want:
        if r not in seen: seen.add(r); order.append(r)

    if args.cal_ascii:
        cal_ascii(inst, args.outdir)
    for name in order:
        s, e, desc = REGIONS[name]
        print(f"\n=== {name}: {desc} ===")
        dump_region(inst, name, s, e, args.outdir)
    print("\nAll requested regions complete. Files in", args.outdir)

if __name__ == "__main__":
    main()
```

## If something goes wrong
- `--id` returns nothing / times out → wrong address; try `GPIB0::18::INSTR`, or
  check the analyzer's GPIB address (SHIFT/CONFIG → ANALYZER ADDRESS) and NI-MAX.
- `pyvisa` can't find a VISA library → NI-VISA isn't on the PATH; reinstall
  NI-488.2/NI-VISA (Runtime is enough).
- Reads return implausible constant values (all 0x00 / 0xFF) → tell me; that's a
  protocol/terminator issue, not the real data, and I'll adjust.

---

## BONUS (please also do this): capture a USB trace of NI's driver init

**Why:** this exact adapter (an NI GPIB-USB-HS, `3923:709b`) works on Windows but
NOT yet on Linux (linux-gpib). It's a newer hardware revision that reports an
init status byte `0x15` the Linux driver doesn't recognize, then hangs. The one
piece of data needed to fix the Linux driver is a **USB capture of what NI's
Windows driver sends when it opens the adapter** — and this Windows PC is the
only place that trace exists. Capturing it now (while set up) saves a second trip.

**This is a passive capture — it does not touch the instrument's data. Safe.**

Steps:
1. Install **Wireshark** and, when its installer offers it, the **USBPcap** capture
   component (or install USBPcap separately from https://desowin.org/usbpcap/).
   Reboot if USBPcap asks.
2. Fully close NI-MAX and any program using the adapter. Open Wireshark; in the
   capture-interfaces list pick the **USBPcap** interface that shows the NI
   **GPIB-USB-HS** device (USBPcapN — match it by unplugging/replugging to see
   which interface's device list changes, or by the "GPIB-USB-HS" product string).
3. **Start** the capture, then trigger a fresh driver open of the adapter — do ONE
   of:
   - open **NI-MAX**, expand Devices/Interfaces, click the GPIB-USB-HS, and do
     "Scan for Instruments" / open a VISA session to the 8593E; **or**
   - run this tiny script (opens + IDs, which forces the full init):
     `python -c "import pyvisa; r=pyvisa.ResourceManager(); d=r.open_resource('GPIB0::7::INSTR'); print(d.query('ID?'))"`
   - Best of all: **unplug the adapter, Start the capture, then plug it back in**
     so the capture includes the driver's fresh attach/init from scratch, then do
     the open/ID above.
4. **Stop** the capture and **Save As** `ni_gpib_hs_init.pcapng`.
5. Give me:
   - the saved `ni_gpib_hs_init.pcapng` file (the important one), and
   - the adapter's info from Device Manager → the GPIB-USB-HS → Details →
     "Hardware Ids" (shows VID/PID/REV), plus its NI-488.2 driver version.

I'll diff that capture against the Linux driver's init sequence to find the extra
step this revision needs, patch linux-gpib, and then the whole backup runs from
Linux next time. (Full analysis: `pkg/859x/LINUX_GPIB_NI_HS.md`.)

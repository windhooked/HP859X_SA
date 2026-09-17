# linux-gpib × NI GPIB-USB-HS (REV_0101) — handoff for the Linux-side agent

**From:** the agent on the Windows PC where this adapter works under NI-VISA.
**To:** the agent with hands on the Linux machine running linux-gpib.
**Goal:** make this specific NI GPIB-USB-HS work under linux-gpib so the HP 8593E
backup can run from Linux instead of Windows.

---

## 1. The exact hardware (verified from Windows Device Manager)

| Field | Value |
|---|---|
| Product | NI GPIB-USB-HS |
| USB VID:PID | `3923:709b` |
| **Hardware revision** | **`REV_0101`** (full: `USB\VID_3923&PID_709B&REV_0101`) |
| Board serial | `02170F4A` |
| Windows driver | National Instruments 15.5.0.49152 (2016-01-21) |
| NI-488.2 API | ni4882.dll 15.5.0f0 |
| VISA | visa32.dll 5.7.520.0 (IVI) |

It is a **plain GPIB-USB-HS, not an HS+** (PID 709b, not 761e/7618). So the HS+
firmware-upload path (`hsplus_load`, PID 0x761e→0x7618) does **not** apply here — this
device needs no firmware upload, only correct init handling.

**Reported symptom:** under linux-gpib it reports an init status byte **`0x15`** the
driver doesn't recognise, then hangs. (See §4 for why "hangs" needs verifying.)

---

## 2. The single most important thing to collect first

Before any patching, get the **full** kernel log of a failed attach, including the raw
16-byte block the driver dumps on a mismatch:

```
sudo dmesg -w &        # or journalctl -kf
# then replug the adapter / gpib_config, and capture everything with:
dmesg | grep -iE "ni_usb|gpib|wait_for_ready|unexpected"
```

The driver calls `ni_usb_dump_raw_block(buffer, retval)` on any mismatch, which prints
**all 16 bytes**. That tells us the exact `buffer[j]` position of the `0x15` and the full
pattern this REV_0101 returns — which is what every patch decision hinges on. A prior
newer-revision report showed e.g. `buffer[6]=0x16` and `buffer[7]=0x4/0x6`; ours is one
step along that family. **Paste that raw block back to this thread / file.**

---

## 3. The code that matters: `ni_usb_hs_wait_for_ready()`

File: `drivers/staging/gpib/ni_usb/ni_usb_gpib.c` (kernel staging), or
`drivers/ni_usb/ni_usb_gpib.c` (sourceforge linux-gpib). The init polls the device with
`NI_USB_POLL_READY_REQUEST`, reads a 16-byte buffer, and validates byte-by-byte. Current
upstream (v6.14) excerpt of the decision bytes:

```c
if (buffer[++j] != 0x0) {                 // buffer[6]
    ready = 1;
    // NI-USB-HS+ sends 0xf here
    if (buffer[j] != 0x2 && buffer[j] != 0xe && buffer[j] != 0xf &&
        buffer[j] != 0x16) {
        dev_err(... "expected 0x2, 0xe, 0xf or 0x16" ...);
        unexpected = 1;                   // <-- ONLY logs; does NOT clear `ready`
    }
}
if (buffer[++j] != 0x0) {                 // buffer[7]
    ready = 1;
    // MC usb-488 sends 0x5; MC usb-488A sends 0x6
    if (buffer[j] != 0x3 && buffer[j] != 0x5 && buffer[j] != 0x6 &&
        buffer[j] != 0x8) { ...; unexpected = 1; }
}
...
if (buffer[++j] != 0x0) {                 // buffer[10]
    ready = 1;
    if (buffer[j] != 0x96 && buffer[j] != 0x7 && buffer[j] != 0x6e) { ...; unexpected = 1; }
}
if (unexpected)
    ni_usb_dump_raw_block(buffer, retval);
if (ready)
    break;                                // success path
```

### The crucial observation

`ready` is set to 1 whenever **any** of `buffer[6]`, `buffer[7]`, `buffer[10]` is non-zero.
An **unrecognised** value there sets `unexpected = 1`, which **only logs and dumps** — it
does **not** clear `ready` and does **not** fail. Upstream even **already accepts `0x16`**
at `buffer[6]` (added for an earlier newer-revision unit).

**Therefore, on current staging, `0x15` at buffer[6] would set `ready`, print one warning,
and proceed — it would NOT hang.** Worst case if [6]/[7]/[10] are all zero: the loop spins
50 × 100 ms = 5 s, then `retval = 0` and returns success anyway. **This function is
essentially hang-proof.** That drives the diagnosis below.

---

## 4. Diagnosis decision tree

**Step A — identify the codebase (this changes everything):**
```
modinfo ni_usb_gpib | grep -E "filename|version|vermagic"
dpkg -l | grep -i gpib          # Debian/Ubuntu package version
# in-tree staging?  ls /lib/modules/$(uname -r)/kernel/drivers/staging/gpib/ni_usb/
```

- **SourceForge linux-gpib 4.3.x (most distros):** older `wait_for_ready`. Fetch that exact
  version's function and compare its accepted-value lists — they are shorter (likely no
  `0x16`, possibly stricter). If that version genuinely hard-fails, the fix is the simple
  constant addition in §5. **Most likely your case.**
- **Kernel staging 6.13+:** already forgiving (see §3). If it *still* fails, the `0x15` is a
  symptom, not the cause — go to Step C.

**Step B — confirm whether it truly hangs or just warns.** Given §3, a real hang is unlikely
to originate in `wait_for_ready`. Check the timestamps in dmesg: does it print the warning
then continue, stall exactly ~5 s, or block indefinitely on a *later* call
(`ni_usb_read`/`ni_usb_write`/board-online bulk transfer)? Where it blocks is the real bug.

**Step C — if the hang is downstream, suspect init state, not the status byte:**
- On **Windows**, NI's own driver leaves the adapter **not Controller-In-Charge** after
  attach — every bus op returned `ECIC` until an explicit `SendIFC` made it CIC. If the
  Linux init likewise doesn't establish controller/IFC state for this revision, downstream
  ops will time out and *look* like a hang. Check the online/IFC path
  (`ni_usb_online`, `SendIFC`/`ni_usb_command`, REN assertion).
- There is a known **`ni_usb_init` buffer-overflow fix** in the 2024 staging patch series —
  make sure your tree has it; an overflow there can corrupt exactly this init handshake.

---

## 5. Candidate patch (once the raw block pins the position)

If the dump shows `0x15` at `buffer[6]` (most likely, by analogy to the `0x16` case), add it
to the accepted list so the spurious warning stops:

```c
        if (buffer[j] != 0x2 && buffer[j] != 0xe && buffer[j] != 0xf &&
            buffer[j] != 0x16 && buffer[j] != 0x15) {   // <-- add 0x15 (REV_0101)
```

If it lands at `buffer[7]` or `buffer[10]`, add it to *that* position's list instead — the
raw block tells you which. On **old sourceforge 4.x**, apply the equivalent change in that
version's function (line numbers differ; the byte-check structure is the same).

**Do not** just widen the check blindly — confirm the position from the dump first, and keep
the change minimal so it can go upstream. If §4 Step C shows a downstream hang, the constant
addition alone will NOT fix it; the missing piece is init/IFC or an extra control request
(see §6).

---

## 6. What a Windows USB capture would add (still available on this PC)

The staging driver's own comment (line ~2113) admits HS+ init was reverse-engineered from
Windows and *"I'm not sure what the other 2 requests do."* For this REV_0101, a fresh
capture of NI's Windows driver init would reveal:
1. the exact `buffer[]` pattern this revision returns (removes all guessing in §5), and
2. any **extra control request** NI sends that linux-gpib omits (the likely downstream fix).

This Windows box is the only place that trace exists. **If the raw dmesg block (§2) plus the
minimal patch (§5) don't resolve it, request the capture** and the Windows-side agent will
produce `ni_gpib_hs_init.pcapng` (USBPcap across an unplug/replug + a VISA open) and extract
the control-transfer sequence. Not done yet — deferred until we know the patch alone is
insufficient, to avoid an install that may need a reboot.

---

## 7. Concrete next steps for you (Linux side)

1. `modinfo ni_usb_gpib` + `dpkg -l | grep gpib` → report the version/tree (§4A).
2. Reproduce the failure with `dmesg`/`journalctl -kf` capturing; paste the **full raw
   16-byte block** from `ni_usb_dump_raw_block` (§2).
3. State whether it warns-and-continues, stalls ~5 s, or blocks indefinitely, and on which
   function (§4B).
4. If old/strict version and hard-fail at a status byte → apply §5, rebuild, retest.
5. If downstream hang → check IFC/CIC/online path (§4C) and request the Windows capture (§6).
6. Report back: version, raw block, where it blocks. That's enough to finalise the patch.

---

## 8. Sources

- linux-gpib `ni_usb_gpib.c` (kernel staging, v6.14): torvalds/linux
  `drivers/staging/gpib/ni_usb/ni_usb_gpib.c`
- SourceForge bug #47 "NI GPIB-USB-HS new model does not work" (buffer[7]/[9] mismatches)
- linux-gpib-general list: "NI GPIB-USB-HS hang?" and "ni_usb_hs problem"
- fmhess/linux_gpib_firmware, fmhess/hsplus_load (HS+ only — not this device)
- Gersoft-lab/ni-usb-gpib-fixes (newer-firmware quirks; verify branch/paths)
- Windows-side facts: see this repo's `FINDINGS.md` (the ECIC/not-CIC init detail is in §"Hardware gotcha").

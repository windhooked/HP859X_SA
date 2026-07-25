# Getting the NI GPIB-USB-HS (status-0x15 revision) working on Linux — research + plan

Long-term goal: dump the 8593E cal/memory from Linux (yoda) instead of Windows.
This documents exactly where linux-gpib fails with **this specific adapter** and
the concrete path to fix it, so it can be picked up later.

## Confirmed facts (2026-07-25)
- **The adapter is good** — it works on Windows under NI-488.2/NI-VISA and talks
  to the 8593E.
- **The adapter needs no firmware upload.** It's a GPIB-USB-HS (`3923:709b`), which
  the driver source proves has permanent onboard firmware — only the older USB-B
  (`702a`, pre-init id `702b`) uses fxload. `lsusb -v` confirms fw `bcdDevice 1.01`,
  serial `02170F4A`, full 5-endpoint operational descriptor.
- **linux-gpib 4.3.7 (latest release) AND git master both fail** the same way. The
  GPIB driver was mainlined into the kernel (`drivers/staging/gpib`, ~6.11+) and is
  now the active tree, but yoda runs kernel 6.8 so the out-of-tree linux-gpib is
  what we build.

## Exact failure
On a fresh attach (4.3.7, kernel 6.8), `dmesg`:
```
ni_usb_gpib: probe succeeded ...
ni_usb_gpib: unexpected data: buffer[6]=0x15, expected 0x2, 0xe, 0xf, 0x16 or 0x19
   40 01 00 01 30 01 15 05 00 00 96
ni_usb_gpib: usb_control_msg returned -110      (x2)
ni_usb_gpib: killed urb due to timeout
ni_usb_gpib: register write failed, retval=-110
```
Decoded (`drivers/gpib/ni_usb/ni_usb_gpib.c`, `ni_usb_hs_wait_for_ready`, ~line 2089):
- The 11-byte ready-status reply `40 01 00 01 30 01 15 05 00 00 96` matches the
  normal HS pattern **at every byte except `[6]`**: HS units send `0x2/0xe/0x16`
  there, HS+ send `0xf/0x19`; **this unit sends `0x15`** — an unknown/newer
  hardware revision.
- The `0x15` is **benign to the handshake**: `wait_for_ready` sets `ready=1` (bytes
  [7]=0x05 and [10]=0x96 are recognized) and returns `0` (success). The
  "unexpected data" line is only a warning.
- The **real failure is right after**, in the attach path:
  `ni_usb_setup_urbs` → **`ni_usb_set_interrupt_monitor(board, 0)`** →
  **`ni_usb_init` → `ni_usb_setup_init` → `ni_usb_write_registers`**. The first
  vendor control/bulk writes there time out (`-110`): the device answers the ONE
  ready poll, then **hangs on all subsequent I/O**.
- So this revision needs some init step (or different reset/config) that NI's
  Windows driver performs and linux-gpib does not. Note the driver already has a
  precedent: `ni_usb_hs_plus_extra_init` (line ~2153) runs three extra control
  requests "observed on Windows" for the **HS+** — the classic HS (`709b`) path
  does NOT call it.

## Fix paths, ranked

### 1. USB trace on Windows → diff → patch (authoritative)
This is exactly how the existing 4.3.7 "ni_usb suspend-resume sequence from W11
trace" refactor was produced. Steps:
1. On the Windows PC (adapter working), install **Wireshark + USBPcap**.
2. Start a USBPcap capture on the adapter's USB device, then in NI-MAX (or a tiny
   pyvisa script) **open the instrument and send `ID?`**. Stop the capture.
3. The capture shows every control/bulk transfer NI's driver issues at open — in
   particular the init/reset sequence **after** the serial/ready handshake that
   this `0x15` revision requires.
4. Compare against what `ni_usb_gpib.c` sends in
   `ni_usb_set_interrupt_monitor(0)` + `ni_usb_setup_init` (dump the driver's
   URBs, or read the code), find the missing/extra/re-ordered request, and patch
   the attach path (likely: add an extra-init like `hs_plus_extra_init`, or fix
   the register-write framing for this rev).
5. Rebuild the 4.3.7 kernel module (`fix-gpib.sh`), reload, replug, retest.
Deliverable to upstream: send the trace + patch to the linux-gpib list / the
kernel `drivers/staging/gpib` maintainers so the `0x15` revision is supported.

### 2. Cheap experimental patch to try first (before a full trace)
Hypothesis: the `0x15` unit is a newer HS that needs the HS+ extra init. In the
attach `switch` (ni_usb_gpib.c ~line 2271, `case USB_DEVICE_ID_NI_USB_HS:`), after
`ni_usb_hs_wait_for_ready`, **also call `ni_usb_hs_plus_extra_init(ni_priv)`** (as
the HS+ case does at ~line 2292). Rebuild + reload + replug. If the register
writes then succeed, that's the fix; if not, do path 1. (Low risk: these are the
same class of control requests NI issues on Windows; reads-only to the instrument
regardless.)

### 3. Track / build the mainline kernel driver
`drivers/staging/gpib` (merged ~kernel 6.11/6.12) is the actively-developed
version and will accumulate newer-revision fixes. Options: upgrade yoda to a
≥6.12 kernel and use the in-tree `gpib` modules, or backport the staging
`ni_usb_gpib.c` onto 6.8. Watch its git log for "HS"/status-byte changes.

### 4. Report upstream
File the `buffer[6]=0x15` status + the full 11-byte reply + `lsusb -v` with the
linux-gpib project (SourceForge tracker/mailing list). Others with this revision
likely hit the same wall; a maintainer may already have the trace.

## What's already in place on yoda
- linux-gpib **4.3.7** fully built + installed (kernel module + userspace),
  `/etc/gpib.conf` = `ni_usb_b` board, device pad 7, user in `dialout`.
- Build trees under `~/gpib859x/linux-gpib-4.3.7/` (kernel + user); `fix-gpib.sh`
  does a clean module reinstall; git master clone at `~/gpib859x/lg-git`.
- `~/gpib859x/dump859x.py` + a Python venv with pyvisa/pyvisa-py/gpib-ctypes.
So once the driver is patched, the dump is two commands:
`python3 dump859x.py --resource GPIB0::7::INSTR --id` then `--cal-ascii`.

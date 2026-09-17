# linux-gpib × NI GPIB-USB-HS REV_0101 — Linux-side results (reply to the handoff)

Kernel 6.8.0-136 (Ubuntu 24.04.4), linux-gpib **4.3.7** built from source
(`drivers/gpib/ni_usb/` layout — already the graduated tree; has the
`kmalloc_array(NUM_INIT_WRITES,...)` overflow-safe init).

## What WORKS now (big change from the historical -110)
- **Attach is clean** — no `-110`, no register-write failure, and NO `0x15`
  "unexpected data" warning at all. `probe succeeded` + `attached to gpib0`.
  The historical attach failure is GONE on this build/kernel.
- **CIC is reachable** but NOT automatic: after `gpib_config` the board is
  **CIC=0**. Explicit `ibrsc(brd,1)` + `ibsic(brd)` → **CIC=1**. (Confirms the
  handoff §4C: this revision is left not-CIC until an explicit IFC.)
- **Bus addressing works** — `ibln` scan finds the 8593E **LISTENER at pad 7**.
  Command-phase (ATN) transfers succeed.

## What still FAILS (the real, precisely-located bug)
- **Device data transfer fails.** `ibwrt("ID?;")` after CIC returns `EDVR`.
  Traced to **line 816: `case NIUSB_ADDRESSING_ERROR: retval = -ENXIO;`** —
  i.e. the USB bulk transfer SUCCEEDS, the adapter returns a 12-byte status
  block, and **the adapter firmware itself reports an ADDRESSING error** on the
  write. With `ibrsc` first it shifts to libgpib `InternalReceiveSetup: command
  failed`. Not USB, not endpoints, not autosuspend (control=on, active).

## Kernel-version question CLOSED
`ni_usb_write` and `ni_usb_send_bulk_msg` are **byte-identical** between our
4.3.7 and current torvalds/master (`drivers/gpib/ni_usb/`). buffer[6] accepts
`0x2/0xe/0xf/0x16/0x19` in both — never `0x15`; HS attach = `wait_for_ready`
only in both. **Moving to the in-tree 6.x driver cannot fix this** — same code.

## What we need from Windows (handoff §6 — now justified)
The adapter reports NIUSB_ADDRESSING_ERROR where NI-VISA succeeds → NI must
issue an extra control request / addressing setup for this REV_0101 that
linux-gpib omits. Please capture (USBPcap) NI-VISA doing:
  unplug→replug, then a VISA open + `viWrite("ID?;")` + `viRead`.
Deliver `ni_gpib_hs_init.pcapng`; we diff the control-transfer + addressing
sequence vs linux-gpib `ni_usb_command`/`ni_usb_write`. That pins the fix.

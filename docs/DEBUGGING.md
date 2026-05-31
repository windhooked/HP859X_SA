# Interactive debugging — GDB remote stub

`cmd/gdbserver` exposes the running 8593A emulator (with all device models wired)
as a **GDB remote target**, so you get breakpoints, single-step, register/memory
inspection, and — the key RE feature — **read/write/access watchpoints** (break
when *anyone* touches a given address). Implemented in `pkg/emu/gdb` over the
bus `OnRead`/`OnWrite` hooks.

## Start the server

```bash
# attach at reset:
go run ./cmd/gdbserver
# or fast-forward the boot first (with the IRQ5 timer), then attach near a point
# of interest (e.g. just before the operating UI renders at ~150M cycles):
go run ./cmd/gdbserver -boot 145000000      # then connect and step into the draw
```
It listens on `:3333` (override with `-addr`). `GDB_LOG=1` logs the RSP packets.

## Connect

**gef / pwndbg over a real m68k gdb (recommended — full register fidelity):**
```bash
gdb -ex 'set architecture m68k' -ex 'target remote :3333'
# in gdb:  watch *0xFFB0EC    rwatch *0x2b37f    b *0x5ED7E    c    si    info reg
```
Needs an m68k-aware gdb (`m68k-elf-gdb`, `gdb-multiarch`, or a gdb built
`--target=m68k-elf`). gef/pwndbg are Python plugins layered on that gdb.

**rizin (already installed — zero extra setup):**
```bash
rizin -a m68k -b 32 -e cfg.bigendian=true -d gdb://localhost:3333
# memory/disasm/breakpoints/watchpoints/stepping all work:
#   pd 8        ds        db 0x5ED7E      dc      dm        px 32 @ 0xFFB0EC
```
Caveat: rizin's XML-register mapping is i386-centric, so the m68k PC/SR *register
roles* don't map perfectly (read them via the gdb `p` protocol or memory if
needed). Everything else works.

## Custom monitor commands

```
monitor boot <cycles>   fast-forward the firmware (with IRQ5) by N cycles
monitor irq <n>         pulse autovector IRQ n once
```

## Cracking RE nuts with watchpoints

The motivating use: find the global **annunciator status word** the firmware
tests before drawing ADC-* FAIL / REF UNLOCK / OVEN COLD. Approach:
1. `-boot` to just before the annunciators draw.
2. Set a watchpoint on the screen/VRAM region or the candidate RAM word.
3. `continue`; at the hit, inspect the PC + backtrace + nearby memory to find the
   status decision — then model the analog status that flips it.

Watchpoints replace the one-off Go trace probes (`cmd/annunctrace` etc.) with
proper interactive break-on-access.

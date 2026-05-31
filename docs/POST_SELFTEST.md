# A16 Power-On Self-Test (POST) — the "FAIL: xxxx" display

Cracked 2026-05-31 using the new GDB watchpoint debugger (`pkg/emu/gdb`,
`cmd/gdbserver`) + `cmd/failcode` + `cmd/post`.

## The display

At boot the firmware renders `FAIL: DF0F 0000000000` on the left of the screen.
This is the **power-on self-test result**, not a RAM word — it is formatted on
the fly from two hardware status latches.

## The reporter (ROM 0x184DE)

```
0184EA  move.b $f610.w, D6    ; read POST result LOW  byte  @ 0xFFF610
0184EE  not.b  D6             ; latches are active-low (set bit = test PASSED)
0184F0  move.b $f612.w, D0    ; read POST result HIGH byte  @ 0xFFF612
0184F4  not.b  D0
0184F6  andi.b #$ec, D0       ; only bits 0xEC of the high byte count as failures
0184FA  or.b   D6, D0
0184FC  beq    $18558         ; (~f610 | (~f612 & 0xEC)) == 0  →  NO failure → skip
...
01851C  jsr    $6ca.w         ; format NOT(f612) as hex  → the "DF"
018526  jsr    $6ca.w         ; format NOT(f610) as hex  → the "0F"
```

So **`FAIL: DF0F` = `NOT(f612):NOT(f610)`**. A clean POST requires:

- `f610 == 0xFF`  (all 8 low-byte tests passed)
- `f612 & 0xEC == 0xEC`  (high-byte bits 2,3,5,6,7 passed; bits 4,1,0 don't count)

## How f610/f612 are built (ROM 0x4998 + 0x4534 analog suite)

The POST clears `f610`/`f612`, then runs a suite of A16 bus/peripheral integrity
tests and `or.b`s a PASS bit per subsystem. `f610`/`f612` are read/write latches
in the MMIO backing store, so the writes stick; the tests fail on our virtual
instrument because they probe hardware readback paths a flat backing store does
not replicate. Each bit:

| latch.bit | test | ROM | what it checks | model |
|-----------|------|-----|----------------|-------|
| f614/f616 strap | "mark all pass" | 0x49A0 | if either status input ≠ 0 → `f610=f612=0xFF` then run detailed suite (which `or.b`s, never clears) | **assert f614=f616=0xFF** (POST-bypass strap) |
| f612.3 | data-path loopback | 0x4A0E | write pattern→`0xFFF700`, read `0xFFF780`, expect echo | **f780↔f700 mirror** (addr bit 7 not decoded) |
| f612.6 | address-decoder latch | 0x4AA0 | write `0xFFF700+i*2`, read `0x320000 & 0x1F`, expect `==i` | **A16AddrLatch @ 0x320000** ← MMIO addrLatch |
| f612.7 | HD63484 VRAM wrap | 0x4B0C→0xD6B2 | write pattern to ACRTC VRAM, read back ×16384, expect match | **NOT YET** — needs the HD63484 RD command |

`bb2c` is the suite-local accumulator (all 27 ROM refs live in 0x4500..0x49E8),
so the bypass strap only affects the POST verdict — safe.

## Status (2026-05-31)

Three faithful hardware models implemented in `pkg/emu/device/mmio.go` +
`pkg/emu/machine/machine.go`:
1. **POST-bypass strap** f614/f616 (constructor).
2. **f700↔f780 data-path mirror** (Read).
3. **A16 write-address latch @ 0x320000** (`addrLatch` + `A16AddrLatch`).

Result: `DF0F → CC00 → C000 → 8000`. **f610 fully clean; f612 = 0x7F** (15/16
bits). The last bit (f612.7) is the HD63484 ACRTC VRAM read-back wrap at ROM
0xD6B2 — write a pattern to VRAM (cmd 0x5800), then a 16384-word read-back loop
(cmd 0x4400, MAR=0x4000:0) comparing each read of `0xFFF5FE` to the pattern. Our
display tracks `vram` + `MAR` for raster *writes*; implementing the inverse RD
path would clear it.

## Note: the status annunciators are SEPARATE

`REF UNLOCK`, `ADC-TIME FAIL`, `OVEN COLD` persist unchanged across all four FAIL
states above — they are **not** driven by the f610/f612 POST word. They have an
independent status source (still to be cracked with the same watchpoint method).

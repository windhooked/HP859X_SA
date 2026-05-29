# Open Investigation — HP-IB Command Dispatch

This document captures the **end-to-end dispatch problem** the emulator
has reached the limits of without solving, plus everything we've ruled
out and the remaining hypotheses worth testing. The goal is that a
future investigator (Claude or human) can pick up cleanly without
re-reading the conversation that produced these findings.

## The Goal

Make the emulated HP 8593A actually **execute an HP-IB command** end-to-end:

1. Inject a command like `IP;` (Initial Preset) from the host side
2. See the firmware parse it, look it up, and call the handler
3. Observe the handler's side effects in RAM (for IP: 15+ specific state
   cells get cleared by `fcn.520` at PC `0x4DF72`)

Equivalent goal via the matrix path: emulate pressing the **PRESET
hardkey** and see the same handler fire.

Today we can do steps 1 and 2 partially (the bytes arrive in the
firmware's parser buffer) but **step 3 — actual execution — never
fires** under any input or state combination we've tested.

## What Works (the receive chains)

Both physical input paths in the firmware are fully reverse-engineered
and modeled. The receive end is solid.

### Path A — PS/2 external keyboard / HP-IB (IRQ4)

```
Machine.SendHPIB(bytes)
  → TMS9914A.Push(bytes)               # device/tms9914a.go
  → bytes appear at 0xFFF140 / 0xFFF160 # MMIO routing in device/mmio.go
  → IRQ4 → handler at ROM PC 0x2642
  → push to bc12 FIFO at 0xFFBC12
  → operating tick slot 0x69A → fcn.58c2e (PC 0x58C2E)
  → fcn.57278 (per-byte classifier — handles control bytes, F-keys, prefixes)
  → fcn.5714c (PS/2 Set 2 scancode → ASCII table at ROM 0x55C28, 17+ verified entries)
  → fcn.567e0 → fcn.564be → fcn.56414 (buffer-append helpers)
  → ASCII byte lands at 0xFFBE02 + bc36 (write idx)
```

Verified by `cmd/hpibstep` — for input `IP;` (scancodes 0x43, 0x4D, 0x4C)
the buffer at `0xFFBE02` correctly contains `49 50 3B` and `bc36`
advances to 3.

### Path B — Front-panel hardkeys + softkeys (IRQ3)

```
Machine.FrontPanel.InjectMatrix([6]byte) or .SetBit(byteIdx, bit)
  → device at MMIO 0xEF4000..0xEF401F  # device/frontpanel.go
  → IRQ3 → handler at ROM PC 0x2B1E
  → bset bit 0 of 0xFFBC67 ("key available" flag)
  → clr ack at 0xFFEF401B
  → operating tick clears bc67.0 at PC 0x18F42, then:
      slot 0x430 (DLP-state) → save d0-d1 to 0xFFA39C
      slot 0x736 → fcn.59D2A (READ matrix: 12 nibbles at 0xEF4001..0xEF4017 → 6 bytes in d0-d1)
      save matrix to 0xFFA3A4
  → dispatch is then GATED (see below) — this is where we get stuck.
```

Verified by `TestDriveOperatingTickClearsKeyAndSweepFlags` (which
unfortunately regressed under the SystemID strap change — see
"Known regressions" below).

## What Dispatch Triggers Have Been RULED OUT

Every plausible dispatch trigger has been tested empirically with a
focused tool. None fires the IP-witness cells (the 15 RAM cells
`fcn.520` clears).

| Hypothesis | Test tool | Verdict |
|---|---|---|
| TMS9914A direct EOI read | grep for `0xfff60[0-8e]` in rom.asm | Firmware never reads `DIR`/`BSR`/`IS0`. Only `CPTR` in self-test + setup writes to `AUXCR`/`IMR`/`SPMR`. The chip is configured but not polled for data. |
| `bf05` byte at IRQ4 (μC bridge status) | `cmd/bf05probe` — 10 patterns covering all unaccounted bits 3,6,7 + bit-0/1/2/4/5 combinations including 0xFF | Constant ~1369 RAM cells change across all patterns (cycle-driven, not pattern-driven). Zero IP-witness cells fire. EOI is NOT in bf05. |
| `bc67.1 + b072.14` matrix dispatch gates | `cmd/keymatrix3` + `Machine.PressKey` | Gates can be forced via μC RAM-master modelling. Receive chain advances to PC `0x18F66`, but per-key dispatch still doesn't fire. Initial "witness=2" finding was a cycle-count confound — corrected. |
| Enter key (PS/2 `0x5A`) | `cmd/hpibstep` PC-trace + `cmd/ringprobe` | Enter routes through `fcn.56d1a` (binary dispatcher) at code `0x9800` → nested jump table at `fcn.6862` → `0x56F2E` → mode-2 path at `0x56FB2` → `fcn.56e12` (ensures trailing `;`) → `fcn.56cd2` which **saves a 10-byte BUFFER-CONTROL STRUCT (not text!) to a 40-slot ring at 0xFFA61C**, incrementing `a632 += 3` and `a634 += 1`. The operating tick at PC `0x18BD6` polls `a630/a632/a634`, sees `a632 > a630`, and calls slot `0x72A` → `fcn.34EE8` (the **ring consumer / display refresher** — pops bytes via `fcn.4258`, dispatches on byte values cmp 0x3F='?', 0x75='u', etc.). Confirmed via `cmd/ringprobe`: after 10 × `DriveOperatingTick(10M)`, `a630/a632` BOTH advance to 0x1B (27) and `a634` resets to 0 — the ring IS consumed end-to-end. **But IP-witness cells still don't change** — this is the **recall-history / display-refresh** path, NOT the command executor. The ring saves buffer METADATA (size 0xF4, base 0xFFBE02, indices) so the user can recall the line via softkey, not the literal text. |
| EXECUTE TITLE softkey id `0x98` | `cmd/exectitle` (direct invocation of `fcn.610` with `d0=0x98`) | The id 0x98 turned out to be a label-DISPLAY position (fcn.E7A2 → fcn.BE22 treats the table entry as a length-prefixed string pointer for screen rendering), not a handler index. |
| Any single PS/2 scancode 0x01-0xFF | `cmd/keysweep` | No single scancode fires IP-witness cells. Top hits (`0x77` = 88 cells, `0x5A` = 86 cells) are scancode-specific state init, not IP. |
| Any single matrix bit (48 positions) | `cmd/keymatrix` | All bits produce identical 25-cell common change. No per-key dispatch fires. |

## Master Dispatch Jump Table at ROM 0xC4..0x1B2E

User's instinct was right: ALL handler functions ARE in a comprehensive
jump table.

**1128 contiguous slots** at PC `0x000C4` through `0x01B2E`, each
exactly 6 bytes (`4EF9 hhhh llll` = JMP absolute long). Every reachable
handler in the firmware is here, indexed by its slot PC:

```
PC 0x00C4 → jmp 0x0434E6
PC 0x00CA → jmp 0x05ECB6
PC 0x00D0 → jmp 0x00ABDE
PC 0x00D6 → jmp 0x00C470
...
PC 0x0520 → jmp 0x04DF72   ← Initial Preset (fcn.520)
...
PC 0x069A → jmp 0x058C2E   ← HP-IB parser (fcn.69A → fcn.58C2E)
PC 0x0736 → jmp 0x059D2A   ← matrix read (fcn.736 → fcn.59D2A)
...
PC 0x1B2E → jmp 0x008696   (last slot)
```

The firmware addresses slots via `JSR $XXX.w` short addressing —
e.g., `jsr $0520.w` reaches IP.

**Use `cmd/jumptable` to dump or look up any slot.** The tool also
walks the parser-name table at ROM `0x07E780+` and decodes the handler
bytes for each entry. Only ~6 of the 242 long mnemonics encode their
dispatch as "bytes 2-3 = slot offset"; the rest use type-dependent
encodings whose format is not yet fully decoded.

Short mnemonics (`IP`, `CF`, `SP`, `RB`, `VB`, etc.) are NOT in the
parser-name table at all — those dispatch through `fcn.1B7BE`'s big
inline switch instead.

So the dispatch hierarchy is:

| Layer | Handles | Mechanism |
|---|---|---|
| 1 | All handlers globally | Master jump table at PC 0xC4..0x1B2E (1128 slots) |
| 2 | Long mnemonics (ID, REV, PRINT, IDNUM, ...) | Parser-name table at ROM 0x07E780+ → handler-byte-decoded slot call |
| 3 | Short mnemonics (IP, CF, SP, ...) | fcn.1B7BE inline switch dispatching to slots like 0x520 (IP), etc. |
| 4 | Single PS/2 bytes (Enter, F-keys, Arrows, prefixes) | fcn.57278 byte cascade + fcn.56d1a/fcn.6862 nested inline jump tables |
| 5 | Front-panel matrix bits | (gated dead path at PC 0x18F66 — see ruled-out table) |

## Recall Mechanism (the Up/Down Arrow side of the ring)

The Service Guide documents (lines 8264, 8460) that `⇑` and `⇓` arrows
edit/view previous data. In PS/2 Set 2 these are **extended scancodes**:

| Key | PS/2 Set 2 sequence |
|---|---|
| Up Arrow (recall previous) | `0xE0 0x75` |
| Down Arrow (recall next) | `0xE0 0x72` |
| Left Arrow | `0xE0 0x6B` |
| Right Arrow | `0xE0 0x74` |

`fcn.57278` handles `0xE0` by setting `bc65.3` (extended prefix mode).
The next byte enters the bit-3-SET path at PC `0x57374`:

- Bytes `0x69..0x75` dispatch via inline jump table at `0x574B2` →
  for **0x75 (Up)** the offset at `0x57496 = 0xFFB6 = -0x4A` resolves
  to handler at PC **`0x5746C`**:

  ```
  0x5746C  btst.b 0xD, 0xbc64.w     ; mode flag
  0x57472  bne.b 0x57484            ; if set → parser code 0x9900
  ... fcn.057A helper test ...
  0x57484  -6(a6) = 0x9900          ; emit binary dispatch code 0x9900
  ```

- Parser code `0x9900` → `fcn.56d1a` binary dispatcher → inline jump
  table at PC `0x57144` → index `(0x99 & 0x3F) - 0x10 = 9` → offset
  `0xFF92 = -0x6E` → handler at PC **`0x570DA`**:

  ```
  0x570DA  d6 = bc38                 ; recall history POSITION counter
  0x570DE  d6 -= 1
  0x570E0  ble.b 0x570F2             ; at top of history → skip
  0x570E2  tst.w bc2e                ; check recall buffer ready
  0x570E6  ble.b 0x570F2
  0x570E8  d0 = bc38 - 1
  0x570EE  bsr fcn.56780             ; ← THE RECALL HANDLER
  ```

So the chain is:

1. User types `IP;` + Enter (PS/2 scancodes `0x43, 0x4D, 0x4C, 0x5A`)
2. `fcn.56cd2` saves the buffer-control struct to ring slot at `0xFFA62A`
3. `bc38` (recall position) stays at top
4. User presses Up Arrow (`0xE0 0x75`)
5. Parser emits code `0x9800` → dispatcher → PC `0x570DA`
6. `bc38` decremented to walk one step back through history
7. `fcn.56780` redisplays the saved entry from the ring slot

Empirical verification with a focused step-trace probe (developed
in-session, not landed as a `cmd/` tool) confirms `fcn.34EE8` (the
ring consumer) fires twice after the Up Arrow injection — but the
specific dispatch into `fcn.56780` requires the parser's bit-3
extended-prefix mode to be sticky across the two-byte `E0/75`
sequence, which our forced-PC stepper doesn't reliably preserve.
The chain is in the firmware; verifying the recall handler fires
end-to-end would need a longer natural-loop run.

## Open Hypotheses for the Next Round

After the systematic ruling-out above, here are the remaining real
candidates. Each is paired with a concrete experimental approach.

### Hypothesis 1 — The μC bus-masters RAM directly

**Theory**: in real hardware, the front-panel μC (a separate CPU) has
its own bus master access and writes specific RAM addresses (probably
in the `0xFFB0xx` or `0xFFBCxx` region) when it completes a key debounce
or an HP-IB Listen + EOI sequence. The firmware's dispatch gates read
these RAM cells expecting the μC to have set them, but our `FrontPanel`
device is a passive MMIO target — it can't write RAM.

**Evidence**: `bc67.1` has ZERO `bset` references in the whole Rev L
firmware. The dispatch path at PC `0x18F66` checks `b072.14` — also
never `bset` anywhere. These two gates LITERALLY cannot be set by the
firmware itself; they must come from outside.

**Concrete next experiment**:

1. Pick a specific HP-IB command that has documented hardware behavior
   (e.g. `IP;` — Initial Preset).
2. With a real 8593A connected via HP-IB, capture the FULL CPU bus
   trace using a logic analyzer attached to the M68K data bus during
   the moment the controller sends `IP;<EOI>`.
3. Identify EVERY RAM address that gets written from a non-CPU source
   (i.e., the μC's DMA writes).
4. Add a side-channel API to our `FrontPanel` / `TMS9914A` model that
   does the same writes when `InjectMatrix` or `Push(…, EOI=true)` is
   called.
5. Verify dispatch fires.

Without a real instrument trace, the alternative is to **search the
ROM for any function called by ALL non-firmware code paths** —
something the IRQ handlers call that touches `bc67.1`. If none exists,
that confirms the μC-DMA hypothesis.

### Hypothesis 2 — Natural main-loop progression we don't drive to

**Theory**: our `DriveOperatingTick` primitive forces PC to
`0x18ADC` (the operating-tick body entry) and runs a fixed cycle
budget. The firmware's NATURAL idle-loop progression — beyond just the
tick body — may include the dispatch trigger. We never let the firmware
just run free for long enough.

**Evidence**: the boot completes (Phase-1 gate green), the operating
loop runs (Phase-4 gate green, display renders the top banner). But
all our input tests use `DriveOperatingTick` rather than `CPU.Run` over
millions of cycles in the natural loop.

**Concrete next experiment**:

1. After `BootToOperating`, send `IP;<CR>` via PS/2 scancodes.
2. Then call `m.CPU.Run(100_000_000)` with periodic IRQ5 injection
   (timer tick) — let the firmware naturally cycle through its main
   loop without forcing PC.
3. Watch IP-witness cells over time (snapshot every 5M cycles).
4. If they EVER change, the firmware's natural loop reaches dispatch;
   the forcing approach was the obstruction. If they NEVER change in
   100M cycles, hypothesis 1 is more likely.

### Hypothesis 3 — A different MMIO interface we haven't discovered

**Theory**: the 8593A has more MMIO devices than we model. The boot
PROBE we found at PC `0x2E74` reads `0xFFF73C/F73E/F77C/F77E` (the
SystemID strap, now modeled). There may be OTHER hardware probes for
runtime input — e.g. an HP-IB "Listen" state register that the firmware
polls when it expects commands.

**Evidence**: the `cmd/displayprobe` and `cmd/keyprobe` tools earlier
in the project found unmapped MMIO accesses during boot. Some of those
weren't tracked through to identify what device they probe.

**Concrete next experiment**:

1. Re-run `cmd/keyprobe` (or build a fresh MMIO-access histogram) over
   a 100M-cycle run AFTER boot, with an HP-IB command queued.
2. Identify any MMIO addresses outside the modeled devices that the
   firmware reads from with a non-trivial frequency.
3. For each unmapped MMIO hit, search rom.asm for the access PC to
   understand what hardware it's probing.

## Supporting Tooling (cmd/*)

All these tools exist and work. Listed by usefulness for the dispatch
question.

| Tool | Purpose |
|---|---|
| `cmd/hpibstep` | Focused PC-trace probe — sends ASCII via scancodes, force PC to `0x18ADC`, single-steps with watch-PC counters + new-function discovery + IP-witness-cell dump |
| `cmd/hpibascii` | ASCII→PS/2-scancode encoder + injector + buffer state dump |
| `cmd/hpibtrace` | RAM-diff command tracer (baseline vs with-command) |
| `cmd/keymatrix` / `keymatrix3` | 48-bit matrix sweep + multi-tick variants |
| `cmd/keysweep` | 256-PS/2-scancode brute-force sweep with baseline filter |
| `cmd/hpibkeyprobe` | Single-scancode dump-all-changes probe |
| `cmd/bf05probe` | Focused 10-pattern sweep of the μC status byte |
| `cmd/softkeys` | Extracts the 113-entry softkey label table from ROM |
| `cmd/menudump` | Dumps the active handler-label table at RAM `0xFF9914` |
| `cmd/exectitle` | Direct EXECUTE TITLE handler-id `0x98` invocation |
| `cmd/probeoptions` | Boot-time IDNUM detection state |

## Relevant ROM PCs

| PC | What | Reached via |
|---|---|---|
| `0x2642` | IRQ4 handler (HP-IB/keyboard) | Hardware IRQ4 |
| `0x2B1E` | IRQ3 handler (front-panel matrix) | Hardware IRQ3 |
| `0x18ADC` | Operating-tick body entry | Operating loop |
| `0x18F42` | `bclr bc67.0` (key-available ack) | Inside operating tick |
| `0x18F5E` | `btst bc67.1` ← **dispatch gate 1** (dead in Rev L) | Operating tick after IRQ3 |
| `0x18F66` | `btst b072.14` ← **dispatch gate 2** (dead in Rev L) | After gate 1 |
| `0x18F84` | Per-key dispatch via slot `0x67C` | After both gates pass |
| `0x58C2E` | HP-IB parser body (fcn.58c2e) | Slot `0x69A` from operating tick |
| `0x57278` | Per-byte classifier (fcn.57278) | Called from 0x58c2e per byte |
| `0x5714C` | PS/2 scancode → ASCII translator (fcn.5714c) | Called from 0x57278 for printable bytes |
| `0x55C28` | PS/2 Set 2 scancode table | Read by 0x5714c |
| `0x567E0` | ASCII per-byte dispatcher (fcn.567e0) | Called from 0x58c2e for high-byte-zero codes |
| `0x56e12` | Trailing-`;`-ensure function (fcn.56e12) | Called by Enter dispatch path |
| `0x56cd2` | Save-to-history-ring at `0xFFA634` (fcn.56cd2) | Called by Enter dispatch path |
| `0x4DF72` | **Initial Preset handler body** (fcn.520 = slot 0x520) | Called from `reset_pc @ 0x184B6` (boot) + `fcn.1B7BE @ +0xE5A` |
| `0x1B7BE` | Huge inline switch dispatcher (3 KB span 0x1B100..0x1C9EE) | Reached by fall-through from internal branches |
| `0x1C618` | IP case inside fcn.1B7BE | Computed entry within the switch |
| `0x2E74` | Boot-time SystemID probe (writes 0xFFBF26 from MMIO 0xFFF73C+) | Called from boot fcn.2EE8 |
| `0x1A3E0` | Model-detection dispatcher (sets IDNUM at 0xFFBFEE) | Called from boot |
| `0x59D2A` | Front-panel matrix read routine | Slot 0x736 from operating tick |

## Key RAM Cells

| Address | Purpose |
|---|---|
| `0xFFBC12` | HP-IB parser FIFO (bc12 family — bc26/bc28 are read/write indexes) |
| `0xFFBC67` | Key-available flag (bit 0) + dispatch gate (bit 1, never set internally) |
| `0xFFBE02` | ASCII command buffer (where decoded bytes accumulate) |
| `0xFFBC34/36` | ASCII buffer base/write-index |
| `0xFFA634+` | 40-slot recall-history ring (10 bytes per slot) |
| `0xFFA39C/A3A4` | Saved DLP-state + matrix bytes from operating tick |
| `0xFFB00C` | Extracted board ID (0..7, 3 bits) |
| `0xFFBFEE` | IDNUM (model number as 0x218E..0x2194) |
| `0xFFBF26/2A` | Boot-read longwords from SystemID MMIO strap |
| `0xFFB072` | bit 14 = dispatch gate 2 (never set internally) |

## Cross-references

- `docs/rom_annotations.md` — full per-subsystem deep dive with PC-level
  traces (the "definitive" reference; this doc is a higher-level handoff)
- `pkg/emu/device/systemid.go` — SystemID strap notes with
  "NEEDS-FURTHER-INVESTIGATION" comments on the option-bit decoding
- `pkg/emu/machine/machine.go` — `PressKey` method docs explain the μC
  RAM-master modelling
- `~/.claude/projects/-Users-hannesdw-src-HP859X-SA/memory/` — long-term
  memories: `rev-l-firmware-switch`, `emulator-architecture`,
  `rev-l-key-consumer-chain`, etc.

## Known Regressions (pay-down debt)

The SystemID strap change (8595 → 8593) altered the boot path:
post-boot end-PC moved from `0x456A` (ROM checksum loop) to `0x4832`.
`DriveOperatingTick` / `LoopBreaker` are still tuned for the 8595 path
and don't reach the parser slot for the 8593 path. Two tests are
skipped pending re-tune:

- `TestDriveOperatingTickClearsKeyAndSweepFlags` (`pkg/emu/machine/frontpanel_test.go`)
- `TestSendHPIBPlusDriveOperatingTickDrainsParserFIFO` (`pkg/emu/machine/hpib_test.go`)

This is a finite task: trace where the 8593 boot path ends up, adjust
LoopBreaker entries to bypass the new stall sites, and re-validate the
parser-FIFO-drain end-to-end. Likely 1-2 hour task.

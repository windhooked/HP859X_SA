# Front-panel input — CORRECTED (2026-07-12): the "bus-master µC keystone" is REFUTED

> **★★★ CORRECTED 2026-07-12.** This document previously scoped a "front-panel µC
> bus-master model" around the dispatch block at `0x18F42–0x18FA4` and its "gates"
> `bc67.1` / `b072.14`. Three independent disassembly decodes (plus byte-level
> verification) established that **that entire path is the TIMEDATE on-screen
> clock renderer, not key dispatch**, and the bus-master hypothesis is
> unsupported. The original text below the fold is retained only as a record of
> the disproven framing. The REAL dispatch spine is now decoded (see
> [MEASURE_MODE_HANDOFF.md](MEASURE_MODE_HANDOFF.md) ★2026-07-12); the real
> key-input *source* is the open question.

## What the old "keystone" actually is

- **`0xEF4000–401F` is exclusively the RTC clock chip** (OKI MSM6242-class):
  12 odd-offset BCD nibble regs (`4001–4017` = S1,S10,MI1,MI10,H1,H10,D1,D10,
  MO1,MO10,Y1,Y10) + 3 control regs (`401B/1D/1F`). Exhaustive enumeration:
  **no other offset in the window is ever accessed** — there is no key matrix
  there. The PAL select is literally named **LRTC**. `[V]`
- **IRQ3 = the RTC tick**, not a key interrupt: handler `fcn.2b1e` does
  `bset #0,bc67` + ACK `EF401B` only. `[V]`
- The `0x18F42` block: `bclr bc67.0` (consume tick) → `fcn.430→0x59e2c` /
  `fcn.736→0x59d2a` (read the 12 BCD regs → packed time/date longwords at
  `a39c`/`a3a4`) → gates → `fcn.67c→0x5a0e8` = **format ("HH:MM:SS MON DD,
  19YY", month table `JAN…DEC` @ ROM `0x5a49c`) and BLIT to the display**
  (`fcn.154→0xb3f4` set-position, `fcn.100→0xbe22` draw-string). The pushed
  word pairs `(0xe1,0xffe0)` / `(0xbe,0x5)` are **screen X/Y coordinates**. `[V]`
- **The "gates" are firmware-owned TIMEDATE display flags**, not µC writes:
  - `bc67.1` = timedate-annotation-enable. Its setter is the **TIMEDATE ON
    command case inside `fcn.12288` at `0x12668`** — a word RMW on `0xbc66`
    (`andi #0xfffc; ori #3; move.w d6,0xbc66`), invisible to every `bset` grep.
    **The "zero `bset` refs in all of Rev L" cornerstone was a grep-scope
    artifact.** The OFF case is the lone `bclr` @`0x1266e` + erase rectangle. `[V]`
  - `b072.14` = display-init/annotation-allowed status (init `bset` @`0x1C48A`). `[M]`
  - `ba86.0` = layout flag with plain `st.b`/`clr.b` firmware writers
    (`0xb080/0xb0a2/0xb0a8`) — selects the timestamp position. `[V]`
- **Why probing showed "zero per-key differentiation":** the probes injected key
  codes into the RTC data registers — the clock formatter renders the same
  timestamp shape for any BCD digits. The null result was structural. `[V]`
- The emulator's `device.FrontPanel` **RTC decode is correct** (`rtcNibble` map
  and the 4→5/busy-bit handshake match `fcn.59e2c/59d2a/59998` exactly). Its
  key-matrix/`InjectMatrix` API writes into what the firmware treats as clock
  registers — it never could produce a key. `[V]`
- Positive follow-up: enabling TIMEDATE (`bc66` word RMW: set bits 0+1, with
  `b072.14` set) should draw a live clock from the modeled RTC — a validation
  test worth adding.

## The REAL dispatch spine (decoded 2026-07-12 — see MEASURE_MODE_HANDOFF ★)

Operating loop `fcn.18348` drains the command-source ring (`bb96/bb98` head/tail)
→ parse (`fcn.427c`→`fcn.1a6e2`) → builds a record at `0xbb82` {`b03e` context
longs, +8 = **`b1e4` command word**, +0xa = `b1fe`} → if `b1e0 < 0` (pending) →
`fcn.12b10` (record processor) → class byte ≠ 0 → `fcn.12288` class dispatch →
(class `0x27`) → `fcn.5f968` = CONTS. Typed `CONTS` is a **hard no-op by design**
(class `0x10` → zero jump-table entry). The minimal legitimate CONTS dispatch —
`push.w #0x2701; d0=0x00010000; jsr fcn.12288` — **verified in the emulator**
(`TestCONTSDispatchDiag`): `b0a1.3` sets via the firmware's own dispatcher.
Sweep re-arm (`a9a0`) needs a further sweep-restart trigger (next work).

## ★★ RESOLVED (2026-07-14): the front-panel input port is the EF8000 keyboard channel — there is NO separate key/RPG port

Two exhaustive sweeps (all 135 `fcn.11750` command-word call sites; every IRQ
handler; every unattributed MMIO read in the ROM) establish:

- **Only two operator-input ports exist in the entire firmware**, both serviced
  by IRQ4 (`fcn.2642`, sub-source selected statically by `b05f.0` from `0x9b20`/
  `bf09`) and both feeding the raw input ring `0xbc12` (control block; buffer
  enqueued via `fcn.42f8`): **HP-IB** (`0xFFF140` data / `0xFFF160` status) and
  the **keyboard-controller serial channel `0xEF8000` (data) / `0xEF8002`
  (control/status)**. Nothing else in the address space is an input: the 82C55
  PPI is write-only (zero reads in the whole ROM — rules out matrix scanning),
  `EF40xx` is the RTC, F2xx–F7xx are sweep/DAC/display, `2FC0xx` is config CMOS.
- **All 135 `fcn.11750` (`b1e4` command-word) callers pass constants or
  RAM-derived values** — the parser and command handlers, never hardware. The
  parsed-command ring `0xbb82` is likewise fed only by firmware-generated bytes.
- **The `EF8000` decoder `fcn.57278` is a full IBM-AT Set-2 engine** (break
  `0xF0`, extended `0xE0`, modifier handling) that splits two populations by
  scan-code VALUE:
  - **instrument/front-panel keys**: the special block **`0x69–0x75`**
    (`subi #0x69` → indexed dispatch) + 16-bit key IDs **`0x8d00–0x9c00`** +
    event bases `0x7580–0x7589` emitted via `fcn.56a6a`;
  - **typewriter keys**: ASCII via table (the external-keyboard text path).
- So on the real instrument the front panel (hard keys, softkeys, RPG) is a
  µC speaking Set-2 codes on the SAME serial line as the external keyboard.
  (Whether the panel µC and the external-keyboard controller are one part or
  two muxed sources is off-CPU-bus — a hardware question that does not affect
  the emulator: the firmware accepts the codes on EF8000 either way.)

**This retroactively explains:** why `CAL DISP;` provably executes via the "AT
keyboard path" (that IS the panel channel); why F1–F6 already drive softkeys
1–6; why installing HP-IB "steals IRQ4 from the AT keyboard" (`b05f.0` route).

## The actionable next step (replaces the port hunt)

**Map the instrument-key scan codes.** Decode `fcn.57278`'s special-code
dispatch: the `0x69–0x75` indexed table and the `0x8d00–0x9c00` ID sites —
which scan code = FREQUENCY / SPAN / AMPLITUDE / SWEEP / CAL / softkeys 1–8 /
RPG up/down. Then extend `device.ATKeyboard` + the GUI bindings with the named
front-panel keys — full front-panel control through the already-working
injection path, no new device model needed. (The CONT-softkey → class-`0x27`
emitter will fall out of the same decode: softkey code → `fcn.56a6a` event →
menu state machine → one of the constant-`fcn.11750` callers.)

---

## DISPROVEN original framing (2026-06-07) — retained for the record

The original document claimed: the front-panel µC is a bus master writing
validated key frames into M68K RAM; `bc67.1`/`b072.14` are dispatch gates with
(nearly) no firmware writers; `fcn.67c` is the key dispatch; keys become ASCII
via `fcn.59ef0` and feed the command parser; black-box probing was exhausted.
Every load-bearing element of that framing is refuted above: the "frames" are
RTC time/date snapshots, the "gates" are display-enable flags with firmware
writers (word-RMW / `st.b` forms the greps missed), `fcn.67c` draws the clock,
and `fcn.59ef0` is the date formatter (its ':' separators are the time colons).
The probes (`TestFrontPanelEntryDiag`, `TestKeyMapProbeDiag`, `cmd/keymatrix*`)
measured the clock path. Lesson (again): **verify a "no writers" claim against
word-RMW and `st`/`clr`/dynamic-bit forms before building a hypothesis on it.**

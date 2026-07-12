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

## The open question (replaces the old one)

**Where do front-panel key/softkey/RPG events physically enter?** Not `EF40xx`
(RTC), not the 82C55 PPI at `0xF000` (6 init writes, zero reads), not the
MC68230 PIT beyond the AT-keyboard regs (`EF8000/02/14`). The producer must
feed the command ring / write `b1e4` with class-bearing words (the `fcn.11750`
setter has ~14 call sites — walk those callers back to hardware reads). The
softkey emit for CONT must produce a class-`0x27` word; the emitter site is
un-pinned. Resolution paths: (a) RE the `fcn.11750` / ring-producer call sites
back to their MMIO source; (b) a real-unit bus capture remains the authoritative
fallback (the old C2 — but its "bus-master RAM write-set" premise is gone; what
we'd capture now is *which port* the key scan reads).

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

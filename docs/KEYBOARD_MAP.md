# External keyboard → HP 8593A key map (firmware `fcn.57278`, 2026-07-15)

The front panel — softkeys, menu keys, and the RPG knob — is driven over the
**AT Set-2 keyboard serial channel** at `0xEF8000/0xEF8002` (IRQ4), the SAME line
as an external keyboard (see [FRONTPANEL_UC_SCOPE.md](FRONTPANEL_UC_SCOPE.md) ★★).
There is **no dedicated FREQUENCY/CAL/SGL-SWP hardkey** on this channel — those
are reached by softkeys, the three menu keys, or typed remote commands (F8 → type
`CF 300MHZ;` etc.). The AT Set-2 decoder is **`fcn.57278`** (ROM `0x57278`).

Model: [pkg/emu/device/atkeyboard.go](../pkg/emu/device/atkeyboard.go) (`ATKey`
+ `atSet2`); GUI bindings in [cmd/gui/main.go](../cmd/gui/main.go) (`atBindings`).
All scan codes below are firmware-verified against `fcn.57278`.

## Function keys (softkeys + menus) — no prefix

| Key | AT SC | HP function | event | key-ID |
|---|---|---|---|---|
| F1–F6 | 05 06 04 0C 03 0B | **Softkeys 1–6** | 0x752b–0x7530 | 0x8100–0x8600 |
| F7 | 83 | Softkey 7 / prefix | 0x7531 | 0x8700 |
| F8 | 0A | Softkey 8 / remote-command entry | 0x7532 | 0x8800 |
| F9 | 01 | **MKR menu** | 0x7533 | 0x8900 |
| F10 | 09 | **SPAN menu** | 0x7534 | 0x8a00 |
| F11 | 78 | **AMPLITUDE menu** | 0x7535 | 0x8b00 |
| F12 | 07 | Screen-title recall | 0x7536 | 0x8c00 |

## RPG knob / step keys = the four arrows (E0-prefixed)

The knob and STEP ▲▼ are the arrow keys; each keypress = one increment (auto-repeat
= continuous turn). In `fcn.56a6a` the four get **step-size offsets by modifier**:
`+4` Shift, `+8` Ctrl, `+0xc` Alt (`0x56b56`–`0x56b80`).

| Key | AT SC | function | event | key-ID |
|---|---|---|---|---|
| ↑ Up | E0 75 | active-function STEP ▲ | 0x7586 | 0x9900 |
| ↓ Down | E0 72 | active-function STEP ▼ | 0x7587 | 0x9b00 |
| ← Left | E0 6B | knob CCW | 0x7588 | 0x9a00 |
| → Right | E0 74 | knob CW | 0x7589 | 0x9c00 |

(knob-vs-step split among the pairs is inferred; not proven inside `fcn.57278`.)

## Data entry & control

| Key | AT SC | function |
|---|---|---|
| 0–9, A–Z, symbols | main table `0x55c28` | typed chars / DATA digits (via `fcn.5714c`) |
| Numeric keypad (plain) | 69–7d | DATA digits `0–9 . + - *` (NumLock/no-E0) |
| ENTER | 5A | terminate entry (event 0x7585) |
| Backspace | 66 | delete char (moveq 0x25) |
| ESC | 76 | title mode (event 0x757f) |
| KP Enter | E0 5A | = ENTER |
| KP `/` | E0 4A | `/` (event 0x7582) |
| Print Screen | E0 7C | COPY / plot (event 0x7580, key-ID 0x8d00) |

## Editor/cursor navigation (E0-prefixed) — key-ID only, no instrument event

Used by the title / DLP text editor (cursor motion in raw-keycode mode), not the
menu system.

| Key | AT SC | key-ID |
|---|---|---|
| Home | E0 6C | 0x9200 |
| End | E0 69 | 0x9600 |
| Insert | E0 70 | 0x9100 |
| Delete | E0 71 | 0x9500 |
| Page Up | E0 7D | 0x9300 |
| Page Down | E0 7A | 0x9700 |
| Tab | 0D | 0x9d00 |
| NumLock / ScrollLock | 77 / 7E | 0x9400 / 0x8e00 |

## How an event reaches a function

Instrument-event codes flow `fcn.57278 → fcn.56a6a → fcn.cc4(0x34746)/fcn.cac(0x3480a)`,
which format a `"KEY <n>;"` / `"SPKEY <n>;"` parser command and submit it to the
command interpreter `fcn.34644` — the SAME path as HP-IB. So the event const IS the
internal HP keycode; the human name (softkey-n / MKR / SPAN / AMPLITUDE) is assigned
downstream by the `KEY <n>` menu dispatch. The event-vs-key-ID choice is gated by
`bc64` bit 13 / `fcn.56bd6` (raw-keycode / text-entry mode vs instrument-event mode).

## Verification status (2026-07-15) — RECEIVED, but softkey/menu ACTION is partial

Measured, not assumed:
- **Reception works.** Every key press reaches the firmware: pressing F9/F10/F11
  sets the command word `b1e4` and triggers heavy redraws (glyph count jumps).
- **Typed remote commands fully execute** (F8 → type `CAL DISP;` → Enter — locked
  separately). This is the reliable control path today.
- **Some key ACTIONS complete:** F12 (title recall) visibly enters "Keyboard
  Entry → Title" mode.
- **BUT menu-switching does NOT complete** (fully decoded 2026-07-16, `TestMenuDispatchDiag`
  + `TestShowMenuDiag`):
  - F9 → the AT decoder emits the parser command **`KEYEXC 30003;`** (`fcn.56a6a`
    @0x56a6a → KEY builder `fcn.34746`@0x34760, seed bytes `"KEYEXC"`; softkeys/chars
    → `"SFPKEY"` via `fcn.3480a`). `KEYEXC`/`SFPKEY` are **direct-C** commands
    (parser entries `01 80 01 ea` / `01 80 01 7a`; `0x80` = direct-C flag).
  - The handler writes a key-specific command word to `b1e4` (F9→`0x07`, F10→`0x08`,
    F11→`0x05`) — each a **CLASS-0** word (class byte `= b1e4>>8 = 0`). The record
    processor `fcn.12b10`@0x12d7c-80 (`andi.w #0xff00,d6; beq 0x12dd6`) routes class-0
    words to the **data-entry path** (`0x12dd6`→`0x1344c`), NOT to the class dispatcher
    `fcn.12288`. **Neither branch calls the menu system.**
  - The menu-install mechanism is **`fcn.5a918(idx)`**@0x5a918 (state base `0xFF9562`,
    vtable `0xFF9566 = 0xFF9594+idx*0xE0`, index `0x956a`), reached only via
    **SHOW_MENU `fcn.5ada4` = trampoline `0xc40`**, driven by the softkey/menu state
    machine `fcn.51982` (0x5284e/0x528b2) — **orthogonal** to the b1e4 class word.
    `TestShowMenuDiag` proves it WORKS in isolation (calling `0xc40` changes
    `0x956a`/`0x9566`) — **but the softkey labels still don't repaint** (the label
    redraw `fcn.e7a2` is gated on `b071` bit 0, which is clear).
  - So the missing link is two gated hops with the **same direct-C root as CONTS**:
    (a) the event→SHOW_MENU binding (`KEYEXC` yields a class-0 word, never `jsr 0xc40`),
    and (b) the `b071.0` label-redraw gate. Faithfully closing it = fixing the
    **direct-C command dispatch** (deep RE); forcing SHOW_MENU + the redraw would be a
    half-mock (the `EnterContinuousSpectrum` lesson). Full trail:
    [MEASURE_MODE_HANDOFF.md](MEASURE_MODE_HANDOFF.md) §"EVENT→ACTION DISPATCH".

Unmapped E0 codes: 0x6a, 0x6d–0x6f, 0x73 (no HP function).

# Front-panel µC bus-master model — scope (2026-06-07)

The keystone for ALL front-panel input — hard keys, **softkeys (the CONT softkey that
sets CONTS → arms the boot sweep → draws the trace)**, and the **RPG knob**. This scopes
what must be modelled. It is the confirmed root under the trace-draw blocker (full trail:
[MEASURE_MODE_HANDOFF.md](MEASURE_MODE_HANDOFF.md)).

## The problem, confirmed

The front-panel is a **separate µC** (`0xEF4000`, PAL `LRTC`, IRQ3). Our `device.FrontPanel`
is a **passive MMIO target** — it answers reads but cannot write the CPU's RAM. The real µC
is a **bus master**: after debouncing a key it writes validated state directly into main RAM.
Two of those writes are dispatch GATES the firmware itself NEVER sets:

| Cell | Gate site | Evidence |
|---|---|---|
| `0xFFBC67` bit 1 | `0x18F5E btst #1,bc67` | **zero `bset` refs** in all of Rev L |
| `0xFFB072` bit 14 | `0x18F66 btst #14,b072` | **zero `bset` refs** in all of Rev L |

So the dispatch `fcn.67c` (`0x5a0e8`) can never run from a passive model.

## What's verified (this session)

- **Entry works:** IRQ3 handler `fcn.2B1E` sets `bc67.0` + ACKs `0xEF401B`; the operating
  loop reaches the consume `0x18F42` (`bclr bc67.0`) — 44661× under the sweep-driven boot.
  (Corrects the stale "consumer never reached".) `TestFrontPanelEntryDiag`.
- **Gates are THE blocker:** force `bc67.1`+`b072.14` → `fcn.67c` runs (38× per key). But
  **all 48 matrix bits AND all 100 BCD codes produce IDENTICAL behaviour** — zero per-key
  differentiation, zero softkey-dispatch (`0x1EFDE`), zero command lookup (`fcn.320fe`).
  `TestKeyMapProbeDiag`. So the 2 gates are necessary but NOT sufficient.
- **Keys become ASCII:** the dispatch calls `fcn.59ef0`, which converts frame nibbles to
  ASCII digits (`lsr #4; ori #0x30`) + a `':'` separator and feeds the command parser. So a
  front-panel key is formatted into a string and parsed like a typed command — and it reads
  **multiple frame bytes** (`-0x3(a6)`/`-0x2(a6)` = the d1 longword), not just one.

## Conclusion: black-box probing is exhausted

Injecting raw bits, BCD codes, with/without the gates — none produces a recognized key. The
µC writes MORE than the 2 known gates and/or the valid-key frame format is multi-field and
not what we inject. The exact contract can't be cracked by poking from outside.

## Scope of the fix

1. **Give `device.FrontPanel` a bus-master write capability** — a handle to write main RAM
   (the machine wires `fp.busWrite = m.Bus.Write` at construction). On a key/RPG event the
   device performs the µC's RAM writes, not just MMIO answers.
2. **Model the validated-key event end to end:** on `PressKey`/`PressSoftkey`/`TurnKnob`,
   write the gate flags (`bc67.1`, `b072.14`) AND present the correct frame the firmware's
   `fcn.59e2c`/`fcn.59d2a`/`fcn.59ef0` decode into a recognized key/command.
3. **RPG:** model the knob count in the same frame + whatever cell the µC posts it to.

## The ONE open unknown (gates this)

**The exact valid-key contract** — (a) the full set of RAM cells the µC writes (beyond the 2
gates; candidates: the `fcn.520`/IP-cleared cells, `0xFFB20E`/`0xFFBF01` touched by the key
path), and (b) the multi-byte frame encoding `fcn.59ef0` expects for a specific key/softkey.

Two ways to resolve it (Hypothesis 1 in [INVESTIGATION.md](INVESTIGATION.md)):
- **RE path:** decode `fcn.59ef0` + `fcn.67c` input fields fully — what frame layout yields a
  valid ASCII command, and what RAM the µC must have pre-written for `fcn.67c` to act.
- **Capture path (authoritative):** logic-analyzer trace of a real 8593A's M68K bus while a
  key is pressed; log every RAM write from a non-CPU master → that IS the µC's write set.

Until that contract is known, a faithful model can't be built — and forcing cells is a
half-mock (per the project rule). The probes (`TestFrontPanelEntryDiag`, `TestKeyMapProbeDiag`,
`cmd/keymatrix*`) are the harness for the RE path.

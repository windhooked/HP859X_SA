# HP 8593A Emulator — RE-State Audit & Modernization Blueprint

> Produced by executing [docs/ASSESSMENT_PROMPT.md](ASSESSMENT_PROMPT.md) (adversarial
> audit → modernization blueprint). Scope: **8593A + all its options**. Date: 2026-06-14.
>
> **Tagging.** Every load-bearing claim is **`[VERIFIED]`** (reproduced this session — a
> test run green, or re-derived from the disassembly) or **`[ASSERTED]`** (doc/comment-derived,
> not independently confirmed). Grades: **FAITHFUL** / **FUNCTIONAL-APPROX** / **STUB-TUNED** /
> **GAP** / **UNKNOWN-CONTRACT**.
>
> **Canonical firmware:** plain **Rev L 98.06.15** (the Opt-027 dump is corrupt and archived —
> several historical docs say "Opt-027"; that framing is stale). `[VERIFIED]` via CLAUDE.md +
> the working faithful boot.

## Changes since the 2026-06-14 assessment (light refresh, 2026-07-12)

- **★ Gating item 1 — the A7-bus register→DAC map — is RESOLVED, without hardware.**
  Capture C1 was assumed to need GPIB/bus access; it fell to *reader-side
  disassembly RE* + the text-extractable service guide instead (`pdftotext` works
  on `08590-90316.pdf`). Full map: [A7_ANALOG_IO_BUS.md](A7_ANALOG_IO_BUS.md)
  ★-sections. Headlines: the YTO coil DACs are **direct packed word ports**
  (`F704` = coarse[0:11]+RF-atten[12:14], `F702` fine, `F700` FM; `F712` =
  3rd-conv+IF-gain; `F718` = cal-atten), NOT behind the F728/72A select pair;
  the sub-bus registers are named (reg 0 YTO chain, reg 2 measurement-MUX,
  reg 3 status/readback, reg 5 timebase DAC, reg 7 status/ID); the low-memory
  6-byte-stride jmp table `0x502–0xd00` is the firmware's **A7 driver API**
  (Path-5 gold). Emulator now models the map (chain assembly, getters) and the
  sweep window **centres on the firmware's real YTO tuning** (`syncSweepTune`).
- **New residuals replacing item 1:** DAC→Hz calibration (~10 % off — firmware
  paints 1.450 GHz where the linear model derives 1.206 GHz from coarse `0x08E8`);
  RF-atten line-code→dB decode; span-DAC location.
- **Gate-1/Gate-2 convergence hardened:** `fcn.12288` fully decoded (typed-command
  CLASS dispatcher, 33-case table `0x12754`; class 0x27 → CONTS); the service
  softkey IDs dispatch through the key-event emit layer = Gate 2's machinery.
  **C2 remains the top capture; the C1 precedent says try reader-side RE first.**
- §3a orphan `musashi_config.h` deleted (footgun closed). Five commits:
  `1b7616d…c704050`.

## 0. What was actually run this session (the evidence base)

- `make build` → exit 0. `[VERIFIED]`
- `go test -short ./...` → **116 PASS / 0 FAIL / 37 SKIP**, all 9 packages `ok`
  (musashi, unicorn, bus, device, hd63484, machine, analog, romloader, emutest). `[VERIFIED]`
- Non-`-short` boot/sweep/POST run (the "instrument is alive" claims):
  - `TestMachineBootFaithful` **PASS** — full ROM-checksum + march-RAM test, **no LoopBreaker**,
    only the IRQ5 tick injected; reaches the operating loop. `[VERIFIED]`
  - `TestMachineOperatingLoopNoReboot` **PASS** — no reboot loop. `[VERIFIED]`
  - `TestBootToOperatingWithSweep` **PASS** — sweep-driven boot, trace buffer fills. `[VERIFIED]`
  - `TestBootWithHPIBOption` **PASS** — boots with Option 041 installed. `[VERIFIED]`
  - `TestGraticuleGridVisible` **PASS** — graticule grid renders. `[VERIFIED]`
  - `TestPOSTSelfTestPasses` **PASS** — asserts `f610==0xFF && f612==0xFF && failCode==0x0000`
    → **POST is fully clean**. `[VERIFIED]` (corrects an audit-agent claim of a residual `0x0080`,
    which was inherited from a stale doc.)
  - `TestMachineBootScreen`, `TestTraceSolid` → **SKIP** (golden/trace-paint deferred) — i.e. the
    **trace line itself is not asserted to paint**. Consistent with Gate 1 (§5). `[VERIFIED]`

**Headline truth:** the instrument **boots the real firmware to its operating UI, passes POST
clean, renders the graticule, runs sweep cycles, and fills the trace buffer with a correct
spectrum** — all reproduced this session. What is **not** working: the **trace line paint**, **front-panel
input dispatch**, and **HP-IB query responses**. All three are control-flow / unknown-contract
gates, not data gaps (§5).

---

## 1. Subsystem audit matrix

| Subsystem | Real part | Addr | Grade | Conf | Evidence | Key divergence / gap |
|---|---|---|---|---|---|---|
| **M68K CPU** | MC68000 | — | FAITHFUL | High | `[VERIFIED]` musashi + diffcores tests green; effective `m68kconf.h` (no `-DMUSASHI_CNF`) | §3a orphan `musashi_config.h` (ADDRESS_ERROR OFF) **deleted 2026-07-12** |
| **Bus / decode** | U114 22V10 PAL | 24-bit | FUNCTIONAL-APPROX | High | `[VERIFIED]` bus_test green | Linear range-list dispatch, **no cycle counter exposed to devices** (§3) |
| **ROM** | 4×27C020 | `0x000000` | FAITHFUL | High | `[VERIFIED]` romloader_test; reset SP=`0xFF948A`/PC=`0x1B34` | — |
| **CalNVRAM** | A16A1 batt SRAM | `0x200000` | FAITHFUL | High | `[VERIFIED]` calnvram_test; checksum sweep ROM `0x454A` | Per-band cal **tables not populated** (firmware falls back to ROM defaults) — GAP for measurement accuracy |
| **CalRAM / DLPRAM / TestRAM / RAM** | A16 SRAM | various | FAITHFUL (mapping) | High | `[VERIFIED]` boot tests | DLPRAM lower bound `0xFC0000` is approximate `[ASSERTED]` |
| **HD63484 ACRTC** | U301 | `0xFFF5FC/E` | FAITHFUL | High | `[VERIFIED]` hd63484 pkg 41 pass/2 skip; full command decode | Status byte `0x27` always-ready (STUB, no observable effect); **trace-line render unasserted** (test gap) |
| **Analog physics model** | (behavioral) | — | FAITHFUL | High | `[VERIFIED]` analog_test; YTO 3.0–6.8214 GHz, IF 3.9214 GHz, MU 0..8000, 300 MHz CAL | Behavioral, not circuit-level; RBW fixed 1 MHz |
| **Indirect ADC bus** | U47 12-bit ADC + mux | `0xFFF75C/E` | FAITHFUL (ch0/1/3-7) / FUNCTIONAL-APPROX (ch2) | High/Med | `[VERIFIED]` analogbus_test; 0x9A conversion state machine | ch2 (+2VREF) returns a constant — true transfer function unknown |
| **A7 analog I/O bus** | A16→A7 iface | `0xFFF700–73E` | FUNCTIONAL-APPROX (map KNOWN) | High | `[VERIFIED]` 2026-07-12 disasm RE + a7iobus_test; sweep window rides real YTO DACs | **map RESOLVED** (see refresh note): direct packed DAC ports + named sub-bus regs; residuals = DAC→Hz cal (~10 %), atten→dB, span-DAC |
| **SweepEngine** | sweep+detector | `0xFFF200` | FAITHFUL | High | `[VERIFIED]` sweepengine_test; CAL peak lands at right point | feeds correct data; the *paint* is Gate 1 |
| **Sweep-status `0xFFF300`** | sweep clock | `0xFFF300` | STUB-TUNED (bit12) / GAP (bit11) | High | `[VERIFIED]` sweepgate_test (region classifier) | bit12 always-ready; **bit11 sweep-complete not auto-asserted** → DRIVETICK root |
| **Front-panel µC** | sep. µC (LRTC) | `0xEF4000` | UNKNOWN-CONTRACT | High | `[VERIFIED]` disasm (§5 Gate 2); frontpanel_test (RTC/IRQ3 only) | bus-master RAM write-set unknown; **key dispatch impossible from passive model** |
| **AT keyboard** | AT Set-2 / MC68230 | `0xEF8000` | FAITHFUL | High | `[VERIFIED]` atkeyboard_test | byte-inject FIFO; parser-side ASCII translation is separate |
| **MC68230 PIT** | timer chip | `0xEF8000` | STUB (zeroed) | High | `[VERIFIED]` machine.go | **not modeled** — its IRQ5 timer is synthesized in software (§3) |
| **TMS9914A HP-IB** | TI GPIB ctlr | `0xFFF600` | STUB-TUNED | High | `[VERIFIED]` tms9914a_test | registers + IRQ masking faithful; **no talker/listener state machine** |
| **Option 041 board** | smart I/O µC | `0xFFF100` | FUNCTIONAL-APPROX | High | `[VERIFIED]` gpib_test; receive works | **query RESPONSE blocked** — addressing-commit gate never closes (§6) |
| **POST self-test** | A16 diag | `0xFFF610/2` | FUNCTIONAL-APPROX | High | `[VERIFIED]` post_test PASS, clean `0x0000` | 3 of 4 groups strapped-to-pass; HD63484 VRAM-readback bit **genuinely modeled & closed** |
| **System-ID / IDNUM** | board straps | `0xFFF73C…` | FUNCTIONAL-APPROX | Med | `[VERIFIED]` idnum_test; IDNUM=`0x2191` (8593) | option bits beyond model number not populated (§6) |
| **DLP runtime/VM** | (firmware) | ROM | FUNCTIONAL-APPROX | Med | `[ASSERTED]` docs; interpreter runs at boot | measure-mode trace-draw source never scheduled (§5 Gate 1) |

---

## 2. The MMIO contract surface (the HW/FW boundary)

This is the hardware-abstraction boundary every "original-FW-on-new-HW" and "swap-a-block"
decision hinges on. `[VERIFIED]` by reading [pkg/emu/device/mmio.go](../pkg/emu/device/mmio.go)
and the device models; cross-checked against `docs/HARDWARE.md`.

| Address | Width | Direction | Semantics | Fidelity |
|---|---|---|---|---|
| `0xFFF000–00F` | byte | R/W | 82C55A PPI front-panel I/O | backing RAM |
| `0xFFF200` | word | R | detector video-ADC (when `SweepActive`) → SweepEngine | FAITHFUL data |
| `0xFFF300` | word | R/W | sweep-status: **bit12 ready (forced)**, **bit11 sweep-complete (GAP)** | STUB / GAP |
| `0xFFF5FC` | word | W | HD63484 command | FAITHFUL |
| `0xFFF5FD` | byte | R | HD63484 status → constant `0x27` | STUB-TUNED |
| `0xFFF5FE` | word | R/W | HD63484 data / block-read | FAITHFUL |
| `0xFFF600–60F` | byte | R/W | TMS9914A (2-byte stride) | STUB-TUNED |
| `0xFFF700–77F` | word | W | A16 data-path block; write-index latched for POST | partial |
| `0xFFF780–7FF` | word | R | **mirror of `0xFFF700`** (A7 bit not decoded) — POST loopback | FAITHFUL (models real decode) |
| `0xFFF728 / 72A` | word | W→sel / R/W | **A7 serial sub-bus** (reg 0 YTO chain, reg 2 measurement-MUX, reg 3 status/readback, reg 5 timebase DAC, reg 6 mode, reg 7 status/ID) | FAITHFUL protocol + **map KNOWN (2026-07-12)** |
| `0xFFF700/702/704` | word | W | **YTO coil DACs** FM / fine / coarse — F704 packs coarse[0:11]+RF-atten[12:14]+flag[15] | map KNOWN (2026-07-12) |
| `0xFFF712 / 714 / 718` | word | W | 3rd-conv DAC+IF-gain / YTF DAC+atten / cal-atten latches | map KNOWN (2026-07-12) |
| `0xFFF73C/73E/77C/77E` | word | R | system-ID straps → IDNUM | FUNCTIONAL-APPROX |
| `0xFFF75C / 75E` | word | W→sel / R/W | **indirect ADC bus** (mux + DAC + 12-bit ADC, status select `0x9A`) | FAITHFUL (state machine) |
| `0xFFF614/616` | word | (init) | POST bypass straps `=0xFF` | STUB-TUNED |
| `0x320000` | word | R | A16 write-address latch (POST decoder test) | FAITHFUL-for-purpose |
| `0x200000–20FFFF` | any | R/W | CalNVRAM | FAITHFUL |
| `0xEF4000–401F` | byte | R/W | front-panel µC (keys/RPG/RTC) | partial (RTC + read only) |
| `0xEF8000–80FF` | byte | R/W | MC68230 PIT / AT keyboard | keyboard FAITHFUL, PIT stub |

**Verdict:** the boundary is **well-mapped at the protocol/address level** (a near-complete decode
spec), but **two surfaces are protocol-faithful yet semantically opaque** — the A7 analog bus
(`0xFFF728/72A`) register meanings, and the front-panel µC's bus-master write-set. Those two
opacities are the cross-cutting blockers for every modernization path (§7).

---

## 3. Timing-model verdict

**There is no hardware time model. Every interrupt is software-synthesized; no free-running
clock exists, and devices have no notion of elapsed time.** `[VERIFIED]` by reading
[machine.go](../pkg/emu/machine/machine.go) and bus.go.

- **No global cycle counter** is exposed to devices. `bus.Device` is `Read/Write` only — a device
  cannot schedule a future event or time out. `[VERIFIED]`
- **IRQ5 (timer)** is the *only* autonomous interrupt, and it is a **software accumulator**
  (`Timer.accum += ranCycles; for accum>=Period { SetIRQ(5); Run(400); SetIRQ(0) }` in
  `pumpTimer`). The real **MC68230 PIT is a zeroed stub**. `[VERIFIED]`
- **IRQ1/IRQ6 (sweep)** are **force-injected** by `driveSweepCycle`, gated on a heuristic
  (the `bf34` capture vector + a PC-region poll classifier + a decaying `sweepHold`). `[VERIFIED]`
- **IRQ3 (keys) / IRQ4 (HP-IB)** are hand-pulsed by test/GUI drivers. `[VERIFIED]`
- **`bit11` sweep-complete** (`0xFFF300`) is **never auto-asserted** on buffer-fill — the missing
  hardware handshake the firmware's idle loop waits on (the DRIVETICK root). `[VERIFIED]`
- `LoopBreaker` short-circuits the ROM-checksum/march-RAM delay loops (a **speed** optimization, not
  a correctness crutch — `BootToOperatingFaithful` runs them for real and still boots). `[VERIFIED]`

**(3a) A dead-orphan config footgun — RESOLVED 2026-07-12 `[VERIFIED]`:** two files used to set
`M68K_EMULATE_ADDRESS_ERROR` oppositely — `third_party/musashi/m68kconf.h:235` = `OPT_ON`, and the
stale orphan `pkg/emu/cpu/musashi/musashi_config.h:42` = `OPT_OFF`. Resolved by reading the build:
`m68k.h:50` selects the alternate config **only when `-DMUSASHI_CNF` is defined**, and the cgo CFLAGS
in `musashi.go:11` set **no such flag** → the build includes **`m68kconf.h`, so `ADDRESS_ERROR` is
effectively ON**. `musashi_config.h` was a stale orphan from an earlier Phase-0 build path, included by
nothing — it would have misled the next port (flip on `-DMUSASHI_CNF` and address-error silently turns
OFF, re-opening the DLP-derail failure mode on any firmware hitting the malformed-token path).
**Deleted this session** (`git rm`; musashi build + tests re-verified green); no `-DMUSASHI_CNF` and no
`#include` of it remained anywhere in the tree.

**Consequence:** the model is *sufficient to boot and run the firmware's logic*, but it is
**not a timing model** — which is precisely what an FPGA/Zynq port (Path 3) and a faithful "original
FW on new HW" (Path 1) need. This is the single largest architectural gap for modernization.

---

## 4. Hardware-driver-layer inventory (the bottom of the stack — gates Paths 2 & 5)

What the firmware *writes* to control the analog hardware and *reads* back. This layer — not the
DLP/UI — is what a minimal-firmware or native-reimplement path depends on.

| Control function | Bus path | Write contract | Readback | RE status |
|---|---|---|---|---|
| **Video detector / trace** | `0xFFF200` R | (read-only) | 9-bit ADC per IRQ6 sample → trace buf `0x2FD508` | **`[VERIFIED]`** faithful (SweepEngine) |
| **ADC mux + convert** | `0xFFF75C/75E` | sel `0x91`=channel, `0x95-97`=offset DAC, trigger via DAC-low write | sel `0x9A` status (state machine), `0x9F/9D` 12-bit result | **`[VERIFIED]`** (ch2 transfer fn approximate) |
| **LO / YTO tune** | direct `F700/F702/F704` + serial reg 0 | coil DACs (F704 = coarse[0:11]+atten[12:14]) + `fcn.223b6` chain (AD60 ÷3/÷40) | reg-7 bit1 lock-error (two-point check) — tune is OPEN-LOOP (cal-based) | **`[VERIFIED]` 2026-07-12** — RESOLVED; residual = DAC→Hz cal (~10 %) |
| **Sweep span** | ? | span DAC location still unknown (main/FM span mode = AD7C bit5 via reg 6) | — | **UNKNOWN port** (the one remaining tune unknown) |
| **RF attenuator / step gain** | `F704[12:14]` / `F714[12:14]`; cal-atten `F718[8:12]` | active-low 3-line code (~step / ~(step+1)); cal-atten code==dB | — | **`[VERIFIED]` 2026-07-12** — RESOLVED; residual = line-code→dB decode in the model |
| **IF gain / resolution BW** | `F712[8:13]` (IF step/lin gain, table `0x7734`) | 6-bit IFG1-6 line code | — | gain **`[VERIFIED]`**; RBW/BW-companding DACs still unmapped (RBW fixed 1 MHz in model) |
| **Reference-level DAC** | `F712[0:7]` (3rd-conv variable gain) | 8-bit, CAL AMPTD binary-search calibrated | — | **`[VERIFIED]` 2026-07-12** — RESOLVED |
| **Analog settle/lock** | `0xFFF728/72A` (A7) | — | reg 3: `(x & 0xC0)==0x80` settled gate | **`[VERIFIED]`** gate; other bits passthrough |
| **Sweep arm / complete** | `0xFFF300` | bit13 arm, write to ACK/clear bit11 | bit12 ready (forced), bit11 complete (**GAP**) | STUB/GAP |

**Critical conclusion for the user's preferred Path 5 — UPDATED 2026-07-12:** the gating unknown
is **resolved**. The analog protocol, physics, ADC/detector data path AND the register→DAC map are
now known (which port/field = YTO coarse/fine/FM, RF atten, IF gain, cal-atten, timebase; plus the
firmware's own A7 driver API at jmp-slots `0x502–0xd00`). A minimal firmware/monitor that tunes the
instrument is now authorable; the remaining polish items are the DAC→Hz calibration curve, the
atten line-code→dB decode, and the span-DAC port. **Path 5 is unblocked at the driver layer.**

---

## 5. The two control-flow gates (re-verified)

### Gate 1 — Trace paint `[VERIFIED via disasm + DIAG tests]`
- Display-mode cell `0xFFB0EC` ends boot at **`0x01` (CONFIG)**, never `0x31` (spectrum). `[VERIFIED]`
- Sweep-arm counter `0xFFA9A0` ends at **`0xFFFF` (-1, disabled)**, written from the abort path
  `0x92B2`; the arm path `0x90C8` is gated by **`btst #3, b0a1` (CONTS) at `0x8F5A`**. `[VERIFIED]`
- **CONTS bit `b0a1.3` is never set** during boot; its only writer `fcn.5f968` runs **0×**. `[VERIFIED]`
- The sweep cycle itself **completes** (`befa` bit13 set 45×) — data is fine; the **arm** is gated. `[VERIFIED]`
- `__GTTDRW` trace-draw trampoline (`0x65986`, pushes DLP idx `0x2B`) is never scheduled. `[VERIFIED]`
- Forcing CONTS alone yields only **+2.6% vectors** — necessary but **not sufficient**; downstream
  measure-mode DLP scheduling is a further gate. `[VERIFIED]`

**Root:** the firmware never transitions into continuous-sweep MEASURE mode (the path that would set
CONTS and schedule the trace-draw DLP source). This is an **UNKNOWN-CONTRACT / control-flow** gap,
not a data gap. **Convergence with Gate 2 `[VERIFIED via MEASURE_MODE_HANDOFF.md + FRONTPANEL_UC_SCOPE.md]`:**
CONTS is not a typed command but a **type-0x10 softkey/state command (ID `0x74`, the SWEEP→CONT
softkey)** — its only writer `fcn.5f968` toggles `b0a1.3` via `bchg` (not `bset`, which is why a naïve
grep for the writer misses it). So the power-up CONTS default flows through the **same softkey/state
dispatch as Gate 2's front-panel input** — the two gates are not independent. The `b0ec→0x31` framing
above was the *pre-reframe* lead; the handoff doc demoted it (`b0ec` gates sweep-time limits, not the
arm). Practical consequence: the Gate-2 bus-probe capture (**C2**) is likely to unblock **both** gates,
which raises its priority relative to the standalone C3 capture (§8).

### Gate 2 — Front-panel µC keystone `[VERIFIED via disasm]`
- The dispatch gate cells **`0xFFBC67` bit1** (zero `bset` refs in all of Rev L) and **`0xFFB072`
  bit14** (one `bset` at `0x1C48A`, an init context — never on the key path) are confirmed by
  grepping the disassembly. `[VERIFIED]`
- Gate sites `0x18F5E (btst #1,bc67)` and `0x18F66 (btst #14,b072)` exist as described. A third
  µC-owned cell `0x18F6E (btst #0,ba86)` follows — but it is a dispatch-form **SELECTOR**, not a gate
  (clear → `fcn.67c(0xe1,0xffe0)`; set → `fcn.67c(0xbe,0x5)` unless `b1e4==0x34`), zero `bset` refs,
  unmodeled. `[VERIFIED via disasm 2026-07-12]`
- The IRQ3→`bc67.0` entry handshake **works** (operating loop reached 44k×, the `bclr` consumer
  runs). `[VERIFIED]` — the *entry* is fine; the *dispatch* is gated.
- Forcing the gate bits + any matrix bits produces **zero per-key differentiation** — the µC writes
  more than the two known cells, and/or the valid-key frame is a multi-byte format `fcn.59ef0`
  decodes that black-box probing cannot reconstruct. `[VERIFIED]` (probing exhausted.)

**Root:** the front-panel µC is a **bus master** writing a validated key frame into M68K RAM; our
passive device cannot reproduce a write-set we cannot observe. **UNKNOWN-CONTRACT**, resolvable
authoritatively only by a real-bus capture (§8).

**Both gates reduce to the same shape — and likely the same root:** the firmware is waiting for state
that a real peripheral (the µC, or the sweep/analog hardware) supplies, whose exact contract we can't
see from inside the emulator. Beyond that shared shape, the Gate-1 convergence note above shows CONTS
rides the **same softkey/state dispatch** as the Gate-2 front-panel path — so the front-panel µC
bus-master capture (**C2**) is the single highest-leverage §8 item: it plausibly unblocks the trace
paint *and* interactivity together. That is the through-line of this whole project — and the reason §8
(ground truth) is the highest-leverage investment.

---

## 6. 8593A option matrix `[mostly ASSERTED — service-manual derived]`

| Option | Function | Detection | Emulation status |
|---|---|---|---|
| **041** (HP-IB+parallel) | GPIB + Centronics | I/O-board descriptor `0xFFF11E=4` → `bf09=4` | **FUNCTIONAL-APPROX** — receive `[VERIFIED]`, query response blocked |
| **021** (HP-IB) | GPIB only | `bf09=4` (descriptor doesn't distinguish from 041) | partially covered by TMS9914A model |
| **023 / 043** (RS-232 [+parallel]) | serial | `bf09=8` | **ABSENT** |
| **026 / 027** (freq extension) | low-freq / Y2K-era | system-ID straps + CalNVRAM flags | **ABSENT** (IDNUM only; extension non-functional) |
| **Tracking generator** | sweep tone out | unknown strap/flag | **UNKNOWN-CONTRACT** |

IDNUM resolves to 8593 (`0x2191`) `[VERIFIED]`, but **per-option presence bits** (in system-ID
LONGWORD-B and CalNVRAM `HAVE(*)` flags) are **not populated** — so "all supported options" is, today,
**only the HP-IB receive path**. Full option emulation needs the system-ID/CalNVRAM option-flag
layout reverse-engineered, which is itself partly a ground-truth task (dump a real unit's NVRAM).

---

## 7. Modernization blueprint (five paths)

**Cross-cutting gating items (resolve these and every path improves):**
1. ~~**A7-bus register→DAC semantic map is UNKNOWN**~~ — **RESOLVED 2026-07-12 via
   disassembly RE** (see the refresh note). Residuals: DAC→Hz calibration, atten
   line-code→dB, span-DAC port. Paths 2 & 5 are unblocked at the driver layer.
2. **No hardware time model** — blocks Path 3 (FPGA) and weakens Path 1.
3. **Two UNKNOWN-CONTRACT peripherals** (front-panel µC write-set; CONTS/measure-mode entry) — block
   interactivity and the trace paint on Paths 1 & 4.
4. **Several STUB-TUNED registers** (sweep-status, HD63484 status, POST straps) carry their fragility
   onto any new platform if copied verbatim.

### Path 1 — Original firmware on new HW (Pi Zero / Zynq, modern peripherals behind same MMIO)
- **Supported today:** the MMIO decode is a near-complete contract (§2); the CPU/ROM/RAM models are
  FAITHFUL; the firmware demonstrably boots faithfully and runs. A software M68K core (Musashi) ports
  to ARM/Linux (Pi) trivially.
- **Missing:** a **real time model** (PIT, sweep clock, ADC conversion timing) to replace the
  hand-pulsed scaffolding; faithful versions of the STUB-TUNED registers; the two UNKNOWN-CONTRACT
  peripherals for full interactivity.
- **Verdict:** feasible as a **bring-up platform now** (boots, renders). "Functional instrument" on
  it still needs the same gates §5 closed. Best *immediate* host for a debuggable environment.

### Path 2 — Reimplement firmware natively (RE as spec, no M68K at runtime)
- **Supported today:** the analog **physics** model is portable and FAITHFUL; many control semantics
  are documented.
- **Missing:** the **top of the stack** — measurement/cal algorithms, the DLP command semantics, the
  full command grammar — plus the same A7 register map. Largest RE surface of any path.
- **Verdict:** viable as a **spectral-analysis library** (FrequencyModel+SpectrumModel+Detector are
  ready); a full instrument reimplementation is a multi-quarter archaeology effort.

### Path 3 — FPGA / SoC (Zynq) hybrid
- **Supported today:** little — there is **no per-cycle device model** to map to fabric.
- **Missing:** a clock/event architecture (a global cycle counter + per-device timed callbacks) must
  exist *first*; today's "synthesize IRQs after Run()" pattern doesn't translate to RTL.
- **Verdict:** **premature** until the time model (gating item 2) and the analog register map exist.
  The audit's timing verdict is the gate here.

### Path 4 — Swap individual HW blocks (incremental)
- **Ranked candidates by interface-completeness:**
  1. **HD63484 CRT controller — strongest candidate.** The command decode is FAITHFUL and effectively
     a drop-in spec (§1); a modern panel driver consuming the `0xFFF5FC/E` command stream is well-bounded.
  2. **Front-panel µC** — a natural block to *replace* (it's already a separate processor), but the
     replacement must implement the unknown bus-master write contract (Gate 2) — so it's swap-blocked
     by the same UNKNOWN-CONTRACT.
  3. **ADC / detector** — protocol known, physics modeled; swappable once the A7 map is known.
- **Verdict:** **HD63484 replacement is the most actionable modernization unit today.**

### Path 5 — Minimal M68K firmware → remote control by a new MCU/PC (the "headless RF front-end")
- **Supported today:** ADC/detector **data path `[VERIFIED]`**; analog **protocol** modeled; physics
  faithful; the boot doesn't *need* the UI/DLP layer for this.
- **Missing (the one thing):** the **A7-bus register→DAC semantic map** (gating item 1). With it, a
  minimal monitor that exposes "set YTO, set atten, set RBW, read detector" over a serial/SPI link is
  a small, well-bounded program — you'd bypass the entire DLP/UI/front-panel stack and both control-flow
  gates of §5.
- **Verdict:** **the lowest-risk, highest-leverage first modernization step** — *conditional on*
  resolving the A7 register map. It sidesteps the two hardest unknowns (front-panel µC, measure-mode
  DLP) entirely. Recommended (see below).

---

## 8. Ground-truth capture appendix (what ends the guessing)

The project's recurring "CORRECTED / harness artifact" pattern is the cost of inferring peripheral
contracts from inside the emulator. These captures resolve the open contracts authoritatively. Tag
= the path(s) each unblocks.

| # | Open contract | Capture | Access needed | Unblocks |
|---|---|---|---|---|
| C1 | ~~**A7-bus register→DAC map**~~ — **RESOLVED 2026-07-12 by reader-side disassembly RE, no hardware** (A7_ANALOG_IO_BUS.md ★). A real-unit log would still *validate* the map + settle the residuals (DAC→Hz curve, span DAC) | Log `0xFFF728/72A` + `0xF700–0xF71E` writes while changing one parameter at a time; correlate | GPIB dump or bus probe (now optional) | validation + Path-5 polish |
| C2 | **Front-panel µC write-set + valid-key frame** (Gate 2) — capturing the **CONT softkey** press also yields the CONTS→`b0a1.3` write-set, so this likely resolves Gate 1 too (see §5 convergence) | Logic-analyzer on the M68K bus during a keypress; log every RAM write from a **non-CPU master** | **bus probe** (LA) | **interactivity + trace paint**; Paths 1, 4 (top-priority §8 item) |
| C3 | **CONTS / measure-mode entry** (Gate 1) — *may be subsumed by C2* if the CONT softkey capture there exposes the same cells | Capture the RAM cells (`b0ec`, `a9a0`, `b0a1`) + DLP scheduling on a real unit at power-up and on `CONT` softkey | GPIB dump | trace paint; Paths 1, 4 |
| C4 | **ADC ch2 (+2VREF) transfer fn** | Read the indirect ADC at GND and +2V refs on a real unit | GPIB dump | measurement accuracy |
| C5 | **Option/system-ID + CalNVRAM `HAVE(*)` flags** | `CAL DUMP` over GPIB (some PDFs already exist); read system-ID straps | GPIB only | option matrix (§6) |
| C6 | **HP-IB talker-addressing commit** | Capture the addressing handshake on a real bus controller↔8593A session | GPIB analyzer | query responses (§6) |

> **Access note:** `pkg/859x/dump.py` (PyVISA `ZSETADDR`/`ZRDWR?`) already reads instrument memory over
> GPIB — so C1, C3, C4, C5 are reachable with **GPIB-only** access. C2 and C6 need a **bus/IEEE-488
> analyzer**. If you have a real 8593A, **C1 + C5 via dump.py is the cheapest, highest-value first
> capture** and directly unblocks the recommended path.

---

## 9. Recommended first move

**~~Resolve the A7-bus register map (C1)~~ — DONE (2026-07-12, disassembly-only).**
The original rationale stands and its precondition is met: the driver layer is
mapped, so a **Path-5 minimal-control firmware/monitor is now directly buildable**
(prototype it in the emulator against the jmp-slot A7 API; validate on hardware
whenever a unit is available). The updated ordering:
1. **Path-5 monitor PoC** — unblocked, sidesteps both §5 gates, first tangible
   modernization artifact.
2. **Gate-2 reader-side RE** (in progress 2026-07-12) — the C1 precedent says the
   µC contract may also fall to consumer-side disassembly (satisfy the reader:
   `fcn.430/736` frame acquisition, `fcn.67c`/`fcn.59ef0` decode, the softkey
   emit layer) without the C2 capture. Unblocks input + trace paint + service keys.
3. Residual calibration (DAC→Hz, atten→dB, span DAC) as polish.

Parallel, independent quick win: **HD63484 replacement spec (Path 4)** — the decode is already a
drop-in contract, so it can proceed without any capture.

What to explicitly **not** do yet: the FPGA/Zynq path (Path 3) — it's blocked on a time model that
doesn't exist, and building that is a larger architectural project than the above.

---

*Audit method: `make build` + `go test` (116/0/37), 5 parallel subsystem Explore agents (each
verifying via tests + disassembly), central non-`-short` boot verification, and direct resolution of
three agent/doc contradictions (address-error config, POST cleanliness, canonical firmware). Load-bearing
claims tagged VERIFIED/ASSERTED inline.*

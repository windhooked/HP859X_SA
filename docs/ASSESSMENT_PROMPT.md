# Assessment & Modernization Prompt (HP 8593A emulator)

> **What this is.** A self-contained prompt for a *future Claude session* with no
> prior context. Paste the "PROMPT" block below as the task. It drives a rigorous,
> adversarially-verified assessment of the reverse-engineering / emulation state of
> this project, and turns that assessment into a modernization/porting blueprint.
>
> **Why it exists.** This project has a long, documented history of conclusions that
> were later marked *"CORRECTED / superseded / was a harness artifact."* An accurate
> assessment therefore cannot trust the docs at face value — it must re-derive
> load-bearing claims from the firmware and the running emulator. That discipline is
> the whole point of this prompt.
>
> **How to invoke.** Start a fresh session in the repo root and say:
> *"Read docs/ASSESSMENT_PROMPT.md and execute the PROMPT block."* Allocate a large
> budget — this is a multi-hour, multi-agent job, not a quick answer.

---

## PROMPT (give this to the future session)

You are auditing the **HP 8593A spectrum-analyzer reverse-engineering / emulation
project** in this repository and producing a **modernization blueprint** from the
audit. Your output is a single living document at
**`docs/ASSESSMENT_AND_MODERNIZATION.md`** (overwrite/extend if it exists). If a
prior version exists, **read it first** and include a **"Changes since last
assessment"** section: grade changes, gates closed/opened, captures performed, and
which of the previous run's recommendations were actioned vs. not.

### Mission (two phases, in order)

1. **Audit** — Produce an *honest, independently-verified* map of the current
   RE/emulation state for the **HP 8593A and every hardware option it supports**:
   for each subsystem, what is faithfully modeled vs. hacked vs. unknown vs. missing,
   with a confidence rating and the evidence you used.
2. **Blueprint** — From that audit, assess the path to a **fully functional
   instrument emulation** and to **modernizing/porting** it. The end purpose is a
   *running, debuggable environment and a blueprint* for replacing aging hardware
   (e.g. the HD63484 CRT controller) and/or moving to a new platform (Pi Zero, Zynq,
   etc.). Evaluate all five modernization paths in "Phase 2" below.

### Non-negotiable epistemic stance (read first, apply throughout)

This is the part that makes the assessment *accurate*. The failure mode to avoid is
inheriting a stale or confounded conclusion and building a blueprint on it.

- **Every claim in CLAUDE.md, the memory dir, the `docs/*.md` files, and code
  comments is a HYPOTHESIS with a date and provenance — not a fact.** Note when a
  doc contradicts another or carries a "stale/superseded" banner.
- **This prompt is itself dated and NOT exempt.** Any specific address, cell, or
  claim embedded below is a snapshot, not ground truth — an earlier version of this
  prompt asserted "zero `bset` refs" for a gate cell that in fact has one
  (`0xFFB072.14`: `bset` at `0x1C48A`). Re-derive; don't inherit.
- **A claim is "VERIFIED" only if you reproduced it THIS session** by one of:
  (a) reading a test that asserts it and running it green (`make test` / targeted
  `go test`); (b) observing it live via the GDB server (`cmd/gdbserver`,
  `pkg/emu/gdb` — watchpoints/breakpoints/single-step) or a trace you ran;
  (c) re-deriving it from the firmware disassembly (`docs/rom.asm`,
  `docs/rom_analysis.md`) or the source `*.HEX` via the tools. Otherwise label it
  **ASSERTED (unverified)**.
- **Distinguish "the firmware does X" from "our model makes the firmware *appear* to
  do X."** A stub that returns a constant the firmware accepts is **not** evidence
  that the real hardware behaves that way. Always ask: would this hold against real
  silicon, or only against our stub?
- **Beware the documented artifact classes** that produced false conclusions here:
  too-short runs; wrong command terminator (`;` vs LF/0x0A on the HP-IB path);
  forced-PC / forced-RAM-cell substitutes that lack live operating-loop context;
  reading the *passive* boot when the *sweep-driven* boot behaves differently. When
  a claim is load-bearing for the blueprint, **re-test it on the natural path with a
  long run** before trusting it.
- **Negative results are results.** Record what you tried that did *not* reproduce a
  claimed behavior.
- For every subsystem you grade **FAITHFUL**, state **what real-hardware observation
  would falsify it** — that sentence is what tells the modernization reader whether
  they can build on it.

### Phase 0 — Orient (do this before auditing)

Read, in order, treating each as hypothesis: `CLAUDE.md`; the memory index
`~/.claude/projects/-Users-hannesdw-src-HP859X-SA/memory/MEMORY.md`
and the memory files it points to; `docs/HARDWARE.md` (the register-level map — the
single most important doc for modernization); `docs/rom_analysis.md`. Then skim the
blocker/subsystem docs: `DRIVETICK_BLOCKER.md`, `TRACE_DISPLAY_PATH.md`,
`MEASURE_MODE_HANDOFF.md`, `FRONTPANEL_UC_SCOPE.md`, `ANALOG_BUS_MODEL.md`,
`A7_ANALOG_IO_BUS.md`, `POST_SELFTEST.md`, `DLP_RUNTIME.md`,
`DLP_VM_ARCHITECTURE.md`, `HPIB_E2E_FLOW.md`. Build the device inventory from
`pkg/emu/device/` and the wiring in `pkg/emu/machine/machine.go` and `pkg/emu/bus/`.
Confirm the build/test commands in CLAUDE.md actually run (`make build`, `make test`,
`make tools`). Enumerate the 8593A's hardware options from the service manuals in
`docs/` (e.g. Option 041 HP-IB; Opt-027; tracking generator; any others the parts
list / service guide name) — the audit must cover each.

### Phase 1 — Adversarial RE-state audit

Use this classification taxonomy for every subsystem **and every MMIO register**:

| Grade | Meaning |
|---|---|
| **FAITHFUL** | Models the real part's documented behavior; firmware couldn't tell it from silicon. Cite the datasheet / service-manual basis + a falsifying observation. |
| **FUNCTIONAL-APPROX** | Correct-enough for the firmware to proceed, but not contract/cycle-faithful. Name the divergence. |
| **STUB-TUNED** | Returns hand-tuned constants chosen to make the firmware advance. *Not* a hardware model; flag the risk that it steers firmware down wrong branches. |
| **GAP** | Unmodeled — reads return default/0 or the region is unmapped. |
| **UNKNOWN-CONTRACT** | The required behavior isn't understood (the device can't be faithfully modeled until ground truth resolves it). |

Confidence: **High / Medium / Low**, tagged with evidence type (test / live-trace /
disasm / doc-only).

**Cover at minimum these subsystems** (8593A + options), one audit row each:
CPU core (Musashi primary, Unicorn oracle); bus + address-decode PAL (U114);
ROM (Rev L) / CalNVRAM / CalRAM / RAM / TestRAM; the **timing/IRQ model** (autovector
table, IRQ1–7 handlers, and critically *how time advances* — is there any
free-running clock, or is every IRQ hand-pulsed?); HD63484 ACRTC + VRAM + the decoded
display command protocol; the indirect analog/ADC bus (`0xFFF75C/75E`, U47 12-bit ADC,
the input mux); the A7 analog I/O bus (`0xFFF728/72A` — LO/RF/IF/counter); the sweep
subsystem (sweep DACs, `0xFFF300` status bits 11/12, IRQ1/IRQ6 capture, SweepEngine
data path, trace buffer at `0x2FD508`); the front-panel µC (keys/softkeys/RPG/RTC,
IRQ3, and the *bus-master RAM-write* contract); the AT-keyboard / MC68230 PIT path
(IRQ4); HP-IB (TMS9914A + the Option-041 option board); the DLP runtime/VM
(interpreter, scheduler, command tables, the `__GTTDRW` trace-draw path); POST
self-test + annunciators. Then a row per **hardware option**.

**Per-subsystem fields:** real part (chip/board) → address range → what's modeled →
**grade + confidence + evidence** → known divergences from real HW → the ground-truth
observation that would resolve any open question.

**Special deliverables of Phase 1** (these feed the blueprint directly):

1. **The MMIO contract surface.** A complete, verified table of every address the
   firmware touches and its semantics. This *is* the hardware-abstraction boundary;
   every "Original-FW-on-new-HW" and "swap-a-block" decision hinges on it. Verify
   `docs/HARDWARE.md` against the firmware rather than copying it.
2. **The timing-model verdict.** State plainly whether the emulator has a real time
   model or relies on hand-pulsed IRQs + constant status stubs, and which firmware
   behaviors depend on timing the model currently fakes. (This gates the FPGA path
   and the "is the boot success real or a frozen poll" question.)
3. **Re-verify the headline open problems** rather than restating them. Derive the
   *current* set from the blocker docs — do not inherit the list or its specifics
   from this prompt. (As of 2026-07 they were: the **trace-paint gate** — latest
   claim in `MEASURE_MODE_HANDOFF.md`, whose CONTS-softkey reframe demoted the older
   `0xB0EC` framing — and the **front-panel µC bus-master keystone** — latest claim
   in `FRONTPANEL_UC_SCOPE.md`; the newest RE says the two likely share a root in
   the softkey/state dispatch.) For each, re-derive the gate cells and their writers
   from the disassembly with your own greps/traces, confirm whether the latest doc
   claim still holds, and grade the surrounding models.
4. **The "hardware-driver layer" inventory.** Separately catalog the *bottom of the
   firmware stack* — the routines that directly drive the analog hardware (sweep-DAC
   programming, ADC read, RF/IF/LO control via the A7 bus, attenuator/step control,
   detector). This layer is what Path 5 (minimal-firmware) and Path 2 (reimplement)
   depend on; assess how completely it is RE'd.

### Phase 2 — Modernization blueprint

Organize around the **hardware/firmware boundary** the MMIO surface defines. First
state the **cross-cutting gating items**: the UNKNOWN-CONTRACT and STUB-TUNED
subsystems that block multiple paths until resolved (expect the front-panel µC, the
A7 analog bus, and the analog status-timing to dominate). Then evaluate each path —
for each: what the current RE state *already supports*, what's missing, the key
risks, a rough effort/sequence, and which audit gaps must close first.

1. **Original firmware on new hardware** (M68K emulated or real, on Pi Zero / Zynq;
   modern peripherals behind the same MMIO contracts — e.g. replace the HD63484 with
   a modern display driver). Gated by: completeness + *faithfulness* of the MMIO
   surface (STUB-TUNED registers carry their fragility forward) and the timing model.
2. **Reimplement firmware natively** (rewrite instrument logic in modern code, RE as
   the behavioral spec, no M68K at runtime). Gated by: how much of the *top of the
   stack* — measurement/cal/sweep algorithms, DLP semantics — is actually RE'd.
3. **FPGA / SoC (Zynq) hybrid** (CPU core for firmware; FPGA fabric for timing-
   critical or analog-adjacent peripherals — sweep ramp, ADC sampling, CRT refresh).
   Gated by: the timing-model verdict and which devices need real-time fabric.
4. **Swap individual hardware blocks** (incremental). Rank replacement candidates by
   (a) how well the interface is RE'd and (b) modern-part availability. Expect the
   HD63484 CRT controller and the front-panel µC to be the leading candidates;
   justify the ranking from the audit.
5. **Minimal M68K firmware for remote control by a new MCU/PC** (the user's path).
   Strip/replace the HP firmware with a minimal M68K monitor that exposes the A16 +
   analog boards as a register/command interface, turning the instrument into a
   *headless RF front-end* driven by modern software. Gated almost entirely by the
   **hardware-driver-layer inventory** from Phase 1.4 — this path needs only the
   *bottom* of the stack understood (DAC writes, ADC reads, RF/IF/LO/attenuator/sweep
   control), which is more concrete than the DLP/UI layer. Assess feasibility on that
   basis and call out whether it is the lowest-risk first step toward modernization.

**Ground-truth capture plan (mandatory appendix).** List the specific open contracts
the audit could not resolve from inside the emulator, and for each give the exact
real-hardware capture that would resolve it — e.g. a logic-analyzer trace of the M68K
bus during a keypress to log the front-panel µC's non-CPU-master RAM writes; A7-bus
`0xFFF728/72A` reads/writes during a real sweep; analog `0x9A` status timing during
PRESET ADC cal; `0xFFF300` sweep-complete timing during a real sweep; a GPIB `CAL
DUMP` (`pkg/859x/dump.py`) of NVRAM. Note which captures need bus-probe access vs.
GPIB-only access. This appendix is what converts "we're guessing" into "here's the
one experiment that ends the guessing."

### Working method

- **Fan out with subagents** (Explore for read-only surveys, one per subsystem) and
  consider a Workflow for the audit matrix — this is a broad, parallelizable sweep.
  Verify findings yourself before committing them.
- **Prefer the GDB server + tests over one-off `cmd/` tools.** The project rule is to
  not create a new `cmd/` probe per question; drive the live firmware with
  watchpoints/breakpoints, or add a Go test with assertions. Use existing `cmd/`
  tools that already exist.
- HP-IB / remote commands are **`;`-terminated** in firmware semantics but the parser
  path consumes **LF (0x0A)** — get this right when exercising the command path, or
  you'll reproduce the old false "blocked" reading.
- Write any rendered/probe screenshots to `./screens/` (committed), never just /tmp.
- **Close the loop on corrections.** When the audit contradicts a doc, write the
  correction back into that doc (the project's "CORRECTED (date)" banner pattern) —
  don't let it live only in the assessment. Apply trivial, safe cleanups you find
  (dead files, stale banners) directly, or list them in an explicit **"Unactioned
  recommendations"** section at the end of the output.
- Be explicit about confidence and about what you did **not** verify. An accurate
  "we don't know X, and here's the experiment to find out" beats a confident guess.

### Definition of done

`docs/ASSESSMENT_AND_MODERNIZATION.md` contains: (1) the subsystem audit matrix with
grades/confidence/evidence covering 8593A + all options — **including, for every
FAITHFUL grade, the real-hardware observation that would falsify it** (the 2026-06-14
run dropped this requirement; don't); (2) the verified MMIO contract surface; (3) the
timing-model verdict; (4) the hardware-driver-layer inventory; (5) the five-path
modernization blueprint with cross-cutting gating items; (6) the ground-truth capture
appendix; (7) if a prior assessment existed, the "changes since last assessment"
section; (8) corrections written back into contradicted docs + the "unactioned
recommendations" list for anything left. Every load-bearing claim is tagged VERIFIED
or ASSERTED. The blueprint names a recommended first move and justifies it from the
audit, not from prior assumptions.

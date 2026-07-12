// Package machine wires the HP 8593A's address space and attaches a Musashi
// M68K CPU to it.
//
// Phase 2 memory map (24-bit, all addresses masked to 0xFFFFFF):
//
//	0x000000–0x0FFFFF  ROM (1 MB, read-only — Rev L firmware on 4×27C020)
//	0x200000–0x20FFFF  CalNVRAM (64 KB; A16A1 battery-backed cal SRAM via
//	                    U114 PAL's LCAL select — blank by default = "no cal")
//	0xEF4000–0xEF401F  FrontPanel (front-panel μP; PAL LRTC select)
//	0xEF8000–0xEF80FF  PITStub (256 B; MC68230 PIT — PAL LKBD select; IRQ4
//	                    handler accesses 0xEF8000/0xEF8002)
//	0xFC0000–0xFEBFFF  DLPRAM (176 KB; DLP / working-memory heap — $bb4e/$bb54)
//	0xFEC000–0xFEFFFF  TestRAM (16 KB; march-test target; see note below)
//	0xFF0000–0xFFEFFF  RAM (60 KB; stack + firmware variables)
//	0xFFF000–0xFFFFFF  MMIO (4 KB; 82C55A PPI, SCI display controller,
//	                         TMS9914A HP-IB — see pkg/emu/device)
//	everything else    unmapped — reads return 0x00 per onFaultZero
//
// TestRAM note: the HP 8593A boot firmware performs a march RAM test across
// 0xFEC000–0xFFC000 (64 KB). That range spans TestRAM (0xFEC000–0xFEFFFF)
// and the lower 48 KB of the main RAM region (0xFF0000–0xFFBFFF). Without
// the TestRAM mapping, the 16 KB below 0xFF0000 is unmapped and all writes
// are silently dropped, causing the test to report false errors in D1 and
// pollute the 9914A HP-IB register writes that encode the result.
//
// Periodic IRQ5 injection: the IRQ5 handler at 0x19E2 increments the timer
// counter at RAM[0xFFBFCA]. Callers must inject periodic IRQ5 ticks via
// CPU.SetIRQ(5) + CPU.Run(N) + CPU.SetIRQ(0) to keep the timer running and
// unblock timer-wait loops (e.g. 0x36D66). See TestMachineBootBulk.
//
// Additional MMIO regions observed during boot-trace analysis (unmapped;
// OnFault returns 0 to keep the boot sequence moving):
//
//	0x310000           sweep-generator output latch (write-only)
//	0x320000           A16 write-address diagnostic latch (mapped — returns the
//	                    index of the last 0xFFF700-block write for the POST
//	                    address-decoder self-test; see docs/POST_SELFTEST.md)
package machine

import (
	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	musashi "github.com/windhooked/HP859X_SA/pkg/emu/cpu/musashi"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
)

// Memory map constants.
const (
	ROMBase = uint32(0x000000)
	ROMSize = uint32(0x100000) // 1 MB — Rev L (4 × 27C020); see pkg/emu/romloader

	// CalNVRAM: A16A1 battery-backed calibration SRAM (decoded by U114 PAL's
	// LCAL signal). 64 KB at 0x200000. Default contents are zero ("dead
	// battery"); the firmware treats that as "no cal" and falls back to
	// defaults — same observable behaviour as the previous OnFault→0 mapping.
	// Sourced from device.CalNVRAMBase / device.CalNVRAMSize.

	// PITStub: MC68230 PIT stub at 0xEF8000. The IRQ4 handler at 0x1248 reads
	// and writes 0xEF8000 (PGCR) and 0xEF8002 (PSRR). Mapping this as zeroed
	// RAM prevents bus-fault noise from those accesses; the timer wait at
	// 0x36D66 is unblocked by IRQ5 injection (see package doc), not by this.
	PITBase = uint32(0xEF8000)
	PITSize = uint32(0x000100) // 256 B — covers all 68230 register offsets

	// TestRAM covers the lower half of the march-test range (0xFEC000–0xFEFFFF).
	// The upper half (0xFF0000–0xFFBFFF) falls within the main RAM region.
	// CalRAM is a working-buffer region the firmware copies the cal NVRAM
	// contents into at boot (4 KB copy starting at 0x2FC000) and then uses
	// extensively as a read/write scratchpad — ~490 references in docs/rom.asm
	// span offsets 0x000–0xDF5. Without this mapping, all the cal-data round-
	// trips silently drop writes / return 0, leaving the firmware unable to
	// initialise sweep / display config blocks. The IRQ6 sample-capture
	// handler at ROM 0x40D4 tests `btst #4, $2fc013.l` — that bit decides the
	// "store sample" vs "end-of-sweep" path. 16 KB covers the observed range
	// with headroom for dynamic indirect accesses.
	CalRAMBase = uint32(0x2FC000)
	CalRAMSize = uint32(0x004000) // 16 KB

	// DLPRAM: the firmware's DLP / working-memory heap. fcn.358C + the
	// allocator at fcn.0x3380 partition this region: empirically (cmd/naturalkey)
	// the DLP heap pointer $bb4e settles at 0xFC9C12 and the DLP symbol-table
	// base $bb54 at 0xFD8DEC — both BELOW the old RAM map (which started at
	// 0xFEC000), so without this mapping every DLP symbol-table access went to
	// the OnFault void (returning 0) and the startup-DLP interpreter could not
	// run. Mapping 0xFC0000–0xFEBFFF makes the heap real. (The exact lower
	// bound is approximate — 0xFC0000 covers all observed accesses with
	// headroom; the real A16 SRAM is a contiguous block 0xFC0000–0xFFFFFF.)
	DLPRAMBase = uint32(0xFC0000)
	DLPRAMSize = uint32(0x02C000) // 0xFC0000–0xFEBFFF (176 KB)

	TestRAMBase = uint32(0xFEC000)
	TestRAMSize = uint32(0x004000) // 16 KB (0xFEC000–0xFEFFFF)

	RAMBase = uint32(0xFF0000)
	RAMSize = uint32(0x00F000) // 60 KB — stack + firmware variables (0xFF0000–0xFFEFFF)

	MMIOBase = device.MMIOBase // 0xFFF000
	MMIOSize = device.MMIOSize // 4 KB (0xFFF000–0xFFFFFF)
)

// Machine is a wired HP 8593A: ROM + RAM + MMIO devices + Musashi M68K CPU.
type Machine struct {
	Bus      *bus.Bus
	CPU      *musashi.CPU
	ROM      *bus.ROM
	CalNVRAM *device.CalNVRAM // A16A1 cal SRAM at 0x200000 (PAL LCAL)
	CalRAM   *bus.RAM         // Cal-data working buffer at 0x2FC000 (16 KB)
	// ATKeyboard models the external AT keyboard receiver wired to the MC68230 PIT
	// serial port (0xEF8000–0xEF80FF). Replaces the zeroed-RAM PIT stub: the
	// firmware's IRQ4 handler polls 0xEF8002 for data-ready (bit 1) and reads the
	// scan-code byte from 0xEF8000. Inject host key events by calling
	// m.ATKeyboard.Enqueue(ATMake(k)) / Enqueue(ATBreak(k)), then fire IRQ4 while
	// m.ATKeyboard.Pending() is true. See device.ATKeyboard and device.ATKey.
	ATKeyboard *device.ATKeyboard
	FrontPanel *device.FrontPanel
	TestRAM    *bus.RAM
	RAM        *bus.RAM
	MMIO       *device.HP8593AMMIO

	// SweepDrive, when true, makes the boot loop fire the sweep interrupts
	// (IRQ1 step + IRQ6 capture) autonomously while the firmware has a sweep
	// armed — modelling the analog sweep board that raises those IRQs as its
	// ramp advances. Off by default so BootToOperating (and the golden screen
	// test) are unaffected; enabled by BootToOperatingWithSweep. See
	// driveSweepCycle and docs/TRACE_DISPLAY_PATH.md "TWO-IRQ SWEEP MODEL".
	SweepDrive bool
	sweepAccum int // cycle accumulator that paces sweep points (see driveSweepCycle)
	// sweepHold counts down the driveSweepCycle chunks remaining in the "firmware
	// is sweeping" hold. Each sweep-wait poll (MMIO.ConsumeSweepRegionPoll) refreshes
	// it to sweepHoldChunks; while >0 the sweep injection free-runs. It decays to 0
	// within ~1 frame once the firmware stops polling the sweep-wait (CAL DISP / menus).
	sweepHold int

	// Timer is the autonomous periodic-interrupt source: it synthesises the
	// firmware's IRQ5 timer tick that real hardware (the MC68230 PIT, a zeroed
	// stub here) raises on its own. Every driver advances the CPU through
	// pumpTimer/RunTimed so the firmware's timer-driven delay loops (the bf12
	// free-run counter and the befb.7 countdown, both incremented by the IRQ5
	// ISR at ROM 0x3ECE) resolve uniformly — without each call site hand-pulsing
	// IRQ5. See docs/HPIB_E2E_FLOW.md "CORRECTION (2026-06-02)".
	Timer Timer
}

// Timer models the periodic IRQ5 timer-tick source. Period is the number of
// mainline CPU cycles between ticks; ServiceCost is the cycle budget given to
// the handler to run once asserted. accum carries the running cycle count so a
// tick fires every whole Period regardless of how callers chunk their Run()s.
type Timer struct {
	Period      int  // mainline cycles between IRQ5 ticks
	ServiceCost int  // cycles allowed for the IRQ5 handler to run
	Enabled     bool // when false, pumpTimer is a no-op (legacy/manual driving)
	accum       int  // executed-cycle accumulator toward the next tick
}

// New8593A creates a Machine loaded with romImage. The image must be exactly
// 512 KB (ROMSize bytes); it is mapped read-only at address 0.
// RAM regions are zero-initialised.
// The 4 KB MMIO window at 0xFFF000–0xFFFFFF is backed by device stubs that
// return ready/idle values for hardware polling loops.
func New8593A(romImage []byte) (*Machine, error) {
	b := &bus.Bus{}

	// Unmapped reads return 0x00 — keeps the boot sequence moving past
	// addresses not yet modelled (secondary ROM scan, sweep generator, etc.).
	b.OnFault = func(addr uint32, sz bus.Size, write bool) uint32 { return 0 }

	rom := bus.NewROM(romImage)
	calNVRAM := device.NewCalNVRAM()
	calNVRAM.Synthesize() // embed valid checksum so the startup check passes
	calRAM := bus.NewRAM(CalRAMSize)
	atKbd := device.NewATKeyboard()
	fp := device.NewFrontPanel()
	testRAM := bus.NewRAM(TestRAMSize)
	ram := bus.NewRAM(RAMSize)
	mmio := device.NewHP8593AMMIO()

	b.Map(ROMBase, uint32(len(romImage)), "ROM", rom)
	b.Map(device.CalNVRAMBase, device.CalNVRAMSize, "CalNVRAM", calNVRAM)
	b.Map(CalRAMBase, CalRAMSize, "CalRAM", calRAM)
	b.Map(PITBase, PITSize, "PIT", atKbd)
	b.Map(device.FrontPanelBase, device.FrontPanelSize, "FrontPanel", fp)
	b.Map(DLPRAMBase, DLPRAMSize, "DLPRAM", bus.NewRAM(DLPRAMSize))
	b.Map(TestRAMBase, TestRAMSize, "TestRAM", testRAM)
	b.Map(RAMBase, RAMSize, "RAM", ram)
	b.Map(MMIOBase, MMIOSize, "MMIO", mmio)
	// A16 write-address diagnostic latch at 0x320000 — the POST address-decoder
	// self-test (ROM 0x4AA0) reads back the index of its last 0xFFF700-block
	// write here. Backed by the MMIO's addrLatch. See device/mmio.go.
	b.Map(0x320000, 0x10, "A16AddrLatch", mmio.AddrLatch())

	// HP-IB address: the firmware loads its own HP-IB primary address from the
	// battery-backed config at CalRAM 0x2FC000 (ROM 0x3a22: `move.b 0x2fc000,
	// befc`), masks it to 5 bits, and programs it into the option board so the
	// board knows which bus address to answer. On a fresh (zeroed) CalRAM that
	// address is 0 (uninitialised), which the firmware shows as "ADDRESS 0" and
	// which prevents HP-IB talk/listen addressing from ever matching. Seed the
	// HP default (18) so the instrument has a valid address out of the box.
	calRAM.Write(0, bus.Byte, uint32(DefaultHPIBAddress))

	c, err := musashi.New(b)
	if err != nil {
		return nil, err
	}

	// Let the MMIO sweep-status read handler see the current PC, so it can tell a
	// sweep-wait poll (spectrum display) from the background poll — the trigger
	// the analog sweep driver gates on (driveSweepCycle).
	mmio.PCFunc = func() uint32 { return c.Reg(cpu.PC) }

	return &Machine{
		Bus: b, CPU: c, ROM: rom, CalNVRAM: calNVRAM, CalRAM: calRAM,
		ATKeyboard: atKbd, FrontPanel: fp,
		TestRAM: testRAM, RAM: ram, MMIO: mmio,
		Timer: Timer{Period: bootChunkCycles * bootIRQPeriod, ServiceCost: bootIRQServiceCost, Enabled: true},
	}, nil
}

// DefaultHPIBAddress is the HP factory-default instrument HP-IB primary address
// (18), seeded into the battery-backed config (CalRAM 0x2FC000) at construction.
const DefaultHPIBAddress = 18

// SetHPIBAddress writes the instrument's HP-IB primary address into the
// battery-backed config (CalRAM 0x2FC000) that the firmware loads at boot
// (befc, ROM 0x3a22). Call before booting. addr is masked to the valid 5-bit
// HP-IB range (0–30).
func (m *Machine) SetHPIBAddress(addr byte) {
	m.CalRAM.Write(0, bus.Byte, uint32(addr&0x1f))
	if m.MMIO.GPIB != nil {
		m.MMIO.GPIB.PrimaryAddr = addr & 0x1f
	}
}

// Boot-loop tuning (see package doc for why each is needed).
const (
	bootChunkCycles     = 2000 // cycles per Run() call
	bootBreakThresh     = 50   // consecutive same-loop chunks before LoopBreaker fires
	bootIRQPeriod       = 5    // inject an IRQ5 timer tick every N chunks
	bootIRQServiceCost  = 400  // cycles allowed for the IRQ5 handler to run
	sweepCyclesPerPoint = 2300 // pace: one sweep point per ~2300 cycles ⇒ ~58 ms / 401-pt sweep
	sweepStepCost       = 400  // cycles allowed for the IRQ1 sweep-step handler to run
	sweepCaptureCost    = 250  // cycles allowed for the IRQ6 capture handler to run
	// sweepHoldChunks is how many driveSweepCycle chunks the sweep injection keeps
	// free-running after each sweep-wait poll. It must bridge the gap between the
	// firmware's (sparse, when fed) sweep-wait polls — ~one sweep ≈ 461 chunks — so
	// 1000 (≈2M cycles ≈ one GUI frame) bridges comfortably while still letting the
	// hold decay within a frame once the firmware leaves the sweep code (cal mode).
	sweepHoldChunks = 1000
)

// pumpTimer advances the autonomous timer by ranCycles of just-executed mainline
// CPU time, firing the IRQ5 tick (assert → service → clear) once per whole Period
// that has elapsed. This is the single place IRQ5 is synthesised; every driver
// funnels through it (directly or via RunTimed) so timer-driven firmware delays
// resolve no matter how the caller chunks execution. No-op when the timer is
// disabled.
//
// Servicing must give the handler real cycles to run: a bare SetIRQ with only a
// single Step (or an immediate clear) never lets the 68000 take the autovector
// exception, so the ISR never runs and bf12/befb never advance — the failure
// mode that masqueraded as "the firmware is frozen". See docs/HPIB_E2E_FLOW.md.
func (m *Machine) pumpTimer(ranCycles int) {
	if !m.Timer.Enabled || m.Timer.Period <= 0 {
		return
	}
	m.Timer.accum += ranCycles
	for m.Timer.accum >= m.Timer.Period {
		m.Timer.accum -= m.Timer.Period
		m.CPU.SetIRQ(5)
		m.CPU.Run(m.Timer.ServiceCost)
		m.CPU.SetIRQ(0)
	}
}

// RunTimed advances the CPU by approximately `cycles` mainline cycles while the
// autonomous timer raises IRQ5 at its programmed period. This is the universal
// "advance the machine" primitive — new drivers (command execution, the GUI,
// probes) should call it instead of hand-pulsing IRQ5, which guarantees the
// firmware's timer-wait loops keep ticking. It runs in bootChunkCycles sub-steps
// so a long request still produces correctly-spaced ticks.
func (m *Machine) RunTimed(cycles int) {
	for done := 0; done < cycles; done += bootChunkCycles {
		n := bootChunkCycles
		if rem := cycles - done; rem < n {
			n = rem
		}
		ran := m.CPU.Run(n)
		m.pumpTimer(ran)
	}
}

// BootToOperating runs the firmware to its operating loop using the fast path:
// the LoopBreaker force-exits the long ROM-checksum / march-RAM-test /
// calibration-delay loops, plus periodic IRQ5 timer-tick injection. This is the
// canonical boot for tests and tools (reaches the operating loop in ~5.7M
// cycles). Call CPU.Reset() first; afterwards MMIO.Display holds the screen.
//
// The LoopBreaker is purely a SPEED optimisation, not a correctness crutch:
// with correct ROM and real RAM those loops pass on their own — see
// BootToOperatingFaithful. The only injected stimulus either path genuinely
// needs is the IRQ5 timer tick (real hardware the emulator must supply).
func (m *Machine) BootToOperating(maxCycles int) {
	m.bootLoop(maxCycles, emutest.NewLoopBreaker(bootBreakThresh))
}

// BootToOperatingFaithful runs the firmware to its operating loop the way real
// hardware does: NO LoopBreaker, so the ROM checksum (~5M cycles), march RAM
// test (~8M), and calibration delay run to completion against the real ROM/RAM.
// Only the IRQ5 timer tick is injected. Reaches the operating loop in ~20M
// cycles; budget maxCycles accordingly (≥30M). Use this to validate that the
// hardware mocks are sufficient to boot without test-only shortcuts.
func (m *Machine) BootToOperatingFaithful(maxCycles int) {
	m.bootLoop(maxCycles, nil)
}

// BootToOperatingWithSweep boots like BootToOperating but ALSO drives the analog
// sweep: it enables the SweepEngine detector model (SweepActive) and fires the
// sweep interrupts (IRQ1 step + IRQ6 capture) whenever the firmware has a sweep
// armed (see driveSweepCycle). This lets the firmware run real sweep cycles —
// capturing the modelled spectrum into the trace buffer, completing sweeps via its
// own IRQ handlers, and drawing the trace + advancing the operating loop. Use this
// (not BootToOperating) when you want a live, sweeping instrument; it is kept
// separate so the deterministic golden-screen boot is unaffected. Inject signals
// via m.MMIO.Sweep.Spectrum before calling. Budget maxCycles ≥ 150M.
func (m *Machine) BootToOperatingWithSweep(maxCycles int) {
	m.SweepDrive = true
	m.MMIO.SweepActive = true
	m.bootLoop(maxCycles, emutest.NewLoopBreaker(bootBreakThresh))
}

// bootLoop is the shared boot driver: run in chunks, optionally break known
// delay loops (lb may be nil), and inject the periodic IRQ5 timer tick. When
// SweepDrive is set it also drives the sweep interrupts (see driveSweepCycle).
func (m *Machine) bootLoop(maxCycles int, lb *emutest.LoopBreaker) {
	for done := 0; done < maxCycles; done += bootChunkCycles {
		ran := m.CPU.Run(bootChunkCycles)
		if lb != nil {
			lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		}
		m.pumpTimer(ran)
		if m.SweepDrive {
			m.driveSweepCycle()
		}
	}
}

// driveSweepCycle models the analog sweep board: while the firmware has the
// trace-capture handler armed (bf34 = 0x40B8 store / 0x410A peak-detect) and the
// trace buffer isn't full (A5 < bf30), it fires IRQ1 (sweep update/step — advances
// the ramp and reprograms the f200/f300/f400 sweep DACs) then a burst of IRQ6
// (sample capture). The firmware's own IRQ handlers raise befa bit13 (sweep-done)
// when the buffer fills (IRQ6 end-of-sweep 0x40C2) — so the sweep completes, the
// trace processes and draws, and the operating loop advances, the faithful way
// (no flag-forcing). Data comes from m.MMIO.Sweep via SweepActive. See
// docs/TRACE_DISPLAY_PATH.md "TWO-IRQ SWEEP MODEL".
// SweepIdle reports whether the firmware is NOT currently sweeping — i.e. the
// sweep-wait hold has fully decayed (no sweep-wait poll for ~1 frame). It is the
// firmware-display-mode signal the GUI uses to pick its render source: while
// sweeping (idle=false) render the flicker-free STABLE snapshot so the
// periodically-repainted graticule grid stays on screen; while idle (idle=true —
// CAL DISP, menus, the firmware not driving the spectrum) render the LIVE buffer
// so those non-sweep screens refresh. (The sweep gate keeps the live buffer
// stable in those modes, since it no longer injects phantom sweeps there.)
func (m *Machine) SweepIdle() bool { return m.sweepHold == 0 }

// DriveOneSweepChunk fires the sweep IRQs for one boot-chunk's worth of cycles.
// GUI and other real-time drivers call this once per CPU chunk (after CPU.Run)
// to keep the sweep progressing in lock-step. Equivalent to what bootLoop does
// when SweepDrive=true, but exposed for callers that run their own loop.
// No-ops when SweepDrive is false.
func (m *Machine) DriveOneSweepChunk() {
	if m.SweepDrive {
		m.driveSweepCycle()
	}
}

func (m *Machine) driveSweepCycle() {
	// Faithful trigger: only inject a sweep while the firmware is in its sweep
	// code. It reads the sweep-status register (0xFFF300) from a sweep-wait region
	// (measurement processor 0x171xx / operating-loop wait 0x188BA) only when
	// displaying the spectrum; a non-sweep screen (CAL DISP, menus) reaches neither
	// and so never opens the gate. Those polls are sparse when the firmware is
	// being fed (it isn't spinning), so a one-shot gate would throttle injection to
	// nothing — instead each poll REFRESHES a short hold and we free-run inject
	// while the hold is live, matching the old continuous feed. In cal mode no poll
	// refreshes it, so the hold decays within ~1 frame and the screen persists.
	// This replaces the old unconditional free-run (gated only on the bf34 capture
	// vector, which stays armed in cal mode) that dragged the firmware into an SCLR
	// storm and wiped the cal display.
	if m.MMIO.ConsumeSweepRegionPoll() {
		m.sweepHold = sweepHoldChunks
	} else if m.sweepHold > 0 {
		m.sweepHold--
	}
	if m.sweepHold == 0 {
		return
	}
	bf34 := m.Bus.Read(0xFFBF34, bus.Long)
	if bf34 != 0x40B8 && bf34 != 0x410A {
		return
	}
	m.syncSweepTune()
	// Pace to real sweep time: accumulate elapsed boot-chunk cycles and emit one
	// sweep point (IRQ1 step + IRQ6 capture) per sweepCyclesPerPoint, so a
	// 401-point sweep spans ~58 ms rather than completing in a single burst.
	m.sweepAccum += bootChunkCycles
	bf30 := m.Bus.Read(0xFFBF30, bus.Long)
	for m.sweepAccum >= sweepCyclesPerPoint && m.CPU.Reg(cpu.A5) < bf30 {
		m.sweepAccum -= sweepCyclesPerPoint
		m.CPU.SetIRQ(1) // sweep step: advance the ramp, reprogram the sweep DACs
		m.CPU.Run(sweepStepCost)
		m.CPU.SetIRQ(0)
		m.CPU.SetIRQ(6) // capture one sample
		m.CPU.Run(sweepCaptureCost)
		m.CPU.SetIRQ(0)
	}
	// Don't let the accumulator run away while the buffer is full (between a
	// completed sweep and the firmware's re-arm) — that would burst-fire the next
	// sweep's first points and break the pacing.
	if m.CPU.Reg(cpu.A5) >= bf30 && m.sweepAccum > sweepCyclesPerPoint {
		m.sweepAccum = sweepCyclesPerPoint
	}
}

// syncSweepTune feeds the firmware's real YTO coil DACs (direct ports
// 0xFFF700/702/704 = FM/fine/coarse — the ★ 2026-07-12 A7 map) into the
// SweepEngine's FrequencyModel, so the swept frequency window centres on
// wherever the firmware has actually tuned the YTO rather than a fixed span.
// Called each sweep cycle while the capture handler is armed; reading the
// centre DACs is idempotent (the analog ramp, not the CPU, sweeps around them).
// The span stays SweepEngine.SpanHz (band-0 default) pending the span-DAC RE.
func (m *Machine) syncSweepTune() {
	if m.MMIO.Sweep == nil {
		return
	}
	fm, fine, coarse := m.MMIO.YTOCoilDACs()
	// F704 packs coarse-DAC[0:11] | RF-atten[12:14] | flag[15] (reg-2 field-map
	// RE, 2026-07-12) — mask to the low 12 bits for the DAC value. F700/F702 are
	// pure FM/fine DACs. (At boot atten==0 so the raw word already equals the
	// DAC, but mask so a non-zero attenuator setting can't corrupt the tune.)
	m.MMIO.Sweep.Tune.FMDAC = int(fm & 0x0FFF)
	m.MMIO.Sweep.Tune.FineDAC = int(fine & 0x0FFF)
	m.MMIO.Sweep.Tune.CoarseDAC = int(coarse & 0x0FFF)
	m.MMIO.Sweep.TuneActive = true
}

// OperatingTickEntry is the ROM address of the firmware's main UI tick —
// the function called "key consumer" in earlier notes. fcn.18568 in the
// Rev L disassembly. See docs/rom_annotations.md "Firmware dispatch jump
// table" / slot 0x148.
const OperatingTickEntry = uint32(0x018568)

// OperatingTickDeepBlock is the entry to the deep-path block of the
// operating tick that leads to the key-flag bclr at PC 0x18F42 AND
// (via slot dispatch) the sweep-done bclr at fcn.17346. Use this with
// DriveOperatingTick to bypass the entry-block MMIO reads (which would
// overwrite any pre-arm of $b010 via `move.w $f300.w, $b010.w` at PC
// 0x1856C).
const OperatingTickDeepBlock = uint32(0x018ADC)

// SendHPIB queues `bytes` for the firmware to receive over HP-IB, then
// drives the natural receive path until the chip's input buffer is
// drained (or `maxCycles` elapses). The firmware's IRQ4 handler at
// PC 0x2642 reads bytes from the chip's DIR register and pushes them
// into the FIFO at RAM 0xFFBC12 via fcn.42F8. The operating tick's
// slot 0x69A dispatch then pops bytes from that FIFO and feeds them
// to the HP-IB command parser at fcn.58C2E.
//
// Returns the number of bytes still queued (0 = chip drained, the
// firmware's IRQ4 path consumed them all).
//
// Verification baseline: after SendHPIB("abc"), the FIFO at
// RAM[0xFFBC14..] should hold 'a', 'b', 'c'. (FIFO write index at
// $bbbc advances by 3.) Reaching the COMMAND HANDLER for a recognised
// mnemonic (e.g. CF) requires the operating tick body to dispatch
// slot 0x69A → fcn.58C2E, which is gated by the LAYER 2 obstruction
// documented elsewhere — pair SendHPIB with DriveOperatingTick for
// end-to-end execution.
func (m *Machine) SendHPIB(bytes []byte, maxCycles int) int {
	// Feed the option-board controller when the HP-IB option is installed
	// (its register model owns the receive path); otherwise fall back to the
	// TMS9914A input buffer used by the bare-instrument receive tests.
	if m.MMIO.GPIB != nil && m.MMIO.GPIB.Installed {
		m.MMIO.GPIB.Push(bytes)
	} else {
		m.MMIO.HPIB.Push(bytes)
	}

	// The IRQ4 handler at PC 0x2642 gates on `btst #0, $b05f.w` —
	// it routes to the f160-reading data path only when b05f bit 0
	// is set (otherwise it falls through to the PIT path at PC
	// 0x26DC). Pre-arm bit 0 so our path runs.
	b05f := byte(m.Bus.Read(0xFFB05F, bus.Byte)) | 0x01
	m.Bus.Write(0xFFB05F, bus.Byte, uint32(b05f))

	// Drive the receive path: fire IRQ4 with the chip in BI state so
	// the firmware's handler reads bytes from DIR (via the MMIO
	// route) into bf05, then bc12.
	for done := 0; done < maxCycles; done += bootChunkCycles {
		// Inject IRQ4 if the chip has data pending.
		if m.hpibPending() == 0 {
			break
		}
		m.CPU.SetIRQ(4)
		m.CPU.Run(bootIRQServiceCost)
		m.CPU.SetIRQ(0)
		ran := m.CPU.Run(bootChunkCycles)
		// Autonomous timer ticks between IRQ4 deliveries so timer waits
		// inside the receive/parse path advance.
		m.pumpTimer(ran)
	}
	return m.hpibPending()
}

// hpibPending reports how many received HP-IB bytes are still queued in the
// active receive buffer (option-board controller when installed, else the
// TMS9914A).
func (m *Machine) hpibPending() int {
	if m.MMIO.GPIB != nil && m.MMIO.GPIB.Installed {
		return m.MMIO.GPIB.PendingInput()
	}
	return m.MMIO.HPIB.PendingInput()
}

// driveHPIB runs the operating loop for maxCycles, injecting IRQ4 (HP-IB
// service), IRQ5 (timer) and the sweep clock each chunk so the firmware's
// receive/parse/output paths make progress. Stops early when stop() is true.
func (m *Machine) driveHPIB(maxCycles int, stop func() bool) {
	for done := 0; done < maxCycles; done += bootChunkCycles {
		ran := m.CPU.Run(bootChunkCycles)
		m.CPU.SetIRQ(4)
		m.CPU.Run(bootIRQServiceCost)
		m.CPU.SetIRQ(0)
		m.pumpTimer(ran)
		m.DriveOneSweepChunk()
		if stop != nil && stop() {
			return
		}
	}
}

// GPIBSend delivers an HP-IB program message (e.g. "CF 1.5GHZ;") to the
// instrument as if a bus controller addressed it as listener and sent the
// bytes, then lets the firmware parse and execute it. The option board must be
// installed (InstallHPIB). It does not collect a response — use GPIBQuery for
// queries. maxCycles bounds the drive time.
func (m *Machine) GPIBSend(msg string, maxCycles int) {
	if m.MMIO.GPIB == nil || !m.MMIO.GPIB.Installed {
		return
	}
	m.MMIO.GPIB.AddressListener()
	m.SendHPIB([]byte(msg), maxCycles/2)
	// Let the parser/executor run to completion.
	m.driveHPIB(maxCycles/2, func() bool { return m.hpibPending() == 0 })
}

// GPIBQuery delivers a query (e.g. "ID?") as listener, then addresses the
// instrument as talker and collects the response the firmware emits onto the
// HP-IB output port (captured by the option-board controller). Returns the
// response bytes. The option board must be installed.
func (m *Machine) GPIBQuery(query string, maxCycles int) []byte {
	if m.MMIO.GPIB == nil || !m.MMIO.GPIB.Installed {
		return nil
	}
	m.MMIO.GPIB.ResetTX()

	// Stage 1: addressed as listener, send the query text; let it parse.
	m.MMIO.GPIB.AddressListener()
	m.SendHPIB([]byte(query), maxCycles/3)
	m.driveHPIB(maxCycles/3, func() bool { return m.hpibPending() == 0 })

	// Stage 2: addressed as talker — the firmware should format and drain the
	// response onto the output port. Drive until output stops growing.
	m.MMIO.GPIB.AddressTalker()
	last, stable := 0, 0
	m.driveHPIB(maxCycles/3, func() bool {
		n := len(m.MMIO.GPIB.TX())
		if n == last {
			stable++
		} else {
			stable, last = 0, n
		}
		return n > 0 && stable > 8 // output settled
	})
	return m.MMIO.GPIB.TX()
}

// DriveOperatingTick pre-arms the RAM flags that the operating tick's
// deep-path block tests, forces the CPU PC into the deep block, and
// runs for `maxCycles` cycles with periodic IRQ5 ticks so internal
// timer-wait loops advance. Returns the PC at exit.
//
// The deep-block path flows through:
//
//	0x18ADC → 0x18B00 → 0x18E20 → 0x18E54 → 0x18E62 → 0x18E6E →
//	(gated on 9afb bit 2 set) → 0x18E76 → bra 0x18F42 → bclr fires
//
// and also reaches the sweep-done processor at fcn.17346 via slot
// dispatches from inside the deep block. Empirically (cmd/tickflags
// force-experiment, commit d40b9d8) with the pre-arms below the bclr
// at PC 0x18F42 fires 2× and the bclr at PC 0x17346 fires 6× within
// 20M cycles, clearing both the key flag (bc67 bit 0) and the
// sweep-done flag (befa bit 13).
//
// Pre-arm:
//
//	$b1e0 := 0x0200   bit 9 set so 0x18AFC doesn't skip to 0x18FD6
//	$befa &= ~0x0400  bit 10 clear so 0x18B00 doesn't skip to 0x18FD6
//	$9afb |= 0x04     bit 2 set so 0x18E6E falls through to bra-to-bclr
//
// Why this exists: the natural dispatch chain via fcn.1B40 never
// reaches fcn.18568 in our environment because path A at PC 0x1E60
// always pre-sets RAM[0xFFBF0A] to the sweep-handler pointer 0x3AD0
// (which itself doesn't return), so fcn.1B40's stack-rts dispatches
// to the sweep handler instead of slot 0x148 (operating tick). Even
// when fcn.18568 IS reached (via ForceOperatingTick from the entry),
// the entry block's MMIO reads overwrite the pre-arm before the
// deep-path checks fire. DriveOperatingTick is the validated
// programmatic substitute.
func (m *Machine) DriveOperatingTick(maxCycles int) uint32 {
	pc, _ := m.DriveOperatingTickUntil(nil, maxCycles)
	return pc
}

// DriveOperatingTickUntil is the predicate-driven form of
// DriveOperatingTick: it applies the same pre-arms + PC force, then
// pumps the loop in `bootChunkCycles` chunks with periodic IRQ5
// injection, calling `pred` every chunk. It returns as soon as `pred`
// returns true (with met=true) or when `maxCycles` is exhausted (met
// =false). Pass nil pred to disable early-exit (matches the legacy
// DriveOperatingTick behaviour).
//
// Why this exists: post-Rev-L the foreground DLP ring at 0xFFA61C
// (and the alt queue at 0xFFBBA6) accumulate work during boot that
// the operating loop drains over many ticks (see docs/DLP_RUNTIME.md).
// Fixed-budget DriveOperatingTick can spend its entire 20M-cycle
// budget inside DLP interpretation and never reach the deep-block
// bclr at 0x18F42 or the parser dispatch at slot 0x69A. Predicates
// like "bc67 bit 0 cleared" or "bc26 read index advanced" let the
// caller wait until the OBSERVABLE post-condition holds, with a
// budget that needs to be large enough but not exact.
//
// Cycle returned is the total spent inside the chunked loop
// (excluding the IRQ-service-cost overlay, which is small).
func (m *Machine) DriveOperatingTickUntil(pred func() bool, maxCycles int) (pc uint32, cycles int) {
	m.Bus.Write(0xFFB1E0, bus.Word, 0x0200)
	befa := m.Bus.Read(0xFFBEFA, bus.Word) &^ 0x0400
	m.Bus.Write(0xFFBEFA, bus.Word, befa)
	b9afb := m.Bus.Read(0xFF9AFB, bus.Byte) | 0x04
	m.Bus.Write(0xFF9AFB, bus.Byte, b9afb)

	// Tried clearing the coroutine frame at 0xFFB218 to bypass the
	// jmp (a0) at PC 0x18BD0 — empirically (cmd/tickprobe, Rev L) it
	// reads as 0 after boot anyway, and the firmware still exits via
	// other branches before reaching the bclr at 0x18F42. Left out
	// because it does nothing. See the test skip comments for the
	// full picture.

	m.CPU.SetReg(cpu.PC, OperatingTickDeepBlock)
	for done := 0; done < maxCycles; done += bootChunkCycles {
		ran := m.CPU.Run(bootChunkCycles)
		m.pumpTimer(ran)
		if pred != nil && pred() {
			return m.CPU.Reg(cpu.PC), done + bootChunkCycles
		}
	}
	return m.CPU.Reg(cpu.PC), maxCycles
}

// ForceOperatingTick directly invokes the firmware's main operating-tick
// function at fcn.18568 by setting the CPU's PC to OperatingTickEntry and
// running for `maxCycles` cycles, with periodic IRQ5 (timer) injection
// every bootIRQPeriod chunks of bootChunkCycles cycles. Returns the PC
// reached when execution stops (either by the cycle budget or by the
// function transferring control to a handler that doesn't return —
// e.g. through the stack-rts trick fcn.1B40 uses).
//
// IRQ5 is essential: the operating tick contains timer-wait loops (most
// notably at PC 0x250C8 inside one of its sub-routines) that spin until
// the bf12 timer counter — advanced by the IRQ5 handler at ROM 0x3ECE
// (`addq.l #1, $bf12.w`) — reaches a target value. Without injected
// ticks the operating tick blocks here instead of reaching the
// key-flag bclr at PC 0x18F42.
//
// Why this exists: in the natural-dispatch chain the firmware never
// reaches the operating tick because the dispatcher's path-A entry at
// PC 0x1E60 always pre-sets RAM[0xFFBF0A] to the sweep-handler pointer
// 0x3AD0 BEFORE calling fcn.1B40, so the dispatcher's stack-rts trick
// branches to 0x3AD0 (which itself never returns). The tick is therefore
// unreachable via interrupts alone.
//
// Empirically (commit 160cd38, cmd/keystate force-experiment) jumping
// the CPU to fcn.18568 directly causes the function to execute end-to-
// end: each iteration clears the key flag at PC 0x18F42
// (`bclr #0, $bc67.w`), runs the dispatch helpers at slots 0x430 /
// 0x67C / 0x69A / 0x6DC / 0x736, and eventually re-arms `bf0a` for the
// next handler.
//
// This API is the programmatic primitive a future "tick the instrument
// for one UI frame" caller can use — equivalent to one front-panel
// refresh cycle on real hardware. Pair with PressKeyMatrix to drive
// the firmware through a key-press end-to-end.
func (m *Machine) ForceOperatingTick(maxCycles int) uint32 {
	m.CPU.SetReg(cpu.PC, OperatingTickEntry)
	for done := 0; done < maxCycles; done += bootChunkCycles {
		ran := m.CPU.Run(bootChunkCycles)
		m.pumpTimer(ran)
	}
	return m.CPU.Reg(cpu.PC)
}

// PressKeyMatrix injects a raw front-panel key-matrix bitmap and runs the
// machine until the firmware reads it (or maxCycles elapses). It delivers IRQ3
// once (the front-panel "key available" interrupt — handler ROM 0x1582 latches
// flag bd77.0), then runs the main loop with the usual IRQ5 timer ticks until
// the key-read routine (ROM 0x3AB52) consumes the matrix into RAM 0x8F1E.
//
// The 6-byte bitmap uses the packing documented on device.FrontPanel. Returns
// true if the firmware read the key. Call after the machine has booted (e.g.
// after BootToOperating).
//
// KNOWN LIMITATION: IRQ3 delivery and the read protocol are verified, but in
// the operating state the emulator currently reaches the firmware does not
// consume the key (its key consumer at 0x01089A is never reached), so this
// returns false. Locating the firmware's key-poll trigger is pending — see the
// skipped TestFrontPanelKeyReadChain. For an end-to-end key-handling test
// in the meantime, use ForceOperatingTick after PressKeyMatrix — the
// operating tick runs synchronously and consumes the key flag.
func (m *Machine) PressKeyMatrix(matrix [6]byte, maxCycles int) bool {
	m.FrontPanel.InjectMatrix(matrix)

	// Deliver the front-panel interrupt once; the handler latches bd77.0.
	m.CPU.SetIRQ(3)
	m.CPU.Run(bootIRQServiceCost)
	m.CPU.SetIRQ(0)

	lb := emutest.NewLoopBreaker(bootBreakThresh)
	for done := 0; done < maxCycles; done += bootChunkCycles {
		ran := m.CPU.Run(bootChunkCycles)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		m.pumpTimer(ran)
		if m.FrontPanel.Consumed() {
			return true
		}
	}
	return m.FrontPanel.Consumed()
}

// PressKey injects a single matrix-bit press AND sets the two RAM gate
// bits the operating tick checks before dispatching the key:
//
//	0xFFBC67 bit 0  ← IRQ3 sets this naturally ("key flag")
//	0xFFBC67 bit 1  ← gate at PC 0x18F5E (`btst.b 0x1, 0xbc67.w`)
//	0xFFB072 bit 14 ← gate at PC 0x18F66 (`btst.b 0xe, 0xb072.w`)
//
// The two gate bits are NEVER set anywhere in the Rev L firmware (zero
// `bset` references). In real hardware they must be set by the front-
// panel μC writing to RAM as a bus master after debouncing/validating
// a key — our μC model is a passive MMIO target, so we model the
// effect here.
//
// Empirically (cmd/keymatrix3), with both gate bits forced + any
// matrix bit + IRQ3, the operating tick enters the matrix-dispatch
// path at 0x18F66 and touches 2 of the 15 fcn.520-clear cells
// (0xFFB20E + 0xFFBF01). Per-key differentiation isn't yet observed —
// more downstream state is gated. PressKey is the canonical
// experiment harness for finding that state.
func (m *Machine) PressKey(byteIdx, bit int) {
	m.FrontPanel.SetBit(byteIdx, bit)

	// μC RAM-master side effects: set the gate bits the operating tick
	// downstream tests.
	v67 := byte(m.Bus.Read(0xFFBC67, bus.Byte)) | 0x02
	m.Bus.Write(0xFFBC67, bus.Byte, uint32(v67))
	v72 := uint32(m.Bus.Read(0xFFB072, bus.Word)) | 0x4000
	m.Bus.Write(0xFFB072, bus.Word, v72)

	// Fire IRQ3 so the handler sets bc67.0.
	m.CPU.SetIRQ(3)
	m.CPU.Run(bootIRQServiceCost)
	m.CPU.SetIRQ(0)
}

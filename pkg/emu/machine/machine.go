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
//	0x320000           hardware-status ID register (read-only)
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
	ROMBase  = uint32(0x000000)
	ROMSize  = uint32(0x100000) // 1 MB — Rev L (4 × 27C020); see pkg/emu/romloader

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

	TestRAMBase = uint32(0xFEC000)
	TestRAMSize = uint32(0x004000) // 16 KB (0xFEC000–0xFEFFFF)

	RAMBase = uint32(0xFF0000)
	RAMSize = uint32(0x00F000) // 60 KB — stack + firmware variables (0xFF0000–0xFFEFFF)

	MMIOBase = device.MMIOBase // 0xFFF000
	MMIOSize = device.MMIOSize // 4 KB (0xFFF000–0xFFFFFF)
)

// Machine is a wired HP 8593A: ROM + RAM + MMIO devices + Musashi M68K CPU.
type Machine struct {
	Bus        *bus.Bus
	CPU        *musashi.CPU
	ROM        *bus.ROM
	CalNVRAM   *device.CalNVRAM // A16A1 cal SRAM at 0x200000 (PAL LCAL)
	CalRAM     *bus.RAM         // Cal-data working buffer at 0x2FC000 (16 KB)
	PIT        *bus.RAM         // MC68230 PIT stub (zeroed RAM)
	FrontPanel *device.FrontPanel
	TestRAM    *bus.RAM
	RAM        *bus.RAM
	MMIO       *device.HP8593AMMIO
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
	pit := bus.NewRAM(PITSize)
	fp := device.NewFrontPanel()
	testRAM := bus.NewRAM(TestRAMSize)
	ram := bus.NewRAM(RAMSize)
	mmio := device.NewHP8593AMMIO()

	b.Map(ROMBase, uint32(len(romImage)), "ROM", rom)
	b.Map(device.CalNVRAMBase, device.CalNVRAMSize, "CalNVRAM", calNVRAM)
	b.Map(CalRAMBase, CalRAMSize, "CalRAM", calRAM)
	b.Map(PITBase, PITSize, "PIT", pit)
	b.Map(device.FrontPanelBase, device.FrontPanelSize, "FrontPanel", fp)
	b.Map(TestRAMBase, TestRAMSize, "TestRAM", testRAM)
	b.Map(RAMBase, RAMSize, "RAM", ram)
	b.Map(MMIOBase, MMIOSize, "MMIO", mmio)

	c, err := musashi.New(b)
	if err != nil {
		return nil, err
	}

	return &Machine{
		Bus: b, CPU: c, ROM: rom, CalNVRAM: calNVRAM, CalRAM: calRAM,
		PIT: pit, FrontPanel: fp,
		TestRAM: testRAM, RAM: ram, MMIO: mmio,
	}, nil
}

// Boot-loop tuning (see package doc for why each is needed).
const (
	bootChunkCycles    = 2000 // cycles per Run() call
	bootBreakThresh    = 50   // consecutive same-loop chunks before LoopBreaker fires
	bootIRQPeriod      = 5    // inject an IRQ5 timer tick every N chunks
	bootIRQServiceCost = 400  // cycles allowed for the IRQ5 handler to run
)

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

// bootLoop is the shared boot driver: run in chunks, optionally break known
// delay loops (lb may be nil), and inject the periodic IRQ5 timer tick.
func (m *Machine) bootLoop(maxCycles int, lb *emutest.LoopBreaker) {
	for done := 0; done < maxCycles; done += bootChunkCycles {
		m.CPU.Run(bootChunkCycles)
		if lb != nil {
			lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		}

		if (done/bootChunkCycles)%bootIRQPeriod == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(bootIRQServiceCost)
			m.CPU.SetIRQ(0)
		}
	}
}

// OperatingTickEntry is the ROM address of the firmware's main UI tick —
// the function called "key consumer" in earlier notes. fcn.18568 in the
// Rev L disassembly. See docs/rom_annotations.md "Firmware dispatch jump
// table" / slot 0x148.
const OperatingTickEntry = uint32(0x018568)

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
		m.CPU.Run(bootChunkCycles)
		if (done/bootChunkCycles)%bootIRQPeriod == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(bootIRQServiceCost)
			m.CPU.SetIRQ(0)
		}
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
		m.CPU.Run(bootChunkCycles)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)

		if (done/bootChunkCycles)%bootIRQPeriod == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(bootIRQServiceCost)
			m.CPU.SetIRQ(0)
		}
		if m.FrontPanel.Consumed() {
			return true
		}
	}
	return m.FrontPanel.Consumed()
}

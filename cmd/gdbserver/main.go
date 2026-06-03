// Command gdbserver exposes the HP 8593A emulator as a GDB remote target, so any
// GDB front-end (gef/pwndbg, gdb -tui, VSCode) or rizin (rizin -d gdb://...) can
// set breakpoints/watchpoints, single-step, and inspect registers/memory against
// the real firmware running under our device models.
//
//	go run ./cmd/gdbserver -boot 140000000      # fast-forward to just before the UI render
//	# then, in another shell:
//	rizin -a m68k -b 32 -d gdb://localhost:3333
//	#   or: gdb -ex 'set architecture m68k' -ex 'target remote :3333'
package main

import (
	"flag"
	"log"

	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/gdb"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	addr := flag.String("addr", ":3333", "listen address")
	boot := flag.Int("boot", 0, "fast-forward N CPU cycles (with IRQ5) before serving")
	operating := flag.Bool("operating", false, "full sweep-driven boot to the live operating loop (normal run level) before serving")
	opCycles := flag.Int("opcycles", 250_000_000, "cycle budget for -operating boot")
	hpibCmd := flag.String("hpib", "", "install Option 041 + send this LF-terminated HP-IB command before serving (e.g. -hpib MEASOFF)")
	romdir := flag.String("rom", "hp8593a_eeproms", "ROM directory")
	flag.Parse()

	rom, err := romloader.LoadDir(*romdir)
	if err != nil {
		log.Fatal(err)
	}
	m, err := machine.New8593A(rom)
	if err != nil {
		log.Fatal(err)
	}
	m.CPU.Reset()

	if *operating {
		if *hpibCmd != "" {
			m.MMIO.InstallHPIB()
		}
		m.MMIO.SweepActive = true
		m.SweepDrive = true
		log.Printf("booting to the live operating loop (%d cycles, sweep-driven)...", *opCycles)
		m.BootToOperatingWithSweep(*opCycles)
		log.Printf("at operating level: PC=%06X", m.CPU.Reg(cpu.PC))
		if *hpibCmd != "" {
			m.MMIO.GPIB.AddressListener()
			pending := m.SendHPIB([]byte(*hpibCmd+"\n"), 5_000_000)
			log.Printf("sent HP-IB %q+LF (chip-pending=%d) — set a breakpoint and `continue` to see if it executes", *hpibCmd, pending)
		}
	}

	srv := gdb.New(m)
	if *boot > 0 {
		log.Printf("fast-forwarding %d cycles before serving...", *boot)
		srv.FastForward(*boot)
		log.Printf("ready at the boot point")
	}
	if err := srv.Serve(*addr); err != nil {
		log.Fatal(err)
	}
}

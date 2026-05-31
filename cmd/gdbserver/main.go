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

	"github.com/windhooked/HP859X_SA/pkg/emu/gdb"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	addr := flag.String("addr", ":3333", "listen address")
	boot := flag.Int("boot", 0, "fast-forward N CPU cycles (with IRQ5) before serving")
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

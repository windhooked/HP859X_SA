// Command keyfifo dumps the front-panel key FIFO at RAM[0xFFBBA6] after
// boot, then experiments with pushing a key byte to make bbba != bbbc,
// which should make the dispatcher fcn.1D58 take path B (the natural
// path to the operating tick at fcn.18568) instead of path A (the
// perpetual sweep redirect via bf0a = 0x3AD0).
//
// FIFO struct layout (decoded from fcn.42F8 push semantics):
//
//	$bba6 + 0x10 = $bbb6   data buffer pointer (4 bytes)
//	$bba6 + 0x0e = $bbb4   buffer capacity (word)
//	$bba6 + 0x14 = $bbba   READ index (word)
//	$bba6 + 0x16 = $bbbc   WRITE index (word)
//
// FIFO empty ⇔ READ == WRITE ⇔ bbba == bbbc → dispatcher takes path A.
// FIFO non-empty ⇔ bbba != bbbc → dispatcher takes path B.
//
// Usage:
//
//	go run ./cmd/keyfifo/   # boots, dumps struct, then tries pushing
package main

import (
	"fmt"
	"os"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m, err := machine.New8593A(rom)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m.CPU.Reset()
	m.BootToOperating(30_000_000)

	dump := func(label string) {
		size := m.Bus.Read(0xFFBBA6+0x0E, bus.Word)
		bufHi := m.Bus.Read(0xFFBBA6+0x10, bus.Word)
		bufLo := m.Bus.Read(0xFFBBA6+0x12, bus.Word)
		readIdx := m.Bus.Read(0xFFBBA6+0x14, bus.Word)  // = bbba
		writeIdx := m.Bus.Read(0xFFBBA6+0x16, bus.Word) // = bbbc
		fmt.Printf("%-20s  size=%04X  buf=%04X%04X  read(bbba)=%04X  write(bbbc)=%04X  empty=%v\n",
			label, size, bufHi, bufLo, readIdx, writeIdx, readIdx == writeIdx)
	}

	dump("Post-boot FIFO state")

	// Inject IRQ3 — pushes nothing to FIFO by itself, only sets bc67.0.
	m.CPU.SetIRQ(3)
	m.CPU.Run(400)
	m.CPU.SetIRQ(0)
	dump("After IRQ3")

	// Run a bit of operating loop — does the firmware push to FIFO
	// in response to bc67 set? Probably not (the FIFO push happens
	// from the operating tick which we don't reach naturally).
	const chunkCycles = 50_000
	for i := 0; i < 50; i++ {
		m.CPU.Run(chunkCycles)
		m.CPU.SetIRQ(5)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
	}
	dump("After 50 op-loop chunks")

	// Now MANUALLY push to the FIFO: increment bbbc by 1.
	// This should make the dispatcher take path B.
	bbbcOld := m.Bus.Read(0xFFBBA6+0x16, bus.Word)
	m.Bus.Write(0xFFBBA6+0x16, bus.Word, bbbcOld+1)
	dump("After manual bbbc++")

	// Run a few chunks WITH IRQ4 injection AND befd bit 7 pre-armed.
	// This combo simulates what the HP-IB data-send routine at PC 0x2258
	// would naturally produce (writes 0xE7 to f120 → bit 7 ends up in
	// befd via fcn.1D58's `or.b $f120, $befd`).
	for i := 0; i < 100; i++ {
		m.CPU.Run(chunkCycles)
		m.CPU.SetIRQ(5)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
		if i%8 == 4 {
			// Pre-arm befd bit 7 (so fcn.1D58 takes deep branch) AND
			// befe bit 6 (so the 0x1ED0 bsr $1b40 fires inside path B,
			// dispatching with bf0a==0 → operating tick at slot 0x148).
			befd := byte(m.Bus.Read(0xFFBEFD, bus.Byte)) | 0x80
			befe := byte(m.Bus.Read(0xFFBEFE, bus.Byte)) | 0x40
			m.Bus.Write(0xFFBEFD, bus.Byte, uint32(befd))
			m.Bus.Write(0xFFBEFE, bus.Byte, uint32(befe))
			// Also force bf0a clear so fcn.1B40 dispatches to slot 0x148
			// (operating tick) and not to whatever was previously queued.
			m.Bus.Write(0xFFBF0A, bus.Long, 0)
			m.CPU.SetIRQ(4)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	dump("After 100 chunks w/ IRQ4 + befd.7")
	bc67 := m.Bus.Read(0xFFBC67, bus.Byte)
	fmt.Printf("\nbc67 = %02X (bit 0 = %d; key flag should be 0 if consumer ran)\n",
		bc67, bc67&1)
}

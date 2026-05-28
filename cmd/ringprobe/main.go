// Command ringprobe — tests the user's brilliant insight: is the
// command ring buffer at 0xFFA61C consumed by an interpreter?
//
// Discovery: operating tick at PC 0x18BD6 polls a630/a632/a634 and on
// data calls slot 0x72A → fcn.34EE8 which is a byte-by-byte command
// interpreter (cmp 0x3F='?', 0x75='u', dispatch to handlers).
//
// Test flow:
//   1. Boot
//   2. Send IP via PS/2 scancodes (buffer fills at 0xFFBE02)
//   3. Send Enter scancode 0x5A — fcn.56cd2 saves to ring at 0xFFA61C+,
//      increments a634, does NOT update a630/a632.
//   4. Force a632 (write idx) > a630 (read idx) so consumer's
//      cmp.w 0x16(a4), 0x14(a4) bne 0x34F0A trigger fires.
//   5. Drive operating tick.
//   6. Check IP-witness cells.
//
// Watch PCs: 0x34EE8 (consumer entry), 0x4DF72 (IP body).
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

var ipScancodes = []byte{0x43, 0x4D, 0x5A} // 'i','p',<CR>

var ipClearCells = []uint32{
	0xFFAD6C, 0xFFAD6A, 0xFFAD74, 0xFFAD72, 0xFFAD6E, 0xFFAD70,
	0xFFA9AC, 0xFFB0EC, 0xFFB058, 0xFFAD64, 0xFFB20E, 0xFFBA5E,
	0xFFA5D4, 0xFFBF01, 0xFFBAF8,
}

var watchPCs = map[uint32]string{
	0x18BD6: "ring-presence check (a630 vs a632)",
	0x18BE6: "ring-consume call (a0 = ring base)",
	0x18BEA: "jsr slot 0x72A = fcn.34EE8 (RING CONSUMER)",
	0x34EE8: "fcn.34EE8 entry (byte interpreter)",
	0x34F0E: "fcn.34EF2 (a4 = ring; cmp 14(a4) vs 16(a4))",
	0x34F16: "skip-or-consume gate",
	0x34F28: "byte-pop loop top",
	0x4DF72: "fcn.520 IP body (success!)",
}

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(30_000_000)

	// Snapshot the ring + IP witnesses before sending.
	fmt.Println("=== state immediately after boot ===")
	dumpState(m)

	// Send IP + Enter scancodes.
	pending := m.SendHPIB(ipScancodes, 5_000_000)
	if pending != 0 {
		fmt.Printf("WARN: %d bytes left pending after send\n", pending)
	}

	// Pre-arm the operating tick state.
	m.Bus.Write(0xFFB1E0, bus.Word, 0x0200)
	m.Bus.Write(0xFFBEFA, bus.Word, 0x2000)
	m.CPU.SetReg(cpu.PC, 0x18ADC)

	fmt.Println("\n=== state after SendHPIB('iP<CR>') + pre-arm ===")
	dumpState(m)

	// Step a chunk so the receive chain + Enter dispatch run.
	steps := 100_000
	visits := make(map[uint32]int)
	for i := 0; i < steps; i++ {
		pc := m.CPU.Reg(cpu.PC)
		if _, ok := watchPCs[pc]; ok {
			visits[pc]++
		}
		if err := m.CPU.Step(); err != nil {
			fmt.Printf("step %d err: %v\n", i, err)
			break
		}
	}

	fmt.Printf("\n=== state after %d steps (Enter dispatch should have run) ===\n", steps)
	dumpState(m)
	dumpVisits(visits)

	// Take baseline snapshot for IP-witness comparison.
	basePre := snapshotCells(m, ipClearCells)

	// Drive multiple operating tick iterations — each re-enters the
	// 0x18ADC body and re-polls the ring at 0x18BD6.
	fmt.Println("\n>>> driving 10 × DriveOperatingTick(10M) — ring should be polled and consumed ...")
	for i := 0; i < 10; i++ {
		m.DriveOperatingTick(10_000_000)
	}

	fmt.Println("\n=== state after 10 ticks ===")
	dumpState(m)

	// Check IP-witness cells.
	fmt.Println("\nIP-witness cell changes:")
	witnessHit := 0
	for _, addr := range ipClearCells {
		now := byte(m.Bus.Read(addr, bus.Byte))
		if now != basePre[addr] {
			fmt.Printf("  %#06X  %#02X → %#02X  CHANGED\n",
				addr, basePre[addr], now)
			witnessHit++
		}
	}
	if witnessHit == 0 {
		fmt.Println("  (no IP-witness cells changed)")
	} else {
		fmt.Printf("  %d/%d witness cells changed — IP path FIRED!\n",
			witnessHit, len(ipClearCells))
	}
}

func snapshotCells(m *machine.Machine, addrs []uint32) map[uint32]byte {
	out := make(map[uint32]byte, len(addrs))
	for _, a := range addrs {
		out[a] = byte(m.Bus.Read(a, bus.Byte))
	}
	return out
}

func dumpState(m *machine.Machine) {
	fmt.Printf("  bc36=%#04X bc34=%#06X  (PS/2 buffer write idx + ptr)\n",
		m.Bus.Read(0xFFBC36, bus.Word),
		m.Bus.Read(0xFFBC34, bus.Long))
	fmt.Printf("  a630=%#04X a632=%#04X a634=%#04X  (ring read/write/count)\n",
		m.Bus.Read(0xFFA630, bus.Word),
		m.Bus.Read(0xFFA632, bus.Word),
		m.Bus.Read(0xFFA634, bus.Word))
	fmt.Printf("  a89f=%#04X (gate bit 5)  a896=%#04X (cleared by fcn.34EF2)\n",
		m.Bus.Read(0xFFA89F, bus.Byte),
		m.Bus.Read(0xFFA896, bus.Word))

	// Hint at ring contents (10 bytes per slot starting at ~a62A).
	fmt.Printf("  ring slot 1 @ 0xFFA62A: ")
	for i := uint32(0); i < 10; i++ {
		b := byte(m.Bus.Read(0xFFA62A+i, bus.Byte))
		if b >= 32 && b < 127 {
			fmt.Printf("%c", b)
		} else {
			fmt.Printf(".")
		}
	}
	fmt.Println()

	// Show ASCII buffer contents at 0xFFBE02 (where PS/2 chars land).
	fmt.Printf("  ASCII buf @ 0xFFBE02:  ")
	for i := uint32(0); i < 16; i++ {
		b := byte(m.Bus.Read(0xFFBE02+i, bus.Byte))
		if b == 0 {
			break
		}
		if b >= 32 && b < 127 {
			fmt.Printf("%c", b)
		} else {
			fmt.Printf(".")
		}
	}
	fmt.Println()
}

func dumpVisits(visits map[uint32]int) {
	if len(visits) == 0 {
		return
	}
	fmt.Println("  watched-PC visits this segment:")
	for pc, name := range watchPCs {
		if visits[pc] > 0 {
			fmt.Printf("    %#06X (%dx)  %s\n", pc, visits[pc], name)
		}
	}
}

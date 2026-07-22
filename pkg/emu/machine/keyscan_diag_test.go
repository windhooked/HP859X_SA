package machine

import (
	"fmt"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestKeyScanDiag — brute-force the key-FIFO raw-code space: push each raw byte
// into the 0xBB58 FIFO (as the firmware's own decode does), drive, and record
// the resulting b1e4 (active function), b070 (label-shown flag), menu index and
// vtable. Fresh boot per code (clean attribution); recovers from strictness
// panics (unimplemented display opcodes) and logs them. Finds the FREQUENCY/
// SPAN codes (cases b1e4=9/0xA + labels ON). RAWLO/RAWHI env bound the scan.
// DIAG=1.
func TestKeyScanDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	lo, hi := 0x20, 0x3F
	if s := os.Getenv("RAWLO"); s != "" {
		fmt.Sscanf(s, "%x", &lo)
	}
	if s := os.Getenv("RAWHI"); s != "" {
		fmt.Sscanf(s, "%x", &hi)
	}
	for raw := lo; raw <= hi; raw++ {
		func() {
			panicked := ""
			m, _ := New8593A(rom)
			m.CPU.Reset()
			m.MMIO.SweepActive = true
			m.SweepDrive = true
			m.BootToOperatingWithSweep(250_000_000)
			rd := func(a uint32, sz bus.Size) uint32 { return m.Bus.Read(a, sz) }
			m.Bus.Write(0xFFB1E4, bus.Word, 0xFFFF) // marker
			buf := rd(0xFFBB58+0x10, bus.Long)
			cap_ := rd(0xFFBB58+0xE, bus.Word)
			wr := rd(0xFFBB58+0x16, bus.Word)
			m.Bus.Write(buf+wr, bus.Byte, uint32(raw))
			m.Bus.Write(0xFFBB58+0x16, bus.Word, (wr+1)%cap_)
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = fmt.Sprintf("  [PANIC: %.60v]", r)
					}
				}()
				m.driveHPIB(30_000_000, nil)
			}()
			b1e4 := rd(0xFFB1E4, bus.Word)
			b070 := rd(0xFFB070, bus.Word)
			mark := ""
			if b1e4 == 9 || b1e4 == 0xA {
				mark = "  <<<< SHOW-LABELS case!"
			}
			if b070&1 != 0 {
				mark += " [b070.0 SET]"
			}
			t.Logf("raw %#02x -> b1e4=%#06x b070=%#06x menu=%d vt=%#x%s%s",
				raw, b1e4, b070, rd(0xFF956A, bus.Word), rd(0xFF9566, bus.Long), mark, panicked)
		}()
	}
}

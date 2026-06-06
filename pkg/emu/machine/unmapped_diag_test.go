package machine

import (
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// TestUnmappedReadsDiag finds reads to addresses OUTSIDE our known device ranges
// during boot — to detect an unmodeled NVRAM/state region (e.g. the user-NVRAM the
// EEVblog thread describes). Known mapped: ROM 0-0xFFFFF, CalNVRAM 0x200000-0x20FFFF,
// FrontPanel 0xEF4xxx, PIT 0xEF8xxx, DLPRAM/RAM/MMIO 0xFC0000-0xFFFFFF, plus the
// 0x310000/0x320000 latches. Anything else the firmware reads is an unmodeled region.
// DIAG=1.
func TestUnmappedReadsDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	m := diagBootMachine(t)
	known := func(a uint32) bool {
		switch {
		case a <= 0x0FFFFF: // ROM
			return true
		case a >= 0x200000 && a <= 0x20FFFF: // CalNVRAM
			return true
		case a >= 0xEF4000 && a <= 0xEF401F: // FrontPanel
			return true
		case a >= 0xEF8000 && a <= 0xEF80FF: // PIT/ATkbd
			return true
		case a >= 0xFC0000 && a <= 0xFFFFFF: // DLPRAM/TestRAM/RAM/MMIO
			return true
		case a >= 0x310000 && a <= 0x320003: // sweep/diag latches
			return true
		}
		return false
	}
	page := map[uint32]int{}        // 64KB page of unmodeled reads
	pcByPage := map[uint32]uint32{} // a sample PC per page
	total := 0
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if known(addr) {
			return
		}
		total++
		p := addr >> 16
		page[p]++
		pcByPage[p] = m.CPU.Reg(cpu.PC)
	}
	m.BootToOperatingWithSweep(260_000_000)
	m.Bus.OnRead = nil

	t.Logf("reads to UNMODELED addresses during boot: %d (distinct 64KB pages: %d)", total, len(page))
	type kv struct {
		k uint32
		v int
	}
	var ks []kv
	for k, v := range page {
		ks = append(ks, kv{k, v})
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i].v > ks[j].v })
	for i, e := range ks {
		if i >= 16 {
			break
		}
		t.Logf("  page 0x%06X..  reads=%-7d (e.g. from PC 0x%X)", e.k<<16, e.v, pcByPage[e.k])
	}
	if total == 0 {
		t.Log("⇒ NO unmodeled reads — there is no separate user-NVRAM region the firmware accesses at boot")
	}
	_ = fmt.Sprint
}

// Command caltrace boots the HP 8593A firmware with CalNVRAM access tracing
// enabled and dumps a histogram of (offset, size, read/write) tuples plus a
// chronological log of the first N distinct offsets touched.
//
// The goal is to reverse-engineer the Rev L cal-data layout: which bytes the
// firmware reads as "configuration" (small staged values like the 17.12.90
// `0x200a3c` sweep gate), which it writes (cal updates), and what regions are
// only swept as bulk checksum data.
//
// Usage:
//
//	go run ./cmd/caltrace/ [cycles] [top]
//
//	cycles = boot cycle budget (default 30 000 000 — same as renderframe)
//	top    = how many hottest read offsets to print (default 40)
package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

type stat struct {
	off    uint32
	reads  int
	writes int
	last   uint32 // last value seen (handy for spotting small staged values)
	sz     bus.Size
}

func main() {
	cycles := 30_000_000
	top := 40
	faithful := false
	for _, a := range os.Args[1:] {
		switch a {
		case "--faithful":
			faithful = true
		default:
			if n, err := strconv.Atoi(a); err == nil && n > 0 {
				if cycles == 30_000_000 {
					cycles = n
				} else {
					top = n
				}
			}
		}
	}

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

	// Histogram keyed by (offset, size, write).
	// Using a plain map; the cal NVRAM is only 64 KB so worst case is bounded.
	stats := make(map[uint32]*stat)
	firstSeen := make([]uint32, 0, 256)
	seen := make(map[uint32]bool)

	m.CalNVRAM.Trace = func(off uint32, sz bus.Size, val uint32, write bool) {
		s, ok := stats[off]
		if !ok {
			s = &stat{off: off, sz: sz}
			stats[off] = s
			firstSeen = append(firstSeen, off)
			seen[off] = true
		}
		if write {
			s.writes++
		} else {
			s.reads++
		}
		s.last = val
	}

	m.CPU.Reset()
	if faithful {
		m.BootToOperatingFaithful(cycles)
	} else {
		m.BootToOperating(cycles)
	}

	// Report.
	fmt.Printf("=== CalNVRAM access summary (boot=%d cycles, %d distinct offsets) ===\n",
		cycles, len(stats))

	all := make([]*stat, 0, len(stats))
	for _, s := range stats {
		all = append(all, s)
	}

	// Filter out the "swept once" offsets — the byte-checksum loop at ROM
	// 0x454A reads every one of the 64 KB exactly once and dominates the
	// histogram. Real cal-data bytes get hit multiple times (loop polls)
	// or written (cal updates), so they stand out under reads>1 OR writes>0.
	interesting := make([]*stat, 0, 64)
	for _, s := range all {
		if s.reads > 1 || s.writes > 0 {
			interesting = append(interesting, s)
		}
	}
	sort.Slice(interesting, func(i, j int) bool {
		if interesting[i].reads != interesting[j].reads {
			return interesting[i].reads > interesting[j].reads
		}
		return interesting[i].off < interesting[j].off
	})
	fmt.Printf("\nInteresting offsets (reads>1 OR writes>0) — these are the real cal-data sites:\n")
	fmt.Printf("  %-8s  %5s  %5s  %3s  %-10s\n", "off", "reads", "wr", "sz", "last")
	for i, s := range interesting {
		if i >= top {
			break
		}
		fmt.Printf("  +%06X  %5d  %5d  %3d  %#010x\n", s.off, s.reads, s.writes, s.sz, s.last)
	}
	fmt.Printf("(showing %d of %d interesting offsets)\n", min(top, len(interesting)), len(interesting))

	// First-N-distinct order tells the boot-time access sequence (header read
	// order; first-touched offsets are usually structural fields).
	fmt.Printf("\nFirst 32 distinct offsets touched (chronological — boot-time order):\n")
	for i, off := range firstSeen {
		if i >= 32 {
			break
		}
		s := stats[off]
		fmt.Printf("  %2d. +%06X  sz=%d  reads=%d  writes=%d  last=%#x\n",
			i+1, off, s.sz, s.reads, s.writes, s.last)
	}

	// Coverage: what fraction of the 64 KB is touched?
	nReadBytes := 0
	for _, s := range all {
		if s.reads > 0 {
			nReadBytes += int(s.sz)
		}
	}
	fmt.Printf("\nCoverage: %d/65536 bytes read (%.1f%%)\n",
		nReadBytes, 100*float64(nReadBytes)/65536)
}

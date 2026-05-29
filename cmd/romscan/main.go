// Command romscan catalogues ROM regions by data signature — identifies
// menu structs, command tables, string tables, jump tables, float
// constant pools, DLP source, and other era-typical software patterns.
//
// Approach: walk the ROM in 256-byte windows, score each window against
// signatures, then merge consecutive windows of the same dominant
// signature into reported "regions".
package main

import (
	"fmt"
	"sort"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

type sig int

const (
	sigUnknown sig = iota
	sigVectorTable
	sigJumpTable
	sigCode
	sigCString
	sigPascalString
	sigDLPSource
	sigFloatPool
	sigIntPool
	sigLookupTable
	sigZeros
	sigFFs
)

var sigName = map[sig]string{
	sigUnknown:      "unknown",
	sigVectorTable:  "M68K vector table",
	sigJumpTable:    "4EF9-stride jump table",
	sigCode:         "M68K code",
	sigCString:      "C-style strings (NUL-terminated)",
	sigPascalString: "Pascal/HP-style length-prefixed strings",
	sigDLPSource:    "compiled DLP source (DLP IF/ENDIF/MN/KL patterns)",
	sigFloatPool:    "FP/double constant pool",
	sigIntPool:      "integer constant pool",
	sigLookupTable:  "small-range LUT (calibration/translation)",
	sigZeros:        "zero-filled (padding/unused)",
	sigFFs:          "FF-filled (erased/uninitialized)",
}

func classify(rom []byte, off uint32, size uint32) sig {
	end := off + size
	if end > uint32(len(rom)) {
		end = uint32(len(rom))
	}
	w := rom[off:end]

	// Easy cases first.
	zeros, ffs := 0, 0
	for _, b := range w {
		if b == 0 {
			zeros++
		} else if b == 0xFF {
			ffs++
		}
	}
	if zeros > len(w)*9/10 {
		return sigZeros
	}
	if ffs > len(w)*9/10 {
		return sigFFs
	}

	// 4EF9 stride detection — JSR/JMP absolute long table.
	if size >= 12 {
		jmpHits := 0
		for i := 0; i+5 < len(w); i += 6 {
			if w[i] == 0x4E && w[i+1] == 0xF9 {
				jmpHits++
			}
		}
		if jmpHits > int(size)/6*8/10 {
			return sigJumpTable
		}
	}

	// M68K vector table — high density of 24-bit ROM pointers
	// (high byte = 0, next byte = 0-0x0F since ROM is 1 MB).
	if off < 0x100 {
		ptrs := 0
		for i := 0; i+3 < len(w); i += 4 {
			if w[i] == 0 && w[i+1] < 0x10 {
				ptrs++
			}
		}
		if ptrs > len(w)/4*8/10 {
			return sigVectorTable
		}
	}

	// DLP-source detection — look for HP-IB-style command fragments
	// (uppercase, semicolons, IF/ENDIF/MN keywords).
	asciiCount := 0
	semiCount := 0
	upperCount := 0
	for _, b := range w {
		if b >= 0x20 && b < 0x7F {
			asciiCount++
		}
		if b == ';' {
			semiCount++
		}
		if b >= 'A' && b <= 'Z' {
			upperCount++
		}
	}
	if asciiCount > len(w)*7/10 && semiCount > 5 && upperCount > len(w)/5 {
		return sigDLPSource
	}

	// Pascal-string detector — sequences like (small-len) (ascii*len) (small-len)...
	if asciiCount > len(w)/2 {
		// Look for the softkey 60-marker + length pattern dominating.
		marker60 := 0
		for i := 0; i+1 < len(w); i++ {
			if w[i] == 0x60 && w[i+1] < 0x20 {
				marker60++
			}
		}
		if marker60 > 4 {
			return sigPascalString
		}
		// Otherwise, regular NUL-terminated C strings.
		nulCount := zeros
		if nulCount > len(w)/12 && asciiCount > len(w)*6/10 {
			return sigCString
		}
	}

	// Integer constant pool — many 16/32-bit values with regular
	// alignment and limited range.
	if size >= 64 {
		ranges := make(map[int]int)
		for i := 0; i+1 < len(w); i += 2 {
			v := int(w[i])<<8 | int(w[i+1])
			ranges[v/256]++
		}
		// Many small values clustered in 1-2 ranges → LUT.
		if len(ranges) < 8 && ranges[0] > len(w)/4 {
			return sigLookupTable
		}
	}

	// Default: if mostly ASCII, call it strings; otherwise probably code.
	if asciiCount > len(w)*7/10 {
		return sigCString
	}
	// Look for typical M68K opcode density (link.w = 4E56, rts = 4E75 etc.)
	linkOps := 0
	for i := 0; i+1 < len(w); i++ {
		if w[i] == 0x4E && (w[i+1] == 0x56 || w[i+1] == 0x75 || w[i+1] == 0x5E) {
			linkOps++
		}
	}
	if linkOps > 2 {
		return sigCode
	}
	return sigUnknown
}

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}

	const window = uint32(256)
	// Score each window.
	type wseg struct {
		off  uint32
		s    sig
	}
	var segs []wseg
	for off := uint32(0); off < uint32(len(rom)); off += window {
		segs = append(segs, wseg{off, classify(rom, off, window)})
	}

	// Merge consecutive same-signature windows.
	type region struct {
		start, end uint32
		s          sig
	}
	var regions []region
	cur := region{segs[0].off, segs[0].off + window, segs[0].s}
	for _, w := range segs[1:] {
		if w.s == cur.s {
			cur.end = w.off + window
		} else {
			regions = append(regions, cur)
			cur = region{w.off, w.off + window, w.s}
		}
	}
	regions = append(regions, cur)

	// Filter — only show regions ≥ 1 KB OR with a non-padding signature.
	fmt.Printf("=== ROM region map (signature-classified, 256-byte windows) ===\n\n")
	for _, r := range regions {
		size := r.end - r.start
		if size < 256 {
			continue
		}
		// Skip very small unknown/padding regions.
		if size < 1024 && (r.s == sigUnknown || r.s == sigZeros || r.s == sigFFs) {
			continue
		}
		fmt.Printf("  0x%06X..0x%06X  %5d bytes  %s\n",
			r.start, r.end, size, sigName[r.s])
	}

	// Summarize: total bytes per signature.
	fmt.Printf("\n=== summary by signature (totals across ROM) ===\n")
	totals := map[sig]uint32{}
	for _, r := range regions {
		totals[r.s] += r.end - r.start
	}
	type kv struct {
		s sig
		n uint32
	}
	var sorted []kv
	for s, n := range totals {
		sorted = append(sorted, kv{s, n})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].n > sorted[j].n })
	totalRom := uint32(len(rom))
	for _, kv := range sorted {
		fmt.Printf("  %5d KB  %5.1f%%  %s\n",
			kv.n/1024, float64(kv.n)/float64(totalRom)*100, sigName[kv.s])
	}
}

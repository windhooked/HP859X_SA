// Command modulemap walks the rom.asm function listings and groups
// functions by ROM-address proximity into hypothesized "compilation
// units" (source files). C compilers emit functions in source-file
// order, and the linker places object files contiguously — so
// consecutive functions within ~1 KB of each other are statistically
// very likely to come from the same .c file.
//
// Output is a candidate source-file layout: each module gets a guessed
// purpose based on (a) known function PCs we've reverse-engineered and
// (b) the address-range conventions HP used in instrument firmware of
// this era (boot+vectors at 0x0000, math helpers low, mode dispatchers
// in the middle 10000-1FFFF range, parser+output in 50000-5FFFF, etc.).
package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
)

type fcn struct {
	pc   uint32
	name string
}

// knownPurpose maps function PCs we've identified to their purpose.
// Populated from this session's reverse engineering.
var knownPurpose = map[uint32]string{
	0x000520: "slot 0x520 → IP handler",
	0x000610: "slot 0x610 → handler dispatcher",
	0x000706: "slot 0x706 → ring follow-up",
	0x00072A: "slot 0x72A → ring consumer",
	0x000736: "slot 0x736 → matrix read",
	0x00069A: "slot 0x69A → HP-IB parser",
	0x000148: "slot 0x148 → operating tick",
	0x002642: "IRQ4 handler (HP-IB / keyboard)",
	0x002B1E: "IRQ3 handler (front-panel matrix)",
	0x002E74: "SystemID MMIO probe (boot)",
	0x01A3E0: "model-detection dispatch (sets IDNUM)",
	0x004DF34: "IP scheduler gate",
	0x004DF72: "IP body — clears state cells",
	0x018568: "operating tick body",
	0x056CD2: "save buffer-struct to recall ring",
	0x056D1A: "binary dispatcher (parser code → jump table)",
	0x056E12: "ensure trailing ';' on buffer",
	0x057278: "per-byte parser classifier",
	0x05714C: "PS/2 Set 2 scancode → ASCII",
	0x058C2E: "HP-IB parser body",
	0x059D2A: "front-panel matrix read",
	0x000BE22: "length-prefixed string printer",
	0x00E7A2: "menu label display dispatch",
	0x056096: "echo buffer to display",
	0x034EE8: "ring consumer (recall/display refresh)",
}

// addrRangeHints — broad HP instrument firmware conventions for the
// 8593A's 1 MB ROM. These are EDUCATED GUESSES based on what kinds of
// code typically lives at what ROM offsets in the era's HP firmware.
var addrRangeHints = []struct {
	lo, hi  uint32
	purpose string
}{
	{0x000000, 0x0000C4, "M68K exception/vector table (data)"},
	{0x0000C4, 0x001B34, "MASTER DISPATCH TABLE — 1128 slots (data)"},
	{0x001B34, 0x002000, "reset_pc + boot prologue (hand assembly)"},
	{0x002000, 0x002800, "boot init + IRQ handler bodies (mixed asm/C)"},
	{0x002800, 0x004000, "boot probes, mode setup, math helpers"},
	{0x004000, 0x008000, "ROM checksum + RAM march test + cal data load"},
	{0x008000, 0x00C000, "front-panel μC handshake, RAM init"},
	{0x00C000, 0x010000, "string/text rendering helpers (display_text.c)"},
	{0x010000, 0x018000, "scalar dispatch helpers (variable getters)"},
	{0x018000, 0x01A000, "operating tick + dispatcher (operating_loop.c)"},
	{0x01A000, 0x020000, "model/option detection (options.c, boot_id.c)"},
	{0x020000, 0x028000, "HP-IB output formatters (REV, ID, error messages)"},
	{0x028000, 0x030000, "calibration data tables + helpers"},
	{0x030000, 0x036000, "command interpreter, ring consumer (recall.c)"},
	{0x036000, 0x040000, "trace processing, marker math"},
	{0x040000, 0x048000, "frequency / sweep control"},
	{0x048000, 0x04E000, "user-function compiler + executor"},
	{0x04D000, 0x04E000, "Initial Preset handler (preset.c)"},
	{0x04E000, 0x055000, "video/sweep capture, IRQ6 path"},
	{0x055000, 0x05A000, "HP-IB parser + command dispatch (parser.c)"},
	{0x05A000, 0x060000, "softkey label dispatch, front-panel keys"},
	{0x060000, 0x070000, "compiled DLP source (data, not code)"},
	{0x070000, 0x07C000, "DLP runtime + helpers"},
	{0x07C000, 0x07F800, "softkey label table + parser-name table (data)"},
	{0x07F800, 0x080000, "extension command name table (data)"},
	{0x080000, 0x100000, "Option 027 (26.5 GHz) extension code"},
}

func main() {
	f, err := os.Open("/Users/hannesdw/src/HP859X_SA/docs/rom.asm")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	// Scan for "┌ fcn.NNNNNNNN()" markers.
	fcnRe := regexp.MustCompile(`^┌ fcn\.([0-9a-f]{8})\(\)`)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	var fns []fcn
	for scanner.Scan() {
		m := fcnRe.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		pc, _ := strconv.ParseUint(m[1], 16, 32)
		fns = append(fns, fcn{pc: uint32(pc), name: m[1]})
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
	sort.Slice(fns, func(i, j int) bool { return fns[i].pc < fns[j].pc })

	fmt.Printf("=== identified %d functions in ROM ===\n\n", len(fns))

	// Group: a new module starts when the gap to the previous function
	// exceeds threshold OR we're at the start. Threshold tuned to ~1 KB
	// (typical max function size + slack).
	const gapThreshold = uint32(0x400)
	type module struct {
		start, end uint32
		count      int
		known      []string
	}
	var modules []module
	cur := module{start: fns[0].pc}
	prev := fns[0].pc
	for _, fn := range fns {
		if fn.pc-prev > gapThreshold && fn.pc-cur.start > 0 {
			cur.end = prev
			modules = append(modules, cur)
			cur = module{start: fn.pc}
		}
		cur.count++
		if p, ok := knownPurpose[fn.pc]; ok {
			cur.known = append(cur.known, fmt.Sprintf("0x%06X %s", fn.pc, p))
		}
		prev = fn.pc
	}
	cur.end = prev
	modules = append(modules, cur)

	fmt.Printf("=== %d hypothesized compilation units (proximity-clustered) ===\n", len(modules))
	for i, m := range modules {
		// Try to find a range hint covering this module.
		hint := ""
		for _, h := range addrRangeHints {
			if m.start >= h.lo && m.start < h.hi {
				hint = h.purpose
				break
			}
		}
		fmt.Printf("\n  Module %3d  ROM 0x%06X..0x%06X  (%d fcns, %d KB)  %s\n",
			i+1, m.start, m.end, m.count, (m.end-m.start)/1024+1, hint)
		for _, k := range m.known {
			fmt.Printf("    └ %s\n", k)
		}
	}
}

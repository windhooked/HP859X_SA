// Command softkeys extracts and prints the 8593A softkey label table
// from ROM. Each softkey record has the format:
//
//	60 LEN <ASCII text> 20 10 NN 00 00
//	       \------------/    ^^
//	         label text   handler ID
//
// The handler IDs are indices into the per-menu dispatch chain; when a
// softkey position is pressed (via PS/2 F-key or front-panel softkey),
// the firmware looks up the current menu's softkey-position-N handler
// ID and invokes it via fcn.610 (= fcn.E7A2 → handler table at RAM
// 0x9566).
//
// EXECUTE|TITLE has handler ID 0x98 — per the Service Guide (Ch.3
// p.205) this softkey runs the title buffer text AS an HP-IB command.
package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	type entry struct {
		off       uint32
		text      string
		handlerID byte
	}
	var hits []entry
	seen := map[string]bool{}

	for i := uint32(0); i < uint32(len(rom))-10; i++ {
		if rom[i] != 0x60 {
			continue
		}
		ln := rom[i+1]
		if ln < 1 || ln > 31 {
			continue
		}
		textStart := i + 2
		j := textStart
		for j < i+40 && j < uint32(len(rom)) && rom[j] >= 0x20 && rom[j] < 0x7F {
			j++
		}
		// Need trailing 10 NN 00 00 marker.
		if j+4 > uint32(len(rom)) {
			continue
		}
		if rom[j] != 0x10 || rom[j+2] != 0x00 || rom[j+3] != 0x00 {
			continue
		}
		text := string(rom[textStart:j])
		// Trim trailing spaces.
		for len(text) > 0 && text[len(text)-1] == ' ' {
			text = text[:len(text)-1]
		}
		if len(text) < 3 || len(text) > 25 {
			continue
		}
		// Only ASCII + a few markers.
		ok := true
		for _, c := range []byte(text) {
			if !(c >= 32 && c < 127) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		key := fmt.Sprintf("%s|%02X", text, rom[j+1])
		if seen[key] {
			continue
		}
		seen[key] = true
		hits = append(hits, entry{off: i, text: text, handlerID: rom[j+1]})
		i = j + 4 - 1
	}

	fmt.Printf("=== 8593A softkey-label table — %d unique entries ===\n", len(hits))

	// Sort by handler ID.
	sort.Slice(hits, func(a, b int) bool { return hits[a].handlerID < hits[b].handlerID })
	for _, h := range hits {
		fmt.Printf("  ROM 0x%06X  id=0x%02X  %q\n", h.off, h.handlerID, h.text)
	}
}

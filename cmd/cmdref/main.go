// Command cmdref generates the full HP 859x GPIB/DLP command reference from the
// Rev L ROM image. It decodes every entry in the parser command table
// (ROM 0x7C800..0x80000), resolves each handler through the master dispatch
// table (0xC4 + slot*6) or the secondary DLP table (0x71E02), auto-classifies
// the handler (DLP-source trampoline / constant stub / native code), flags each
// command documented/undocumented/DLP-internal, and merges a hand/agent-authored
// behavior overlay from docs/command_notes.json.
//
// Outputs (regenerable — re-run any time to resync with the ROM):
//
//	docs/COMMAND_REFERENCE.md     human table
//	docs/command_reference.json   machine-readable records
//
// The deep-RE behavior paragraphs live in docs/command_notes.json (keyed by
// command name) so regenerating from ROM never clobbers them.
//
// Usage:  go run ./cmd/cmdref            (writes into docs/)
//
//	go run ./cmd/cmdref -outdir X  (alternate output dir)
package main

import (
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

//go:embed documented.txt
var documentedTxt string

const (
	parserStart = 0x7C800
	parserEnd   = 0x80000

	dispBase = 0x000C4 // master dispatch table (4EF9 <target> entries)
	dispEnd  = 0x01B34

	dlpTableBase = 0x71E02 // secondary DLP runtime table (4-byte ROM ptrs)
	dlpSlotBase  = 0x47D
	dlpSlotEnd   = 0x6E9
)

// Command is one parser-table entry, fully resolved and classified.
type Command struct {
	Name        string `json:"name"`
	Tag         string `json:"tag"`                  // record tag: 30/40/10/20
	ArgType     string `json:"arg_type"`             // the type/terminator byte after the name
	Slot        uint16 `json:"slot"`                 // dispatch-table slot index
	HandlerPC   uint32 `json:"handler_pc"`           // resolved handler address (0 if unresolved)
	TableOffset uint32 `json:"table_offset"`         // where the record lives in the parser table
	Status      string `json:"status"`               // documented | undocumented | dlp-internal
	Kind        string `json:"kind"`                 // trampoline | stub | native | unresolved
	Category    string `json:"category"`             // auto keyword category (refined by notes)
	DLPSource   string `json:"dlp_source,omitempty"` // decoded DLP source snippet for trampolines
	StubConst   string `json:"stub_const,omitempty"` // constant returned by a stub
	Behavior    string `json:"behavior,omitempty"`   // deep-RE paragraph (from notes overlay)
	Verified    bool   `json:"verified,omitempty"`   // notes overlay marked this hand-verified
}

// Note is one entry in the behavior overlay (docs/command_notes.json).
type Note struct {
	Behavior string `json:"behavior"`
	Category string `json:"category,omitempty"`
	Verified bool   `json:"verified,omitempty"`
}

func be16(b []byte, o uint32) uint16 { return binary.BigEndian.Uint16(b[o : o+2]) }
func be32(b []byte, o uint32) uint32 { return binary.BigEndian.Uint32(b[o : o+4]) }

func resolveSlot(rom []byte, idx uint16) uint32 {
	pc := uint32(dispBase) + uint32(idx)*6
	if pc+6 <= uint32(len(rom)) && rom[pc] == 0x4E && rom[pc+1] == 0xF9 {
		return be32(rom, pc+2)
	}
	if uint32(idx) >= dlpSlotBase && uint32(idx) <= dlpSlotEnd {
		to := uint32(dlpTableBase) + (uint32(idx)-dlpSlotBase)*4
		if to+4 <= uint32(len(rom)) {
			return be32(rom, to)
		}
	}
	return 0
}

func isNameByte(b byte) bool { return b >= 0x20 && b < 0x7F }

func validName(s string) bool {
	if len(s) < 2 || len(s) > 12 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

// decodeHandler locates the 3-byte handler (TYPE>=0x80, slotHi, slotLo) that
// follows the name+type bytes, returning (argType, slotIdx). It tolerates the
// NUL form and the 0x01/0x02/0xff type-byte forms.
func decodeHandler(rom []byte, nameEnd uint32) (argType byte, slot uint16, ok bool) {
	// nameEnd points just past the ASCII name (at the type/terminator byte).
	term := rom[nameEnd]
	// Search the next few bytes for the TYPE>=0x80 that starts the handler.
	for k := nameEnd; k < nameEnd+4 && k+3 <= uint32(len(rom)); k++ {
		if rom[k] >= 0x80 {
			return term, be16(rom, k+1), true
		}
	}
	// 4-byte "00 ARG slotHi slotLo" form.
	if rom[nameEnd] == 0x00 && nameEnd+4 <= uint32(len(rom)) {
		return term, be16(rom, nameEnd+2), true
	}
	return term, 0, false
}

// classify inspects the handler bytes to label the handler kind and, for DLP
// trampolines, extract the DLP source-text snippet they run.
func classify(rom []byte, pc uint32) (kind, dlpSrc, stubConst string) {
	if pc == 0 || pc+4 > uint32(len(rom)) {
		return "unresolved", "", ""
	}
	// Constant stub:  move.w #imm,D0 (30 3C iiii) ; rts (4E 75)
	if rom[pc] == 0x30 && rom[pc+1] == 0x3C && pc+6 <= uint32(len(rom)) &&
		rom[pc+4] == 0x4E && rom[pc+5] == 0x75 {
		return "stub", "", fmt.Sprintf("0x%04X", be16(rom, pc+2))
	}
	// DLP-source trampoline: within the first ~28 bytes there's a
	//   lea (d16,PC),A0/A4   (41 FA dddd | 49 FA dddd)
	// and a jsr to the DLP scheduler  jsr $0D18.w / $0D12.w  (4E B8 0D 18|12).
	var leaAt uint32
	var haveLea, haveJsr bool
	for k := pc; k < pc+28 && k+2 <= uint32(len(rom)); k += 2 {
		if (rom[k] == 0x41 || rom[k] == 0x49) && rom[k+1] == 0xFA {
			leaAt = k
			haveLea = true
		}
		if rom[k] == 0x4E && rom[k+1] == 0xB8 && k+4 <= uint32(len(rom)) {
			v := be16(rom, k+2)
			if v == 0x0D18 || v == 0x0D12 {
				haveJsr = true
			}
		}
	}
	if haveLea && haveJsr {
		// source = (leaAt+2) + sign_extend(d16)
		d := int32(int16(be16(rom, leaAt+2)))
		src := uint32(int32(leaAt+2) + d)
		return "trampoline", readDLPSource(rom, src), ""
	}
	return "native", "", ""
}

// readDLPSource extracts a readable snippet of the DLP source text at off.
func readDLPSource(rom []byte, off uint32) string {
	if off >= uint32(len(rom)) {
		return ""
	}
	var b strings.Builder
	for i := off; i < off+80 && i < uint32(len(rom)); i++ {
		c := rom[i]
		if c == 0x00 {
			break
		}
		if c >= 0x20 && c < 0x7F {
			b.WriteByte(c)
		} else if c == 0x0A || c == 0x0D {
			b.WriteByte(' ')
		} else {
			// non-text control byte — stop at the first run of binary.
			if b.Len() > 0 {
				break
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// category assigns a keyword bucket from the command name (refined by notes).
func category(name string) string {
	n := name
	switch {
	case strings.HasPrefix(n, "__"):
		return "dlp-internal"
	case strings.HasPrefix(n, "CAL") || strings.Contains(n, "MXR") || strings.Contains(n, "FLTDATA") ||
		strings.Contains(n, "YTF") || strings.Contains(n, "TWEAK") || strings.Contains(n, "OFST") ||
		n == "FACTSET" || strings.Contains(n, "AMPCOR") || strings.Contains(n, "CORREK") || strings.Contains(n, "KFACT"):
		return "cal/factory"
	case strings.Contains(n, "DAC") || strings.Contains(n, "ADC"):
		return "dac/adc"
	case strings.HasPrefix(n, "DD"):
		return "data-direct"
	case strings.Contains(n, "PLL") || strings.Contains(n, "LOCK") || strings.HasPrefix(n, "HN") ||
		strings.Contains(n, "PHASE") || n == "SETPLL" || n == "SYNCMODE" || n == "EXTOED":
		return "synth/pll"
	case strings.Contains(n, "STAT") || n == "SHOWOPT" || strings.HasPrefix(n, "PWRUP") ||
		n == "FORCECNT" || n == "GSTAT" || strings.Contains(n, "CNTL") || n == "IDNUM":
		return "diag/status"
	case strings.HasPrefix(n, "MK") || strings.HasPrefix(n, "ZMK"):
		return "marker"
	case strings.HasPrefix(n, "LIM"):
		return "limit-line"
	case strings.HasPrefix(n, "WIN"):
		return "window"
	case strings.HasPrefix(n, "TR") || strings.HasPrefix(n, "TRA") || strings.Contains(n, "TRACE"):
		return "trace"
	case strings.HasPrefix(n, "K") && len(n) <= 3:
		return "softkey"
	case strings.Contains(n, "TV") || strings.Contains(n, "CATV") || strings.Contains(n, "DOCAT"):
		return "tv/catv"
	case strings.Contains(n, "RD") || strings.Contains(n, "WR") || n == "BADDR" || strings.HasPrefix(n, "STOR") || n == "MOV":
		return "read/write"
	default:
		return "other"
	}
}

func main() {
	outdir := flag.String("outdir", "docs", "output directory for the reference files")
	romdir := flag.String("romdir", "hp8593a_eeproms", "ROM source directory")
	notesPath := flag.String("notes", "docs/command_notes.json", "behavior overlay (optional)")
	empPath := flag.String("empirical", "docs/command_notes_empirical.json",
		"bench-verified overlay; takes precedence over -notes (optional)")
	flag.Parse()

	rom, err := romloader.LoadDir(*romdir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load ROM:", err)
		os.Exit(1)
	}

	// documented set (from embedded list).
	documented := map[string]bool{}
	for _, ln := range strings.Split(documentedTxt, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		documented[ln] = true
	}

	// behavior overlay (optional): deep-RE notes first, then bench-verified
	// empirical notes which override them (empirical > disassembly inference).
	notes := map[string]Note{}
	loadNotes := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		m := map[string]Note{}
		if err := json.Unmarshal(data, &m); err != nil {
			fmt.Fprintf(os.Stderr, "warning: bad notes json %s: %v\n", path, err)
			return
		}
		for k, v := range m {
			notes[k] = v
		}
		fmt.Printf("merged %d behavior notes from %s\n", len(m), path)
	}
	loadNotes(*notesPath)
	loadNotes(*empPath) // precedence: overrides deep-RE for bench-verified commands

	// Decode the parser table.
	seen := map[string]bool{}
	var cmds []Command
	off := uint32(parserStart)
	for off < parserEnd-8 {
		tag := rom[off]
		if tag != 0x30 && tag != 0x40 && tag != 0x10 && tag != 0x20 {
			off++
			continue
		}
		ns := off + 2
		ne := ns
		for ne < ns+14 && ne < uint32(len(rom)) && isNameByte(rom[ne]) {
			ne++
		}
		name := strings.TrimRight(string(rom[ns:ne]), " ")
		if !validName(name) {
			off++
			continue
		}
		argType, slot, ok := decodeHandler(rom, ne)
		if !ok {
			off++
			continue
		}
		if seen[name] {
			off = ne + 1
			continue
		}
		seen[name] = true
		pc := resolveSlot(rom, slot)
		kind, dlpSrc, stubConst := classify(rom, pc)

		status := "undocumented"
		if strings.HasPrefix(name, "__") {
			status = "dlp-internal"
		} else if documented[name] {
			status = "documented"
		}
		cat := category(name)
		c := Command{
			Name: name, Tag: fmt.Sprintf("0x%02X", tag), ArgType: fmt.Sprintf("0x%02X", argType),
			Slot: slot, HandlerPC: pc, TableOffset: off, Status: status, Kind: kind,
			Category: cat, DLPSource: dlpSrc, StubConst: stubConst,
		}
		if nt, ok := notes[name]; ok {
			c.Behavior = nt.Behavior
			c.Verified = nt.Verified
			if nt.Category != "" {
				c.Category = nt.Category
			}
		}
		cmds = append(cmds, c)
		off = ne + 1
	}

	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })

	if err := os.MkdirAll(*outdir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	writeJSON(filepath.Join(*outdir, "command_reference.json"), cmds)
	writeMarkdown(filepath.Join(*outdir, "COMMAND_REFERENCE.md"), cmds, len(notes))

	// Summary.
	var nDoc, nUndoc, nDlp, nTramp, nStub, nNative, nNotes int
	for _, c := range cmds {
		switch c.Status {
		case "documented":
			nDoc++
		case "undocumented":
			nUndoc++
		case "dlp-internal":
			nDlp++
		}
		switch c.Kind {
		case "trampoline":
			nTramp++
		case "stub":
			nStub++
		case "native":
			nNative++
		}
		if c.Behavior != "" {
			nNotes++
		}
	}
	fmt.Printf("%d commands: %d documented, %d undocumented, %d dlp-internal\n", len(cmds), nDoc, nUndoc, nDlp)
	fmt.Printf("handlers: %d trampoline, %d stub, %d native\n", nTramp, nStub, nNative)
	fmt.Printf("behavior notes present: %d/%d\n", nNotes, len(cmds))
	fmt.Printf("wrote %s/COMMAND_REFERENCE.md + command_reference.json\n", *outdir)
}

func writeJSON(path string, cmds []Command) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cmds); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func mdEsc(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	// Collapse any control characters (newlines, stray bytes) to spaces so a
	// note can never break the Markdown table row.
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return ' '
		}
		return r
	}, s)
	return s
}

func writeMarkdown(path string, cmds []Command, nNotes int) {
	var b strings.Builder
	fmt.Fprintf(&b, "# HP 859x GPIB/DLP command reference (firmware-extracted)\n\n")
	fmt.Fprintf(&b, "Auto-generated by `cmd/cmdref` from the Rev L ROM parser table "+
		"(`0x7C800..0x80000`). **%d commands.** Regenerate with `go run ./cmd/cmdref`.\n\n", len(cmds))
	fmt.Fprintf(&b, "- **Status**: `documented` (in the HP 8590 E-series Programmer's Guide), "+
		"`undocumented` (service/commissioning — not in the guide), `dlp-internal` "+
		"(`__`-prefixed DLP runtime trampolines).\n")
	fmt.Fprintf(&b, "- **Handler kind**: `trampoline` (runs a DLP source macro via the "+
		"scheduler at `0x0D18`), `stub` (returns a constant), `native` (compiled C/asm handler).\n")
	fmt.Fprintf(&b, "- **Handler PC** is the resolved dispatch target in the 1 MB image; "+
		"disassemble with `./bin/disasm <pc> <pc+N>`.\n")
	fmt.Fprintf(&b, "- **Behavior** column is the deep-RE overlay from `docs/command_notes.json` "+
		"(%d/%d filled).\n\n", nNotes, len(cmds))

	// Category index.
	byCat := map[string][]string{}
	for _, c := range cmds {
		byCat[c.Category] = append(byCat[c.Category], c.Name)
	}
	var cats []string
	for k := range byCat {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	fmt.Fprintf(&b, "## Categories\n\n")
	for _, k := range cats {
		fmt.Fprintf(&b, "- **%s** (%d): %s\n", k, len(byCat[k]), strings.Join(byCat[k], " "))
	}

	fmt.Fprintf(&b, "\n## All commands\n\n")
	fmt.Fprintf(&b, "| Command | Status | Cat | Kind | Handler PC | Slot | Arg | Behavior / DLP source |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---|---|---|\n")
	for _, c := range cmds {
		detail := c.Behavior
		if detail == "" {
			if c.DLPSource != "" {
				detail = "DLP src: `" + mdEsc(trunc(c.DLPSource, 60)) + "`"
			} else if c.StubConst != "" {
				detail = "returns " + c.StubConst
			}
		} else {
			detail = mdEsc(trunc(detail, 120))
			if c.Verified {
				detail = "✓ " + detail
			}
		}
		pc := "—"
		if c.HandlerPC != 0 {
			pc = fmt.Sprintf("0x%06X", c.HandlerPC)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | 0x%04X | %s | %s |\n",
			c.Name, c.Status, c.Category, c.Kind, pc, c.Slot, c.ArgType, detail)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

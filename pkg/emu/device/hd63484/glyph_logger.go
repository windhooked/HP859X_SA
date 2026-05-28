package hd63484

import (
	"fmt"
	"os"
	"sync"
)

// GlyphLogger appends a one-line record per blitted glyph to a file. Each
// line is the printable ASCII character recovered from the row bitmap (via
// a small font table built from the firmware's actual glyphs), or
// "?[HHHH]" where HHHH is a 16-bit FNV-1a hash of the row tuple when no
// match is found. The position (penX, penY) is logged too so a transcript
// can be reassembled into screen rows.
//
// Enabled via the HD63484_GLYPHLOG environment variable: set it to a
// writable file path. Unset (or empty) ⇒ no logger is attached and there
// is zero per-glyph overhead.
//
// The format is one record per line:
//
//	GLYPH x=NNN y=NNN char=X
//
// where X is either a single printable ASCII character or "?[HHHH]". A
// fresh logger overwrites any prior file (it opens with O_TRUNC); within
// a single run the file is append-only.
type GlyphLogger struct {
	mu sync.Mutex
	f  *os.File
}

// newGlyphLoggerFromEnv returns a logger backed by the file named in
// HD63484_GLYPHLOG, or nil if the env var is empty / unset / unopenable.
// We log file-open failures to stderr once and continue without a logger
// so a misconfigured env doesn't break headless runs.
func newGlyphLoggerFromEnv() *GlyphLogger {
	path := os.Getenv("HD63484_GLYPHLOG")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hd63484: cannot open HD63484_GLYPHLOG=%q: %v\n", path, err)
		return nil
	}
	return &GlyphLogger{f: f}
}

// Record writes one line for the glyph at (x, y) with the given row
// bitmap. Safe for concurrent callers (the chip itself is single-threaded
// but tests / probes may invoke Record from multiple goroutines).
func (g *GlyphLogger) Record(x, y int, rows [glyphRows]uint16) {
	if g == nil || g.f == nil {
		return
	}
	ch := decodeGlyph(rows)
	g.mu.Lock()
	fmt.Fprintf(g.f, "GLYPH x=%d y=%d char=%s rows=%04X,%04X,%04X,%04X,%04X,%04X,%04X,%04X\n",
		x, y, ch, rows[0], rows[1], rows[2], rows[3], rows[4], rows[5], rows[6], rows[7])
	g.mu.Unlock()
}

// RecordColours captures the FG / BG colour selector words from a glyph
// packet. The chip records these per-glyph so the logger can include them
// in the next Record() call; useful for diagnosing why a glyph rendered
// dark or with unexpected colour. The chip calls this from feedGlyph.
func (g *GlyphLogger) RecordColours(fg, bg uint16) {
	if g == nil || g.f == nil {
		return
	}
	g.mu.Lock()
	fmt.Fprintf(g.f, "COLOUR fg=%04X bg=%04X\n", fg, bg)
	g.mu.Unlock()
}

// Close flushes and closes the underlying file. Idempotent.
func (g *GlyphLogger) Close() error {
	if g == nil || g.f == nil {
		return nil
	}
	err := g.f.Close()
	g.f = nil
	return err
}

// decodeGlyph maps a row-tuple to either a printable ASCII string or
// "?[HHHH]" where HHHH is a 16-bit FNV-1a hash of the tuple. Stripping the
// graticule background dots (bits at column positions 1 and 5 within each
// 8-pixel block, i.e. mask 0x22) lets the table store *pure-character*
// bitmaps that aren't polluted by the dotted background the firmware
// blits underneath.
func decodeGlyph(rows [glyphRows]uint16) string {
	clean := stripGraticule(rows)
	if ch, ok := glyphFont[clean]; ok {
		return ch
	}
	// Also try the raw rows in case the glyph is genuinely "background-on"
	// (some annunciator highlights blit dotted text).
	if ch, ok := glyphFont[rows]; ok {
		return ch
	}
	return fmt.Sprintf("?[%04X]", fnv16(clean))
}

// stripGraticule masks off the graticule background-dot pattern (bits at
// column positions 1 and 5 within each 8-pixel run = 0x22 per byte =
// 0x2222 per 16-bit word). The firmware paints the dotted background as
// part of its VRAM raster fills; glyphs are then ORed on top, so isolated
// glyph rows have the background dots mixed in.
func stripGraticule(rows [glyphRows]uint16) [glyphRows]uint16 {
	const mask uint16 = ^uint16(0x2222)
	var out [glyphRows]uint16
	for i, r := range rows {
		out[i] = r & mask
	}
	return out
}

// fnv16 returns a 16-bit FNV-1a hash of the 8 row words. Folds the
// standard 32-bit FNV result into 16 bits via XOR.
func fnv16(rows [glyphRows]uint16) uint16 {
	const offset uint32 = 2166136261
	const prime uint32 = 16777619
	h := offset
	for _, r := range rows {
		h ^= uint32(r >> 8)
		h *= prime
		h ^= uint32(r & 0xFF)
		h *= prime
	}
	return uint16(h>>16) ^ uint16(h&0xFFFF)
}

// glyphFont maps cleaned glyph row-tuples to their ASCII characters. Built
// incrementally from observed firmware glyphs (cmd/displayprobe + manual
// decoding of the boot-screen status bar — see screens/glyphs.log produced
// by HD63484_GLYPHLOG runs). Entries are 8-row, MSB-leftmost, with the
// graticule background (mask 0x2222) already removed.
//
// To add new entries: run with HD63484_GLYPHLOG=screens/glyphs.log, look
// at the "?[HHHH]" hashes in the log file, and inspect the rendered
// screen to identify which characters those hashes correspond to. Then
// the bitmaps can be retrieved by feeding the same firmware sequence
// through a probe that dumps the raw rows.
var glyphFont = map[[glyphRows]uint16]string{
	// Empty cell.
	{0, 0, 0, 0, 0, 0, 0, 0}: " ",
}

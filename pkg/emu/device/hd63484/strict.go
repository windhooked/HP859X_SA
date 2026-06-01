package hd63484

import "fmt"

// Faithfulness contract for the HD63484 driver
// ─────────────────────────────────────────────
// The display driver must NOT silently ignore any command or register access.
// Anything it does not faithfully model reaches unimplementedf and PANICS
// deterministically (same input → same panic message), so unmodelled behaviour
// is surfaced immediately instead of being hidden behind a stub.
//
// The only escape is the EXPLICIT ALLOWLIST below: accesses we genuinely cannot
// model faithfully — hardware outside the chip we don't simulate, or behaviours
// with no observable effect on the 8593's 1-bit monochrome display — are listed
// here with a justification and handled as a documented, deterministic stub.
// Adding to the allowlist is a deliberate, reviewable decision; it is never a
// silent drop. Everything not implemented and not allowlisted panics.
//
// Keys are stable identifiers: "cmd:<MNEMONIC>" for command opcodes,
// "ar:<0xNN>" for Address-Register control registers, "wpr:<0xNN>" for
// parameter registers, "mmio:<0xNNN>" for MMIO display ports.

// allowedUnmodeled maps an access key → the justification for permitting it as a
// deterministic stub instead of panicking. Reviewed; grows only by explicit decision.
// probeNoPanic, when true, downgrades the command-position faithfulness panic
// to a tallied record so a probe run can capture the full stream. TEMP.
var probeNoPanic = false

var allowedUnmodeled = map[string]string{
	// ── AR control registers (store-only value latches) ───────────────────────
	// The 8593 drives a 1-bit monochrome CRT; our drawing engine renders a single
	// 1bpp foreground plane and derives geometry from MWR1/VWW. The registers
	// below are latched in ctrlRegs (so reads are faithful) but their side-effects
	// have no observable effect on that monochrome output, so they are not acted on.
	"ar:0x02": "CCR operation/control: graphic-bit-mode (bpp) + logical-operation fields. We render a fixed 1bpp foreground plane; bit-depth/raster-op have no observable effect on the monochrome 8593 output.",
	"ar:0x04": "OMR operation mode: display-enable/sync source. We unconditionally render the framebuffer; the enable/sync-source bits don't change the pixels.",
	"ar:0x06": "DCR display control: layer/split configuration. The 8593 uses a single base layer (DCR=0xC000); split-screen/overlay layering is not used.",
	"ar:0x82": "HSR horizontal sync: H-sync pulse width / timing. Pure CRT raster timing; our render samples by row/col and is timing-independent.",
	"ar:0x84": "HDR horizontal display: H front/back porch timing. CRT raster timing only; no effect on sampled pixels.",
	"ar:0x86": "VSR vertical sync: V-sync timing. CRT raster timing only; no effect on sampled pixels.",
	"ar:0x88": "VDR vertical display: V porch timing. CRT raster timing only; no effect on sampled pixels.",
	"ar:0x8a": "Vertical display-line count register (=0x0100=256 displayed lines). Display height is derived from VWW/MWR1 in render.go; latched here for read-back fidelity.",
	"ar:0x92": "HWR horizontal window: split-screen H window. Single-layer display; windowing not used.",
	"ar:0x94": "VWR-A vertical window (split layer A start). Single-layer display; split windows not used.",
	"ar:0x96": "VWR-B vertical window width (VWW=displayed lines). Geometry is derived directly from MWR1/VWW in render.go; latched here for read-back fidelity.",
	"ar:0x98": "GCR0 graphic cursor: cursor X. The 8593 firmware draws its own UI; the hardware graphic cursor is not displayed.",
	"ar:0x9a": "GCR1 graphic cursor: cursor Y. Hardware graphic cursor not displayed.",
	"ar:0x9c": "GCR2 graphic cursor: cursor definition. Hardware graphic cursor not displayed.",
	"ar:0xe0": "BCUR1 block-cursor start. Hardware block cursor not displayed on the 8593 UI.",
	"ar:0xe2": "BCUR2 block-cursor end. Hardware block cursor not displayed.",
	"ar:0xe8": "CDR cursor definition register. Hardware cursor not displayed.",
	"ar:0xea": "ZFR zoom factor (=0, no zoom). Our render applies no hardware zoom; the firmware always programs ZFR=0.",

	// ── Recognised-but-stubbed commands (framed, side-effect not modelled) ─────
	"cmd:0xd000": "Per-glyph attribute/terminator command (1 arg), emitted after every text glyph (docs/research.md §7 trailer 0805 0000 D000 0907). The argument tracks glyph content but has no observable effect on the 1bpp monochrome render. Framed (1 arg consumed) for stream sync; effect not modelled.",
	"cmd:apll":  "APLL absolute polyline (count-prefixed: 1 count word + 2N coords). Framed correctly so the stream stays in sync, but the vertices are NOT rendered: they are ORG-origin-relative point-runs (fine detail / dotted grid) and drawing them without applying the ORG origin produces spurious segments. Render deferred to the ORG-relative-coordinate task.",
	"cmd:rpll":  "RPLL relative polyline. Same framing + deferral as cmd:apll.",
	"cmd:sclr-area": "SCLR-area (0x5C00|logical-op; 3 args: pattern, ΔX, ΔY). The pattern is a 50%-dither (0x5555/0xAAAA): this is a PATTERNED FILL (background dither / dotted gridlines), not a clear-to-dark — applying it as a region clear erases the trailing characters of labels (verified). Framed (3 args consumed) for stream sync; faithful dither rendering into the dim background plane is a separate task.",
	"cmd:rpr":   "RPR read-parameter-register (0 args; the firmware reads back registers it wrote, e.g. WPR5). The chip read-FIFO is not modelled, so the read returns via the data-port stub (ReadData). The boot's visible render does not depend on the read-back value; framed (no args) for stream sync.",
}

// allowed reports whether key is on the explicit allowlist (a permitted,
// documented stub). Callers that are allowed proceed with their deterministic
// stub behaviour; callers that are not must panic via unimplementedf.
func allowed(key string) bool {
	_, ok := allowedUnmodeled[key]
	return ok
}

// unimplementedf panics with a deterministic, fully-described message. This is
// the faithfulness gate: reaching it means the driver hit a command or register
// access it neither models nor allowlists. The panic is intentional — it must be
// resolved by faithfully implementing the access or adding it to allowedUnmodeled
// with a justification, never by re-silencing it.
func (c *Chip) unimplementedf(format string, args ...interface{}) {
	panic("hd63484 faithfulness gate — unimplemented: " + fmt.Sprintf(format, args...))
}

// gate is the common path for a recognised-but-not-faithfully-modelled access:
// if key is allowlisted it returns true (proceed with the documented stub),
// otherwise it panics with the descriptive message.
func (c *Chip) gate(key, format string, args ...interface{}) bool {
	if allowed(key) {
		return true
	}
	c.unimplementedf(format, args...)
	return false
}

// writeControlReg decodes an AR-addressed control-register write (AR≠0). The
// registers whose effect we model (the display-address set, AR 0xC8..0xCE) have
// explicit handlers; every other control register is store-only and must be on
// the strict allowlist (ar:0xNN) with a justification — otherwise it panics, so
// no control-register access is ever silently mis-parsed as a draw command.
func (c *Chip) writeControlReg(ar, value uint16) {
	c.ctrlRegs[ar&0xFF] = value
	switch ar {
	case 0xC8: // RAR1 — base raster/read address (display start; page-flip)
		c.dispRAR = value
	case 0xCA: // MWR1 — base memory width (words per raster line)
		c.dispMWR = value
	case 0xCC: // SAR1 high — base start address
		c.dispSARHi = value
	case 0xCE: // SAR1 low
		c.dispSARLo = value
	default:
		// Store-only control register: faithful as a value latch, but its
		// side-effect (if any) is not modelled. Permitted only if explicitly
		// allowlisted with a reason; otherwise panic.
		c.gate(fmt.Sprintf("ar:0x%02x", ar),
			"control-register write AR=%#04x value=%#04x (side-effect not modelled)", ar, value)
	}
}

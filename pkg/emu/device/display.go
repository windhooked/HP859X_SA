package device

import "github.com/windhooked/HP859X_SA/pkg/emu/device/hd63484"

// SCIDisplay is the 8593-side wrapper around an HD63484 ACRTC chip model.
// It preserves the existing client API (WriteCmd / WriteData / RenderFrame
// + the diagnostic counter fields Moves/Lines/Rects/Dots/Glyphs/Paints/
// PaintWords/DataWords) via Go's embedded-type field/method promotion, so
// HP8593AMMIO and the rest of the codebase don't need to change. The
// legacy name "SCI" is retained for historical reasons — the protocol
// carried by the data port at 0xFFF5FE IS the HD63484 command set, but
// the name "SCI" stuck from initial RE before we confirmed the chip
// identity. See pkg/emu/device/hd63484/doc.go for the chip's architecture
// and command set.
//
// Direct chip access (parameter registers, pattern RAM, video RAM): the
// embedded *hd63484.Chip is exposed as `d.Chip` (named the same as the
// embedded type), so callers can reach the underlying state with
// `d.Chip.VRAM()`, `d.Chip.PenX()`, etc.
type SCIDisplay struct {
	*hd63484.Chip
}

// Display geometry — re-exported for backwards compatibility with any
// caller that referenced device.DisplayWidth / device.DisplayHeight.
const (
	DisplayWidth  = hd63484.DisplayWidth
	DisplayHeight = hd63484.DisplayHeight
)

// NewSCIDisplay returns a display with a cleared (opaque-black) framebuffer.
func NewSCIDisplay() *SCIDisplay {
	return &SCIDisplay{Chip: hd63484.New()}
}

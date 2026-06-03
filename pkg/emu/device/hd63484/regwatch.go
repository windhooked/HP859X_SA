package hd63484

import (
	"fmt"
	"os"
)

// regwatch.go makes the controller LOUD about control registers we don't fully
// model, so unmodeled display/overlay configuration can't be silently ignored.
//
// Enable via environment (both off by default — zero overhead otherwise):
//   HD63484_REGDUMP=<path>   append every not-fully-modeled AR write to <path>
//   HD63484_REGPANIC=1       panic on the first not-fully-modeled AR write
//
// "Modeled" here means the value is actually CONSUMED by our emulation:
//   "addr"   — MWR/SAR: consumed by calcOffset / the flat-address core.        (modeled)
//   "config" — display/split/window/screen-enable: STORED but NOT yet applied
//              to the rendered output (the scanout doesn't composite screens).  (NOT modeled)
//   "?"      — unrecognised AR. (NOT modeled)
// regWatch reports "config" and "?" writes; "addr" writes are suppressed.

// regInfo returns a human name + modeling status for an AR control register.
func regInfo(ar uint16) (name, status string) {
	switch ar {
	case 0x02:
		return "CCR (command/IRQ ctrl)", "config"
	case 0x04:
		return "OMR (op-mode / superimpose)", "config"
	case 0x06:
		return "DCR (screen enables / attrs)", "config"
	case 0x82:
		return "HSR/HC (h sync / cycle)", "config"
	case 0x84:
		return "HDR (h display start/width)", "config"
	case 0x86:
		return "VSR/VC (v sync / cycle)", "config"
	case 0x88:
		return "VDR (v display start/width)", "config"
	case 0x8A:
		return "SP1 split-width (Base)", "config"
	case 0x8C:
		return "SP0 split-width (Upper)", "config"
	case 0x8E:
		return "SP2 split-width (Lower)", "config"
	case 0x92, 0x94, 0x96, 0x98:
		return "window display reg", "config"
	case 0x9A, 0x9C:
		return "zoom reg", "config"
	case 0xC0, 0xC8, 0xD0, 0xD8:
		return "RARn (raster addr)", "config"
	case 0xC2, 0xCA, 0xD2, 0xDA:
		return "MWRn (memory width)", "addr"
	case 0xC4, 0xC6, 0xCC, 0xCE, 0xD4, 0xD6, 0xDC, 0xDE:
		return "SARn (screen start addr)", "config" // stored, but the scanout doesn't use it yet
	}
	return fmt.Sprintf("AR_0x%02x (unrecognised)", ar), "?"
}

// regWatch reports a control-register write that our emulation doesn't fully
// model — appending to the dump file and/or panicking, per the env switches read
// in New(). No-op (one branch) when neither is enabled.
func (c *Chip) regWatch(ar, value uint16) {
	if c.regDump == nil && !c.regPanic {
		return
	}
	name, status := regInfo(ar)
	if status == "addr" {
		return // consumed by addressing — modeled
	}
	line := fmt.Sprintf("AR=0x%02x  %-30s = 0x%04x  [NOT MODELED: %s]\n", ar, name, value, status)
	if c.regPanic {
		panic("hd63484: write to unmodeled register — " + line)
	}
	if c.regDump != nil {
		_, _ = c.regDump.WriteString(line)
	}
}

// initRegWatch wires up the env-gated register watch (called from New()).
func (c *Chip) initRegWatch() {
	if p := os.Getenv("HD63484_REGDUMP"); p != "" {
		if f, err := os.Create(p); err == nil {
			c.regDump = f
		}
	}
	c.regPanic = os.Getenv("HD63484_REGPANIC") != ""
}

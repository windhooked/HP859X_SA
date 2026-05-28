package device

import "github.com/windhooked/HP859X_SA/pkg/emu/bus"

// ───────────────────────────────────────────────────────────────────────────
// CalNVRAM — model of the A16A1 calibration NVRAM at 0x200000–0x20FFFF.
//
// Decoded from the U114 22V10 memory-decode PAL (`HP80159.PLD` in
// hp8593a_eeproms/PAL_8590-80159.zip): the `LCAL` chip-select asserts when
// `/MA23 * MA21 * /MA20`, i.e. address range 0x200000–0x2FFFFF. The actual SRAM
// chip behind that select is 64 KB; the firmware does a 64 KB linear read scan
// at boot (cmd/hwprobe confirmed) to load cal constants, DLPs, model/serial,
// flatness corrections, etc.
//
// On a real instrument this SRAM is battery-backed (A16A1BT1 lithium cell). If
// the battery is dead or the unit is fresh, the NVRAM reads blank (0x00) — the
// firmware sees no valid cal data and falls back to defaults, displaying the
// "CAL: USING DEFAULT DATA" annunciator. This is also our default state: a
// freshly-constructed CalNVRAM is all zero, behaving like a blank chip, which
// preserves the existing clean-status-screen boot.
//
// Rev L cal-NVRAM access pattern (measured via cmd/caltrace, faithful boot,
// 100M cycles): the firmware reads ALL 65536 bytes exactly once (the
// byte-checksum sweep at ROM 0x454A) and reads offset 0 three more times as
// a longword for the CPU integrity test at ROM 0x44AA–0x44B8 (`move.l ($200000).l,
// D6; move.l D6, ($200000).l; cmp.l ($200000).l, D6`). Offset 0 is also the
// only byte ever WRITTEN during boot (the integrity test's write-back).
// NO other offset is polled or compared against a constant — Rev L's
// "valid cal" condition is checksum-based, not magic-byte-based.
//
// This means `CalNVRAM.Synthesize()` cannot meaningfully advance the boot
// under Rev L the way it did under 17.12.90 (where 0x200A3C was tested
// against a small integer at multiple ROM sites). Cal-data values matter
// only AFTER boot — when the user runs frequency sweeps, switches modes,
// reads correction tables, etc.
//
// Legacy 17.12.90 offsets (kept here as historical reference; do not
// reuse without verifying against Rev L's docs/rom.asm):
//   0x0A3C — 17.12.90 staged model/sweep-gate selector. NO Rev L analogue.
//   0x0013 — 17.12.90 IRQ-mode bit-test. NO Rev L analogue confirmed.
// ───────────────────────────────────────────────────────────────────────────

const (
	CalNVRAMBase = uint32(0x200000)
	CalNVRAMSize = uint32(0x10000) // 64 KB
)

// Known cal NVRAM byte offsets, named so call sites are readable.
const (
	CalSweepGate = 0x0A3C // sweep-gate cal byte (cmpi.w #1, $200a3c.l at 0xF768)
	CalIRQMode   = 0x0013 // bit 4 polled by IRQ handlers (btst #4, $200013.l)
)

// CalNVRAM models the A16A1 battery-backed calibration SRAM at 0x200000.
type CalNVRAM struct {
	b     [CalNVRAMSize]byte
	Trace TraceFunc // if non-nil, called on every Read/Write — see TraceFunc
}

// NewCalNVRAM returns an all-zero ("blank battery") NVRAM. The firmware will
// treat this as no cal data and fall back to defaults — equivalent to the
// behaviour when the region was previously unmapped (OnFault → 0).
func NewCalNVRAM() *CalNVRAM { return &CalNVRAM{} }

// Synthesize is currently a no-op under the Rev L firmware: cmd/caltrace shows
// the boot reads every cal byte once (checksum sweep) and only re-touches
// offset 0 (CPU integrity test). No polled "gate" byte exists in Rev L, so
// writing values to specific offsets has no observable effect on the boot
// progression. The function is preserved (rather than removed) because:
//
//   - post-boot code paths (frequency sweeps, mode switches, correction-
//     table lookups) DO consume cal bytes — synthesised cal data will
//     matter there once we model those paths;
//   - the API + unit-test surface stays stable for future use.
//
// The legacy 17.12.90 implementation set 0x0A3C=5 to pass the staged
// sweep-gate test. That offset has no Rev L analogue — see the package
// comment for the measured access pattern.
func (n *CalNVRAM) Synthesize() {
	// Intentionally empty. Add Rev L cal-byte initialisation as post-boot
	// consumers are RE'd (e.g. when the sweep / frequency-step path needs
	// per-band correction values).
}

// LoadImage replaces the NVRAM contents with the given image. Image must be no
// larger than CalNVRAMSize; shorter images are zero-padded at the tail.
func (n *CalNVRAM) LoadImage(img []byte) {
	for i := range n.b {
		n.b[i] = 0
	}
	if len(img) > len(n.b) {
		img = img[:len(n.b)]
	}
	copy(n.b[:], img)
}

// SetByte / SetWord / SetLong configure individual offsets. Useful for building
// a synthesised cal image incrementally as more offsets are reverse-engineered.
func (n *CalNVRAM) SetByte(off uint32, v byte) { n.b[off] = v }
func (n *CalNVRAM) SetWord(off uint32, v uint16) {
	n.b[off] = byte(v >> 8)
	n.b[off+1] = byte(v)
}
func (n *CalNVRAM) SetLong(off uint32, v uint32) {
	n.b[off] = byte(v >> 24)
	n.b[off+1] = byte(v >> 16)
	n.b[off+2] = byte(v >> 8)
	n.b[off+3] = byte(v)
}

// Image returns a copy of the NVRAM contents (e.g. for inspection / golden).
func (n *CalNVRAM) Image() []byte {
	out := make([]byte, len(n.b))
	copy(out, n.b[:])
	return out
}

// TraceAccess, if set non-nil, is invoked on every Read and Write — used by
// cmd/caltrace to reverse-engineer which cal offsets the firmware touches.
// `write` is true for stores, false for loads; `val` is the value read or
// written. Setting this on a hot device adds per-op overhead, so leave it nil
// outside of dedicated tracing runs.
type TraceFunc func(off uint32, sz bus.Size, val uint32, write bool)

func (n *CalNVRAM) Read(addr uint32, sz bus.Size) uint32 {
	if int(addr)+int(sz) > len(n.b) {
		return 0
	}
	v := beRead(n.b[:], addr, sz)
	if n.Trace != nil {
		n.Trace(addr, sz, v, false)
	}
	return v
}

func (n *CalNVRAM) Write(addr uint32, sz bus.Size, val uint32) {
	if int(addr)+int(sz) > len(n.b) {
		return
	}
	beWrite(n.b[:], addr, sz, val)
	if n.Trace != nil {
		n.Trace(addr, sz, val, true)
	}
}

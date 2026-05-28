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

// SynthesizeRevL builds a minimal-valid Rev L calibration NVRAM image.
//
// The Rev L startup checksum (ROM 0x454A) sweeps all 65536 bytes of the NVRAM
// with two byte accumulators (D2 for even-indexed bytes, D3 for odd-indexed),
// both initialised to 0xFF. After adding every byte, the loop tests:
//
//	tst.b D2 ; sne D2  →  D2 = 0xFF if nonzero (fail), 0x00 if zero (pass)
//
// A checksum PASSES when the accumulator is 0x00, i.e.:
//
//	0xFF + Σ(even-indexed bytes) ≡ 0 (mod 256)  → Σ(even) ≡ 1 (mod 256)
//	0xFF + Σ(odd-indexed  bytes) ≡ 0 (mod 256)  → Σ(odd)  ≡ 1 (mod 256)
//
// Failures set bits 4 (even) and 5 (odd) of D1, which are then ORed into the
// diagnostic word at RAM[0xFFBB2C]. Firmware uses 0xFFBB2C to display the
// "CAL: USING DEFAULT DATA" / "USING DEFAULTS" status annunciators.
//
// Setting byte[0]=0x01 satisfies the even constraint; byte[1]=0x01 satisfies
// odd. All other bytes remain 0x00 (which the firmware treats as "no cal
// constants loaded" and substitutes ROM defaults — correct for an emulator
// that has not yet RE'd the full per-band correction table layout).
func (n *CalNVRAM) SynthesizeRevL() {
	// Clear then install checksum anchor bytes.
	for i := range n.b {
		n.b[i] = 0
	}
	// Even-byte checksum: Σ(positions 0,2,4,…) = 0x01.
	n.b[0] = 0x01
	// Odd-byte checksum: Σ(positions 1,3,5,…) = 0x01.
	n.b[1] = 0x01
}

// Synthesize calls SynthesizeRevL to build a minimal-valid cal image.
// This makes the Rev L startup checksum pass so the firmware clears the
// "CAL: USING DEFAULTS" annunciator and marks calibration as valid in
// RAM[0xFFBB2C]. Real correction-table values are left at zero; the
// firmware substitutes ROM defaults for any zero entries it encounters.
func (n *CalNVRAM) Synthesize() {
	n.SynthesizeRevL()
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

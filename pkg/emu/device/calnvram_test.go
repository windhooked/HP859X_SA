package device

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestCalNVRAMBlank verifies a freshly-constructed CalNVRAM reads 0 everywhere
// — the "dead battery" / blank-chip state that maps to "no cal data" for the
// firmware, matching the previous OnFault→0 behaviour for that region.
func TestCalNVRAMBlank(t *testing.T) {
	n := NewCalNVRAM()
	for _, off := range []uint32{0, CalSweepGate, CalIRQMode, CalNVRAMSize - 4} {
		if v := n.Read(off, bus.Long); v != 0 {
			t.Errorf("blank NVRAM at %#06X = %#08X, want 0", off, v)
		}
	}
}

// TestCalNVRAMReadWriteRoundtrip exercises byte/word/long round-trips via the
// bus interface — the path the firmware actually uses.
func TestCalNVRAMReadWriteRoundtrip(t *testing.T) {
	n := NewCalNVRAM()
	cases := []struct {
		off uint32
		sz  bus.Size
		val uint32
	}{
		{0x100, bus.Byte, 0xA5},
		{0x200, bus.Word, 0x1234},
		{0x300, bus.Long, 0xCAFEBABE},
	}
	for _, c := range cases {
		n.Write(c.off, c.sz, c.val)
		if got := n.Read(c.off, c.sz); got != c.val {
			t.Errorf("%d-byte write@%#06X=%#X read back %#X", c.sz, c.off, c.val, got)
		}
	}
}

// TestCalNVRAMSynthesizeIsNoOpForBlank verifies Synthesize() leaves a blank
// NVRAM blank under Rev L. cmd/caltrace established that no boot-time gate
// byte exists in Rev L (the firmware only reads offset 0 multiple times for
// the CPU integrity test); Synthesize() is intentionally a no-op until
// post-boot cal consumers are RE'd. This test pins that contract so future
// changes that re-introduce boot-time writes are flagged explicitly.
func TestCalNVRAMSynthesizeIsNoOpForBlank(t *testing.T) {
	n := NewCalNVRAM()
	n.Synthesize()
	img := n.Image()
	for i, b := range img {
		if b != 0 {
			t.Fatalf("Synthesize() wrote %#02X at offset %#06X — Rev L boot has no "+
				"gate-byte to set; updating Synthesize requires updating the package "+
				"comment + cmd/caltrace findings", b, i)
		}
	}
}

// TestCalNVRAMLoadImage verifies LoadImage replaces contents and zero-pads.
func TestCalNVRAMLoadImage(t *testing.T) {
	n := NewCalNVRAM()
	n.SetByte(0xFFFF, 0xAA) // sentinel that must be cleared
	img := []byte{0x11, 0x22, 0x33, 0x44}
	n.LoadImage(img)
	if got := n.Read(0, bus.Long); got != 0x11223344 {
		t.Errorf("after LoadImage, read@0 = %#X, want 0x11223344", got)
	}
	if got := n.Read(0xFFFC, bus.Long); got != 0 {
		t.Errorf("after LoadImage, tail not zero-padded: read@FFFC = %#X", got)
	}
}

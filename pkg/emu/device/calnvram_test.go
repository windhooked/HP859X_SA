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

// TestCalNVRAMSynthesizeRevL verifies SynthesizeRevL() produces an image whose
// checksums satisfy the Rev L startup check at ROM 0x454A:
//
//	Σ(even-indexed bytes) ≡ 1 (mod 256)   [D2 init=0xFF → 0xFF+1=0x00 → pass]
//	Σ(odd-indexed  bytes) ≡ 1 (mod 256)   [D3 init=0xFF → 0xFF+1=0x00 → pass]
func TestCalNVRAMSynthesizeRevL(t *testing.T) {
	n := NewCalNVRAM()
	n.SynthesizeRevL()
	img := n.Image()
	if len(img) != int(CalNVRAMSize) {
		t.Fatalf("image length = %d, want %d", len(img), CalNVRAMSize)
	}

	var evenSum, oddSum uint32
	for i, b := range img {
		if i%2 == 0 {
			evenSum += uint32(b)
		} else {
			oddSum += uint32(b)
		}
	}
	if evenSum%256 != 1 {
		t.Errorf("even-byte sum %d mod 256 = %d, want 1 (D2 init=0xFF+sum=0x00 for pass)", evenSum, evenSum%256)
	}
	if oddSum%256 != 1 {
		t.Errorf("odd-byte sum %d mod 256 = %d, want 1 (D3 init=0xFF+sum=0x00 for pass)", oddSum, oddSum%256)
	}

	// Verify the anchor bytes are as documented.
	if img[0] != 0x01 {
		t.Errorf("img[0] = %#02X, want 0x01 (even-sum anchor)", img[0])
	}
	if img[1] != 0x01 {
		t.Errorf("img[1] = %#02X, want 0x01 (odd-sum anchor)", img[1])
	}
	// All other bytes must be zero (firmware uses ROM defaults for zero constants).
	for i := 2; i < len(img); i++ {
		if img[i] != 0 {
			t.Fatalf("img[%d] = %#02X, want 0x00 (non-anchor bytes must be zero)", i, img[i])
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

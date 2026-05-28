package bus

import "testing"

func TestRAMBigEndianAccess(t *testing.T) {
	var b Bus
	ram := NewRAM(0x100)
	b.Map(0x1000, 0x100, "ram", ram)

	b.Write(0x1000, Long, 0x11223344)

	cases := []struct {
		addr uint32
		sz   Size
		want uint32
	}{
		{0x1000, Long, 0x11223344},
		{0x1000, Word, 0x1122},
		{0x1000, Byte, 0x11}, // MSB at lowest address (big-endian)
		{0x1003, Byte, 0x44}, // LSB at highest address
		{0x1002, Word, 0x3344},
	}
	for _, c := range cases {
		if got := b.Read(c.addr, c.sz); got != c.want {
			t.Errorf("Read(%#X, %d) = %#X, want %#X", c.addr, c.sz, got, c.want)
		}
	}
}

func TestROMReadOnlyAndOnWrite(t *testing.T) {
	var b Bus
	rom := NewROM([]byte{0xDE, 0xAD, 0xBE, 0xEF})
	var wrote bool
	rom.OnWrite = func(addr uint32, sz Size, val uint32) { wrote = true }
	b.Map(0, 4, "rom", rom)

	if got := b.Read(0, Word); got != 0xDEAD {
		t.Errorf("rom word = %#X, want 0xDEAD", got)
	}
	b.Write(0, Byte, 0x00)
	if !wrote {
		t.Error("ROM write was not reported via OnWrite")
	}
	if got := b.Read(0, Byte); got != 0xDE {
		t.Errorf("ROM mutated by write: byte = %#X, want 0xDE", got)
	}
}

func TestUnmappedFaults(t *testing.T) {
	var b Bus
	b.Map(0, 0x100, "rom", NewROM(make([]byte, 0x100)))

	var (
		faults   int
		gotAddr  uint32
		gotWrite bool
	)
	b.OnFault = func(addr uint32, sz Size, write bool) uint32 {
		faults++
		gotAddr, gotWrite = addr, write
		return faultValue(sz) // preserve default all-ones behaviour for this test
	}

	if got := b.Read(0x500000, Long); got != 0xFFFFFFFF {
		t.Errorf("faulting read = %#X, want 0xFFFFFFFF", got)
	}
	if faults != 1 || gotAddr != 0x500000 || gotWrite {
		t.Errorf("read fault not reported correctly: faults=%d addr=%#X write=%v", faults, gotAddr, gotWrite)
	}

	b.Write(0x600000, Byte, 1)
	if faults != 2 || gotAddr != 0x600000 || !gotWrite {
		t.Errorf("write fault not reported correctly: faults=%d addr=%#X write=%v", faults, gotAddr, gotWrite)
	}
}

func TestAddrMaskedTo24Bit(t *testing.T) {
	var b Bus
	ram := NewRAM(0x10)
	b.Map(0xFF0000, 0x10, "ram", ram)
	// 0xABFF0000 masks to 0xFF0000.
	b.Write(0xABFF0000, Word, 0xCAFE)
	if got := b.Read(0xFF0000, Word); got != 0xCAFE {
		t.Errorf("masked write/read = %#X, want 0xCAFE", got)
	}
}

func TestOverlapPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on overlapping mapping")
		}
	}()
	var b Bus
	b.Map(0, 0x100, "a", NewRAM(0x100))
	b.Map(0x80, 0x100, "b", NewRAM(0x100)) // overlaps [0x80,0xFF]
}

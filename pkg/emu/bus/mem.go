package bus

// beRead reads a big-endian value of width sz from b at offset addr. Bytes
// past the end of b read as 0 — a multi-byte access that straddles the end of
// a device region (e.g. a word read at the device's last byte) is treated the
// same as an unmapped read for the overflow bytes, rather than panicking.
func beRead(b []byte, addr uint32, sz Size) uint32 {
	rd := func(i uint32) uint32 {
		if i < uint32(len(b)) {
			return uint32(b[i])
		}
		return 0
	}
	switch sz {
	case Byte:
		return rd(addr)
	case Word:
		return rd(addr)<<8 | rd(addr+1)
	default:
		return rd(addr)<<24 | rd(addr+1)<<16 | rd(addr+2)<<8 | rd(addr+3)
	}
}

// beWrite stores val as a big-endian value of width sz into b at offset addr.
// Bytes past the end of b are dropped (see beRead for the rationale).
func beWrite(b []byte, addr uint32, sz Size, val uint32) {
	wr := func(i uint32, v byte) {
		if i < uint32(len(b)) {
			b[i] = v
		}
	}
	switch sz {
	case Byte:
		wr(addr, byte(val))
	case Word:
		wr(addr, byte(val>>8))
		wr(addr+1, byte(val))
	default:
		wr(addr, byte(val>>24))
		wr(addr+1, byte(val>>16))
		wr(addr+2, byte(val>>8))
		wr(addr+3, byte(val))
	}
}

// RAM is a read/write byte-backed region.
type RAM struct{ b []byte }

// NewRAM returns zero-initialized RAM of the given byte size.
func NewRAM(size uint32) *RAM { return &RAM{b: make([]byte, size)} }

// Bytes exposes the backing store (e.g. for loading NVRAM contents or tests).
func (r *RAM) Bytes() []byte { return r.b }

func (r *RAM) Read(addr uint32, sz Size) uint32       { return beRead(r.b, addr, sz) }
func (r *RAM) Write(addr uint32, sz Size, val uint32) { beWrite(r.b, addr, sz, val) }

// ROM is a read-only byte-backed region. Writes are dropped, but reported via
// OnWrite when set (useful for catching firmware that writes to ROM by mistake,
// or for bank-select latches that alias the ROM window).
type ROM struct {
	b       []byte
	OnWrite func(addr uint32, sz Size, val uint32)
}

// NewROM returns a ROM backed by image (not copied).
func NewROM(image []byte) *ROM { return &ROM{b: image} }

func (r *ROM) Read(addr uint32, sz Size) uint32 { return beRead(r.b, addr, sz) }

func (r *ROM) Write(addr uint32, sz Size, val uint32) {
	if r.OnWrite != nil {
		r.OnWrite(addr, sz, val)
	}
}

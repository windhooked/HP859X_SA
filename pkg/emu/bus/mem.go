package bus

// beRead reads a big-endian value of width sz from b at offset addr.
func beRead(b []byte, addr uint32, sz Size) uint32 {
	switch sz {
	case Byte:
		return uint32(b[addr])
	case Word:
		return uint32(b[addr])<<8 | uint32(b[addr+1])
	default:
		return uint32(b[addr])<<24 | uint32(b[addr+1])<<16 |
			uint32(b[addr+2])<<8 | uint32(b[addr+3])
	}
}

// beWrite stores val as a big-endian value of width sz into b at offset addr.
func beWrite(b []byte, addr uint32, sz Size, val uint32) {
	switch sz {
	case Byte:
		b[addr] = byte(val)
	case Word:
		b[addr] = byte(val >> 8)
		b[addr+1] = byte(val)
	default:
		b[addr] = byte(val >> 24)
		b[addr+1] = byte(val >> 16)
		b[addr+2] = byte(val >> 8)
		b[addr+3] = byte(val)
	}
}

// RAM is a read/write byte-backed region.
type RAM struct{ b []byte }

// NewRAM returns zero-initialized RAM of the given byte size.
func NewRAM(size uint32) *RAM { return &RAM{b: make([]byte, size)} }

// Bytes exposes the backing store (e.g. for loading NVRAM contents or tests).
func (r *RAM) Bytes() []byte { return r.b }

func (r *RAM) Read(addr uint32, sz Size) uint32      { return beRead(r.b, addr, sz) }
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

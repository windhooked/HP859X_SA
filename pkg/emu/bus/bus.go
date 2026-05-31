// Package bus models the HP 8593A's 24-bit M68K address space as a set of
// devices mapped over fixed, non-overlapping address ranges. Every CPU core
// routes its memory accesses through Bus.Read/Write so that ROM, RAM, and
// memory-mapped peripherals all share one decode path.
//
// Multi-byte values are big-endian, matching the 68000.
package bus

import "fmt"

// Size is the width of a bus access in bytes.
type Size uint8

const (
	Byte Size = 1
	Word Size = 2
	Long Size = 4
)

// AddrMask is the 68000's 24-bit address bus; the upper byte is ignored.
const AddrMask = 0xFFFFFF

// Device is anything mapped into the address space. addr is an offset relative
// to the start of the device's mapped range, not an absolute bus address.
type Device interface {
	Read(addr uint32, sz Size) uint32
	Write(addr uint32, sz Size, val uint32)
}

type mapping struct {
	start, end uint32 // inclusive [start, end]
	name       string
	dev        Device
}

// Bus dispatches each access to the device mapped over the target address.
// The zero value is ready to use.
type Bus struct {
	maps []mapping
	// OnFault, if set, is invoked for accesses that hit no mapped device.
	// For reads the return value is used; for writes the return value is ignored.
	// When nil, faulting reads return all-ones (0xFF / 0xFFFF / 0xFFFFFFFF).
	OnFault func(addr uint32, sz Size, write bool) uint32

	// OnRead, if set, is invoked after every read (including CPU instruction
	// fetches and operand reads, which route through here via the Musashi
	// callbacks) with the address, size, and value. Diagnostic only — leave nil
	// in normal operation (it adds a per-read call). Used to trace which RAM
	// word the firmware tests before drawing each status annunciator.
	OnRead func(addr uint32, sz Size, val uint32)

	// OnWrite, if set, is invoked before every write with the address, size, and
	// value. Diagnostic only. Used to catch the firmware writing an annunciator
	// string pointer into its draw list (the status decision).
	OnWrite func(addr uint32, sz Size, val uint32)
}

// Map registers dev over [start, start+size). Panics on a zero size or on
// overlap with an existing mapping — both are wiring bugs.
func (b *Bus) Map(start, size uint32, name string, dev Device) {
	if size == 0 {
		panic("bus: zero-size mapping " + name)
	}
	end := start + size - 1
	for _, m := range b.maps {
		if start <= m.end && m.start <= end {
			panic(fmt.Sprintf("bus: mapping %s [%06X-%06X] overlaps %s [%06X-%06X]",
				name, start, end, m.name, m.start, m.end))
		}
	}
	b.maps = append(b.maps, mapping{start: start, end: end, name: name, dev: dev})
}

func (b *Bus) find(addr uint32) *mapping {
	for i := range b.maps {
		if addr >= b.maps[i].start && addr <= b.maps[i].end {
			return &b.maps[i]
		}
	}
	return nil
}

// Read returns the value at addr. Unmapped accesses invoke OnFault (if set)
// and return its result; otherwise returns all-ones for the given size.
func (b *Bus) Read(addr uint32, sz Size) uint32 {
	addr &= AddrMask
	var v uint32
	if m := b.find(addr); m != nil {
		v = m.dev.Read(addr-m.start, sz)
	} else if b.OnFault != nil {
		v = b.OnFault(addr, sz, false)
	} else {
		v = faultValue(sz)
	}
	if b.OnRead != nil {
		b.OnRead(addr, sz, v)
	}
	return v
}

// Write stores val at addr. Unmapped accesses invoke OnFault (if set); writes
// are always dropped when no device is mapped.
func (b *Bus) Write(addr uint32, sz Size, val uint32) {
	addr &= AddrMask
	if b.OnWrite != nil {
		b.OnWrite(addr, sz, val)
	}
	if m := b.find(addr); m != nil {
		m.dev.Write(addr-m.start, sz, val)
		return
	}
	if b.OnFault != nil {
		b.OnFault(addr, sz, true)
	}
}

func faultValue(sz Size) uint32 {
	switch sz {
	case Byte:
		return 0xFF
	case Word:
		return 0xFFFF
	default:
		return 0xFFFFFFFF
	}
}

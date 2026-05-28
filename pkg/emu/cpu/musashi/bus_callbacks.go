// Bus-routing callbacks exported to C so Musashi's memory-access externs
// dispatch through the active bus.Bus instead of a flat C array.
//
// This file must NOT have a cgo preamble: cgo forbids a preamble and
// //export directives in the same file.
package musashi

import "C"

import (
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// activeBus is the bus.Bus currently attached to the Musashi singleton.
// Set by New(); must be non-nil before any emulation step.
var activeBus *bus.Bus

//export m68k_read_memory_8
func m68k_read_memory_8(addr C.uint) C.uint {
	return C.uint(activeBus.Read(uint32(addr), bus.Byte))
}

//export m68k_read_memory_16
func m68k_read_memory_16(addr C.uint) C.uint {
	return C.uint(activeBus.Read(uint32(addr), bus.Word))
}

//export m68k_read_memory_32
func m68k_read_memory_32(addr C.uint) C.uint {
	return C.uint(activeBus.Read(uint32(addr), bus.Long))
}

//export m68k_write_memory_8
func m68k_write_memory_8(addr C.uint, val C.uint) {
	activeBus.Write(uint32(addr), bus.Byte, uint32(val))
}

//export m68k_write_memory_16
func m68k_write_memory_16(addr C.uint, val C.uint) {
	activeBus.Write(uint32(addr), bus.Word, uint32(val))
}

//export m68k_write_memory_32
func m68k_write_memory_32(addr C.uint, val C.uint) {
	activeBus.Write(uint32(addr), bus.Long, uint32(val))
}

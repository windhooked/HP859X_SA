package device

import "github.com/windhooked/HP859X_SA/pkg/emu/bus"

// ATKeyboard models the external keyboard receiver in the HP 8590-series: an IBM
// AT-compatible keyboard whose scan codes (Set 2) arrive over the MC68230 PIT
// serial port at 0xEF8000–0xEF80FF (the same range as the PIT stub). This device
// replaces the zeroed-RAM PIT stub for the two registers the firmware actually uses.
//
// Firmware receive contract (reverse-engineered from ROM 0x26DC):
//   0xEF8002  status: bit 1 = data-ready; == 0xFF means "no data" (firmware also
//             checks cmpi.b #-1 to catch 0xFF and treats it as empty).
//   0xEF8000  data:   next scan-code byte (consumed on read).
//
// The protocol is "byte-inject": we bypass the AT bit-serial clock/data protocol
// and write bytes directly into the FIFO. The firmware's transport code polls the
// status register on every IRQ4 tick; if data is ready it reads the byte and pushes
// it into the HP-IB ring buffer (0xFFBC12 via fcn.42f8).
//
// AT scan-code set 2 uses:
//   make sequence: <byte>  (one byte for most keys; E0 <byte> for extended)
//   break sequence: F0 <byte>  (or E0 F0 <byte> for extended)
//
// Usage:
//  1. Call Enqueue(bytes) to push one make or break sequence.
//  2. Call Pending() to know whether to fire IRQ4.
//  3. The firmware reads status + data; the FIFO auto-advances.
//
// The full 256-byte PIT register space is backed by a small RAM so other PIT
// accesses (PGCR at 0xEF8000, PSRR at 0xEF8002 during init) continue to work
// as readable/writable stores (the init writes 0x72/0x5D/0x10/0x24 to 0xEF8002).
type ATKeyboard struct {
	regs [256]byte // full PIT register file (other accesses read back what was written)
	fifo []byte    // pending scan-code bytes to deliver to the firmware
}

// NewATKeyboard returns an idle AT keyboard stub.
func NewATKeyboard() *ATKeyboard { return &ATKeyboard{} }

// Enqueue appends scan-code bytes to the delivery FIFO.
// Pass a complete make or break sequence (see ScanSet2 constants).
func (k *ATKeyboard) Enqueue(bytes ...byte) {
	k.fifo = append(k.fifo, bytes...)
}

// Pending reports whether there are undelivered scan-code bytes.
// The GUI / run-loop should fire IRQ4 while Pending() is true so the
// firmware's IRQ4 handler picks up the data at 0x26DC.
func (k *ATKeyboard) Pending() bool { return len(k.fifo) > 0 }

func (k *ATKeyboard) Read(addr uint32, sz bus.Size) uint32 {
	off := addr & 0xFF
	switch off {
	case 0x00: // data register — return next FIFO byte, consume it
		if len(k.fifo) > 0 {
			b := k.fifo[0]
			k.fifo = k.fifo[1:]
			return uint32(b)
		}
		return 0x00
	case 0x02: // status register — bit 1 = data-ready
		if len(k.fifo) > 0 {
			return 0x02
		}
		// Return 0 (no bit 1 set). Avoid 0xFF: firmware treats 0xFF as "no data"
		// (cmpi.b #-1, $ffef8002 at 0x26E6) so returning 0 is the clean empty state.
		return 0x00
	default:
		return uint32(k.regs[off])
	}
}

func (k *ATKeyboard) Write(addr uint32, sz bus.Size, val uint32) {
	k.regs[addr&0xFF] = byte(val)
}

// ── AT keyboard scan-code Set 2 table ────────────────────────────────────────
//
// Make (press) codes for the keys the HP 8590 external keyboard uses.
// Break (release) = F0 prefix + the same make byte (or E0 F0 byte for extended).
// Source: IBM PC/AT Technical Reference Manual, Chapter 4 (keyboard scan codes).
//
// Extended keys (E0 prefix) are listed as two-byte make sequences.
// The full Set 2 table only; the firmware processes this set.

// ATMake returns the AT Set-2 make (press) scan-code bytes for an HP front-panel
// or host function key. Returns nil if the key has no AT mapping.
func ATMake(k ATKey) []byte {
	if sc, ok := atSet2[k]; ok {
		return sc
	}
	return nil
}

// ATBreak returns the AT Set-2 break (release) scan-code bytes.
func ATBreak(k ATKey) []byte {
	make := ATMake(k)
	if make == nil {
		return nil
	}
	if len(make) == 2 && make[0] == 0xE0 {
		return []byte{0xE0, 0xF0, make[1]}
	}
	return []byte{0xF0, make[0]}
}

// ATKey is an abstract key identifier used to look up AT scan codes.
// It covers all keys the HP external keyboard firmware mapping uses.
type ATKey uint8

const (
	// Alphanumeric
	ATKeyA ATKey = iota
	ATKeyB
	ATKeyC
	ATKeyD
	ATKeyE
	ATKeyF
	ATKeyG
	ATKeyH
	ATKeyI
	ATKeyJ
	ATKeyK
	ATKeyL
	ATKeyM
	ATKeyN
	ATKeyO
	ATKeyP
	ATKeyQ
	ATKeyR
	ATKeyS
	ATKeyT
	ATKeyU
	ATKeyV
	ATKeyW
	ATKeyX
	ATKeyY
	ATKeyZ
	ATKey0
	ATKey1
	ATKey2
	ATKey3
	ATKey4
	ATKey5
	ATKey6
	ATKey7
	ATKey8
	ATKey9
	// Punctuation / symbols
	ATKeySpace
	ATKeyEnter
	ATKeyBackspace
	ATKeyEscape
	ATKeyPeriod
	ATKeyComma
	ATKeyMinus
	ATKeyEquals
	ATKeySemicolon
	ATKeySlash
	ATKeyBackslash
	ATKeyBracketLeft
	ATKeyBracketRight
	ATKeyGrave
	ATKeyApostrophe
	// Function keys — firmware mapping from HP 8590 Programmer's Guide Table 5-8
	ATKeyF1  // → Softkey 1
	ATKeyF2  // → Softkey 2
	ATKeyF3  // → Softkey 3
	ATKeyF4  // → Softkey 4
	ATKeyF5  // → Softkey 5
	ATKeyF6  // → Softkey 6
	ATKeyF7  // → Prefix mode
	ATKeyF8  // → Remote commands mode
	ATKeyF9  // → MKR menu
	ATKeyF10 // → SPAN menu
	ATKeyF11 // → AMPLITUDE menu
	ATKeyF12 // → Screen title recall
	// Navigation
	ATKeyUp
	ATKeyDown
	ATKeyLeft
	ATKeyRight
	ATKeyPrintScreen
	// Numpad (DATA keys map here)
	ATKeyNum0
	ATKeyNum1
	ATKeyNum2
	ATKeyNum3
	ATKeyNum4
	ATKeyNum5
	ATKeyNum6
	ATKeyNum7
	ATKeyNum8
	ATKeyNum9
	ATKeyNumDecimal
	ATKeyNumEnter
	ATKeyNumPlus
	ATKeyNumMinus
)

// atSet2 maps ATKey → AT scan-code set 2 make bytes.
var atSet2 = map[ATKey][]byte{
	// Letters (set 2 scan codes)
	ATKeyA: {0x1C}, ATKeyB: {0x32}, ATKeyC: {0x21}, ATKeyD: {0x23},
	ATKeyE: {0x24}, ATKeyF: {0x2B}, ATKeyG: {0x34}, ATKeyH: {0x33},
	ATKeyI: {0x43}, ATKeyJ: {0x3B}, ATKeyK: {0x42}, ATKeyL: {0x4B},
	ATKeyM: {0x3A}, ATKeyN: {0x31}, ATKeyO: {0x44}, ATKeyP: {0x4D},
	ATKeyQ: {0x15}, ATKeyR: {0x2D}, ATKeyS: {0x1B}, ATKeyT: {0x2C},
	ATKeyU: {0x3C}, ATKeyV: {0x2A}, ATKeyW: {0x1D}, ATKeyX: {0x22},
	ATKeyY: {0x35}, ATKeyZ: {0x1A},
	// Digits
	ATKey0: {0x45}, ATKey1: {0x16}, ATKey2: {0x1E}, ATKey3: {0x26},
	ATKey4: {0x25}, ATKey5: {0x2E}, ATKey6: {0x36}, ATKey7: {0x3D},
	ATKey8: {0x3E}, ATKey9: {0x46},
	// Punctuation
	ATKeySpace:        {0x29},
	ATKeyEnter:        {0x5A},
	ATKeyBackspace:    {0x66},
	ATKeyEscape:       {0x76},
	ATKeyPeriod:       {0x49},
	ATKeyComma:        {0x41},
	ATKeyMinus:        {0x4E},
	ATKeyEquals:       {0x55},
	ATKeySemicolon:    {0x4C},
	ATKeySlash:        {0x4A},
	ATKeyBackslash:    {0x5D},
	ATKeyBracketLeft:  {0x54},
	ATKeyBracketRight: {0x5B},
	ATKeyGrave:        {0x0E},
	ATKeyApostrophe:   {0x52},
	// Function keys
	ATKeyF1:  {0x05}, ATKeyF2: {0x06}, ATKeyF3: {0x04}, ATKeyF4: {0x0C},
	ATKeyF5:  {0x03}, ATKeyF6: {0x0B}, ATKeyF7: {0x83}, ATKeyF8: {0x0A},
	ATKeyF9:  {0x01}, ATKeyF10: {0x09}, ATKeyF11: {0x78}, ATKeyF12: {0x07},
	// Navigation (extended)
	ATKeyUp:    {0xE0, 0x75},
	ATKeyDown:  {0xE0, 0x72},
	ATKeyLeft:  {0xE0, 0x6B},
	ATKeyRight: {0xE0, 0x74},
	// PrintScreen (extended)
	ATKeyPrintScreen: {0xE0, 0x7C},
	// Numpad
	ATKeyNum0: {0x70}, ATKeyNum1: {0x69}, ATKeyNum2: {0x72}, ATKeyNum3: {0x7A},
	ATKeyNum4: {0x6B}, ATKeyNum5: {0x73}, ATKeyNum6: {0x74}, ATKeyNum7: {0x6C},
	ATKeyNum8: {0x75}, ATKeyNum9: {0x7D}, ATKeyNumDecimal: {0x71},
	ATKeyNumEnter: {0xE0, 0x5A}, ATKeyNumPlus: {0x79}, ATKeyNumMinus: {0x7B},
}

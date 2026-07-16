package device

import (
	"testing"
)

// TestATKeyboardProtocol verifies the byte-inject contract the firmware uses:
// 0xEF8002 bit1=data-ready; 0xEF8000=data byte; 0xFF on empty status.
func TestATKeyboardProtocol(t *testing.T) {
	k := NewATKeyboard()
	// empty: status bit1 clear, data zero
	if s := k.Read(0x02, 0); s != 0x00 {
		t.Errorf("empty status = %#02x, want 0x00", s)
	}
	// inject a scan code
	k.Enqueue(0x1C) // ATKeyA make
	if !k.Pending() {
		t.Fatal("Pending should be true after Enqueue")
	}
	// status must have bit1 set
	if s := k.Read(0x02, 0); s&0x02 == 0 {
		t.Errorf("status = %#02x, want bit1 set", s)
	}
	// data read returns the byte and consumes it
	if d := k.Read(0x00, 0); d != 0x1C {
		t.Errorf("data = %#02x, want 0x1C", d)
	}
	if k.Pending() {
		t.Error("Pending should be false after consuming the byte")
	}
}

// TestATScanCodes spot-checks Set-2 make/break for a few keys.
func TestATScanCodes(t *testing.T) {
	cases := []struct {
		k    ATKey
		make []byte
		brk  []byte
	}{
		{ATKeyA, []byte{0x1C}, []byte{0xF0, 0x1C}},
		{ATKeyEnter, []byte{0x5A}, []byte{0xF0, 0x5A}},
		{ATKeyF1, []byte{0x05}, []byte{0xF0, 0x05}},
		{ATKeyF9, []byte{0x01}, []byte{0xF0, 0x01}},
		{ATKeyUp, []byte{0xE0, 0x75}, []byte{0xE0, 0xF0, 0x75}},
		// Firmware-confirmed editor/cursor nav keys (fcn.57278, E0-prefixed).
		// See docs/KEYBOARD_MAP.md.
		{ATKeyHome, []byte{0xE0, 0x6C}, []byte{0xE0, 0xF0, 0x6C}},
		{ATKeyEnd, []byte{0xE0, 0x69}, []byte{0xE0, 0xF0, 0x69}},
		{ATKeyInsert, []byte{0xE0, 0x70}, []byte{0xE0, 0xF0, 0x70}},
		{ATKeyDelete, []byte{0xE0, 0x71}, []byte{0xE0, 0xF0, 0x71}},
		{ATKeyPageUp, []byte{0xE0, 0x7D}, []byte{0xE0, 0xF0, 0x7D}},
		{ATKeyPageDown, []byte{0xE0, 0x7A}, []byte{0xE0, 0xF0, 0x7A}},
	}
	for _, c := range cases {
		if got := ATMake(c.k); !bytesEq(got, c.make) {
			t.Errorf("ATMake(%d) = %v, want %v", c.k, got, c.make)
		}
		if got := ATBreak(c.k); !bytesEq(got, c.brk) {
			t.Errorf("ATBreak(%d) = %v, want %v", c.k, got, c.brk)
		}
	}
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

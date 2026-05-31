package machine_test

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestPOSTSelfTestPasses asserts the power-up self-test completes with a clean
// result (FAIL code 0x0000) once every A16 subsystem the POST exercises is
// modelled.
//
// The reporter at ROM 0x184DE formats the on-screen "FAIL: xxxx" code as
// NOT(0xFFF612):NOT(0xFFF610) — two active-low PASS-bit latches the POST
// (ROM 0x4998 + analog suite 0x4534) sets one bit per subsystem test. A clean
// pass requires BOTH latches fully set: f610 == 0xFF and f612 == 0xFF, which
// the reporter renders as 0x0000 (no FAIL line on screen — see the boot-screen
// golden, which no longer carries the "FAIL: 8000" text it did at 15/16).
//
// The last subsystem to pass was the HD63484 ACRTC display-RAM read-back
// (f612 bit 7, ROM 0xD6B2): the firmware block-fills video memory with a test
// pattern (0x5800), rewinds the read pointer, then reads every word back via
// the RD path and XOR-verifies it. Modelling that read-back data path
// (pkg/emu/device/hd63484: blockFill + ReadData) sets the final bit.
func TestPOSTSelfTestPasses(t *testing.T) {
	m := newMachine(t)
	m.CPU.Reset()
	m.BootToOperating(30_000_000)

	f610 := byte(m.Bus.Read(0xFFF610, bus.Byte))
	f612 := byte(m.Bus.Read(0xFFF612, bus.Byte))

	// FAIL code as the firmware's reporter computes it.
	failCode := uint16(^f612)<<8 | uint16(^f610)

	if f610 != 0xFF {
		t.Errorf("POST PASS latch f610 = %#02x, want 0xFF (subsystem tests in the f610 group failed)", f610)
	}
	if f612 != 0xFF {
		t.Errorf("POST PASS latch f612 = %#02x, want 0xFF (bit 7 = HD63484 display-RAM read-back; others = A16 bus tests)", f612)
	}
	if failCode != 0x0000 {
		t.Errorf("POST FAIL code = %#04x, want 0x0000 (clean self-test)", failCode)
	}
}

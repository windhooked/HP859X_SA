package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestModelIDDiag checks the model-number setup the EEVblog thread flagged as
// behavior-gating: 0xBFEE = decimal model in hex (8593=0x2191, 8595=0x2193);
// 0xBFF0 = model suffix ('E'=0x45/'G'=0x47); 0xB00C = board_id (should be 3).
// Many dispatch/mode branches gate on 0xBFEE. DIAG=1.
func TestModelIDDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(250_000_000)
	bfee := m.Bus.Read(0xFFBFEE, bus.Word)
	t.Logf("0xBFEE (model number) = 0x%04X (8593=0x2191, 8590=0x218E, 8595=0x2193)", bfee)
	t.Logf("0xBFF0 (model suffix byte) = 0x%02X ('E'=0x45 'G'=0x47)", byte(m.Bus.Read(0xFFBFF0, bus.Byte)))
	t.Logf("0xB00C (board_id) = 0x%X (want 3)", m.Bus.Read(0xFFB00C, bus.Word))
	t.Logf("0xBF26 (SystemID longword A) = 0x%08X", m.Bus.Read(0xFFBF26, bus.Long))
	switch bfee {
	case 0x2191:
		t.Log("⇒ model = 8593 (CORRECT)")
	default:
		t.Logf("⇒ model is NOT 8593 (0x2191) — model-gated branches may be wrong")
	}
}

package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestTraceBufDiag dumps the trace buffer (0x2FD508, 401 samples) min/max after a
// sweep-driven boot, to classify the static-ADC effect on the trace: buffer still
// holds the CAL peak (max≈0x17F) ⇒ render/snapshot issue; buffer flat ⇒ the static
// fix changed sweep capture. DIAG=1.
func TestTraceBufDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(250_000_000)
	m.BootToOperatingWithSweep(40_000_000)
	min, max, nonzero := 0xFFFF, 0, 0
	for i := 0; i < 401; i++ {
		v := int(m.Bus.Read(0x2FD508+uint32(i*2), bus.Word) & 0xFFFF)
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		if v != 0 {
			nonzero++
		}
	}
	t.Logf("trace buffer @0x2FD508: min=0x%X max=0x%X nonzero=%d/401", min, max, nonzero)
}

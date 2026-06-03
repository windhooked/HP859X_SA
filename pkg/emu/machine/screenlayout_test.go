package machine

import (
	"fmt"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestScreenLayout reads the firmware's HD63484 SCREEN LAYOUT by tracing the
// control-register (AR-addressed) writes during boot + operating display. The
// ACRTC composites up to four logical screens (Base/Upper/Lower/Window), each
// with its own Start Address Register (SAR0-3), Memory Width (MWR0-3) and Raster
// Address (RAR0-3); these are written through the MPU control interface, NOT the
// graphics command FIFO, so they don't appear in a CmdCapture. This answers the
// open question "does the graticule survive the per-sweep SCLR because it lives
// in a DIFFERENT screen/address region than the trace?" — by reading how many
// screens the firmware enables and where each one's frame buffer starts.
//
// Run verbose to see the full programming sequence:
//
//	SCREENLAYOUT=1 DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib \
//	  go test ./pkg/emu/machine/ -run TestScreenLayout -v
func TestScreenLayout(t *testing.T) {
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom not available")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true

	// Capture the control-register writes across boot AND a stretch of operating
	// display (the screen layout may be (re)programmed at mode entry).
	m.MMIO.Display.Chip.StartCmdCapture(1 << 20)
	m.BootToOperatingWithSweep(250_000_000)
	m.BootToOperatingWithSweep(40_000_000)
	ctrl := m.MMIO.Display.Chip.CtrlCapture()

	// Replay the AR writes in order, reconstructing the banked SAR/MWR/RAR state
	// exactly as strict.go's writeControlReg does, so the FINAL values reflect the
	// firmware's programmed layout. Also keep a human-readable log of every
	// screen-layout-relevant write.
	var sar [4]uint32
	var mwr, rar [4]uint16
	var sp [3]uint16
	var omr, dcr uint16
	type ev struct {
		name string
		val  uint16
	}
	var seq []ev
	bank := func(ar uint16) int { return int((ar & 0x18) >> 3) }
	for _, w := range ctrl {
		ar, v := w[0]&0xFF, w[1]
		switch ar {
		case 0x04:
			omr = v
			seq = append(seq, ev{"OMR (Operation Mode)", v})
		case 0x06:
			dcr = v
			seq = append(seq, ev{"DCR (Display Control)", v})
		case 0x8C:
			sp[0] = v & 0x0fff
			seq = append(seq, ev{"SP0 split-width", v})
		case 0x8A:
			sp[1] = v & 0x0fff
			seq = append(seq, ev{"SP1 split-width", v})
		case 0x8E:
			sp[2] = v & 0x0fff
			seq = append(seq, ev{"SP2 split-width", v})
		case 0xC2, 0xCA, 0xD2, 0xDA:
			b := bank(ar)
			mwr[b] = v & 0x0fff
			seq = append(seq, ev{fmt.Sprintf("MWR%d (mem width)", b), v})
		case 0xC4, 0xCC, 0xD4, 0xDC:
			b := bank(ar)
			sar[b] = (uint32(v&0x000f) << 16) | (sar[b] & 0xffff)
			seq = append(seq, ev{fmt.Sprintf("SAR%d high", b), v})
		case 0xC6, 0xCE, 0xD6, 0xDE:
			b := bank(ar)
			sar[b] = (uint32(v) & 0xffff) | (sar[b] & 0xf0000)
			seq = append(seq, ev{fmt.Sprintf("SAR%d low", b), v})
		case 0xC0, 0xC8, 0xD0, 0xD8:
			b := bank(ar)
			rar[b] = v
			seq = append(seq, ev{fmt.Sprintf("RAR%d (raster addr)", b), v})
		}
	}

	t.Logf("control-register writes captured: %d total, %d screen-layout-relevant", len(ctrl), len(seq))
	screen := []string{"Base", "Upper/Char", "Lower", "Window"}
	t.Log("=== final programmed screen layout ===")
	for b := 0; b < 4; b++ {
		row := 0
		if mwr[b] > 0 {
			row = int(sar[b]) / int(mwr[b])
		}
		t.Logf("  screen %d (%-10s): SAR=0x%05x  MWR=%-4d  RAR=0x%04x  → start row ~%d",
			b, screen[b], sar[b], mwr[b], rar[b], row)
	}
	t.Logf("  OMR=0x%04x  DCR=0x%04x  split-widths SP0=%d SP1=%d SP2=%d", omr, dcr, sp[0], sp[1], sp[2])

	if os.Getenv("SCREENLAYOUT") != "" {
		t.Log("=== full programming sequence ===")
		for i, e := range seq {
			t.Logf("  [%3d] %-22s = 0x%04x", i, e.name, e.val)
		}
	}

	// How many DISTINCT non-zero frame-buffer start addresses did the firmware
	// program? ≥2 distinct SARs ⇒ the graticule and trace genuinely live in
	// separate screens/regions (the manual's compositing model), which is the
	// answer to the persistent-graticule question.
	configured := 0
	for b := 0; b < 4; b++ {
		if mwr[b] > 0 { // a screen the firmware actually configured (non-zero pitch)
			configured++
		}
	}
	t.Logf("configured screens (non-zero MWR): %d", configured)

	// FINDING (locked in): the 8593 firmware configures exactly ONE ACRTC screen
	// (bank 1: SAR1/MWR1). It does NOT use the chip's multi-screen compositing
	// (SAR0/2/3 stay unprogrammed). Therefore the persistent-graticule question is
	// NOT answered by separate display regions — the firmware redraws the graph
	// (graticule + trace) every sweep into the single frame buffer via APLL
	// polylines / ARCT rects, interleaved with the per-strip SCLR clears. If this
	// assertion ever fails (the firmware starts using >1 screen), the compositing
	// model has to be revisited.
	if configured != 1 {
		t.Errorf("expected exactly 1 configured ACRTC screen, got %d (multi-screen compositing would change the rendering model)", configured)
	}
	if mwr[1] == 0 {
		t.Errorf("expected the single configured screen to be bank 1 (SAR1/MWR1); MWR1=0")
	}
}

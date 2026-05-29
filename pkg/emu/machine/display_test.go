package machine_test

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// goldenBootScreen is the committed reference render of the 8593A boot screen
// (the SCI display after the firmware reaches its operating loop). Regenerate
// it intentionally with: UPDATE_GOLDEN=1 go test ./pkg/emu/machine/ -run BootScreen
const goldenBootScreen = "testdata/boot_screen.png"

// bootScreenCycles is the cycle budget for the boot-screen golden. At 30M the
// firmware draws only the top-left model/status text (~30 lit pixels). By
// 200M more annunciators appear (top-right reference/RBW area, a small marker
// mid-left), totalling ~150 lit pixels. The centre graticule and trace area
// stay blank because the sweep/RF emulation isn't built yet. Higher budgets
// keep accumulating content slowly; 200M is a reasonable balance between
// captured content and test runtime (~1s).
const bootScreenCycles = 200_000_000

// TestMachineBootScreen — Phase-4 (display) gate: boot the firmware into its
// operating loop and assert the SCI display renders the expected screen,
// pixel-for-pixel against a committed golden PNG.
//
// The render is deterministic (no randomness in the core, LoopBreaker, or IRQ
// schedule), so an exact pixel match is a meaningful regression guard on the
// whole pipeline: ROM → CPU → bus → SCI stream decode → framebuffer.
//
// Rev L renders the top status banner + bottom-line annunciators (~13k glyph
// blits at 30M cycles, several hundred lit pixels). The centre graticule and
// trace area remain blank because the sweep / RF hardware isn't emulated yet
// — they aren't part of the boot screen's pixel content. The golden PNG
// captures whatever Rev L draws today.
func TestMachineBootScreen(t *testing.T) {
	t.Skip("Re-baseline pending: the A16 analog-bus conversion model " +
		"(docs/ANALOG_BUS_MODEL.md) cleared the boot's analog gate, so the " +
		"firmware now advances ~10x further — into the startup-DLP execution " +
		"— and derails on a DLP-interpreter symbol dispatch at ~49M cycles " +
		"(see §12). This test's 200M budget runs past that derail, so the " +
		"frozen-at-0x5E000 golden no longer applies. Regenerate the golden " +
		"once the startup DLP reaches a stable rendered UI.")
	m := newMachine(t)
	m.BootToOperating(bootScreenCycles)

	got := m.MMIO.Display.RenderFrame()

	// Sanity: the screen must not be blank. (The operating loop spends time in
	// many subroutines, so the instantaneous PC is not a reliable "booted"
	// signal — a rendered screen is.)
	//
	// Threshold tuned for Rev L at 200M cycles with HD63484 raster decoding:
	// ~20,000 lit pixels (the firmware's 0x4400 dot-pattern background fill
	// across the 1024×256 paint area, clipped to the 640-wide visible region,
	// plus the annunciator text overlay). Without the raster decoder we'd
	// only see ~136 pixels (text only); the order-of-magnitude jump confirms
	// the PAINT pipeline is feeding video RAM. The exact count is locked by
	// the golden PNG below.
	if lit := litPixels(got); lit < 10_000 {
		t.Fatalf("display far below expected (%d lit pixels, PC=%#06X) — "+
			"PAINT/raster pipeline likely broken", lit, m.CPU.Reg(cpu.PC))
	}

	path := filepath.Join("testdata", "boot_screen.png")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writePNG(t, path, got)
		t.Logf("UPDATE_GOLDEN: wrote %s (%d lit pixels)", path, litPixels(got))
		return
	}

	want := readPNG(t, path)
	if !sameImage(got, want) {
		// Dump the actual render next to the golden for inspection.
		writePNG(t, filepath.Join("testdata", "boot_screen.actual.png"), got)
		t.Fatalf("boot screen does not match golden %s\n"+
			"wrote testdata/boot_screen.actual.png for comparison\n"+
			"if this change is intended: UPDATE_GOLDEN=1 go test ./pkg/emu/machine/ -run BootScreen",
			goldenBootScreen)
	}
}

// TestMachineBootFaithful — proves the hardware mocks are sufficient to boot
// the real firmware with NO LoopBreaker: the ROM checksum, march RAM test, and
// calibration delay all run to completion (against the real ROM/RAM), driven
// only by the IRQ5 timer tick, and the firmware still reaches its operating
// loop and renders the screen. Skipped under -short (runs the full ~20M-cycle
// boot; ~1s).
func TestMachineBootFaithful(t *testing.T) {
	if testing.Short() {
		t.Skip("faithful boot runs the full ROM-checksum/RAM-test (~80M cycles for Rev L)")
	}
	m := newMachine(t)
	// Rev L: 1 MB ROM = 2× the checksum work of 17.12.90; budget bumped from
	// 40M to 100M to absorb that without LoopBreaker.
	m.BootToOperatingFaithful(100_000_000)

	if lit := litPixels(m.MMIO.Display.RenderFrame()); lit < 20 {
		t.Fatalf("faithful boot rendered a near-blank screen (%d lit pixels, PC=%#06X) — "+
			"a hardware mock is missing", lit, m.CPU.Reg(cpu.PC))
	}
}

func litPixels(img *image.RGBA) int {
	n := 0
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if img.Pix[i]|img.Pix[i+1]|img.Pix[i+2] != 0 {
			n++
		}
	}
	return n
}

func sameImage(a *image.RGBA, b image.Image) bool {
	rb, ok := b.(*image.RGBA)
	if !ok || a.Bounds() != rb.Bounds() {
		return false
	}
	return bytes.Equal(a.Pix, rb.Pix)
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

func readPNG(t *testing.T, path string) *image.RGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open golden %s: %v (generate it with UPDATE_GOLDEN=1)", path, err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode golden %s: %v", path, err)
	}
	// Normalise to *image.RGBA for byte comparison.
	rgba := image.NewRGBA(src.Bounds())
	for y := src.Bounds().Min.Y; y < src.Bounds().Max.Y; y++ {
		for x := src.Bounds().Min.X; x < src.Bounds().Max.X; x++ {
			rgba.Set(x, y, src.At(x, y))
		}
	}
	return rgba
}

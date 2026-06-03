package hd63484

import (
	"bufio"
	"image/png"
	"os"
	"strconv"
	"testing"
)

// TestReplayCapture replays a captured firmware command-FIFO stream into a fresh
// chip and renders it — showing exactly what the firmware's draws produce in our
// model. It also analyses the SCLR sequence: for each region (RWP) it records
// which AND patterns (0x5555 / 0xAAAA) were applied, to see whether regions get
// BOTH (→ clean two-pass clear) or only one (→ left dithered). Diagnostic; set
// REPLAY=<path> to a screens/crt_*.txt capture.
func TestReplayCapture(t *testing.T) {
	path := os.Getenv("REPLAY")
	if path == "" {
		t.Skip("set REPLAY=screens/crt_<ts>.txt")
	}
	f, err := os.Open("../../../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var words []uint16
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		v, err := strconv.ParseUint(sc.Text(), 16, 16)
		if err == nil {
			words = append(words, uint16(v))
		}
	}
	t.Logf("loaded %d words", len(words))

	// Parse-track the SCLR sequence: WPR 0x0c/0x0d set the RWP; each 0x5C0x is
	// followed by pattern, ax, ay. Record patterns seen per RWP.
	rwpPatterns := map[uint32]map[uint16]int{}
	var cur uint32
	hi, lo := uint16(0), uint16(0)
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == 0x080c && i+1 < len(words):
			hi = words[i+1]
			cur = (cur & 0x00fff) | ((uint32(hi) & 0xff) << 12)
		case w == 0x080d && i+1 < len(words):
			lo = words[i+1]
			cur = (cur & 0xff000) | ((uint32(lo) & 0xfff0) >> 4)
		case w&0xFFFC == 0x5C00 && i+1 < len(words):
			pat := words[i+1]
			if rwpPatterns[cur] == nil {
				rwpPatterns[cur] = map[uint16]int{}
			}
			rwpPatterns[cur][pat]++
		}
	}
	both, only5555, onlyAAAA, other := 0, 0, 0, 0
	for _, pats := range rwpPatterns {
		_, h5 := pats[0x5555]
		_, hA := pats[0xAAAA]
		switch {
		case h5 && hA:
			both++
		case h5:
			only5555++
		case hA:
			onlyAAAA++
		default:
			other++
		}
	}
	t.Logf("SCLR RWP regions: total=%d  both(5555+AAAA)=%d  only5555=%d  onlyAAAA=%d  other=%d",
		len(rwpPatterns), both, only5555, onlyAAAA, other)

	// Render the replayed stream.
	c := New()
	for _, w := range words {
		c.WriteData(w)
	}
	out, _ := os.Create("../../../../screens/replay_render.png")
	defer out.Close()
	png.Encode(out, c.RenderFrame())
	t.Log("rendered screens/replay_render.png")
}

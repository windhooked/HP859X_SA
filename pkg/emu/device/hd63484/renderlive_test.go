package hd63484

import "testing"

// TestRenderLiveBypassesSnapshot verifies SetRenderLive makes the scanout reflect
// post-snapshot draws (the live buffer) — the GUI relies on this so CAL DISP /
// command echo / menus refresh instead of freezing on the last grid snapshot.
func TestRenderLiveBypassesSnapshot(t *testing.T) {
	c := New()
	c.core.writeword(5, 0x0001) // word 5, bit0 lit
	c.core.snapshot()           // stable now holds word 5 lit, word 6 dark
	c.core.writeword(6, 0x0001) // word 6, bit0 lit AFTER the snapshot

	// word 6 bit 0 renders at scanout (6*16, 0) = (96,0) with sar1=0.
	stable := c.RenderScanout()
	if stable.RGBAAt(96, 0).R > 30 {
		t.Error("stable render should NOT show the post-snapshot pixel (it reads the snapshot)")
	}

	c.SetRenderLive(true)
	live := c.RenderScanout()
	if live.RGBAAt(96, 0).R <= 30 {
		t.Error("live render SHOULD show the post-snapshot pixel (it reads the live buffer)")
	}

	// And word 5 (pre-snapshot) shows in both.
	if stable.RGBAAt(80, 0).R <= 30 || live.RGBAAt(80, 0).R <= 30 {
		t.Error("pre-snapshot pixel should show in both stable and live")
	}
}

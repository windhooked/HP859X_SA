# screens/

Rendered HP 8593A boot screens for visual inspection. Regenerate any time with:

```bash
go run ./cmd/renderframe/ <cycles> screens/<name>.png
```

| File | Cycle budget | Lit pixels | What's visible |
|---|---|---|---|
| `boot_screen_30M.png`  | 30M  | ~20k | Top-left model/status banner + top-right reference + mid-left marker + **0x4400 raster background dot-pattern across the 1024×256 paint area (clipped to 640 wide)** |
| `boot_screen_60M.png`  | 60M  | ~20k | Same content; sweep arms between 30M and 100M (`FFBF34` switches to capture-handler 0x40B8) |
| `boot_screen_100M.png` | 100M | ~20k | Sweep armed; firmware idling waiting for the trace-render trigger |
| `boot_screen_200M.png` | 200M | 20597 | Sweep armed; **current test golden** |
| `boot_screen_golden.png` | 200M | 20597 | Byte-identical copy of the committed regression target in `pkg/emu/machine/testdata/`. |
| `sweep_run.png`    | 200M continuous | ~20k | IRQ6 + ADC stimulus woven into the main boot loop (every 8 chunks once the sweep is armed). 7684 IRQ6 ticks; firmware processes samples but doesn't render the trace via SCI vector ops (see project guide). |

**Why the lit-pixel count jumped from 136 → ~20k:** added the HD63484 raster-write decoder. The firmware writes a `WPR 0x0C = 0x4000` + `WPR 0x0D = 0x0000` pair to arm video-RAM-write mode, then pours 16,384 data words into a 64×256 cell area = 1024×256 pixels of monochrome data. Previously these writes were silently skipped. The dominant fill value `0x4400` produces vertical-stripe-pair patterns when tiled (bits 10 and 14 set in each 16-pixel word). The visible top portion of every screen is now the firmware's actual background fill + annunciator overlay; the lower portion stays black because the firmware's paint area only covers the top 256 raster rows. The trace itself still doesn't render — see the project guide's "next concrete step" for the remaining work.

The centre graticule, trace, and bottom softkey area stay blank because the
sweep/RF data path isn't emulated yet — the firmware steps the sweep DAC
~22K times during a 30M run but no IRQ6 sample-capture fires, so the trace
buffer never fills. Implementing a minimal sweep mock + decoding the HD63484
line/rect commands that draw the graticule grid is the next focused step
toward a fully populated screen.

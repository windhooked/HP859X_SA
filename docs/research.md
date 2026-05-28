
---

## Research Summary: HP/Agilent 859X Spectrum Analyzer — Emulation Reference Resources

---

### 1. Service Manuals, Schematics, and Memory Maps

**Primary Service Documentation**

The central document for assembly-level hardware knowledge is:

- **8590 Series Analyzers Assembly-Level Repair Service Guide** (HP part 08590-90316, April 2001). Hosted at multiple locations:
  - https://xdevs.com/doc/HP_Agilent_Keysight/08590-90316.pdf (16 MB, clean PDF, confirmed accessible)
  - http://www.to-way.com/teqman/HP/8590x/08590-90316.pdf
  - https://www.emctest.it/public/pages/strumentazione/elenco/Agilent%20-%20HP/8592D%20-.../Agilent-HP_8592D%20-%20Service%20Guide.pdf
  - https://www.rftesolutions.com/pdf/analyzers/spectrum_analyzers/8591E--Service_Guide.pdf

  Content verified by text extraction: Chapters cover troubleshooting, assembly replacement, block diagrams (Chapter 9), analyzer options (Chapter 10), and an extensive error-code table (Chapter 14) that **names all key IC positions on the A16 board**.

- **8590A Service Guide** (early model, component-level):
  - https://archive.org/details/hp_8590A_ServiceGuide (9.2 MB PDF, ~300 DPI scanned)
  - Text extracted from DJVU confirms: **TMS9914** (HPIB controller), **HD63484** (display/video ACRTC), **68230** (PIT/PIO), **MC6845** (CRTC) are all named as IC types on the A16 board

- **8590A Component-Level Information Package** (CLIP, HP part 5963-2591, July 1997 — full schematics):
  - https://xdevs.com/doc/HP_Agilent_Keysight/8590%20CLIP%205963-2591.pdf (17.9 MB, includes foldout schematics)
  - https://archive.org/details/hp_5963-2591 (text version without diagrams)
  - https://www.scribd.com/document/672583641/5963-2591-8590A-Series-Component-Level-Info-Package-July97-1
  - This is the **key schematic document** containing A16 processor board schematics with actual component values, IC part numbers, and address decoding logic.

- **KO4BB manuals directory** (HP 8591/8592/8593/8594/8595): http://ftb.ko4bb.com/getsimple/index.php?id=manuals&amp;dir=HP_Agilent/HP_8591_8593_8594_8595_Performance_Spectrum_Analyzer

- **SchematicsUnlimited** (confirms availability):
  - HP 8590 Series Service Guide Assembly Level (16 MB)
  - HP 8590 Component Level Information Pack (18 MB)
  - HP 8591A/8593A installation/verification/operation manual (16 MB)
  - HP 8591A service manual (16 MB)
  - HP 8593A Power Supply documentation (9 MB)
  - HP 8595A Service Manual (14 MB)
  - URL: https://www.schematicsunlimited.com/h/hp-agilent

**Confirmed A16 Processor/Video Board Architecture** (from 08590-90316 text extraction):

The A16 board (all 8590 E/L-series and 8593A) contains:

| Reference | Function | IC type |
|-----------|----------|---------|
| A16U12 | CPU (M68000) | Motorola MC68000 |
| A16U57 | Parallel I/O / Timer | Motorola MC68230 (PIT) |
| A16U6, U23, U7, U24 | Firmware ROMs | 27C010 or 27C512 (4 × 128 KB) |
| A16U5, U22 | User RAM (battery-backed) | SRAM |
| A16U2, U3 | I/O Bus (odd/even byte) | Bus drivers |
| A16U18 | I/O Address bus | Address decoder |
| A16U305, U306 | Video RAM | SRAM for display |
| (TMS9914A) | HPIB controller | On A20 IB I/O board (Option 021) |
| (HD63484) | Advanced CRT Controller | Display subsystem |

ROM naming in failure codes: **U6 = Odd B LSB, U23 = Even B LSB, U7 = Odd A LSB, U24 = Even A LSB** — this matches your project's existing interleave ordering exactly.

The A16A1 Memory Board (daughter board) holds the battery-backed SRAM with calibration constants, model/serial number, DLP programs, and calibration attenuation data.

**Memory Map** (confirmed from firmware disassembly and self-test error codes in service guide):
- ROM: 0x000000–0x07FFFF (4 × 128 KB = 512 KB, matches your project)
- RAM: contains region at 0xFF4000 (odd/even SRAM, explicitly in self-test error codes from bench.squeaky.tech)
- MMIO: 0xFFF000–0xFFFFFF (4 KB, confirmed by project source)

Specific MMIO assignments confirmed from firmware analysis (cross-referenced against existing project code):
- 0xFFF000–0xFFF00F: 82C55A PPI (front-panel I/O)
- 0xFFF5FC–0xFFF5FF: SCI/display controller (command/status/data for Panasonic TR-60S1A)
- 0xFFF600–0xFFF61F: TMS9914A HP-IB (32-byte window)
- 0xFFF626: HP-IB extended address register
- 0xFFF200: sweep-start latch
- 0xFFF300: sweep-step latch

**PAL/Address Decoder chips** (from mikrocontroller.net forum, confirmed real issue):
- **GAL16V8 part 08590-80105** is used on three boards: memory card reader (08590-60396), tracking generator control (5063-0635), LO distribution amplifier control (5062-8232)
- **08590-80094**: address decoder PAL for 27C512 boards
- **08590-80159** (22V10): for 27C010/1Mbit boards

---

### 2. ROM Dumps and Firmware

**Firmware versions identified** (from mikrocontroller.net forum thread):

| Part number | Date | EPROM size | Board |
|---|---|---|---|
| 08590-900103 | 1990-01-03 | 512 kbit (4×27C512) | 08590-60102 |
| 08590-910717 | 1991-07-17 | 1 Mbit (4×27C010) | 08590-60039 |
| 08590-940822 | 1994-08-22 | 1 Mbit | — |
| 08590-950914 | 1995-09-14 | 2 Mbit | — |
| 08590-980615 | 1998-06-15 | 2 Mbit | — |

**Sources for firmware binaries**:
- **bluefeathertech.com** — mentioned repeatedly in repair forums as a source for HP 8590/8591 firmware EPROM images (site has TLS cert issues, may need HTTP access or alternative mirror)
- **Mikrocontroller.net forum** (thread at https://www.mikrocontroller.net/topic/404954) has zipped firmware binaries and PAL JEDEC/PLD files as **forum attachments** — requires forum login to download
- No public GitHub repository with 859x ROM dumps was found

The firmware rev date (YYMMDD format post-930506, DDMMYY before that) is displayed on power-up.

---

### 3. Reverse Engineering / Hacking Resources

**EEVblog "Hacking old HP Spectrum Analyzers" thread**: https://www.eevblog.com/forum/testgear/hacking-old-hp-spectrum-analyzers/

This is the richest community reverse-engineering resource found. Key documented findings from the thread:
- NVRAM feature flags at **0xFFFFBFF6** — contains model-enable bits written by FACTSET commands
- **process_factset** subroutine at ROM address **0x0001974C** — handles all FACTSET magic numbers
- FACTSET 11023 (0x2B0F) → C-series mode; FACTSET 11076 (0x2B44) → L-series mode
- FM demodulator calibration values live at **0xFFFF9FBC, 0xFFFF9FBE, 0xFFFF9FC0, 0xFFFF9FC2**
- **SERSET nnn** command restores serial number after dead NVRAM battery
- NVRAM model number stored as big-endian 16-bit at **0x7BFEE** (e.g. 8595 = 0x2193)
- NVRAM directory structure starts around **0x4B4CF**
- Hardware NVRAM dump method: remove SRAM card, use Molex 15-92-2050 connector + Arduino/MCU

**EEVblog Option 105 enable** (front-panel procedure without firmware mod, works on 8591E/8594E/8595E rev 950308 and similar):
"Press Frequency -2001 Hz, then Cal &gt; More 1 of 4 &gt; More 2 of 4 &gt; Service Cal &gt; Flatness Data &gt; More, then press top unlabeled softkey"

**Mikrokontroller.net firmware thread**: https://www.mikrocontroller.net/topic/404954 — Board-specific EPROM/PAL combinations, firmware binaries as attachments.

**Mikrokontroller.net PAL thread**: https://www.mikrocontroller.net/topic/582830 — Specifically about recovering the 08590-80105 GAL16V8 fuse map; no responses yet as of early 2026.

No public GitHub emulator or simulator projects for the 859x series were found.

---

### 4. GPIB / HP-IB Interface Details

**TMS9914A** is confirmed as the GPIB controller chip on the A20 IB I/O board (Option 021). The TMS9914A datasheet and full data manual are available:

- **Bitsavers TMS9914A Data Manual (Dec 1982)**: https://www.bitsavers.org/components/ti/TMS9900/TMS9914A_General_Purpose_Interface_Bus_Controller_Data_Manual_Dec82.pdf
- **TMS9914A Data Sheet (Jun 1989)**: https://dn790002.ca.archive.org/0/items/bitsavers_tiTMS9900T89_2839910/TMS9914A_dataSheet_Jun89_text.pdf
- **TekWiki TMS9914 page**: https://w140.com/tekwiki/wiki/TMS9914

The TMS9914A uses 3 address lines (RS0–RS2) selecting 8 internal registers. The chip occupies 8 consecutive memory locations. In the HP 8590 emulator project the window is confirmed at 0xFFF600–0xFFF61F (32-byte window, 2-byte stride — one register per 2-byte slot to match 16-bit bus width).

The GPIB SCPI/HP command set for remote control is documented in:
- **8590 Series Programmer's Guide** (part 08590-90235): https://naic.nrao.edu/arecibo/phil/hardware/spectrumAnalyzer/hpSpecAna_ProgMan_08590-90235.pdf
- **8590 E-Series Programmer's Guide**: http://docs.ampnuts.ru/eevblog.docs/HP_Agilent_Keysight/HP%208590E,%20L%20Series%20Programmers.pdf
- **8590 Series Programming Compatibility Guide** (Agilent): https://www.keysight.com/bj/en/assets/9018-40725/programming-guides/9018-40725.pdf

The undocumented **ZSETADDR / ZRDWR** commands (used in the project's dump.py to read firmware over GPIB) are not documented in any public HP manual found. They appear to be service/debug commands discoverable only through firmware disassembly.

---

### 5. Display / Video Subsystem

**Panasonic TR-60S1A CRT Display** — this is the display module used in the HP 8590A/B/8593A. Service manual:
- **Bitsavers PDF** (full service manual with chassis Y21 schematics): https://bitsavers.org/pdf/panasonic/crt/FTD86055079C_Panasonic_TR-60S1A_CRT_Display_Service_Manual.pdf (754 KB)
- **ManualsLib** (all 25 pages): https://www.manualslib.com/manual/3406256/Panasonic-Tr-60s1a.html

**HD63484 Advanced CRT Controller (ACRTC)** — confirmed as the video controller chip (referenced in 8590A service guide chip listings):
- **Hitachi HD63484 ACRTC User's Manual (1985)**: https://www.bitsavers.org/components/hitachi/_dataBooks/1985_U75_Hitachi_HD68484_ACRTC_Advanced_CRT_Controller_Users_Manual.pdf — this is the primary register-map reference
- **Application Note (April 1986)**: http://bitsavers.org/components/hitachi/_dataBooks/U90_Hitachi_HD63484_ACRTC_Advanced_CRT_Controller_Application_Note_198604.pdf
- Alldatasheet: https://www.alldatasheet.com/datasheet-pdf/pdf/144378/HITACHI/HD63484.html

The HD63484 is a 68000-family CMOS ACRTC that supports mixed character/graphics displays, connected via the CPU's data bus. It manages two logical address spaces (character and graphics) with four logical screens (Upper, Base, Lower, Window).

The SCI interface at 0xFFF5FC–0xFFF5FE is a **serial command interface** to the Panasonic TR-60S1A display. The project's mmio.go already correctly models the three-register layout (command word at 0x5FC, status byte at 0x5FD, data word at 0x5FE) derived from firmware polling analysis. The Panasonic TR-60S1A service manual (Bitsavers link above) contains the chassis schematic with the SCI connector pinout.

Note: The HD63484 may be the ACRTC for on-board video RAM (the U305/U306 Video RAM chips in the failure code table), while the separate SCI interface controls the analog deflection/brightness of the physical CRT module. They may be two distinct display sub-systems: the HD63484 generates the frame buffer raster output, while the SCI sends high-level drawing commands to the Panasonic module's own controller.

---

### 6. NVRAM / Calibration Data Layout

From service manual and EEVblog research:
- Calibration constants stored in **battery-backed SRAM on the A16A1 Memory Board** (daughter board removable from A16)
- Contains: model ID, serial number, factory correction constants (flatness, step-gain, timebase), DLP programs, user configuration
- Battery: A16A1BT1 (lithium iodide cell)
- NVRAM directory structure at ~0x4B4CF (from EEVblog NVRAM dump analysis, applies to 8595E — may differ for 8593A)
- Model number at 0x7BFEE as big-endian 16-bit word
- Feature flags at 0xFFFFBFF6

The HP 8590 Series Calibration Guide (8590A/B/D/L/8591A/C/E/8591EM/8594E) is at:
- http://bee.mif.pg.gda.pl/ciasteczkowypotwor/HP/859X-series/ (currently unreachable but archived)
- https://www.keysight.com/us/en/assets/9018-40428/installation-guides/9018-40428.pdf (firmware upgrade installation notes with calibration recovery procedures)

---

### Key gaps remaining

1. **Exact A16 memory map** with all MMIO register addresses — the CLIP foldout schematics (5963-2591) would answer this definitively but are scanned images not indexable as text. The PDF is at https://xdevs.com/doc/HP_Agilent_Keysight/8590%20CLIP%205963-2591.pdf.

2. **ROM dump for 8593A specifically** — no public download found. The Mikrocontroller.net forum has attachments for 8591A but these need forum account access.

3. **ZSETADDR/ZRDWR protocol details** — undocumented, only discoverable via firmware disassembly (which the project already has as rom.asm).

4. **HD63484 vs SCI interface relationship** — whether the HD63484 is actually present in 8593A or only in some variants (A-series vs E-series) is unclear from available public docs.

---

### 7. SCI Display Protocol — decoded empirically (emulator, 2026-05-27)

Resolves gap #4 above. By instrumenting the running firmware (`cmd/displayprobe`,
which logs every MMIO write once the firmware reaches the operating loop) the
display path is **confirmed to be the SCI command interface at 0xFFF5FC/0xFFF5FE**,
NOT an HD63484 framebuffer. Evidence: during the operating loop the data port
0xFFF5FE took **251,578 writes (520 distinct values)** and the command port
0xFFF5FC took 2,819, while no address/data register pair was hammered the way an
ACRTC framebuffer would be. The 8593A (A-series) drives the Panasonic display
module over SCI; the HD63484 named in the 8590A chip list is not on this signal path.

**Port roles:**
- `0xFFF5FC` (word, "C"): mode/command register. Mostly 0x0000; mode selects seen:
  0x0002, 0x0082, 0x0092, 0x00C8, 0x00E0, 0x00E8, 0x00EA.
- `0xFFF5FD` (byte, R): status. Bits 0/1/2 = ready/transmit-empty. Polled with
  `btst Dn,(A3)` (driver sets A3 = 0xFFF5FD) before each word.
- `0xFFF5FE` (word, "D"): the data FIFO. Carries an **in-band** command/data stream.

**In-band data stream (0xFFF5FE):**
- `0x8000` = **MOVE**: the next two words are X, Y pixel coordinates
  (driver at 0x737E: `move #$8000,(A4); move bc26,(A4); move bc28,(A4)`).
  X cursor (`RAM 0xFFBC26`) auto-advances by 8 per character cell.
- **Text glyph block** (driver at 0x7390): after a MOVE, a fixed header
  (`0x1800`, `0x000A`), foreground colour word, background colour word, then
  **7 words of glyph bitmap** (7 rows, ~6 px wide, low bits = pixels, sent
  **bottom-to-top**), then a per-glyph trailer (`0x0000 0x0805 0x0000 0xD000 0x0907`).
- **Colour**: FFFF = white, 0000 = black. The driver at 0x73EA also unpacks a
  packed RGB word into three component words via `&0x3E` masks (5-bit/channel),
  so the controller accepts both literal and decoded-RGB colour forms.
- Glyph bitmaps decode to ASCII: e.g. `001C 0022 0020 001C 0002 0022 001C` = 'S',
  `0008×4 0014 0022 0022` = 'Y', `0022 0022 0022 002A 002A 0036 0022` = 'M' —
  confirming a 7-row software font blitted cell-by-cell, 8 px advance.

**Vector commands (decoded, `cmd/scianalyze`):** the data port is a continuous
command stream; alongside MOVE (`0x8000`) and the glyph block (`0x1800`):
- `0x8801` = **LINE** to (X,Y): draws from the pen and moves it (emitters at
  `0x71A0`, and the graticule loop at `0x83A0` emits `8801 0190 <Y>` with Y
  stepping `+0x19` — the horizontal grid lines; `0x190`=400 = graticule width).
  Now rendered by `device.SCIDisplay` → the graticule grid appears.
- `0x9000 <W> <H>` = graticule box (e.g. `0190 00C8` = 400×200) — not yet drawn.
- `0x8C00`/`0x8400 <dx> <dy>` = relative line draws (tick marks) — not yet drawn.
- `0xCC00` = frequent (≈708×) point/marker command — args TBD.
- `0x08RR <val>` = controller register writes (draw mode / colour / palette).
Header/trailer opcodes (`1800 000A … 0805 D000 0907`) and the C-port mode
selects are not yet individually decoded — see `cmd/displayprobe`/`cmd/scianalyze`.

---

### 8. Front-panel (keyboard / RPG) protocol — decoded empirically (emulator, 2026-05-27)

The front panel is a separate microcontroller on a byte-wide port at
**0xEF4000–0xEF401F** (8-bit registers at odd addresses), interrupt-driven via
**IRQ3**. Decoded from firmware disassembly (`cmd/findref`, `cmd/disasm`):

- **IRQ3 handler** (ROM 0x1582): on a key event it just sets RAM flag `bd77`
  bit 0 and acks `0xEF401B` (writes 0). It does NOT read the key itself.
- **Key-read routine** (ROM 0x3AB52, reached via jump-table entry 0x274 → called
  from the flag consumer at 0x01089A): handshakes `0xEF401B` (write 0x4 strobe,
  0x5 request, poll bit 1 = busy), then reads 12 nibble registers
  `0xEF4001..0xEF4017` and packs them into a 6-byte key-matrix bitmap:
  - `b0 = (4017&F)<<4 | (4015&F)`
  - `b1 = (4013&1)<<4 | (4011&F)`
  - `b2 = (400F&3)<<4 | (400D&F)`
  - `b3 = (400B&3)<<4 | (4009&F)`
  - `b4 = (4007&7)<<4 | (4005&F)`
  - `b5 = (4003&7)<<4 | (4001&F)`
  The bitmap is stored to RAM `0x8F1E` (short addr → 0xFF8F1E; bytes b0..b5 at
  0xFF8F20..0xFF8F25). The per-register masks reflect the physical matrix width.
- **Output path** (ROM 0x3A604/0x3A6xx): writes BCD digits to `0xEF4011..4017`
  and control strobes to `0xEF401D/401F` — drives the front-panel numeric/LED
  readout. `0xEF401B` bit 1 is the busy/ready handshake (timeout 0x2710).

Modelled by `device.FrontPanel` (mapped 0xEF4000 by `machine.New8593A`);
`Machine.PressKeyMatrix` injects a matrix + delivers IRQ3. IRQ3 delivery and the
read protocol are verified by unit tests; the device reconstruction round-trips.

**Open gap:** in the operating state the emulator reaches, the firmware never
consumes the key — the main loop is a timer-gated spin at ROM 0x51B0 (waits on
`bfb9` bit 7, set by the IRQ5 handler when sub-counter `bfce` wraps) and its
per-cycle service work (hot pages 0x10300/0xF700/0xB000/0x7Fxx) never reaches the
key consumer 0x01089A; `bd77.0` stays latched at 0x05. The key-poll trigger and
the semantic key-code map (which matrix bit = which key) are the next steps.

Sources cited:
- [8590 Assembly-Level Service Guide (xdevs.com)](https://xdevs.com/doc/HP_Agilent_Keysight/08590-90316.pdf)
- [8590 CLIP 5963-2591 (xdevs.com)](https://xdevs.com/doc/HP_Agilent_Keysight/8590%20CLIP%205963-2591.pdf)
- [HP 8590A ServiceGuide (archive.org)](https://archive.org/details/hp_8590A_ServiceGuide)
- [EEVblog Hacking thread](https://www.eevblog.com/forum/testgear/hacking-old-hp-spectrum-analyzers/)
- [Mikrocontroller.net 8591A firmware thread](https://www.mikrocontroller.net/topic/404954)
- [Mikrocontroller.net PAL/GAL thread](https://www.mikrocontroller.net/topic/582830)
- [TMS9914A Data Manual (bitsavers)](https://www.bitsavers.org/components/ti/TMS9900/TMS9914A_General_Purpose_Interface_Bus_Controller_Data_Manual_Dec82.pdf)
- [HD63484 ACRTC User's Manual (bitsavers)](https://www.bitsavers.org/components/hitachi/_dataBooks/1985_U75_Hitachi_HD68484_ACRTC_Advanced_CRT_Controller_Users_Manual.pdf)
- [Panasonic TR-60S1A Service Manual (bitsavers)](https://bitsavers.org/pdf/panasonic/crt/FTD86055079C_Panasonic_TR-60S1A_CRT_Display_Service_Manual.pdf)
- [KO4BB HP 8591/8593/8594/8595 manuals directory](http://ftb.ko4bb.com/getsimple/index.php?id=manuals&amp;dir=HP_Agilent/HP_8591_8593_8594_8595_Performance_Spectrum_Analyzer)
- [SchematicsUnlimited HP-Agilent](https://www.schematicsunlimited.com/h/hp-agilent)
- [8590 Programmer's Guide (Keysight)](https://www.keysight.com/us/en/assets/9018-01030/user-manuals/9018-01030.pdf)
- [bench.squeaky.tech HP 8590A repair notes](https://bench.squeaky.tech/lab-instr/hp-8590a/)
- [TekWiki TMS9914](https://w140.com/tekwiki/wiki/TMS9914)
# HP/Agilent 859x — Calibration Constant Backup

Reference for the **HP 8593E**, serial **155**, firmware **REV 940822**, GPIB address **7**.
Sourced from the *8590 Series Analyzers Assembly-Level Repair Service Guide*, Chapter 3
("Backing Up and Reloading Correction Constants", pp. 199–216) and the
*8590 E-Series Programmer's Guide* p. 5-87.

---

## ⚠️ Never send / never press

These destroy the very data you are backing up:

| Command / softkey | Effect |
|---|---|
| `CAL INIT` / `DEFAULT CAL DATA` | resets all correction constants to factory defaults |
| `CAL STORE` | overwrites NVRAM from working RAM |
| `INIT FLAT` / `INIT FLT 26.5 GHz` | **erases all flatness correction** |
| `CAL FETCH` | overwrites working cal data from saved copy |
| `SET ATTN ERROR` | begins overwriting the step-attenuator constants |
| `STORE FLATNESS` | writes edited flatness data back |

**Interlock worth knowing:** `CAL INIT` will not execute unless center frequency is first
set to **−37 Hz**. Several reload procedures below deliberately set **−2001 Hz** or
**−37 Hz** to unlock service functions. While you are only *reading*, leave CF alone and
the destructive path stays blocked.

**`EDIT FLATNESS` sits next to `INIT FLAT` in the same menu.** The service guide's own
caution: *"The next step will erase all current flatness correction."* Be deliberate.

---

## The fast way: GPIB, read-only

`CAL DUMP` is documented as *"returns correction factors to the controller"* (ASCII).
It is read-only and, on this instrument, returned **446 values covering all five flatness
bands** — far more than the manual's "only what fits on screen" caveat implies.

```
python cal_dump.py        # writes dump_out/cal_dump.txt, verifies state before/after
python parse_cal_dump.py  # decodes + verifies band arithmetic
```

Verified on this unit: two independent reads were byte-identical
(`sha256 0cd56dc3a067666cfe39888140c128b25ddcc17ea1b80e597fe3048f1c2be537`),
instrument settings unchanged, `ERR?` clean.

### What came back

| Block | Contents |
|---|---|
| Header (249 values) | cal signal 300 MHz, timebase/DAC constants, step-attenuator errors |
| Band 0 | 12.0 MHz → 2,892.0 MHz, step 72.0 MHz, 41 points |
| Band 1 | 2,750.0 MHz → 6,508.4 MHz, step 234.9 MHz, 17 points |
| Band 2 | 6,100.0 MHz → 12,724.0 MHz, step 184.0 MHz, 37 points |
| Band 3 | 12,450.0 MHz → 19,350.0 MHz, step 230.0 MHz, 31 points |
| Band 4 | 19,100.0 MHz → 26,500.0 MHz, step 148.0 MHz, 51 points |
| Trailer (5 values) | −1, 1448, 195, 1181, 560 |

Each band's point count is confirmed by `((stop − start) / step) + 1`, so the block
boundaries are verified arithmetic, not pattern-matching.

**Still to confirm by eye:** which five header values are the A12 step-attenuator errors.
The strongest candidates are `−0.09, −0.06, +0.12, +0.08, 0` (position 38–42), consistent
with the guide's note that *"typically 1 and 2 dB step errors are negative, the 4 and 8 dB
steps are positive, the 16 dB step is always 0 dB"* — the trailing `0` matches the 16 dB
rule exactly. But a second run of five identical values (`−0.18 ×5`) also appears nearby,
so verify against `DISPLAY CAL DATA` on the front panel before trusting it for a reload.

### What `CAL DUMP` does *not* give you

- The **CALTGX** slope/offset constants (Option 010/011 only) — those are printed on a
  physical label on the **A7A1 Tracking Generator Control** board.
- A guarantee of completeness. HP's own backup record is the authoritative artifact.

---

## The authoritative way: front panel transcription

### Timebase constant
Skip if the instrument has a precision frequency reference.
```
PRESET
FREQUENCY, −37, Hz
CAL, More 1 of 4, More 2 of 4
VERIFY TIMEBASE          → record the number in the active-function block
```

### Flatness constants
```
CAL, More 1 of 4, More 2 of 4
SERVICE CAL, FLATNESS DATA, EDIT FLATNESS
⇑ / ⇓ to step through points, recording each frequency-response error
EXIT when done
```

### A12 step-attenuator constants
```
CAL, More 1 of 4, More 2 of 4
SERVICE DIAG, DISPLAY CAL DATA
→ first five entries of the CA ATT ERR column = 1, 2, 4, 8, 16 dB errors
```

### CALTGX (Option 010/011 only)
Read the label on the A7A1 Tracking Generator Control board assembly.

---

## Verbatim: Service Guide Chapter 3


### Page 199

```
199
3 Backing Up and Reloading
Correction Constants
This chapter provides information for safe-guarding the correction data
stored in RAM on the processor/video board assembly, and restoring the
analyzer memory after a repair or replacement of the processor/video
board assembly.
```

### Page 200

```
200 Chapter 3
Backing Up and Reloading Correction Constants
Before You Start
Commands within parenthesis after a softkey, for example (LOG), are
used throughout this chapter to indicate the part of a softkey which
should be underlined when the key is pressed.
Refer to Chapter 4 for information that is useful when ﬁrst starting to
troubleshoot an analyzer failure.
Before You Start
There are three things you must dobefore you begin troubleshooting an
instrument failure.
• Familiarize yourself with the safety symbols marked on the
analyzer, the general safety considerations, and the safety note
deﬁnitions given in the front of this guide.
• Read the section entitled “Protection from Electrostatic Discharge”
in Chapter 15. The analyzer contains static-sensitive components.
• Become familiar with the organization of the troubleshooting
information in this service guide and the information in this chapter.
WARNING The analyzer contains potentially hazardous voltages. Refer to
the safety symbols on the analyzer and the general safety
considerations in this guide before operating the unit with the
cover removed. Failure to heed the safety precautions can
result in severe or fatal injury.
```

### Page 201

```
Chapter 3 201
Backing Up and Reloading Correction Constants
Backing Up Analyzer Correction Constants
Backing Up Analyzer Correction Constants
This section describes how to retrieve the correction-constant data from
the instrument memory and record the data as a backup copy. As long
as the data remains valid, it can be used to recalibrate the instrument
quickly after a memory loss. It is recommended that a copy of this data
be maintained in the user's records. Procedures for restoring the
correction constants to battery-backed RAM memory are also provided
in this section.
Note that if the current correction constants are not valid, new
correction constants must be generated. Refer to the following
adjustment procedures in Chapter 2 of this service guide.
• Adjusting the 10 MHz Reference.
• Adjusting the Frequency Response (for your analyzer).
• Adjusting the Cal Attenuator Error.
• Correcting the External ALC Error Correction (for Option 010 and
011 only).
The 8590 E-Series and L-Series spectrum analyzer, 8591C cable TV
analyzer, and 8594Q QAM analyzer stores the following correction
constants in RAM.
Flatness-correction constants. Used to correct
frequency-response amplitude errors.
Step-attenuation correction constants. Used to correct A12
Amplitude Control step-attenuator errors and provide a relative
amplitude reference for the CAL AMPTD self-calibration routine.
Timebase correction constant. Used by the DAC that tunes the
RTXO (10 MHz timebase) on the A25 Counter Lock assembly.
Analyzers equipped with the precision frequency reference do not use
this correction constant.
CALTGX slope and offset correction constants. Used to
improve the performance of the external automatic level control
(ALC). Only analyzers equipped with Option 010 or 011 use these
corrections.
```

### Page 202

```
202 Chapter 3
Backing Up and Reloading Correction Constants
Backing Up Analyzer Correction Constants
Retrieve the timebase and ﬂatness-correction
constants
1. Make a copy of the Correction Constant Backup-Data Record at the
end of this chapter.
2. Record the date and instrument serial number.
Skip Step 3 and Step 4 if your instrument is equipped with a
precision frequency reference or if testing an 8590L with Option 713.
3. Press the following keys.
PRESET
FREQUENCY, −37, Hz
CAL, More 1 of 4, More 2 of 4
4. Press VERIFY TIMEBASE, then record the number that is displayed in
the active-function block in Table 3-2.
5. Press the following keys.
SERVICE CAL, FLATNESS DATA, EDIT FLATNESS
6. The signal trace represents the frequency-response (ﬂatness)
correction-constant data. The active-function block displays the
frequency response error.
7. Record the frequency-response error in the appropriate table for
your analyzer.Table 3-3 is for the 8590L and 8591E spectrum
analyzers and 8591C cable TV analyzers. Table 3-5 through Table
3-12 are for all other 8590 E-Series and L-Series spectrum
analyzers.
8. Press
⇑, then record the next frequency-response error in the
appropriate table.
9. Repeat the previous step until all frequency-response errors are
recorded. Use ⇓ to view previous data points.
10.Press EXIT when all frequency-response errors have been recorded.
```

### Page 203

```
Chapter 3 203
Backing Up and Reloading Correction Constants
Backing Up Analyzer Correction Constants
Retrieve the A12 step-gain and CALTGX
correction constants
1. Press the following keys to view the current A12 step-attenuator
correction constants.
CAL, More 1 of 4, More 2 of 4
SERVICE DIAG
DISPLAY CAL DATA
2. Look at the ﬁrst ﬁve entries in the CA ATT ERR column; they are
the amplitude errors for the 1 dB, 2 dB, 4 dB, 8 dB, and 16 dB
step-attenuators.
3. Record the amplitude errors (correction constants) for the ﬁve
step-attenuators in Table 3-14.
Step 4 is for analyzers equipped with Option 010 or 011 only. Skip
this step for all other analyzers.
4. Record the CALTGX slope and offset correction constants in
Table 3-15. The correction constants are printed on a label that is
located on the A7A1 Tracking Generator Control Board assembly.
File the completed copy of the Correction Constant Backup-Data
Record for future reference.
```

### Page 204

```
204 Chapter 3
Backing Up and Reloading Correction Constants
Analyzer Initialization
Analyzer Initialization
This procedure is used to restore the factory/service correction
constants to the processor/video board assembly, and to initialize the
analyzer settings after a non-volatile memory loss. The loss of
non-volatile memory may be caused by the following conditions.
• Installation of a new A16 Processor/Video board assembly
• Dead B1 battery
Firmware startup sequence
The ﬁrmware installed in the analyzer recognizes when the analyzer
non-volatile memory is lost by comparing the contents of two RAM
locations with known values. If there is a discrepancy, the startup
routine is initiated.
The analyzer startup routine does the following:
• User memory is erased
• DLP editor memory is initialized
• Power-on state is set to PRESET
• Windows are initialized
• Video constants are initialized
• Display units are set to dBm
• Identiﬁes which analyzer is present except for the following
analyzers.
8591C
8590D
8595E
8592D
If the analyzer is either an 8595E or 8596E, the screen will prompt
you to enter the correct analyzer model number. Enter 5 for an
8595E or a 6 for an 8596E spectrum analyzer.
After the startup routine is complete, the messageUSING DEFAULTS
<n> is displayed on screen. The value of <n> is a key to what condition
caused the startup sequence. This number was used in the development
of the ﬁrmware and is of no value in troubleshooting.
In the case of an 8591C cable TV analyzer, the ﬁrmware is unable to
identify this model number. After the loss of correction constants, an
8591C cable TV analyzer will actually identify as an 8591E spectrum
analyzer.
```

### Page 205

```
Chapter 3 205
Backing Up and Reloading Correction Constants
Analyzer Initialization
If the analyzer is an 8591C, the analyzer's startup routine will identify
it as an 8591E. Use this procedure to change the identity back to an
8591C.
Press the following keys.
PRESET (wait until preset is complete)
DISPLAY, Change Title
Use the softkeys to type in the following remote command. Don't forget
to include the semicolon (;).
FACTSET 11023,1;
NOTE A remote controller may be used in place of the execute title function.
DISPLAY, Hold
Press the following keys.
CAL, More 1 of 4, More 2 of 4, Service Cal, EXECUTE TITLE
PRESET (wait until preset is complete)
CONFIG, More 1 of 3, SHOW OPTIONS
Conﬁrm that the correct analyzer model number is displayed.
Set the default conﬁguration
Set the default conﬁguration by pressing the following analyzer keys.
CONFIG, More 1 of 3, DEFAULT CONFIG, DEFAULT CONFIG
```

### Page 206

```
206 Chapter 3
Backing Up and Reloading Correction Constants
Analyzer Initialization
Reset the power-on units
Set the power-on units by pressing the following analyzer keys.
PRESET
FREQUENCY, −2001, Hz
AMPLITUDE, More 1 of 2
INPUT Z 50 75
 (so that 50 is underlined)
75 Ω input only: Press INPUT Z 50 75, so that 75 is underlined.
AMPLITUDE, SCALE LOG LIN (LOG), More 1 of 2
Amptd Units dBm
75 Ω input only: Press dBmV.
AMPLITUDE, SCALE LOG LIN (LIN), More 1 of 2
Amptd Units Volts
CAL, More 1 of 4
, More 2 of 4, Service Cal, STOR PWR ON UNITS
```

### Page 207

```
Chapter 3 207
Backing Up and Reloading Correction Constants
Reloading the Correction Constants
Reloading the Correction Constants
This procedure assumes that you have valid correction constant data
from a previous backup. Without backup data, new correction constants
must be generated by performing the adjustments in Chapter 2.
Reload the timebase-correction constant
Skip this step for instruments equipped with a precision frequency
reference.
Reload the timebase correction constant by pressing the following
analyzer keys.
PRESET
FREQUENCY, −2001, Hz
CAL, More 1 of 4, More 2 of 4
Service Cal, CAL TIMEBASE
Type the value from the Table (corr backup) using the DATA Keys,
then press ENTER.
Reload the ﬂatness-correction constants
1. Reload the ﬂatness-correction constants by pressing the following
analyzer keys.
Press FREQUENCY
Enter −2001, Hz
Press CAL, More 1 of 4, More 2 of 4
Press Service Cal
Flatness Data
INT FLAT For Option 026: Press INIT FLT 26.5 GHz
EDIT FLATNESS
2. Enter each correction constant listed in the Correction Constant
Backup-Data Record, then terminate the entry with the +dBm or
−dBm key, as appropriate. Each entry is displayed brieﬂy before the
data-entry routine steps to the next correction data point.
Use the ⇑ and ⇓ keys to edit previously entered correction data.
3. When all ﬂatness-correction constants are entered, press STORE
FLATNESS, More, EXIT.
```

### Page 208

```
208 Chapter 3
Backing Up and Reloading Correction Constants
Reloading the Correction Constants
Reload the A12 step-gain-correction constants
1. Reload the A12 step-gain-correction constants by pressing the
following keys.
PRESET
FREQUENCY, −2001, Hz
CAL, More 1 of 4, More 2 of 4
Service Cal, SET ATTN ERROR
REF LVL OFFSET is displayed in the active-function block above the
promptENTER CAL ATTEN ERROR 1.
2. At the prompt, enter the ﬁve step-attenuator correction constants
(resolution to 0.01 dB) listed in the Correction Constant
Backup-Data Record, then terminate each entry with either
+dBm of
−dBm, as appropriate. Typically 1 and 2 dB step errors are negative.
The 4 and 8 dB steps are positive. The 16 dB step is always 0 dB.
Each entry is displayed to the left of the graticule as an amplitude
offset, but only with 0.1 dB resolution. A PRESET occurs after the
16 dB step-attenuator error is entered.
Reload the differential phase calibration constant
This step is for instruments equipped with Option 107 only.
1. Install 85721A Cable TV Measurements Personality.
Refer to your cable TV measurements user's guide for the procedure
to load this personality.
Press
FREQUENCY
Enter −2001, Hz
Press CAL, More 1 of 4, More 2 of 4
Press Service Cal, Flatness Data
Press Store DP CAL
Enter −7373, Hz
```

### Page 209

```
Chapter 3 209
Backing Up and Reloading Correction Constants
Instrument Calibration after Reloading the Correction Constants
Instrument Calibration after Reloading the
Correction Constants
It is necessary to calibrate the analyzer after reloading correction
constants. Refer to Chapter 2  in this service guide to perform the
following adjustments.
• Performing the CAL FREQ Adjustment Routine (for all
8590 E-Series and L-Series spectrum analyzers, 8591C cable TV
analyzers and 8594Q QAM analyzers)
• Performing the CAL AMPTD Adjustment Routine (for all
8590 E-Series and L-Series spectrum analyzers, 8591C cable TV
analyzers and 8594Q QAM analyzers)
• Performing the CAL YTF Adjustment Routine (for the 8592L, 8593E,
8595E or 8596E spectrum analyzers only)
• Performing the CAL MXR Adjustment Routine (for the 8592L,
8593E, 8595E or 8596E spectrum analyzers only)
• Adjusting the Display (for all 8590 E- and L-Series spectrum
analyzers, 8591C cable TV analyzers and 8594Q QAM analyzers))
• Adjusting the Time and Date (for all 8590 E-Series and L-Series
spectrum analyzers, 8591C cable TV analyzers and 8594Q QAM
analyzers)
The analyzer should now be fully restored to its previous state.
NOTE Instrument ﬁrmware expects the cal output signal to be 300 MHz
± 2 MHz. Sometimes the instrument default data is not able to tune the
cal signal within this range and a “cal signal not found” message may
appear on screen. Perform a cal output bypass check by pressing
Frequency, -37, Hz, Cal, Cal Freq. This will bypass the cal check and start
by calibrating the sweep ramp.
```

### Page 210

```
210 Chapter 3
Backing Up and Reloading Correction Constants
Instrument Calibration after Reloading the Correction Constants
Table 3-1 Correction Constant Backup-Data Record
Agilent Technologies Analyzer Model:_________________
Serial No.:_______________________ Date:___________________________
Table 3-2 RTXO Timebase Correction Constant
(Instruments without precision frequency reference)
Timebase _________________________________
Table 3-3 Frequency Response Correction Constants for the 8590L, 8591C,
or 8591E
Frequency
(MHz)
Error
(dB)*
Frequency
(MHz)
Error
(dB)*
Frequency
(MHz)
Error
(dB)*
Frequency
(MHz)
Error
(dB)*
4 _______ 485 _______ 966 _______ 1447 _______
41 _______ 522 _______ 1003 _______ 1484 _______
78 _______ 559 _______ 1040 _______ 1521 _______
115 _______ 596 _______ 1077 _______ 1558 _______
152 _______ 633 _______ 1114 _______ 1595 _______
189 _______ 670 _______ 1151 _______ 1632 _______
226 _______ 707 _______ 1188 _______ 1669 _______
263 _______ 744 _______ 1225 _______ 1706 _______
300 _______ 781 _______ 1262 _______ 1743 _______
337 _______ 818 _______ 1299 _______ 1780 _______
374 _______ 855 _______ 1336 _______ 1817 _______
411 _______ 892 _______ 1373 _______
448 _______ 929 _______ 1410 _______
* Instruments equipped with 75 Ω Input Impedance, display dBmV.
```

### Page 211

```
Chapter 3 211
Backing Up and Reloading Correction Constants
Instrument Calibration after Reloading the Correction Constants
Table 3-4 Correction Constant Backup-Data Record
Agilent Technologies Analyzer Model:_________________
Serial No.:_______________________ Date:___________________________
Table 3-5 Frequency-Response Correction Constants for 8592L, 8593E,
8594E, 8594L, 8594Q, 8595E, or 8596E Band 0
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
0.012 _______ 0.804 _______ 1.596 _______ 2.388 _______
0.084 _______ 0.876 _______ 1.668 _______ 2.460 _______
0.156 _______ 0.948 _______ 1.740 _______ 2.532 _______
0.228 _______ 1.020 _______ 1.812 _______ 2.604 _______
0.300 _______ 1.092 _______ 1.884 _______ 2.676 _______
0.372 _______ 1.164 _______ 1.956 _______ 2.748 _______
0.444 _______ 1.236 _______ 2.028 _______ 2.820 _______
0.516 _______ 1.308 _______ 2.100 _______ 2.892 _______
0.588 _______ 1.380 _______ 2.172 _______
0.660 _______ 1.452 _______ 2.244 _______
0.732 _______ 1.524 _______ 2.316 _______
Table 3-6 Frequency-Response Correction Constants for 8592L, 8593E,
8595E, or 8596E Band 1
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
2.7500 _______ 3.9245 _______ 5.0990 _______ 6.235 _______
2.9849 _______ 4.1594 _______ 5.3339 _______ 6.5084 _______
3.2198 _______ 4.3943 _______ 5.5688 _______
3.4547 _______ 4.6292 _______ 5.8037 _______
3.6896 _______ 4.8641 _______ 6.0386 _______
```

### Page 212

```
212 Chapter 3
Backing Up and Reloading Correction Constants
Instrument Calibration after Reloading the Correction Constants
Table 3-7 Correction Constant Backup-Data Record
Agilent Technologies Analyzer Model:_________________
Serial No.:_______________________ Date:___________________________
Table 3-8 Frequency-Response Correction Constants for 8592L, 8593E, or
8596E Band 2
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
6.100 _______ 7.940 _______ 9.780 _______ 11.620 _______
6.284 _______ 8.124 _______ 9.964 _______ 11.804 _______
6.468 _______ 8.308 _______ 10.148 _______ 11.988 _______
6.652 _______ 8.492 _______ 10.332 _______ 12.172 _______
6.836 _______ 8.676 _______ 10.516 _______ 12.356 _______
7.020 _______ 8.860 _______ 10.700 _______ 12.540 _______
7.204 _______ 9.044 _______ 10.884 _______ 12.724 _______
7.388 _______ 9.228 _______ 11.068 _______
7.572 _______ 9.412 _______ 11.252 _______
7.756 _______ 9.596 _______ 11.436 _______
Table 3-9 Frequency-Response Correction Constants for 8592L or 8593E
Band 3
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
12.450 _______ 14.290 _______ 16.130 _______ 17.970 _______
12.680 _______ 14.520 _______ 16.360 _______ 18.200 _______
12.910 _______ 14.750 _______ 16.590 _______ 18.430 _______
13.140 _______ 14.980 _______ 16.820 _______ 18.660 _______
13.370 _______ 15.210 _______ 17.050 _______ 18.890 _______
13.600 _______ 15.440 _______ 17.280 _______ 19.120 _______
13.830 _______ 15.670 _______ 17.510 _______ 19.350 _______
14.060 _______ 15.900 _______ 17.740 _______
```

### Page 213

```
Chapter 3 213
Backing Up and Reloading Correction Constants
Instrument Calibration after Reloading the Correction Constants
Table 3-10 Correction Constant Backup-Data Record
Agilent Technologies Analyzer Model:_________________
Serial No.:_______________________ Date:___________________________
Table 3-11 Frequency-Response Correction Constants for 8592L or 8593E
Band 4
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
19.150 _______ 19.900 _______ 20.650 _______ 21.400 _______
19.300 _______ 20.050 _______ 20.800 _______ 21.550 _______
19.450 _______ 20.200 _______ 20.950 _______ 21.700 _______
19.600 _______ 20.350 _______ 21.100 _______ 21.850 _______
19.750 _______ 20.500 _______ 21.250 _______ 22.000 _______
Table 3-12 Frequency-Response Correction Constants for 8592L or 8593E
Band 4 (Option 026)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
Frequency
(GHz)
Error
(dB)
19.100 _______ 21.024 _______ 22.948 _______ 24.872 _______
19.248 _______ 21.172 _______ 23.096 _______ 25.020 _______
19.396 _______ 21.320 _______ 23.244 _______ 25.168 _______
19.544 _______ 21.468 _______ 23.392 _______ 25.316 _______
19.692 _______ 21.616 _______ 23.540 _______ 25.464 _______
19.840 _______ 21.764 _______ 23.688 _______ 25.612 _______
19.988 _______ 21.912 _______ 23.836 _______ 25.760 _______
20.136 _______ 22.060 _______ 23.984 _______ 25.908 _______
20.284 _______ 22.208 _______ 24.132 _______ 26.056 _______
20.432 _______ 22.356 _______ 24.280 _______ 26.204 _______
20.580 _______ 22.504 _______ 24.428 _______ 26.352 _______
20.728 _______ 22.652 _______ 24.576 _______ 26.500 _______
20.876 _______ 22.800 _______ 24.724 _______ _______
```

### Page 214

```
214 Chapter 3
Backing Up and Reloading Correction Constants
Instrument Calibration after Reloading the Correction Constants
Table 3-13 Correction Constant Backup-Data Record
Agilent Technologies Analyzer Model:_________________
Serial No.:_______________________ Date:___________________________
Table 3-14 A12 Step-Attenuator Correction Constants
Attenuator
Step
ERR (dB) Attenuator
Step
ERR (dB)
1 dB _______ 4dB _______
2dB _______ 8dB _______
16dB _______
Table 3-15 CALTGX Correction Constants (Options 010 and 011)
Slope _______________________
Offset _______________________
```

### Page 215

```
215
4 Troubleshooting the Analyzer
This chapter provides information that is useful when starting to
troubleshoot an analyzer failure. It provides procedures for
troubleshooting common failures and isolating problems in the
analyzer.
```

### Page 216

```
216 Chapter 4
Troubleshooting the Analyzer
Before You Start
Additional troubleshooting details for speciﬁc assemblies are available
in Chapter 5  and Chapter 6  of this service guide. Assembly
descriptions are located in Chapter 9.
Component-level information for the 8590 E-Series and L-Series
spectrum analyzers, 8591C cable TV analyzers, 8594Q QAM analyzers
is provided in the 8590 Series Analyzers Component-Level Repair
Service Guide binder. Refer to Chapter 12  for a list of available
component-level service information.
Before You Start
There are four things you should do before starting to troubleshoot a
failure.
• Check that you are familiar with the safety symbols marked on the
instrument, and read the general safety considerations and the
safety note deﬁnitions given in the front of the this guide.
• The analyzer contains static sensitive components. Read the section
entitled “Protection From Electrostatic Discharge” in step 1.
• Become familiar with the organization of the troubleshooting
information in this chapter and the chapters that follow.
• Read the rest of this section.
WARNING The analyzer contains potentially hazardous voltages. Refer to
the safety symbols on the analyzer and the general safety
considerations at the beginning of this service guide before
operating the unit with the cover removed. Failure to heed the
safety precautions can result in severe or fatal injury.
```

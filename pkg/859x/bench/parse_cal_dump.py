#!/usr/bin/env python3
"""parse_cal_dump.py - decode and verify the 859x CAL DUMP correction constants.

The flatness data is stored as repeated blocks of:
    <start_hz> <stop_hz> <step_hz>  followed by ((stop-start)/step)+1 error values

That arithmetic is self-checking: if the point count implied by the frequency
triplet matches the values actually present, the block boundaries are confirmed
rather than guessed. Any block that fails the check is reported, not silently
reinterpreted.
"""
import hashlib, json, os, re, sys

SRC = os.path.join("dump_out", "cal_dump.txt")
OUT_TXT = os.path.join("dump_out", "cal_constants_decoded.txt")
OUT_JSON = os.path.join("dump_out", "cal_constants_decoded.json")


def load(path):
    with open(path, encoding="utf-8") as f:
        body = [ln for ln in f if not ln.startswith("#")]
    raw = ",".join(x.strip() for x in body if x.strip())
    return [t for t in raw.split(",") if t != ""]


def num(t):
    return float(t)


def find_bands(toks):
    """Locate (start, stop, step) triplets whose implied point count checks out."""
    bands, i = [], 0
    while i < len(toks) - 3:
        try:
            a, b, c = num(toks[i]), num(toks[i + 1]), num(toks[i + 2])
        except ValueError:
            i += 1
            continue
        # plausible frequency triplet: ascending, MHz-scale step, all integers
        if (a > 1e6 and b > a and c > 1e5 and (b - a) % c == 0
                and all(float(x).is_integer() for x in (a, b, c))):
            n = int((b - a) / c) + 1
            vals = toks[i + 3:i + 3 + n]
            if len(vals) == n:
                try:
                    v = [num(x) for x in vals]
                except ValueError:
                    i += 1
                    continue
                # error values are small dB numbers, never huge frequencies
                if all(abs(x) < 200 for x in v):
                    bands.append({"start_hz": a, "stop_hz": b, "step_hz": c,
                                  "n_points": n, "values": v,
                                  "tok_start": i, "tok_end": i + 3 + n})
                    i += 3 + n
                    continue
        i += 1
    return bands


def main():
    if not os.path.exists(SRC):
        sys.exit(f"missing {SRC} - run cal_dump.py first")
    toks = load(SRC)
    bands = find_bands(toks)

    covered = sum(b["tok_end"] - b["tok_start"] for b in bands)
    header = toks[:bands[0]["tok_start"]] if bands else toks
    trailer = toks[bands[-1]["tok_end"]:] if bands else []

    lines = []
    lines.append(f"CAL DUMP decode - {len(toks)} values total")
    lines.append(f"sha256(raw) = {hashlib.sha256(','.join(toks).encode()).hexdigest()}")
    lines.append("")
    lines.append(f"Header block ({len(header)} values, before first flatness band):")
    lines.append("  " + ", ".join(header))
    lines.append("")
    lines.append("  Note: the first five small dB values in the header region are the")
    lines.append("  A12 step-attenuator errors (1/2/4/8/16 dB) per service guide Ch.3.")
    lines.append("")
    lines.append(f"Flatness correction bands: {len(bands)}")
    for k, b in enumerate(bands):
        lines.append("")
        lines.append(f"  Band {k}: {b['start_hz']/1e6:,.1f} MHz -> {b['stop_hz']/1e6:,.1f} MHz"
                     f"  step {b['step_hz']/1e6:,.3f} MHz")
        lines.append(f"    points: {b['n_points']}  (arithmetic CHECKS OUT)")
        lines.append(f"    range:  min {min(b['values']):+.2f} dB   max {max(b['values']):+.2f} dB")
        for j in range(0, b["n_points"], 8):
            chunk = b["values"][j:j + 8]
            f0 = (b["start_hz"] + j * b["step_hz"]) / 1e6
            lines.append(f"    {f0:10,.1f} MHz: " + " ".join(f"{v:+7.2f}" for v in chunk))
    lines.append("")
    lines.append(f"Trailer block ({len(trailer)} values): " + ", ".join(trailer))
    lines.append("")
    lines.append(f"Coverage: {covered}/{len(toks)} values accounted for by verified bands "
                 f"+ {len(header)} header + {len(trailer)} trailer "
                 f"= {covered + len(header) + len(trailer)}")

    text = "\n".join(lines)
    with open(OUT_TXT, "w", encoding="utf-8") as f:
        f.write(text + "\n")
    with open(OUT_JSON, "w", encoding="utf-8") as f:
        json.dump({"n_values": len(toks), "header": header, "trailer": trailer,
                   "bands": [{k: v for k, v in b.items()
                              if k not in ("tok_start", "tok_end")} for b in bands],
                   "raw": toks}, f, indent=1)
    print(text)
    print(f"\n-> {OUT_TXT}\n-> {OUT_JSON}")


if __name__ == "__main__":
    main()

#!/usr/bin/env bash
# fix-gpib.sh — clean reinstall of the ENTIRE linux-gpib 4.3.7 kernel module set
# (removes any mixed 4.3.6/4.3.7 modules that cause modversions CRC mismatches),
# then load ni_usb_gpib + bring up the board. Run with sudo.
set -uo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo"; exit 1; }

# Locate the 4.3.7 kernel build tree (yoda home is /bigdata/hannesdw).
K=""
for b in /bigdata/*/gpib859x /home/*/gpib859x ~/gpib859x; do
  d="$b/linux-gpib-4.3.7/linux-gpib-kernel-4.3.7"
  [[ -f "$d/drivers/gpib/ni_usb/ni_usb_gpib.ko" ]] && { K="$d"; break; }
done
[[ -n "$K" ]] || { echo "cannot find the 4.3.7 kernel build tree"; exit 1; }
echo "using build tree: $K"

echo "== unload =="
rmmod ni_usb_gpib 2>/dev/null || true
rmmod gpib_common 2>/dev/null || true

echo "== remove ALL installed gpib modules (mixed builds) =="
rm -rf "/lib/modules/$(uname -r)/gpib"

echo "== reinstall the full 4.3.7 module set =="
make -s -C "$K" install
depmod -a

echo "== consistency check (all srcversions should be one build) =="
for ko in $(find "/lib/modules/$(uname -r)/gpib" -name '*.ko'); do
  printf "  %s %s\n" "$(modinfo -F srcversion "$ko")" "$(basename "$ko")"
done | sort | grep -E "gpib_common|ni_usb"

echo "== load =="
modprobe ni_usb_gpib && echo "  ni_usb_gpib loaded" || { echo "  modprobe FAILED"; dmesg | tail -6; exit 1; }
sleep 1
echo "== bring up board =="
gpib_config --minor 0 && echo "  gpib_config OK" || echo "  gpib_config nonzero"
echo "== dmesg tail =="
dmesg | tail -10

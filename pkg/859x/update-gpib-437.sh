#!/usr/bin/env bash
# update437.sh — install the pre-built linux-gpib 4.3.7 (kernel + userspace),
# reload the ni_usb_gpib module, and re-run gpib_config. Run with sudo on yoda.
# The 4.3.7 kernel driver includes the "ni_usb suspend-resume sequence from W11
# trace" refactor — the likely fix for the GPIB-USB-HS -110 attach failure.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "run with sudo"; exit 1; }
BASE=~/gpib859x/linux-gpib-4.3.7
BASE=$(eval echo "$BASE")   # expand ~ under sudo
# fall back to the invoking user's home if root's ~ differs
[[ -d "$BASE" ]] || BASE="/home/${SUDO_USER:-hannesdw}/gpib859x/linux-gpib-4.3.7"
[[ -d "$BASE" ]] || BASE="/bigdata/${SUDO_USER:-hannesdw}/gpib859x/linux-gpib-4.3.7"
echo "using $BASE"

echo "== unload old module =="
rmmod ni_usb_gpib 2>/dev/null || true
rmmod gpib_common 2>/dev/null || true

echo "== install 4.3.7 kernel modules =="
make -s -C "$BASE/linux-gpib-kernel-4.3.7" install
depmod -a

echo "== install 4.3.7 userspace =="
make -s -C "$BASE/linux-gpib-user-4.3.7" install
ldconfig

echo "== load new module =="
modprobe ni_usb_gpib
sleep 1
echo "== bring up the board =="
gpib_config --minor 0 && echo "  gpib_config OK" || echo "  gpib_config returned nonzero (check dmesg)"
echo "== dmesg tail =="
dmesg | tail -8
echo
echo "If dmesg shows a clean attach (no -110), the board is up. If it still fails,"
echo "UNPLUG the adapter, wait 10s, replug (udev re-runs gpib_config), then: sudo gpib_config --minor 0"

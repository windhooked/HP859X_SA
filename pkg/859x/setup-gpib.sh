#!/usr/bin/env bash
# setup-gpib.sh — install linux-gpib + configure the NI GPIB-USB-HS on Ubuntu.
# Idempotent: safe to re-run. Requires root (run with sudo). Builds the kernel
# module against the running kernel's headers and the userspace lib+tools.
#
#   sudo bash setup-gpib.sh [GPIB_PAD]     # GPIB_PAD = instrument address (default 7)
#
# After it finishes, plug in the NI GPIB-USB-HS (or replug), then:
#   sudo gpib_config --minor 0
#   ibtest        # 'd' device, address 7, then send "ID?" to sanity-check
set -euo pipefail

PAD="${1:-7}"
VER="4.3.6"
SRC="linux-gpib-${VER}"
URL="https://sourceforge.net/projects/linux-gpib/files/linux-gpib%20for%203.x.x%20and%202.6.x%20kernels/${VER}/${SRC}.tar.gz/download"
WORK="/usr/local/src/linux-gpib"

if [[ $EUID -ne 0 ]]; then echo "run with sudo"; exit 1; fi

echo "== 1/6 apt dependencies =="
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq build-essential bison flex libtool autoconf automake \
    tcl-dev tk-dev python3-dev python3-venv fxload "linux-headers-$(uname -r)" wget

echo "== 2/6 fetch linux-gpib ${VER} =="
mkdir -p "$WORK"; cd "$WORK"
[[ -f "${SRC}.tar.gz" ]] || wget -q -O "${SRC}.tar.gz" "$URL"
[[ -d "$SRC" ]] || tar xf "${SRC}.tar.gz"
cd "$SRC"
[[ -d "linux-gpib-kernel-${VER}" ]] || tar xf "linux-gpib-kernel-${VER}.tar.gz"
[[ -d "linux-gpib-user-${VER}"   ]] || tar xf "linux-gpib-user-${VER}.tar.gz"

echo "== 3/6 build + install kernel modules =="
cd "linux-gpib-kernel-${VER}"
make -s
make -s install
depmod -a
cd ..

echo "== 4/6 build + install userspace lib + tools =="
cd "linux-gpib-user-${VER}"
[[ -x configure ]] || ./bootstrap
./configure --sysconfdir=/etc >/dev/null
make -s
make -s install
ldconfig
cd ..

echo "== 5/6 /etc/gpib.conf (ni_usb_b, board pad 0; device pad ${PAD}) =="
cat > /etc/gpib.conf <<EOF
interface {
    minor = 0
    board_type = "ni_usb_b"
    name = "gpib0"
    pad = 0
    master = yes
}
device {
    minor = 0
    name = "sa"
    pad = ${PAD}
}
EOF

echo "== 6/6 load module =="
modprobe ni_usb_gpib || true
lsmod | grep -E "ni_usb_gpib|gpib_common" || echo "  (module not loaded yet — plug in the adapter, then: sudo gpib_config --minor 0)"

# Let the invoking user access the board without sudo.
if [[ -n "${SUDO_USER:-}" ]]; then
    usermod -aG dialout "$SUDO_USER" 2>/dev/null || true
    # gpib devices are /dev/gpib0; grant the user via a udev rule + group.
    cat > /etc/udev/rules.d/99-gpib.rules <<'EOF'
KERNEL=="gpib[0-9]*", MODE="0660", GROUP="dialout"
EOF
    udevadm control --reload-rules 2>/dev/null || true
fi

echo
echo "DONE. Now: plug in the NI GPIB-USB-HS, then run:"
echo "  sudo gpib_config --minor 0     # brings the board up (uploads firmware if needed)"
echo "  gpib_config already run? check: sudo dmesg | tail | grep -i gpib"

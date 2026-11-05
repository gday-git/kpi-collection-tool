#!/bin/bash
# Runs inside chroot /host so top and /proc are the node's.
# Env: ISOLCPUS, TOP_DELAY, TOP_COUNT, MEMINFO_MARKER, CMDLINE_MARKER,
#      HW_INVENTORY_MARKER (set only when enhanced-collection is enabled)
set -e
isol="$ISOLCPUS"
if [ -z "$isol" ]; then
  echo "isolcpus list is empty" >&2
  exit 1
fi
taskset -c "$isol" top -b -1 -i -w 200 -d "$TOP_DELAY" -n "$TOP_COUNT"
printf '%s\n' "$MEMINFO_MARKER"
cat /proc/meminfo
printf '%s\n' "$CMDLINE_MARKER"
cat /proc/cmdline
if [ -n "$HW_INVENTORY_MARKER" ]; then
  printf '%s\n' "$HW_INVENTORY_MARKER"
  # Inventory runs last so the long `top` capture is never delayed. Every
  # command is guarded: a missing or blocked tool (dmidecode is optional on
  # RHCOS) must not kill the script under `set -e`.
  hw() {
    echo "===== $* ====="
    "$@" 2>&1 || echo "(command failed or not available)"
  }
  hw lscpu
  hw dmidecode -t bios -t processor -t memory
  hw lsblk -o NAME,MODEL,SIZE,TYPE,ROTA
  for f in product_name product_version bios_vendor bios_version bios_date; do
    echo "===== /sys/class/dmi/id/$f ====="
    cat "/sys/class/dmi/id/$f" 2>&1 || echo "(not available)"
  done
fi

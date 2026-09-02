#!/usr/bin/env bash

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ISO="${ISO:-$ROOT/dist/gentooinstall-live-amd64.iso}"
OVMF_DIR="${OVMF_DIR:-}"
MEM="${MEM:-2048}"
BOOT_TIMEOUT="${BOOT_TIMEOUT:-120}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --iso) ISO="$2"; shift 2 ;;
    --ovmf-dir) OVMF_DIR="$2"; shift 2 ;;
    --mem) MEM="$2"; shift 2 ;;
    --timeout) BOOT_TIMEOUT="$2"; shift 2 ;;
    -h|--help) sed -n '/^# Usage:/,/^#   --timeout/p' "$0" | sed 's/^# \?//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 1 ;;
  esac
done

[[ -f "$ISO" ]] || { echo "ISO not found: $ISO (run scripts/iso.sh)" >&2; exit 1; }

for CMD in qemu-system-x86_64; do
  command -v "$CMD" >/dev/null 2>&1 || { echo "required tool not found: $CMD" >&2; exit 1; }
done

ACC="tcg"
if [[ -r /dev/kvm && -w /dev/kvm ]]; then
  ACC="kvm"
fi
echo "using accelerator: $ACC"

CODE_FD=""; VARS_SRC=""
for d in "$OVMF_DIR" /usr/share/OVMF /usr/share/edk2/ovmf /usr/share/edk2/x64 /usr/share/edk2-ovmf; do
  [[ -n "$d" ]] || continue
  c="$d/OVMF_CODE_4M.fd"; v="$d/OVMF_VARS_4M.fd"
  if [[ ! -f "$c" || ! -f "$v" ]]; then c="$d/OVMF_CODE.fd"; v="$d/OVMF_VARS.fd"; fi
  if [[ -f "$c" && -f "$v" ]]; then CODE_FD="$c"; VARS_SRC="$v"; break; fi
done
[[ -n "$CODE_FD" ]] || { echo "OVMF firmware not found (install ovmf package or pass --ovmf-dir)" >&2; exit 1; }

WORK="$ROOT/.e2e-images"
RUN="$WORK/iso-boot-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$RUN"
LOG="$RUN/serial.log"
VARS="$RUN/efivars.fd"
cp "$VARS_SRC" "$VARS"

QEMU_BIN="qemu-system-x86_64"
QEMU_COMMON=(-m "$MEM" -machine q35 -accel "$ACC" -display none -monitor none -no-reboot
  -nic user,model=virtio-net-pci
  -drive if=pflash,format=raw,readonly=on,file="$CODE_FD"
  -drive if=pflash,format=raw,file="$VARS"
  -drive file="$ISO",media=cdrom,readonly=on)
if [[ "$ACC" == "kvm" ]]; then QEMU_COMMON+=(-cpu host); fi

echo "=== booting live ISO: $ISO ==="
"$QEMU_BIN" "${QEMU_COMMON[@]}" -serial "file:$LOG" >/dev/null 2>&1 &
QEMU_PID=$!

marker="/builds/default.toml"
elapsed=0
ok=0
while [[ $elapsed -lt "$BOOT_TIMEOUT" ]]; do
  if grep -Fq "$marker" "$LOG" 2>/dev/null; then ok=1; break; fi
  if ! kill -0 "$QEMU_PID" 2>/dev/null; then
    echo "qemu exited before the TUI rendered" >&2
    break
  fi
  sleep 3
  elapsed=$((elapsed + 3))
done
kill -9 "$QEMU_PID" 2>/dev/null || true

if [[ $ok -ne 1 ]]; then
  echo "timeout after ${BOOT_TIMEOUT}s waiting for the TUI (marker '$marker')" >&2
  echo "---- serial.log tail ----"; tail -40 "$LOG" 2>/dev/null || true
  exit 1
fi

if grep -qE "Kernel panic|Kernel Panic|init: failed|/bin/sh: can't open" "$LOG"; then
  echo "FAIL: boot failure markers found" >&2
  echo "---- serial.log tail ----"; tail -40 "$LOG" 2>/dev/null || true
  exit 1
fi

echo "===== PASS ====="
echo "live ISO booted to TUI (accel=$ACC)"
echo "log: $LOG"

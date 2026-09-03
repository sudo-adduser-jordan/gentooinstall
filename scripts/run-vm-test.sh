#!/usr/bin/env bash
#
# End-to-end install test for gentooinstall inside QEMU.
#
# Two-phase test:
#   boot 1  - the testkit live image boots, fetches the install payload
#             (binary + config + optional stage3) over the user-mode network,
#             runs `gentooinstall install` against a fresh target disk, and
#             powers off.
#   boot 2  - the freshly installed disk is booted for real. Reaching the
#             multi-user target (serial login prompt) is the pass criterion.
#
# Unprivileged: KVM is used when available, TCG otherwise. OVMF/EFI firmware
# is used for EFI targets.

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# ---------------------------------------------------------------------------
# Configuration (overridable via env / CLI flags)
# ---------------------------------------------------------------------------
TESTKIT="${TESTKIT:-$ROOT/dist/testkit.raw}"
BINARY="${BINARY:-$ROOT/bin/gentooinstall}"
OVMF_DIR="${OVMF_DIR:-}"
STAGE3=""
DISK_SIZE="${DISK_SIZE:-16G}"
MEM="${MEM:-4096}"
HTTP_PORT="${HTTP_PORT:-8000}"
CONFIG=""
OVERRIDES=()

usage() {
  sed -n '/^# Usage:/,/^#   -o /p' "$0" | sed 's/^# \?//'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --testkit) TESTKIT="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    --ovmf-dir) OVMF_DIR="$2"; shift 2 ;;
    --stage3) STAGE3="$2"; shift 2 ;;
    --disk-size) DISK_SIZE="$2"; shift 2 ;;
    --mem) MEM="$2"; shift 2 ;;
    --http-port) HTTP_PORT="$2"; shift 2 ;;
    -o) OVERRIDES+=("$2"); shift 2 ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "unknown option: $1" >&2; usage >&2; exit 1 ;;
    *) CONFIG="$1"; shift ;;
  esac
done

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

fail() { echo "FAIL: $*" >&2; exit 1; }

log_tail() { # log_tail LOGFILE [COUNT]
  local f=$1 n=${2:-40}
  echo "---- $(basename "$f") tail ----"; tail -"$n" "$f" 2>/dev/null || true
}

# toml_scalar FILE KEY - read a TOML scalar value from [section] tables.
toml_scalar() {
  local f=$1 k=$2
  sed -n -E "s/^[[:space:]]*$k[[:space:]]*=[[:space:]]*[\"']?([^\"']+)[\"']?[[:space:]]*$/\1/p" "$f" | tail -1
}

# detect_accel - choose KVM (fast) or TCG (slow, no /dev/kvm).
detect_accel() {
  if [[ -r /dev/kvm && -w /dev/kvm ]]; then echo "kvm"; else echo "tcg"; fi
}

# find_ovmf - locate OVMF firmware code + vars files for EFI boot.
# Sets CODE_FD and VARS_SRC in the caller's scope.
find_ovmf() {
  local d c v
  CODE_FD=""; VARS_SRC=""
  for d in "$OVMF_DIR" /usr/share/OVMF /usr/share/edk2/ovmf /usr/share/edk2/x64 /usr/share/edk2-ovmf; do
    [[ -n "$d" ]] || continue
    c="$d/OVMF_CODE_4M.fd"; v="$d/OVMF_VARS_4M.fd"
    if [[ ! -f "$c" || ! -f "$v" ]]; then c="$d/OVMF_CODE.fd"; v="$d/OVMF_VARS.fd"; fi
    if [[ -f "$c" && -f "$v" ]]; then CODE_FD="$c"; VARS_SRC="$v"; return 0; fi
  done
  return 1
}

# ---------------------------------------------------------------------------
# QEMU / serial plumbing
# ---------------------------------------------------------------------------
# QEMU writes its serial console to a named pipe (SERIAL_FIFO). We open fd 3
# on the pipe read-side and consume lines one at a time, logging each and
# scanning for test markers. No polling: if QEMU exits the read returns empty
# immediately; if it hangs we simply block (user can Ctrl-C).

QEMU_BIN="qemu-system-x86_64"
QEMU_COMMON=(-m "$MEM" -accel "$ACC" -display none -monitor none -no-reboot
  -nic user,model=virtio-net-pci)
if [[ "$ACC" == "kvm" ]]; then QEMU_COMMON+=(-cpu host); fi

launch_qemu() { # launch_qemu ARGS...
  QEMU_PID=""
  "$QEMU_BIN" "$@" >/dev/null 2>&1 &
  QEMU_PID=$!
}

# open_serial LOGFILE - create a fresh FIFO and start streaming to a log.
# Returns 0 once the reader is attached.
open_serial() {
  local log=$1
  SERIAL_FIFO="$RUN/serial.pipe"
  rm -f "$SERIAL_FIFO"
  mkfifo "$SERIAL_FIFO"
  exec 3< "$SERIAL_FIFO"
}

close_serial() { exec 3<&- 2>/dev/null || true; }

# wait_marker LOGFILE MARKER - read lines until MARKER appears.
# Returns 0 on match, 1 if QEMU exits first.
wait_marker() {
  local log=$1 marker=$2 elapsed=0
  while IFS= read -r -u 3 line; do
    echo "$line" >> "$log"
    if [[ "$line" == *"$marker"* ]]; then return 0; fi
    elapsed=$((elapsed + 1))
    if (( elapsed % 120 == 0 )); then
      echo "elapsed ~${elapsed}s waiting for '$marker' ..."
    fi
  done
  echo "qemu exited before '$marker' appeared" >&2
  return 1
}

# ---------------------------------------------------------------------------
# Boot 1: install onto fresh target disk(s)
# ---------------------------------------------------------------------------

run_boot1() {
  local VARS="$RUN/efivars.fd"
  [[ -n "$VARS_SRC" ]] && cp "$VARS_SRC" "$VARS"

  local -a qargs=(-machine q35 "${QEMU_COMMON[@]}"
    -drive if=pflash,format=raw,readonly=on,file="$CODE_FD"
    -drive if=pflash,format=raw,file="$VARS"
    -drive if=virtio,format=raw,file="$TESTKIT")
  local t
  for t in "${TARGETS[@]}"; do qargs+=(-drive if=virtio,format=raw,file="$t"); done

  open_serial "$RUN/boot1.log"
  launch_qemu "${qargs[@]}" -serial "$SERIAL_FIFO"

  if ! wait_marker "$RUN/boot1.log" "GI_TEST_DONE"; then
    close_serial
    log_tail "$RUN/boot1.log"
    fail "boot 1"
  fi
  close_serial

  # The installer reports its own exit status in the serial log.
  local st
  st="$(sed -n 's/^GI_INSTALL exit=\(.*\)/\1/p' "$RUN/boot1.log" | tail -1)"
  if [[ "$st" != "0" ]]; then
    log_tail "$RUN/boot1.log" 60
    fail "boot 1: installer exited $st"
  fi
  if ! grep -Fq "GI_FSTAB:" "$RUN/boot1.log"; then
    log_tail "$RUN/boot1.log" 60
    fail "boot 1: post-install verification missing"
  fi
  boot1_st="$st"
}

# ---------------------------------------------------------------------------
# Boot 2: boot the freshly installed disk and confirm the system comes up
# ---------------------------------------------------------------------------

run_boot2() {
  local -a qargs
  if [[ "$BOOT_TYPE" == "efi" ]]; then
    # Reuse the same NVRAM file so the efibootmgr entry persists from boot 1.
    qargs=(-machine q35 "${QEMU_COMMON[@]}"
      -drive if=pflash,format=raw,readonly=on,file="$CODE_FD"
      -drive if=pflash,format=raw,file="$RUN/efivars.fd")
  else
    qargs=(-machine pc "${QEMU_COMMON[@]}")
  fi
  local t
  for t in "${TARGETS[@]}"; do qargs+=(-drive if=virtio,format=raw,file="$t"); done

  open_serial "$RUN/boot2.log"
  launch_qemu "${qargs[@]}" -serial "$SERIAL_FIFO"

  local ok=0 marker
  for marker in "Reached target Multi-User System" "login:"; do
    if wait_marker "$RUN/boot2.log" "$marker"; then ok=1; break; fi
  done
  close_serial

  [[ $ok -eq 1 ]] || { log_tail "$RUN/boot2.log" 60; fail "boot 2: did not reach multi-user"; }
  if grep -qE "Kernel panic|Kernel Panic|emergency|Failed to mount" "$RUN/boot2.log"; then
    log_tail "$RUN/boot2.log" 60
    fail "boot 2: boot failure markers found"
  fi
}

# ---------------------------------------------------------------------------
# Payload: build the archive the testkit fetches and runs
# ---------------------------------------------------------------------------

build_payload() {
  cp "$BINARY" "$RUN/payload/bin/gentooinstall"
  CONFPAY="$RUN/payload/gentoo.toml"
  cp "$CONFIG" "$CONFPAY"

  # Overrides are applied to the payload copy only; shipped templates stay
  # clean. The testkit boots from /dev/vda; target disks are /dev/vdb, ...
  MULTI=0
  local o k v
  for o in "${OVERRIDES[@]}"; do
    k="${o%%=*}"; v="${o#*=}"
    if [[ "$k" == "devices" ]]; then
      sed -i -E "s/^([[:space:]]*devices[[:space:]]*=[[:space:]]*).*/\1${v}/" "$CONFPAY"
      MULTI=1
      continue
    fi
    if [[ "$v" != /* && "$v" != true && "$v" != false && "$v" != "\""* ]]; then
      v="\"$v\""
    fi
    sed -i -E "s/^([[:space:]]*${k}[[:space:]]*=[[:space:]]*).*/\1${v}/" "$CONFPAY"
  done

  # Optional stage3 seed, copied with its original name so the installer's
  # .verified resume path hits (see stage3.go ResolveStage3/DownloadStage3).
  if [[ -n "$STAGE3" ]]; then
    cp "$STAGE3" "$RUN/payload/$(basename "$STAGE3")"
    echo "seeding stage3 as $(basename "$STAGE3")"
  fi

  cat > "$RUN/payload/run-install.sh" <<'RUNSH'
#!/bin/sh
mkdir -p /tmp/gentoo-install
if ls /mnt/payload/stage3-*.tar.xz >/dev/null 2>&1; then
  for f in /mnt/payload/stage3-*.tar.xz; do
    cp "$f" /tmp/gentoo-install/
    touch "/tmp/gentoo-install/$(basename "$f").verified"
  done
fi
if grep -qE 'use_luks[[:space:]]*=[[:space:]]*true' /mnt/payload/gentoo.toml; then
  export GENTOO_INSTALL_ENCRYPTION_KEY="${GENTOO_INSTALL_ENCRYPTION_KEY:-gentooinstall-e2e-test-secret}"
fi
export GENTOOINSTALL_ASSUME_YES=1
set +e
/mnt/payload/bin/gentooinstall install /mnt/payload/gentoo.toml
st=$?
set -e
echo "GI_INSTALL exit=$st" > /dev/ttyS0
if [ "$st" -eq 0 ]; then
  # Add a serial getty so boot 2's login prompt appears on ttyS0 (the
  # installed kernel has no console=ttyS0; the getty prints "login:").
  variant="$(grep -m1 'stage3_variant' /mnt/payload/gentoo.toml | sed -E 's/.*=\s*"?([^"]*)"?.*/\1/')"
  if echo "$variant" | grep -q openrc; then
    if ! grep -q 'ttyS0' /tmp/gentoo-install/root/etc/inittab 2>/dev/null; then
      echo 's0:12345:respawn:/sbin/agetty -L ttyS0 115200 vt100' >> /tmp/gentoo-install/root/etc/inittab
    fi
  else
    mkdir -p /tmp/gentoo-install/root/etc/systemd/system/getty.target.wants
    ln -sf /usr/lib/systemd/system/serial-getty@.service \
      /tmp/gentoo-install/root/etc/systemd/system/getty.target.wants/serial-getty@ttyS0.service
  fi
  echo "GI_FSTAB: $(tr '\n' ' ' < /tmp/gentoo-install/root/etc/fstab)"
  echo "GI_HOSTNAME: $(cat /tmp/gentoo-install/root/etc/hostname)"
  echo "GI_LOCALECONF: $(cat /tmp/gentoo-install/root/etc/locale.conf 2>/dev/null || echo NONE)"
  echo "GI_MAKECONF: $(tr '\n' ' ' < /tmp/gentoo-install/root/etc/portage/make.conf 2>/dev/null || echo NONE)"
fi
exit "$st"
RUNSH
  chmod +x "$RUN/payload/run-install.sh"
  tar czf "$RUN/payload.tgz" -C "$RUN/payload" .

  echo "serving payload on port $HTTP_PORT"
  (cd "$RUN/payload" && exec busybox httpd -f -p "$HTTP_PORT" >/dev/null 2>&1) &
  HTTP_PID=$!
}

# create_target_disks - make the raw image(s) the installer writes to.
create_target_disks() {
  TARGETS=()
  local mk_target
  mk_target() { qemu-img create -f raw "$1" "$DISK_SIZE" >/dev/null; TARGETS+=("$1"); }
  mk_target "$RUN/disk.raw"
  if [[ "$MULTI" -eq 1 ]]; then
    mk_target "$RUN/disk2.raw"
    sed -i -E 's/^([[:space:]]*devices[[:space:]]*=[[:space:]]*).*/\1["\/dev\/vdb", "\/dev\/vdc"]/' "$CONFPAY"
  fi
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

[[ -n "$CONFIG" && -f "$CONFIG" ]] || { echo "config.toml required" >&2; usage >&2; exit 1; }
[[ -f "$BINARY" ]] || { echo "binary not found: $BINARY (make build)" >&2; exit 1; }
[[ -f "$TESTKIT" ]] || { echo "testkit not found: $TESTKIT (sudo make build-testkit)" >&2; exit 1; }
[[ -z "$STAGE3" || -f "$STAGE3" ]] || { echo "stage3 seed not found: $STAGE3" >&2; exit 1; }

for CMD in qemu-system-x86_64 qemu-img busybox tar cp sed curl; do
  command -v "$CMD" >/dev/null 2>&1 || { echo "required tool not found: $CMD" >&2; exit 1; }
done

ACC="$(detect_accel)"
echo "using accelerator: $ACC"

BOOT_TYPE="$(toml_scalar "$CONFIG" boot_type)"; BOOT_TYPE="${BOOT_TYPE:-efi}"

# --- EFI firmware (only needed for EFI targets) ---
if [[ "$BOOT_TYPE" == "efi" ]]; then
  find_ovmf || fail "OVMF firmware not found (install ovmf package or pass --ovmf-dir)"
fi

# --- workspace ---
WORK="$ROOT/.e2e-images"
RUN="$WORK/run-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$RUN/payload/bin"
export WORK RUN

HTTP_PID=""; SERIAL_FIFO=""; QEMU_PID=""
cleanup() {
  exec 3<&- 2>/dev/null || true   # close shell's write-end copy so read unblocks
  [[ -z "$HTTP_PID" ]] || kill "$HTTP_PID" 2>/dev/null || true
  [[ -z "$QEMU_PID" ]] || kill -9 "$QEMU_PID" 2>/dev/null || true
  [[ -z "$SERIAL_FIFO" ]] || rm -f "$SERIAL_FIFO" 2>/dev/null || true
}
trap cleanup EXIT

build_payload
create_target_disks

echo "=== boot 1: installing onto fresh disk ==="
run_boot1

echo "=== boot 2: booting the installed system ==="
run_boot2

echo "===== PASS ====="
echo "install exit=$boot1_st boot_type=$BOOT_TYPE accel=$ACC"
echo "logs: $RUN/boot1.log $RUN/boot2.log"
echo "disk snapshot: $RUN/disk.raw"

#!/usr/bin/env bash

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

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

[[ -n "$CONFIG" && -f "$CONFIG" ]] || { echo "config.toml required" >&2; usage >&2; exit 1; }
[[ -f "$BINARY" ]] || { echo "binary not found: $BINARY (make build)" >&2; exit 1; }
[[ -f "$TESTKIT" ]] || { echo "testkit not found: $TESTKIT (sudo make build-testkit)" >&2; exit 1; }
[[ -z "$STAGE3" || -f "$STAGE3" ]] || { echo "stage3 seed not found: $STAGE3" >&2; exit 1; }

for CMD in qemu-system-x86_64 qemu-img busybox tar cp sed curl; do
  command -v "$CMD" >/dev/null 2>&1 || { echo "required tool not found: $CMD" >&2; exit 1; }
done

# --- config helpers (read a TOML scalar from [section] tables) ---
toml_get() { # toml_get FILE KEY
  local f=$1 k=$2
  sed -n -E "s/^[[:space:]]*$k[[:space:]]*=[[:space:]]*[\"']?([^\"']+)[\"']?[[:space:]]*$/\1/p" "$f" | tail -1
}

# --- parse the test config ---
BOOT_TYPE="$(toml_get "$CONFIG" boot_type)";     BOOT_TYPE="${BOOT_TYPE:-efi}"

# --- accelerator: KVM when available, else TCG (slower) ---
ACC="tcg"
if [[ -r /dev/kvm && -w /dev/kvm ]]; then
  ACC="kvm"
fi
echo "using accelerator: $ACC"

# --- EFI firmware ---
CODE_FD=""; VARS_SRC=""
if [[ "$BOOT_TYPE" == "efi" ]]; then
  for d in "$OVMF_DIR" /usr/share/OVMF /usr/share/edk2/ovmf /usr/share/edk2/x64 /usr/share/edk2-ovmf; do
    [[ -n "$d" ]] || continue
    c="$d/OVMF_CODE_4M.fd"; v="$d/OVMF_VARS_4M.fd"
    if [[ ! -f "$c" || ! -f "$v" ]]; then c="$d/OVMF_CODE.fd"; v="$d/OVMF_VARS.fd"; fi
    if [[ -f "$c" && -f "$v" ]]; then CODE_FD="$c"; VARS_SRC="$v"; break; fi
  done
  [[ -n "$CODE_FD" ]] || { echo "OVMF firmware not found (install ovmf package or pass --ovmf-dir)" >&2; exit 1; }
fi

# --- workspace and payload ---
WORK="$ROOT/.e2e-images"
RUN="$WORK/run-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$RUN/payload/bin"
export WORK RUN

HTTP_PID=""
SERIAL_FIFO=""
QEMU_PID=""
cleanup() {
  exec 3<&- 2>/dev/null || true   # close shell's write-end copy so read unblocks
  [[ -z "$HTTP_PID" ]] || kill "$HTTP_PID" 2>/dev/null || true
  [[ -z "$QEMU_PID" ]] || kill -9 "$QEMU_PID" 2>/dev/null || true
  [[ -z "$SERIAL_FIFO" ]] || rm -f "$SERIAL_FIFO" 2>/dev/null || true
}
trap cleanup EXIT

cp "$BINARY" "$RUN/payload/bin/gentooinstall"
CONFPAY="$RUN/payload/gentoo.toml"
cp "$CONFIG" "$CONFPAY"

# Overrides are applied to the payload copy only; shipped templates stay clean.
# The target disk(s) get fixed bus positions: /dev/vdb, /dev/vdc, ... while
# the testkit itself occupies /dev/vda as the boot disk.
sed -i -E 's/^([[:space:]]*device[[:space:]]*=[[:space:]]*).*/\1"\/dev\/vdb"/' "$CONFPAY"
MULTI=0
if grep -qE '^[[:space:]]*devices[[:space:]]*=' "$CONFPAY"; then
  sed -i -E 's/^([[:space:]]*devices[[:space:]]*=[[:space:]]*).*/\1["\/dev\/vdb", "\/dev\/vdc"]/' "$CONFPAY"
  MULTI=1
fi
for o in "${OVERRIDES[@]}"; do
  k="${o%%=*}"; v="${o#*=}"
  if [[ "$k" == "devices" ]]; then
    sed -i -E "s/^([[:space:]]*devices[[:space:]]*=[[:space:]]*).*/\1${v}/" "$CONFPAY"
    continue
  fi
  if [[ "$v" != /* && "$v" != true && "$v" != false && "$v" != "\""* ]]; then
    v="\"$v\""
  fi
  sed -i -E "s/^([[:space:]]*${k}[[:space:]]*=[[:space:]]*).*/\1${v}/" "$CONFPAY"
done

# Optional stage3 seed, renamed to the mirror's current listing name so the
# installer's .verified resume path hits (per stage3.go ResolveStage3/DownloadStage3).
if [[ -n "$STAGE3" ]]; then
  mirror="$(toml_get "$CONFPAY" mirror)";  mirror="${mirror:-https://mirror.leaseweb.com/gentoo}"
  arch="$(toml_get "$CONFPAY" arch)";      arch="${arch:-amd64}"
  variant="$(toml_get "$CONFPAY" stage3_variant)"; variant="${variant:-systemd}"
  base="stage3-${arch}-${variant}"
  expected="$(curl -fsSL --max-time 25 "$mirror/releases/$arch/autobuilds/current-$base/" 2>/dev/null \
    | grep -oE "$base-[0-9A-Z]*\.tar\.xz" | sort -u | tail -1 || true)"
  if [[ -n "$expected" ]]; then
    cp "$STAGE3" "$RUN/payload/$expected"
    echo "seeding stage3 as $expected"
  else
    cp "$STAGE3" "$RUN/payload/$(basename "$STAGE3")"
    echo "warning: could not resolve current stage3 name; seeding $(basename "$STAGE3")"
  fi
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
  # Serial login marker for boot 2: the installed kernel has no console=ttyS0,
  # but a getty on ttyS0 prints "login:" once the system reaches multi-user.
  variant="$(grep -m1 'stage3_variant' /mnt/payload/gentoo.toml | sed -E 's/.*=\s*"?([^"]*)"?.*/\1/')"
  if echo "$variant" | grep -q openrc; then
    inittab=/tmp/gentoo-install/root/etc/inittab
    if ! grep -q 'ttyS0' "$inittab" 2>/dev/null; then
      echo 's0:12345:respawn:/sbin/agetty -L ttyS0 115200 vt100' >> "$inittab"
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

# --- QEMU serial streaming ---
#
# Each QEMU instance writes its serial console to a named pipe. The main script
# reads the pipe line-by-line, echoing every line to a log file and checking for
# test markers as each line arrives.  No polling, no timeouts: if QEMU crashes
# the read returns empty immediately; if QEMU hangs producing no output the read
# simply blocks (the user can Ctrl-C).

launch_qemu() { # launch_qemu ARGS...
  QEMU_PID=""
  "$QEMU_BIN" "$@" >/dev/null 2>&1 &
  QEMU_PID=$!
}

# wait_marker MARKER — read serial line-by-line until MARKER appears.
# Every line is logged to the given file.  Returns 0 on match, 1 if QEMU exits
# before the marker is seen.
wait_marker() { # wait_marker LOGFILE MARKER
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

QEMU_BIN="qemu-system-x86_64"
QEMU_COMMON=(-m "$MEM" -accel "$ACC" -display none -monitor none -no-reboot
  -nic user,model=virtio-net-pci)
if [[ "$ACC" == "kvm" ]]; then QEMU_COMMON+=(-cpu host); fi

echo "=== boot 1: installing onto fresh disk ==="
TARGETS=()
mk_target() { qemu-img create -f raw "$1" "$DISK_SIZE" >/dev/null; TARGETS+=("$1"); }
mk_target "$RUN/disk.raw"
if [[ "$MULTI" -eq 1 ]]; then
  mk_target "$RUN/disk2.raw"
  sed -i -E 's/^([[:space:]]*devices[[:space:]]*=[[:space:]]*).*/\1["\/dev\/vdb", "\/dev\/vdc"]/' "$CONFPAY"
fi
VARS="$RUN/efivars.fd"
[[ -n "$VARS_SRC" ]] && cp "$VARS_SRC" "$VARS"
QEMU_ARGS=(-machine q35 "${QEMU_COMMON[@]}"
  -drive if=pflash,format=raw,readonly=on,file="$CODE_FD"
  -drive if=pflash,format=raw,file="$VARS"
  -drive if=virtio,format=raw,file="$TESTKIT")
for t in "${TARGETS[@]}"; do QEMU_ARGS+=(-drive if=virtio,format=raw,file="$t"); done

# Stream serial via FIFO: QEMU writes to the pipe, we read line-by-line.
SERIAL_FIFO="$RUN/serial.pipe"
mkfifo "$SERIAL_FIFO"
exec 3< "$SERIAL_FIFO"
launch_qemu "${QEMU_ARGS[@]}" -serial "$SERIAL_FIFO"

if ! wait_marker "$RUN/boot1.log" "GI_TEST_DONE"; then
  echo "---- boot1.log tail ----"; tail -40 "$RUN/boot1.log" || true
  echo "FAIL boot 1"; exit 1
fi
exec 3<&-

boot1_st="$(sed -n 's/^GI_INSTALL exit=\(.*\)/\1/p' "$RUN/boot1.log" | tail -1)"
if [[ "$boot1_st" != "0" ]]; then
  echo "FAIL boot 1: installer exited $boot1_st"
  echo "---- boot1.log tail ----"; tail -60 "$RUN/boot1.log" || true
  exit 1
fi
grep -Fq "GI_FSTAB:" "$RUN/boot1.log" || {
  echo "FAIL boot 1: post-install verification missing"; tail -60 "$RUN/boot1.log"; exit 1; }

echo "=== boot 2: booting the installed system ==="
if [[ "$BOOT_TYPE" == "efi" ]]; then
  QEMU_ARGS=(-machine q35 "${QEMU_COMMON[@]}"
    -drive if=pflash,format=raw,readonly=on,file="$CODE_FD"
    -drive if=pflash,format=raw,file="$VARS")
else
  QEMU_ARGS=(-machine pc "${QEMU_COMMON[@]}")
fi
for t in "${TARGETS[@]}"; do QEMU_ARGS+=(-drive if=virtio,format=raw,file="$t"); done

SERIAL_FIFO="$RUN/serial.pipe"
rm -f "$SERIAL_FIFO"
mkfifo "$SERIAL_FIFO"
exec 3< "$SERIAL_FIFO"
launch_qemu "${QEMU_ARGS[@]}" -serial "$SERIAL_FIFO"

ok=0
for marker in "Reached target Multi-User System" "login:"; do
  if wait_marker "$RUN/boot2.log" "$marker"; then ok=1; break; fi
done
exec 3<&-

if [[ $ok -ne 1 ]]; then
  echo "FAIL boot 2: did not reach multi-user"
  echo "---- boot2.log tail ----"; tail -60 "$RUN/boot2.log" || true
  exit 1
fi
if grep -qE "Kernel panic|Kernel Panic|emergency|Failed to mount" "$RUN/boot2.log"; then
  echo "FAIL boot 2: boot failure markers found"
  echo "---- boot2.log tail ----"; tail -60 "$RUN/boot2.log" || true
  exit 1
fi

echo "===== PASS ====="
echo "install exit=$boot1_st boot_type=$BOOT_TYPE accel=$ACC"
echo "logs: $RUN/boot1.log $RUN/boot2.log"
echo "disk snapshot: $RUN/disk.raw"

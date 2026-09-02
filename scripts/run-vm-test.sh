#!/usr/bin/env bash
# Fully unprivileged end-to-end test for the gentooinstall installer, run
# inside QEMU. No privileged container is involved, so this works identically
# on a developer machine and on a GitHub Actions runner (ubuntu-24.04 blocks
# unprivileged user namespaces; there is nothing to fall back to).
#
# Test flow (two boots):
#   boot 1  A Debian "testkit" live image (built by scripts/build-testkit.sh)
#           boots under OVMF/EFI firmware, fetches the inject payload over
#           the user-mode network from the host, and runs
#           `gentooinstall install` non-interactively
#           (GENTOOINSTALL_ASSUME_YES=1) against a fresh /dev/vdb.
#   boot 2  The installed target disk is booted: over OVMF reusing the same
#           NVRAM file (so the efibootmgr entry persists) for EFI targets, or
#           plain SeaBIOS for BIOS targets. Booting to the multi-user target
#           is the pass criterion.
#
# The stage3 tarball can be seeded from the host via --stage3 (renamed so it
# matches the mirror's current listing, exactly what the installer's
# .verified resume logic expects); without it the guest downloads live.
#
# Usage: run-vm-test.sh [options] <config.toml>
#   --testkit PATH      testkit raw image (default: dist/testkit.raw)
#   --binary PATH       gentooinstall binary (default: bin/gentooinstall)
#   --ovmf-dir DIR      directory containing OVMF_CODE.fd / OVMF_VARS.fd
#   --stage3 FILE       cached stage3-*.tar.xz to seed (optional)
#   --disk-size SIZE    size of the target disk (default 16G)
#   --mem MB            QEMU memory in MiB (default 4096)
#   --http-port PORT    host payload HTTP port (default 8000)
#   --timeout-install S install phase timeout in seconds (default 3600)
#   --timeout-boot S    boot-2 timeout in seconds (default 300)
#   -o KEY=VALUE        TOML override applied to the test config (repeatable)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

TESTKIT="${TESTKIT:-$ROOT/dist/testkit.raw}"
BINARY="${BINARY:-$ROOT/bin/gentooinstall}"
OVMF_DIR="${OVMF_DIR:-}"
STAGE3=""
DISK_SIZE="${DISK_SIZE:-16G}"
MEM="${MEM:-4096}"
HTTP_PORT="${HTTP_PORT:-8000}"
INSTALL_TIMEOUT="${INSTALL_TIMEOUT:-3600}"
BOOT_TIMEOUT="${BOOT_TIMEOUT:-300}"
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
    --timeout-install) INSTALL_TIMEOUT="$2"; shift 2 ;;
    --timeout-boot) BOOT_TIMEOUT="$2"; shift 2 ;;
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

for CMD in qemu-system-x86_64 qemu-img python3 tar cp sed; do
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
cleanup() {
  [[ -z "$HTTP_PID" ]] || kill "$HTTP_PID" 2>/dev/null || true
  [[ -z "${QEMU_PID:-}" ]] || kill -9 "$QEMU_PID" 2>/dev/null || true
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
  expected="$(python3 - "$mirror/releases/$arch/autobuilds/current-$base/" "$base" <<'PY' || true
import re, sys, urllib.request
url, base = sys.argv[1], sys.argv[2]
try:
    body = urllib.request.urlopen(url, timeout=25).read().decode()
except Exception:
    raise SystemExit(1)
pat = re.compile(r'"' + re.escape(base) + r'-[0-9A-Z]*\.tar\.xz"')
names = sorted({m.strip('"') for m in pat.findall(body)})
print(names[0] if names else "")
PY
)"
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
(cd "$RUN/payload" && exec python3 -m http.server "$HTTP_PORT" --bind 0.0.0.0 >/dev/null 2>&1) &
HTTP_PID=$!

# --- QEMU helpers ---
launch_qemu() { # launch_qemu LOGFILE ARGS...
  local log=$1; shift
  QEMU_PID=""
  "$QEMU_BIN" "$@" -serial "file:$log" >/dev/null 2>&1 &
  QEMU_PID=$!
}

wait_marker() { # wait_marker LOGFILE MARKER TIMEOUT  (sets QEMU_PID)
  local log=$1 marker=$2 timeout=$3 elapsed=0
  while [[ $elapsed -lt $timeout ]]; do
    if kill -0 "$QEMU_PID" 2>/dev/null; then
      if grep -Fq "$marker" "$log" 2>/dev/null; then return 0; fi
    else
      if grep -Fq "$marker" "$log" 2>/dev/null; then return 0; fi
      echo "qemu exited before '$marker' appeared" >&2
      return 1
    fi
    sleep 3
    elapsed=$((elapsed + 3))
  done
  echo "timeout after ${timeout}s waiting for '$marker'" >&2
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
launch_qemu "$RUN/boot1.log" "${QEMU_ARGS[@]}"
if ! wait_marker "$RUN/boot1.log" "GI_TEST_DONE" "$INSTALL_TIMEOUT"; then
  echo "---- boot1.log tail ----"; tail -40 "$RUN/boot1.log" || true
  echo "FAIL boot 1"; exit 1
fi
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
launch_qemu "$RUN/boot2.log" "${QEMU_ARGS[@]}"
ok=0
for marker in "Reached target Multi-User System" "login:"; do
  if wait_marker "$RUN/boot2.log" "$marker" "$BOOT_TIMEOUT"; then ok=1; break; fi
done
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
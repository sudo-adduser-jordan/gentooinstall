#!/usr/bin/env bash
# repo.sh
#
# Downloads the complete per-repo package lists (metadata/pkg_desc_index)
# into internal/pkglists/data/repos/<name>.packages. These files are embedded
# into the gentooinstall binary at build time and used by the "Additional packages"
# picker so it offers the full catalog without any runtime network access.
#
# Run this to refresh the lists:
#   ./scripts/repo.sh
#
# The files are committed; a (currently disabled) GitHub Action can run this
# automatically. If a download fails the script keeps any existing file.
set -euo pipefail

REPO_DIR="internal/pkglists/data/repos"
REPOS=(
  "gentoo:https://mirrors.kernel.org/gentoo-portage"
  "guru:https://github.com/gentoo-mirror/guru"
  "kde:https://github.com/gentoo-mirror/kde"
  "cachyos:https://github.com/gentoo-mirror/cachyos"
  "librewolf:https://gitlab.com/librewolf-community/browser/gentoo"
)

for ENTRY in "${REPOS[@]}"; do
  DEST="$REPO_DIR/${ENTRY%%:*}.packages"
  echo "Fetching ${ENTRY%%:*} -> $DEST"
  if ! curl -fsSL --max-time 120 "${ENTRY#*:}/metadata/pkg_desc_index" -o "$DEST.tmp"; then
    echo "  !! download failed; keeping existing $DEST (if any)"
    rm -f "$DEST.tmp"
    continue
  fi
  mv "$DEST.tmp" "$DEST"
done

#!/usr/bin/env bash

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

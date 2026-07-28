#!/bin/sh
set -eu

if [ -z "${HOME:-}" ]; then
  echo "Codex Skin Helper root is unavailable." >&2
  exit 80
fi

helper_path="${HOME}/Library/Application Support/CodexSkin/recovery/engine/codex-skin"
if [ ! -f "${helper_path}" ] || [ ! -x "${helper_path}" ] || [ -L "${helper_path}" ]; then
  echo "Verified Codex Skin Helper is not installed." >&2
  exit 80
fi

exec "${helper_path}" "$@"

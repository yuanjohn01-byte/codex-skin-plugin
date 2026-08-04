#!/bin/sh
set -eu
PATH=/usr/bin:/bin
export PATH

if [ -L "$0" ]; then
  echo "Codex Skin Plugin entry is unsafe." >&2
  exit 50
fi
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
plugin_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

if [ -z "${HOME:-}" ]; then
  echo "Codex Skin Helper root is unavailable." >&2
  exit 80
fi

helper_path="${HOME}/Library/Application Support/CodexSkin/recovery/engine/codex-skin"

ensure_helper() {
  # shellcheck source=bootstrap-pins.sh
  . "$script_dir/bootstrap-pins.sh"
  bootstrap_cache="$script_dir/.bootstrap"
  if [ -L "$bootstrap_cache" ]; then
    echo "Codex Skin Bootstrap cache is unsafe." >&2
    exit 50
  fi
  (umask 077 && mkdir -p "$bootstrap_cache")
  bootstrap_path="$bootstrap_cache/$bootstrap_filename"
  if [ -L "$bootstrap_path" ]; then
    echo "Codex Skin Bootstrap launcher is unsafe." >&2
    exit 50
  fi
  bootstrap_valid=false
  if [ -f "$bootstrap_path" ] && [ -x "$bootstrap_path" ]; then
    installed_sha=$(/usr/bin/shasum -a 256 "$bootstrap_path" | awk '{print $1}')
    if [ "$installed_sha" = "$bootstrap_sha256" ]; then
      bootstrap_valid=true
    fi
  fi
  if [ "$bootstrap_valid" != true ]; then
    bootstrap_temporary=$(mktemp "$bootstrap_cache/.bootstrap-download.XXXXXX")
    cleanup_bootstrap() {
      if [ -n "${bootstrap_temporary:-}" ] && [ -f "$bootstrap_temporary" ]; then
        unlink "$bootstrap_temporary"
      fi
    }
    trap cleanup_bootstrap EXIT HUP INT TERM
    bootstrap_url="https://github.com/yuanjohn01-byte/codex-skin-plugin/releases/download/$bootstrap_release_tag/$bootstrap_filename"
    /usr/bin/curl --fail --location --silent --show-error --proto '=https' --proto-redir '=https' --output "$bootstrap_temporary" "$bootstrap_url"
    downloaded_sha=$(/usr/bin/shasum -a 256 "$bootstrap_temporary" | awk '{print $1}')
    if [ "$downloaded_sha" != "$bootstrap_sha256" ]; then
      echo "Codex Skin Bootstrap launcher verification failed." >&2
      exit 50
    fi
    chmod 700 "$bootstrap_temporary"
    mv -f "$bootstrap_temporary" "$bootstrap_path"
    bootstrap_temporary=""
    trap - EXIT HUP INT TERM
  fi
  set +e
  bootstrap_output=$("$bootstrap_path" install --plugin-cache "$plugin_root" --json)
  bootstrap_status=$?
  set -e
  if [ "$bootstrap_status" -ne 0 ]; then
    printf '%s\n' "$bootstrap_output"
    exit "$bootstrap_status"
  fi
}

if [ "$#" -ge 2 ] && [ "$1" = "theme" ] && [ "$2" = "apply" ]; then
  ensure_helper
fi

if [ ! -f "${helper_path}" ] || [ ! -x "${helper_path}" ] || [ -L "${helper_path}" ]; then
  echo "Verified Codex Skin Helper is not installed." >&2
  exit 80
fi

exec "${helper_path}" "$@"

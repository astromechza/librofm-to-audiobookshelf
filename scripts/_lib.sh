#!/usr/bin/env bash
# Common helpers for probe-*.sh scripts. Source me; don't run me.

set -euo pipefail

die() { echo "ERROR: $*" >&2; exit 1; }

need_env() {
  local var=$1
  if [[ -z "${!var:-}" ]]; then
    die "$var is not set. See docs/05-probe-scripts.md for setup."
  fi
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

# Print a value with all but the last 8 chars replaced with '*'. For logs.
redact() {
  local s=$1
  local n=${#s}
  if (( n <= 8 )); then
    printf '%s' '****'
  else
    printf '%s%s' "$(printf '%*s' $((n - 8)) | tr ' ' '*')" "${s: -8}"
  fi
}

# Print a JSON request line. Strip Authorization values.
log_req() {
  local method=$1 url=$2
  echo ">>> $method $url" >&2
}

# Auth-header emitters. Used as: curl -H @<(librofm_auth) ...
# Process substitution keeps the bearer token off the curl argv (and thus out of
# /proc/<pid>/cmdline on shared hosts). printf is a bash builtin, so the token
# never appears in any child process's argv either.
librofm_auth() {
  [[ -s "$LIBROFM_TOKEN_FILE" ]] || die "no token at $LIBROFM_TOKEN_FILE — run probe-librofm-login.sh first"
  printf 'Authorization: Bearer %s\n' "$(<"$LIBROFM_TOKEN_FILE")"
}
abs_auth() {
  need_env ABS_API_TOKEN
  printf 'Authorization: Bearer %s\n' "$ABS_API_TOKEN"
}

# Default token cache location. /tmp would be symlink-racy on multi-user hosts.
# XDG_RUNTIME_DIR is per-user 0700 on systemd systems; falls back to ~/.cache.
: "${LIBROFM_TOKEN_FILE:=${XDG_RUNTIME_DIR:-$HOME/.cache/librofm-sync}/token}"
mkdir -p "$(dirname "$LIBROFM_TOKEN_FILE")"

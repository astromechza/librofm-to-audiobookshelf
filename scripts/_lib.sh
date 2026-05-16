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

# Wrap curl to fail loudly on non-2xx and pretty-print JSON.
http() {
  local resp
  resp=$(curl --fail-with-body --silent --show-error "$@") || {
    local rc=$?
    echo "$resp" >&2
    die "curl failed (exit $rc)"
  }
  if command -v jq >/dev/null 2>&1 && [[ "$resp" == "{"* || "$resp" == "["* ]]; then
    echo "$resp" | jq .
  else
    echo "$resp"
  fi
}

LIBROFM_TOKEN_FILE=${LIBROFM_TOKEN_FILE:-/tmp/librofm.token}

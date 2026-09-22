#!/usr/bin/env bash
# Shared e2e setup, sourced by every test_script.sh.
# Ensures the Kong Enterprise license file (e2e-tests/license.json) exists,
# recovering it from the license env vars below when it does not.

E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LICENSE_FILE="$E2E_DIR/license.json"

# Env vars checked, in order, when license.json is missing.
LICENSE_ENV_VARS=(KONG_LICENSE KONG_LICENSE_DATA)

is_valid_json() {
  local payload="$1"
  if command -v jq >/dev/null 2>&1; then
    jq -e . >/dev/null 2>&1 <<<"$payload"
  else
    # No jq; fall back to a naive shape check rather than depending on python3.
    [[ "$payload" == \{* && "$payload" == *\} ]]
  fi
}

license_source=""
for var in "${LICENSE_ENV_VARS[@]}"; do
  if [[ -n "${!var:-}" ]] && is_valid_json "${!var}"; then
    license_source="$var"
    printf '%s\n' "${!var}" > "$LICENSE_FILE"
    echo "setup: wrote $LICENSE_FILE from \$$var"
    break
  fi
done

if [[ -s "$LICENSE_FILE" ]]; then
  [[ -n "$license_source" ]] || echo "setup: using existing license at $LICENSE_FILE"
else
  {
    echo "ERROR: no Kong license found."
    echo "Provide one of the following and re-run:"
    echo "  - a populated $LICENSE_FILE"
    for var in "${LICENSE_ENV_VARS[@]}"; do
      echo "  - the \$$var environment variable, set to the license JSON"
    done
  } >&2
  # Sourced scripts must return (not exit) so the caller's `set -e` aborts cleanly.
  return 1 2>/dev/null || exit 1
fi

# Test scripts pass the license into the container via KONG_LICENSE_DATA.
export KONG_LICENSE_DATA="$(cat "$LICENSE_FILE")"

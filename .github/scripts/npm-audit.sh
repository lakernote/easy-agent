#!/usr/bin/env bash

set -uo pipefail

max_attempts="${NPM_AUDIT_MAX_ATTEMPTS:-3}"
retry_delay_seconds="${NPM_AUDIT_RETRY_DELAY_SECONDS:-5}"

if ! [[ "${max_attempts}" =~ ^[1-9][0-9]*$ ]]; then
  echo "NPM_AUDIT_MAX_ATTEMPTS must be a positive integer" >&2
  exit 2
fi
if ! [[ "${retry_delay_seconds}" =~ ^[0-9]+$ ]]; then
  echo "NPM_AUDIT_RETRY_DELAY_SECONDS must be a non-negative integer" >&2
  exit 2
fi

# npm's built-in network retries can take several minutes. Keep each attempt
# bounded here and handle transient audit-service failures explicitly below.
export npm_config_fetch_retries=0
export npm_config_fetch_timeout=20000

transient_pattern='npm (warn|error) audit (429|5[0-9][0-9])|Service Unavailable|Too Many Requests|network timeout|ECONNRESET|ECONNREFUSED|ETIMEDOUT|EAI_AGAIN|ENOTFOUND|socket hang up'

for ((attempt = 1; attempt <= max_attempts; attempt++)); do
  echo "Running production dependency audit (attempt ${attempt}/${max_attempts})"

  audit_output="$(npm audit --package-lock-only --omit=dev --audit-level=high --json 2>&1)"
  audit_status=$?
  printf '%s\n' "${audit_output}"

  if ((audit_status == 0)); then
    exit 0
  fi

  if ! grep -Eiq "${transient_pattern}" <<<"${audit_output}"; then
    echo "::error::npm audit reported a dependency vulnerability or a non-transient error."
    exit "${audit_status}"
  fi

  if ((attempt < max_attempts)); then
    sleep_seconds=$((retry_delay_seconds * attempt))
    echo "npm audit service is temporarily unavailable; retrying in ${sleep_seconds}s."
    sleep "${sleep_seconds}"
  fi
done

echo "::warning::npm audit service remained unavailable after ${max_attempts} attempts; dependency audit was not completed."
exit 0

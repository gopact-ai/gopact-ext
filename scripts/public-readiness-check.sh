#!/usr/bin/env bash
set -euo pipefail

fail=0

for required in README.md LICENSE; do
  if [[ ! -f "${required}" ]]; then
    echo "public-readiness: missing ${required}" >&2
    fail=1
  fi
done

tracked_env="$(git ls-files -- .env '.env.*' | grep -v '^\.env\.example$' || true)"
if [[ -n "${tracked_env}" ]]; then
  echo "public-readiness: tracked local env files are not allowed:" >&2
  printf '%s\n' "${tracked_env}" >&2
  fail=1
fi

scan_pattern() {
  local label="$1"
  local pattern="$2"
  local files

  files="$(git grep -IlE "${pattern}" -- . ':(exclude).env.example' ':(exclude)go.sum' ':(exclude)scripts/public-readiness-check.sh' 2>/dev/null || true)"
  if [[ -n "${files}" ]]; then
    echo "public-readiness: ${label} pattern found in tracked files:" >&2
    printf '%s\n' "${files}" >&2
    fail=1
  fi

  local commit
  while IFS= read -r commit; do
    if git log -1 --format=%B "${commit}" | grep -Eq "${pattern}"; then
      echo "public-readiness: ${label} pattern found in commit message ${commit}" >&2
      fail=1
    fi
  done < <(git rev-list --all)
}

scan_pattern "Ark API key" 'api-key-[0-9]{14,}'
scan_pattern "Agnes secret key" 'sk-vx[[:alnum:]_-]{20,}'
scan_pattern "OpenAI-style secret key" '(^|[^[:alnum:]_])sk-[A-Za-z0-9_-]{24,}'
scan_pattern "Ark endpoint id" 'ep-[0-9]{14}-[[:alnum:]_-]+'
scan_pattern "private key" 'BEGIN (RSA|OPENSSH|EC|DSA)? ?PRIVATE KEY'

exit "${fail}"

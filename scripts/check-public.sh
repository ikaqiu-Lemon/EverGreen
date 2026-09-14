#!/usr/bin/env bash

set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

fail=0

check() {
  local description="$1"
  local pattern="$2"
  shift 2

  if grep -RInE \
    --exclude-dir=.git \
    --exclude-dir=.venv \
    --exclude-dir=.tests-staging \
    --exclude-dir=_report \
    "${pattern}" "$@"; then
    printf '[FAIL] %s\n' "${description}" >&2
    fail=1
  else
    printf '[ok] %s\n' "${description}"
  fi
}

check "no private corporate references" \
  'byte''dance|lark''office\.com|git\.byte''d|code\.byte''d|review\.byte''d' \
  .

check "public docs do not depend on the private planning repository" \
  '(\.\./)?teamwork/projects/' \
  README.md INSTALL.md Makefile internal/version/version.go

check "no private agent-platform branding" \
  '(^|[^A-Za-z])AI''ME([^A-Za-z]|$)|Ai''me' \
  .

check "no common private-key or hosted-token signatures" \
  'BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]+' \
  .

if (( fail != 0 )); then
  exit 1
fi

printf '[PASS] public repository hygiene checks passed\n'

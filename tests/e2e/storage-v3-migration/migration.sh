#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-storage-v3-migration.XXXXXX")"
VAULT="${WORK}/vault"
MIGRATE="${WORK}/migrate-storage-v3"
EG="${WORK}/eg"
MANIFEST="${ROOT}/tests/fixtures/e2e/storage-v3-migration/manifest.json"
FIXTURE="${ROOT}/tests/fixtures/e2e/storage-v3-migration/vault"
PRODUCT_STATUS_BEFORE="$(git -C "${ROOT}" status --short --untracked-files=no)"
trap 'rm -rf "${WORK}"' EXIT

step=0
section() {
  step=$((step + 1))
  printf '\n=== [%02d] %s ===\n' "${step}" "$1"
}

fail() {
  printf '[FAIL] %s\n' "$*" >&2
  exit 1
}

tree_hash() {
  (
    cd "$1"
    find . -type f ! -path './.git/*' ! -path './.index/*' -print0 |
      LC_ALL=C sort -z |
      xargs -0 shasum -a 256
  )
}

json_assert() {
  python3 - "$@" <<'PY'
import json
import sys

path, expression = sys.argv[1:]
with open(path, "r", encoding="utf-8") as handle:
    value = json.load(handle)
safe = {"len": len, "all": all, "bool": bool}
if not eval(expression, {"__builtins__": {}}, {"x": value, **safe}):
    raise SystemExit(f"assertion failed: {expression}")
PY
}

section "build the one-time migrator and eg"
(
  cd "${ROOT}"
  CGO_ENABLED=0 go build -o "${MIGRATE}" ./scripts/migrate-storage-v3
  CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg
)
printf '  [ok] binaries built with CGO_ENABLED=0\n'

section "copy the v1/v2 fixture and establish a Git baseline"
mkdir -p "${VAULT}"
cp -R "${FIXTURE}/." "${VAULT}/"
git -C "${VAULT}" init -q
git -C "${VAULT}" config user.name 'EverGreen Migration Test'
git -C "${VAULT}" config user.email 'evergreen-migration@example.invalid'
git -C "${VAULT}" add -A
git -C "${VAULT}" commit -qm 'fixture baseline'
printf '  [ok] fixture has a v1 Note, v1 Knowledge, v2 seeds, and a wrong-type legacy card\n'

section "default dry-run reports every diff and writes zero Vault bytes"
tree_hash "${VAULT}" >"${WORK}/before.sha"
"${MIGRATE}" --vault "${VAULT}" --manifest "${MANIFEST}" >"${WORK}/dry-run.json"
tree_hash "${VAULT}" >"${WORK}/after.sha"
cmp -s "${WORK}/before.sha" "${WORK}/after.sha" ||
  fail 'dry-run changed Vault bytes'
test -z "$(git -C "${VAULT}" status --porcelain)" ||
  fail 'dry-run changed Git worktree'
json_assert "${WORK}/dry-run.json" \
  'x["status"] == "dry-run" and x["files_changed"] == 4 and len(x["files"]) == 4 and all(f["diff"] for f in x["files"])'
printf '  [ok] dry-run: 4 planned files, complete per-file diffs, zero Vault changes\n'

section "apply commits one complete journal v1 write-set"
"${MIGRATE}" --vault "${VAULT}" --manifest "${MANIFEST}" --apply >"${WORK}/apply.json"
json_assert "${WORK}/apply.json" \
  'x["status"] == "applied" and x["files_changed"] == 4 and bool(x["txn_id"]) and x["counts"]["knowledge"] == 1 and x["counts"]["opinions"] == 1'
TXN_ID="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["txn_id"])' "${WORK}/apply.json")"
json_assert "${VAULT}/.index/txn/${TXN_ID}/intent.json" \
  'x["journal_version"] == 1 and len(x["files"]) == 4 and x["files"][-1]["path"] == "domains/demo/notes/n-20260901-migration.md"'
test -f "${VAULT}/.index/txn/${TXN_ID}/commit" ||
  fail 'transaction commit marker is missing'
printf '  [ok] apply: one journal v1 transaction, full write-set, Note ordered last\n'

section "content, user sections, mapping, and tombstone invariants hold"
NOTE="${VAULT}/domains/demo/notes/n-20260901-migration.md"
CARD="${VAULT}/domains/demo/knowledge/k-20260901-migration-fact.md"
OLD="${VAULT}/domains/demo/knowledge/k-20260901-legacy-opinion.md"
grep -Fq 'Keep this user paragraph byte for byte.' "${NOTE}" ||
  fail 'Note user supplement was lost'
grep -Fq 'Keep this open question?' "${NOTE}" ||
  fail 'Note open question was lost'
grep -Fq '![Diagram](https://example.invalid/diagram.png)' "${NOTE}" ||
  fail 'Note image was lost'
grep -Fq 'code remains exact' "${NOTE}" || fail 'Note code block was lost'
grep -Fq '| key | value |' "${NOTE}" || fail 'Note table was lost'
grep -Fq 'Keep this card supplement byte for byte.' "${CARD}" ||
  fail 'Knowledge user supplement was lost'
grep -Fq "validation: 'pending'" \
  "${VAULT}/domains/demo/opinions/o-20260901-migration-opinion.md" ||
  fail 'new Opinion is not pending'
grep -Fq "deleted_at: '2026-09-02T10:00:00Z'" "${OLD}" ||
  fail 'legacy wrong-type card lacks a logical tombstone'
grep -Fq 'target: o-20260901-migration-opinion' "${OLD}" ||
  fail 'legacy wrong-type card lacks replaced_by'
printf '  [ok] source assets, annotations, user bytes, pending Opinion, and tombstone are intact\n'

section "second apply is a true no-op"
tree_hash "${VAULT}/domains" >"${WORK}/authority-before-noop.sha"
TXN_COUNT_BEFORE="$(find "${VAULT}/.index/txn" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
"${MIGRATE}" --vault "${VAULT}" --manifest "${MANIFEST}" --apply >"${WORK}/noop.json"
tree_hash "${VAULT}/domains" >"${WORK}/authority-after-noop.sha"
cmp -s "${WORK}/authority-before-noop.sha" "${WORK}/authority-after-noop.sha" ||
  fail 'second apply changed authoritative bytes'
TXN_COUNT_AFTER="$(find "${VAULT}/.index/txn" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
test "${TXN_COUNT_BEFORE}" = "${TXN_COUNT_AFTER}" ||
  fail 'second apply allocated a transaction'
json_assert "${WORK}/noop.json" \
  'x["status"] == "noop" and x["files_changed"] == 0 and x["txn_id"] == "" and all(f["action"] == "noop" for f in x["files"])'
printf '  [ok] replay changed no authoritative bytes and allocated no transaction\n'

section "index, check, reconcile, context, search, and export consume the result"
"${EG}" --vault "${VAULT}" index rebuild --json >"${WORK}/index.json"
"${EG}" --vault "${VAULT}" check --json >"${WORK}/check.json"
"${EG}" --vault "${VAULT}" reconcile --dry-run --json >"${WORK}/reconcile.json"
"${EG}" --vault "${VAULT}" context --note n-20260901-migration --json >"${WORK}/context.json"
"${EG}" --vault "${VAULT}" search migration --kind all --json >"${WORK}/search.json"
"${EG}" --vault "${VAULT}" export --plain --output "${WORK}/plain" --json >"${WORK}/export.json"
json_assert "${WORK}/index.json" 'x["exit_code"] == 0'
json_assert "${WORK}/check.json" 'x["exit_code"] == 0'
json_assert "${WORK}/reconcile.json" 'x["exit_code"] == 0'
json_assert "${WORK}/context.json" \
  'x["exit_code"] == 0 and len(x["data"]["draft_candidates"]) == 2'
json_assert "${WORK}/search.json" 'x["exit_code"] == 0 and x["data"]["total"] >= 2'
json_assert "${WORK}/export.json" 'x["exit_code"] == 0'
PLAIN_NOTE="${WORK}/plain/domains/demo/notes/n-20260901-migration.md"
test -f "${PLAIN_NOTE}" || fail 'plain export omitted the Note'
if grep -Eq 'eg:(nr|cd|nc):|\\.eg-candidate' "${PLAIN_NOTE}"; then
  fail 'plain export retained EverGreen protocol'
fi
grep -Fq 'Keep this user paragraph byte for byte.' "${PLAIN_NOTE}" ||
  fail 'plain export lost visible user content'
printf '  [ok] all downstream read, index, reconcile, and plain-export paths pass\n'

section "the test only mutated its disposable copy"
test "$(git -C "${ROOT}" status --short --untracked-files=no)" = "${PRODUCT_STATUS_BEFORE}" ||
  fail 'tracked product worktree changed while running the e2e'
printf '  [ok] real product worktree tracked bytes are unchanged\n'

printf '\n[PASS] storage-v3 migration fixture passed (8 steps)\n'

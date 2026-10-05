#!/usr/bin/env bash

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 512

WORK="$(eg_scratch eg-note-segmentation)"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
NOTE_ID="n-20261004-note-segmentation"
WORKSPACE_ID="ns-20261004-note-segmentation"
NOTE_REL="domains/tech/notes/${NOTE_ID}.md"
WORKSPACE_REL="domains/tech/note-segments/${WORKSPACE_ID}.md"
NOTE="${VAULT}/${NOTE_REL}"
WORKSPACE_FILE="${VAULT}/${WORKSPACE_REL}"
STEP=0
PASS=0
trap 'rm -rf "${WORK}"' EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok() { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die() { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }
run_code() {
  local rc=0
  "${EG}" --vault "${VAULT}" "$@" >"${WORK}/out.json" 2>&1 || rc=$?
  printf '%s\n' "${rc}"
}

step "构建并通过 plan_version 3 生成纯 Note 与 ns workspace"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
"${EG}" --vault "${VAULT}" init --domain tech </dev/null >/dev/null
printf '原文事实。\n' >"${WORK}/article.md"
"${EG}" --vault "${VAULT}" capture --url https://example.com/note-segmentation \
  --title 'Note Segmentation' --reason 'workspace e2e' \
  --body-file "${WORK}/article.md" </dev/null >/dev/null
SRC="$(find "${VAULT}/sources" -name '*.md' -type f | head -1 | xargs basename | sed 's/\.md$//')"
BASE="$("${EG}" --vault "${VAULT}" context --source "${SRC}" --json </dev/null |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["base"]["unprocessed.md"])')"
cat >"${WORK}/plan-v3.json" <<PLAN
{"plan_version":3,"verb":"process","domain":"tech","reason":"n/ns e2e",
 "requirement_ids":["EG-CANDIDATE-REVIEW"],"convergence":[],
 "base":{"unprocessed.md":"${BASE}"},"ops":[{
   "op":"write_note","source":"${SRC}","note_id":"${NOTE_ID}","title":"Note Segmentation",
   "blocks":[
     {"role":"source","source_ref":"L2-L2","heading":"事实","body":"原文事实。"},
     {"role":"agent","annotation":"distinction","body":"这是一条稳定事实。"}
   ],"omissions":[],
   "candidate_drafts":[
     {"key":"cand-fact","kind":"knowledge","logical_slug":"note-fact","title":"Note Fact",
      "note_refs":["B1"],"rel":"support","reason":"Note 来源块直接支持","tags":[],
      "sections":[{"name":"知识内容","body":"原文事实。\n"}]},
     {"key":"cand-view","kind":"opinion","logical_slug":"note-view","title":"Note View",
      "note_refs":["B2"],"rel":"context","reason":"Agent 批注形成判断","tags":[],
      "sections":[{"name":"观点","body":"这是一条可讨论判断。\n"}]}
   ],"candidate_coverage":[
     {"module":"fact","note_refs":["B1"],"summary":"事实块","disposition":"candidate",
      "candidates":["cand-fact"],"reason":""},
     {"module":"view","note_refs":["B2"],"summary":"批注块","disposition":"candidate",
      "candidates":["cand-view"],"reason":""}
 ]}]}
PLAN
"${EG}" --vault "${VAULT}" apply --plan "${WORK}/plan-v3.json" --json </dev/null \
  >"${WORK}/apply-v3.json" 2>&1 || { cat "${WORK}/apply-v3.json"; die "v3 apply 失败"; }
[ -f "${NOTE}" ] || die "纯 Note 未落盘"
[ -f "${WORKSPACE_FILE}" ] || die "ns workspace 未落盘"
! grep -qF '## 提取结果' "${NOTE}" || die "纯 Note 含旧提取结果"
! grep -qF '[Knowledge Candidate]' "${NOTE}" || die "纯 Note 含候选标签"
grep -qF '<!-- eg:cd:2 ' "${WORKSPACE_FILE}" || die "workspace 缺 eg:cd:2"
grep -qF '<!-- eg:cc:2 ' "${WORKSPACE_FILE}" || die "workspace 缺 eg:cc:2"
ok "v3 原子生成纯 n-* 与可编辑 ns-*"

step "修改 workspace 不改 Note；Note 修改后 stale 并显式 rebase"
NOTE_BEFORE="$(shasum -a 256 "${NOTE}" | awk '{print $1}')"
"${EG}" --vault "${VAULT}" candidate show --note "${NOTE_ID}" --json </dev/null \
  >"${WORK}/show.json"
python3 - "${WORK}/show.json" "${WORK}/review.json" <<'PY'
import json, sys
o = json.load(open(sys.argv[1], encoding="utf-8"))["data"]
assert o["schema_version"] == 2 and o["stale"] is False
o["candidates"][0]["sections"][0]["body"] = "用户优化后的知识。\n"
json.dump(o, open(sys.argv[2], "w", encoding="utf-8"), ensure_ascii=False)
PY
"${EG}" --vault "${VAULT}" candidate apply --note "${NOTE_ID}" \
  --file "${WORK}/review.json" --user-request --json </dev/null >/dev/null
[ "$(shasum -a 256 "${NOTE}" | awk '{print $1}')" = "${NOTE_BEFORE}" ] ||
  die "candidate apply 改写了 Note"
grep -qF '用户优化后的知识。' "${WORKSPACE_FILE}" || die "workspace 修改未生效"

python3 - "${NOTE}" <<'PY'
import sys
p = sys.argv[1]
b = open(p, "rb").read().replace("原文事实。".encode(), "修订后的原文事实。".encode(), 1)
open(p, "wb").write(b)
PY
git -C "${VAULT}" add "${NOTE_REL}"
git -C "${VAULT}" commit -qm "user: edit note"
"${EG}" --vault "${VAULT}" candidate show --note "${NOTE_ID}" --json </dev/null \
  >"${WORK}/stale.json"
python3 - "${WORK}/stale.json" "${WORK}/rebase.json" <<'PY'
import json, sys
o = json.load(open(sys.argv[1], encoding="utf-8"))["data"]
assert o["stale"] is True
json.dump(o, open(sys.argv[2], "w", encoding="utf-8"), ensure_ascii=False)
PY
[ "$(run_code materialize --note "${NOTE_ID}" --all --user-request)" = "2" ] ||
  die "stale workspace 未阻断 materialize"
[ "$(run_code candidate apply --note "${NOTE_ID}" --file "${WORK}/rebase.json" --user-request)" = "2" ] ||
  die "stale workspace 未阻断普通 apply"
"${EG}" --vault "${VAULT}" candidate apply --note "${NOTE_ID}" \
  --file "${WORK}/rebase.json" --rebase --user-request --json </dev/null >/dev/null
python3 - "${NOTE}" "${WORKSPACE_FILE}" <<'PY'
import hashlib, re, sys
note = open(sys.argv[1], "rb").read()
workspace = open(sys.argv[2], "rb").read().decode()
want = "sha256:" + hashlib.sha256(note).hexdigest()
got = re.search(r"^note_hash: '([^']+)'$", workspace, re.M).group(1)
assert got == want, (got, want)
assert "用户优化后的知识。" in workspace
PY
ok "workspace 编辑与显式 rebase 均保留用户内容"

step "物化从 ns 复制 payload，并写完整 provenance"
"${EG}" --vault "${VAULT}" materialize --note "${NOTE_ID}" --all \
  --user-request --json </dev/null >"${WORK}/materialize.json"
KPATH="$(python3 - "${WORK}/materialize.json" <<'PY'
import json, sys
o = json.load(open(sys.argv[1], encoding="utf-8"))
print(next(x["path"] for x in o["data"]["materialized_candidates"] if x["kind"] == "knowledge"))
PY
)"
grep -qF '用户优化后的知识。' "${VAULT}/${KPATH}" || die "K/O 未复制 workspace payload"
grep -qF "segmentation: '${WORKSPACE_ID}'" "${VAULT}/${KPATH}" ||
  die "K/O sources 缺 segmentation provenance"
ok "materialize 只以 ns-* 为 candidate 权威"

step "plain export 排除 note-segments"
"${EG}" --vault "${VAULT}" export --plain --output "${WORK}/plain" --json </dev/null >/dev/null
[ -f "${WORK}/plain/${NOTE_REL}" ] || die "plain export 缺纯 Note"
[ ! -e "${WORK}/plain/${WORKSPACE_REL}" ] || die "plain export 不得包含 note-segments"
ok "plain export 不重建重复文章"

step "legacy 未物化 Note 可原子迁移且重复执行幂等"
LEGACY_ID="n-20261004-legacy"
cat >"${WORK}/plan-v2.json" <<PLAN
{"plan_version":2,"verb":"process","domain":"tech","reason":"legacy migration e2e",
 "requirement_ids":[],"convergence":[],"base":{},"ops":[{
  "op":"write_note","source":"${SRC}","note_id":"${LEGACY_ID}","title":"Legacy",
  "blocks":[{"role":"source","source_ref":"L2-L2","body":"原文事实。"}],"omissions":[],
  "candidate_drafts":[{
    "key":"cand-legacy","kind":"knowledge","logical_slug":"legacy","title":"Legacy",
    "source_refs":["L2-L2"],"rel":"support","reason":"legacy","tags":[],
    "sections":[{"name":"知识内容","body":"legacy。\n"}]
  }],
  "candidate_coverage":[{
    "module":"legacy","source_refs":["L2-L2"],"summary":"legacy",
    "disposition":"candidate","candidates":["cand-legacy"],"reason":""
  }]
 }]}
PLAN
"${EG}" --vault "${VAULT}" apply --plan "${WORK}/plan-v2.json" --json </dev/null >/dev/null
"${EG}" --vault "${VAULT}" candidate migrate --note "${LEGACY_ID}" \
  --user-request --json </dev/null >"${WORK}/migrate.json"
LEGACY_NOTE="${VAULT}/domains/tech/notes/${LEGACY_ID}.md"
LEGACY_NS="${VAULT}/domains/tech/note-segments/ns-20261004-legacy.md"
! grep -qF '## 提取结果' "${LEGACY_NOTE}" || die "legacy Note 未净化"
grep -qF '<!-- eg:cd:2 ' "${LEGACY_NS}" || die "legacy candidate 未升级"
HEAD_BEFORE="$(git -C "${VAULT}" rev-parse HEAD)"
"${EG}" --vault "${VAULT}" candidate migrate --note "${LEGACY_ID}" \
  --user-request --json </dev/null >"${WORK}/migrate-noop.json"
[ "$(git -C "${VAULT}" rev-parse HEAD)" = "${HEAD_BEFORE}" ] || die "重复迁移产生 commit"
ok "legacy candidate migration 可重复执行"

printf '\n[PASS] note_segmentation_workspace：%d 项断言全部通过\n' "${PASS}"

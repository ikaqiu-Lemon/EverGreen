#!/usr/bin/env bash
# Candidate review workflow end-to-end contract.

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 512

WORK="$(eg_scratch eg-candidate-review)"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
NOTE_ID="n-20260929-candidate-review"
NOTE_REL="domains/tech/notes/${NOTE_ID}.md"
NOTE="${VAULT}/${NOTE_REL}"
STEP=0
PASS=0
trap 'rm -rf "${WORK}"' EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok() { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die() { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }
code() {
  local rc=0
  "${EG}" --vault "${VAULT}" "$@" >"${WORK}/out.json" 2>&1 || rc=$?
  printf '%s\n' "${rc}"
}
jget() {
  python3 - "$1" "$2" <<'PY'
import json, sys
o = json.load(open(sys.argv[1], encoding="utf-8"))
print(eval(sys.argv[2]))
PY
}
outside_fingerprint() {
  python3 - "$NOTE" <<'PY'
import hashlib, sys
raw = open(sys.argv[1], "rb").read()
start = raw.index(b"<!-- eg:cd:1 ")
end = raw.index("## 存疑与待验证\n".encode(), start)
print(hashlib.sha256(raw[:start] + raw[end:]).hexdigest())
PY
}
artifact_count() {
  find "${VAULT}/domains/tech" \( -path '*/knowledge/*.md' -o -path '*/opinions/*.md' \) \
    -type f 2>/dev/null | wc -l | tr -d ' '
}

step "构建真实 eg 并创建只含 Note candidate 的 vault"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
"${EG}" --vault "${VAULT}" init --domain tech </dev/null >/dev/null
"${EG}" --vault "${VAULT}" config set default_domain tech </dev/null >/dev/null
printf '定义行。\n判断行。\n待确认行。\n' >"${WORK}/article.md"
"${EG}" --vault "${VAULT}" capture --url https://example.com/candidate-review \
  --title 'Candidate Review' --reason 'candidate review e2e' \
  --body-file "${WORK}/article.md" </dev/null >/dev/null
SRC="$(find "${VAULT}/sources" -name '*.md' -type f | head -1 | xargs basename | sed 's/\.md$//')"
BASE="$("${EG}" --vault "${VAULT}" context --source "${SRC}" --json </dev/null |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["base"]["unprocessed.md"])')"
cat >"${WORK}/plan.json" <<PLAN
{"plan_version":2,"verb":"process","domain":"tech","reason":"candidate review e2e",
 "requirement_ids":["EG-CANDIDATE-REVIEW"],"convergence":[],
 "base":{"unprocessed.md":"${BASE}"},"ops":[{
   "op":"write_note","source":"${SRC}","note_id":"${NOTE_ID}","title":"Candidate Review",
   "blocks":[
     {"role":"source","source_ref":"L2-L2","heading":"定义","body":"定义行。"},
     {"role":"source","source_ref":"L3-L3","heading":"判断","body":"判断行。"},
     {"role":"source","source_ref":"L4-L4","heading":"待确认","body":"待确认行。"}
   ],"omissions":[],
   "candidate_drafts":[
     {"key":"cand-fact","kind":"knowledge","logical_slug":"initial-fact","title":"初始事实",
      "source_refs":["L2-L2"],"rel":"support","reason":"定义行直接支持","tags":["initial"],
      "sections":[{"name":"知识内容","body":"初始事实正文。\n"}]},
     {"key":"cand-claim","kind":"opinion","logical_slug":"initial-claim","title":"初始观点",
      "source_refs":["L3-L3"],"rel":"context","reason":"判断行提供背景","tags":[],
      "sections":[{"name":"观点","body":"初始观点正文。\n"}]}
   ],"candidate_coverage":[
     {"module":"m-fact","source_refs":["L2-L2"],"summary":"初始事实",
      "disposition":"candidate","candidates":["cand-fact"],"reason":""},
     {"module":"m-claim","source_refs":["L3-L3"],"summary":"初始观点",
      "disposition":"candidate","candidates":["cand-claim"],"reason":""},
     {"module":"m-pending","source_refs":["L4-L4"],"summary":"待确认",
      "disposition":"note_only","candidates":[],"reason":"先保留在 Note"}
 ]}]}
PLAN
"${EG}" --vault "${VAULT}" apply --plan "${WORK}/plan.json" --json </dev/null \
  >"${WORK}/apply.json" 2>&1 || { cat "${WORK}/apply.json"; die "草稿 apply 失败"; }
[ -f "${NOTE}" ] || die "Note 未落盘"
[ "$(artifact_count)" = "0" ] || die "首次处理不得创建 Knowledge/Opinion"
ok "首次处理只创建 Note candidate，Knowledge/Opinion 为 0"

step "show 导出完整 spec；未授权和 stale spec 均零写入"
"${EG}" --vault "${VAULT}" candidate show --note "${NOTE_ID}" --json </dev/null \
  >"${WORK}/show.json" 2>&1 || { cat "${WORK}/show.json"; die "candidate show 失败"; }
python3 - "${WORK}/show.json" "${WORK}/review.json" <<'PY'
import json, sys
env = json.load(open(sys.argv[1], encoding="utf-8"))
spec = env["data"]
assert spec["schema_version"] == 1
assert len(spec["candidates"]) == 2
json.dump(spec, open(sys.argv[2], "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
BEFORE_HASH="$(sha256sum "${NOTE}" | cut -d' ' -f1)"
[ "$(code candidate apply --note "${NOTE_ID}" --file "${WORK}/review.json")" = "2" ] ||
  die "缺 --user-request 应退 2"
[ "$(sha256sum "${NOTE}" | cut -d' ' -f1)" = "${BEFORE_HASH}" ] || die "未授权 apply 改写了 Note"
python3 - "${WORK}/review.json" "${WORK}/stale.json" <<'PY'
import json, sys
o = json.load(open(sys.argv[1], encoding="utf-8"))
o["note_hash"] = "sha256:" + "0" * 64
json.dump(o, open(sys.argv[2], "w", encoding="utf-8"), ensure_ascii=False)
PY
[ "$(code candidate apply --note "${NOTE_ID}" --file "${WORK}/stale.json" --user-request)" = "2" ] ||
  die "stale note_hash 应退 2"
[ "$(sha256sum "${NOTE}" | cut -d' ' -f1)" = "${BEFORE_HASH}" ] || die "stale apply 改写了 Note"
ok "show spec 完整；授权与 optimistic concurrency 门禁零写入"

step "一次 apply 完成删除、重命名、改类型、slug/source/payload/coverage"
python3 - "${WORK}/review.json" <<'PY'
import json, sys
p = sys.argv[1]
o = json.load(open(p, encoding="utf-8"))
o["candidates"] = [
  {
    "key": "cand-retyped", "kind": "knowledge",
    "logical_slug": "agent-development-boundaries", "title": "用户确认的边界",
    "source_refs": ["L2-L2", "L3-L3"], "rel": "support",
    "reason": "用户扩大来源范围并改为知识", "tags": ["reviewed"],
    "sections": [
      {"name": "知识内容", "body": "用户确认后的完整知识。\n"},
      {"name": "条件与边界", "body": "仅适用于已审阅范围。\n"}
    ]
  },
  {
    "key": "cand-added", "kind": "opinion",
    "logical_slug": "reviewed-claim", "title": "新增观点",
    "source_refs": ["L3-L3"], "rel": "context",
    "reason": "用户新增判断", "tags": [],
    "sections": [
      {"name": "观点", "body": "用户新增的可反驳主张。\n"},
      {"name": "待验证", "body": "需要后续验证。\n"}
    ]
  }
]
o["coverage"] = [
  {"module":"m-reviewed","source_refs":["L2-L2","L3-L3"],
   "summary":"已确认候选","disposition":"candidate",
   "candidates":["cand-retyped","cand-added"],"reason":""},
  {"module":"m-pending","source_refs":["L4-L4"],
   "summary":"仍待确认","disposition":"unresolved",
   "candidates":[],"reason":"等待用户定夺"}
]
json.dump(o, open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
OUTSIDE_BEFORE="$(outside_fingerprint)"
COMMITS_BEFORE="$(git -C "${VAULT}" rev-list --count HEAD)"
"${EG}" --vault "${VAULT}" candidate apply --note "${NOTE_ID}" \
  --file "${WORK}/review.json" --user-request --json </dev/null \
  >"${WORK}/candidate_apply.json" 2>&1 ||
  { cat "${WORK}/candidate_apply.json"; die "candidate apply 失败"; }
[ "$(outside_fingerprint)" = "${OUTSIDE_BEFORE}" ] || die "candidate 管理区外字节发生变化"
[ "$(git -C "${VAULT}" rev-list --count HEAD)" = "$((COMMITS_BEFORE + 1))" ] ||
  die "candidate apply 应恰产生一个 commit"
TXN_ID="$(jget "${WORK}/candidate_apply.json" 'o["data"]["txn_id"]')"
python3 - "${VAULT}/.index/txn/${TXN_ID}/intent.json" "${NOTE_REL}" <<'PY'
import json, sys
o = json.load(open(sys.argv[1], encoding="utf-8"))
assert o["journal_version"] == 1
assert [x["path"] for x in o["files"]] == [sys.argv[2]]
PY
grep -q 'data-slug=agent-development-boundaries' "${NOTE}" ||
  die "逻辑 slug 未写入可见 candidate 属性"
grep -q '#cand-retyped .eg-candidate .knowledge' "${NOTE}" ||
  die "rename/retype 未生效"
grep -q '#cand-added .eg-candidate .opinion' "${NOTE}" || die "add 未生效"
! grep -q '#cand-fact ' "${NOTE}" || die "delete 未生效"
[ "$(artifact_count)" = "0" ] || die "candidate apply 不得提前物化"
ok "完整 review spec 在一个 journal-v1/commit 内原子生效，区外字节不变"

step "unresolved 阻断 --all；关闭 coverage 后相同 spec 重投为 no-op"
HEAD_BEFORE="$(git -C "${VAULT}" rev-parse HEAD)"
NOTE_HASH="$(sha256sum "${NOTE}" | cut -d' ' -f1)"
[ "$(code materialize --note "${NOTE_ID}" --all --user-request)" = "2" ] ||
  die "materialize --all 遇到 unresolved 应退 2"
[ "$(git -C "${VAULT}" rev-parse HEAD)" = "${HEAD_BEFORE}" ] || die "unresolved 失败产生 commit"
[ "$(sha256sum "${NOTE}" | cut -d' ' -f1)" = "${NOTE_HASH}" ] || die "unresolved 失败改写 Note"
[ "$(artifact_count)" = "0" ] || die "unresolved 失败仍创建了产物"

"${EG}" --vault "${VAULT}" candidate show --note "${NOTE_ID}" --json </dev/null \
  >"${WORK}/show_open.json"
python3 - "${WORK}/show_open.json" "${WORK}/review_closed.json" <<'PY'
import json, sys
o = json.load(open(sys.argv[1], encoding="utf-8"))["data"]
for item in o["coverage"]:
    if item["disposition"] == "unresolved":
        item["disposition"] = "note_only"
        item["reason"] = "用户确认只保留在 Note"
json.dump(o, open(sys.argv[2], "w", encoding="utf-8"), ensure_ascii=False, indent=2)
PY
"${EG}" --vault "${VAULT}" candidate apply --note "${NOTE_ID}" \
  --file "${WORK}/review_closed.json" --user-request --json </dev/null \
  >"${WORK}/close.json" 2>&1 || { cat "${WORK}/close.json"; die "关闭 coverage 失败"; }
"${EG}" --vault "${VAULT}" candidate show --note "${NOTE_ID}" --json </dev/null \
  | python3 -c 'import json,sys; json.dump(json.load(sys.stdin)["data"],sys.stdout,ensure_ascii=False)' \
  >"${WORK}/review_noop.json"
HEAD_BEFORE="$(git -C "${VAULT}" rev-parse HEAD)"
"${EG}" --vault "${VAULT}" candidate apply --note "${NOTE_ID}" \
  --file "${WORK}/review_noop.json" --user-request --json </dev/null \
  >"${WORK}/noop.json" 2>&1 || { cat "${WORK}/noop.json"; die "review no-op 失败"; }
[ "$(jget "${WORK}/noop.json" 'o["data"]["txn_id"]')" = "" ] || die "no-op 不得开 txn"
[ "$(git -C "${VAULT}" rev-parse HEAD)" = "${HEAD_BEFORE}" ] || die "no-op 不得产生 commit"
ok "unresolved 门禁、关闭 coverage 与幂等 no-op 均成立"

step "materialize 使用 logical_slug 并逐字复制用户确认 payload"
"${EG}" --vault "${VAULT}" materialize --note "${NOTE_ID}" --all \
  --user-request --json </dev/null >"${WORK}/materialize.json" 2>&1 ||
  { cat "${WORK}/materialize.json"; die "materialize 失败"; }
TODAY="$(date +%Y%m%d)"
KID="k-${TODAY}-agent-development-boundaries"
OID="o-${TODAY}-reviewed-claim"
[ -f "${VAULT}/domains/tech/knowledge/${KID}.md" ] || die "logical slug Knowledge 未落盘"
[ -f "${VAULT}/domains/tech/opinions/${OID}.md" ] || die "logical slug Opinion 未落盘"
grep -qF '用户确认后的完整知识。' "${VAULT}/domains/tech/knowledge/${KID}.md" ||
  die "Knowledge 未逐字复制 review payload"
grep -qF '用户新增的可反驳主张。' "${VAULT}/domains/tech/opinions/${OID}.md" ||
  die "Opinion 未逐字复制 review payload"
[ "$(outside_fingerprint)" = "${OUTSIDE_BEFORE}" ] || die "materialize 改写了候选区外字节"
ok "最终 K/O 使用用户 logical_slug，payload 逐字复制且 Note 区外字节不变"

printf '\n[PASS] candidate_review_workflow：%d 项断言全部通过\n' "${PASS}"

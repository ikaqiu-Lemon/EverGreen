#!/usr/bin/env bash
# M3 期真实 Coding Agent 会话驱动（T-047 验收）。
# 本脚本 = 会话中我（Coding Agent）逐条敲下的命令序列的忠实记录与执行器。
# 只通过 eg CLI 写入，不手工编辑 vault 内任何文件。
set -uo pipefail

EG_BIN="${EG_BIN:-/tmp/eg}"
WORK="${WORK:-/tmp/m3sess/run}"
rm -rf "${WORK}"; mkdir -p "${WORK}"
VAULT="${WORK}/vault"; mkdir -p "${VAULT}"
OUT="${WORK}/artifacts"; mkdir -p "${OUT}"
LOG="${OUT}/commands.log"
: >"${LOG}"

KDIR='domains/ai-infra/knowledge'
STEP=0

eg() { "${EG_BIN}" --vault "${VAULT}" "$@" </dev/null; }
# run <tag> <expected-exit> <cmd...>
run() {
  local tag="$1" want="$2"; shift 2
  STEP=$((STEP + 1))
  local code=0
  eg "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || code=$?
  printf 'S%02d %-28s exit=%s want=%s :: eg %s\n' "${STEP}" "${tag}" "${code}" "${want}" "$*" | tee -a "${LOG}"
  cp "${WORK}/out.txt" "${OUT}/S$(printf %02d ${STEP})-${tag}.out"
  if [ -s "${WORK}/err.txt" ]; then cp "${WORK}/err.txt" "${OUT}/S$(printf %02d ${STEP})-${tag}.err"; fi
  if [ "${code}" != "${want}" ]; then
    printf '  !! 退出码不符预期，stderr：\n' | tee -a "${LOG}"
    sed -n 1,20p "${WORK}/err.txt" | tee -a "${LOG}"
    sed -n 1,20p "${WORK}/out.txt" | tee -a "${LOG}"
    echo "UNEXPECTED:${tag}" >>"${OUT}/unexpected.txt"
  fi
  LAST_CODE="${code}"
}
note() { printf '     · %s\n' "$1" | tee -a "${LOG}"; }
outf() { ls "${OUT}"/S*-"$1".out 2>/dev/null | head -1; }
jget() { python3 -c "import json,sys;d=json.load(open(sys.argv[1]));
import functools
def dig(o,p):
  for k in p.split('.'):
    o=o[int(k)] if k.isdigit() else o[k]
  return o
print(dig(d,sys.argv[2]))" "$1" "$2"; }
snap() { (cd "${VAULT}" && find . -name '*.md' -type f -exec sha256sum {} + | sort) >"$1"; }
filehash() { printf 'sha256:%s' "$(sha256sum "${VAULT}/$1" | cut -d' ' -f1)"; }
fm() { # fm <relpath> <key>  -> 打印 frontmatter 某键行（不存在则空）
  sed -n '2,/^---$/p' "${VAULT}/$1" | grep -E "^$2:" || true
}

echo "=== 会话开始 $(date -u +%Y-%m-%dT%H:%M:%SZ) ===" | tee -a "${LOG}"
"${EG_BIN}" --version | tee -a "${LOG}"

# ── S01 建库
run init 0 init --domain ai-infra
run config-set 0 config set domains ai-infra

# ── S02 摄入材料
BODY="${WORK}/article.txt"
cat >"${BODY}" <<'TXT'
Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks（节选，供 M3 验收会话使用）

结论：把参数化记忆（预训练权重）与非参数化记忆（可检索的外部文档库）组合起来，
在知识密集型任务上同时改善了准确率与可溯源性。参数化记忆负责语言与推理能力，
非参数化记忆负责事实：后者可以在不重新训练模型的前提下被替换、增补与订正。

边界：检索器的召回质量决定上限；当外部库本身陈旧或存在冲突条目时，
组合方案会把错误事实一并放大，因此需要显式的失效与替代机制来维护知识库。
TXT
run capture 0 capture --url https://arxiv.org/abs/2005.11401 \
  --title "Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks" \
  --body-file "${BODY}" --reason "M3 验收会话语料：外部知识库需要失效与替代机制" \
  --domain ai-infra --captured-at 2026-11-09T09:00:00+08:00 --json
SRC="$(jget "$(outf capture)" data.source_id 2>/dev/null || true)"
[ -n "${SRC}" ] || SRC="$(grep -oE 's-[0-9]{8}-[a-z0-9-]+' "$(outf capture)" | head -1)"
note "source_id=${SRC}"

# ── S03 取加工上下文
run context 0 context --source "${SRC}" --json
UNPROC="$(grep -oE '"unprocessed.md":"sha256:[0-9a-f]+"' "$(outf context)" | head -1 | sed 's/.*"\(sha256:[0-9a-f]*\)"/\1/')"
note "base[unprocessed.md]=${UNPROC:0:22}…"

NOTE_ID="n-20261109-rag"
KA="k-20261109-rag-hybrid-memory"
KB="k-20261109-nonparametric-memory"
KC="k-20261109-retriever-recall-bound"
KD="k-20261109-stale-corpus-risk"

# ── S04 Agent 自主生成加工 plan（材料笔记 + 四张知识卡）
cat >"${WORK}/plan-ingest.json" <<JSON
{
  "plan_version": 1,
  "verb": "process",
  "domain": "ai-infra",
  "reason": "把 RAG 节选沉淀成材料笔记与四张可独立复用的知识卡",
  "requirement_ids": ["EG-KNW-04", "EG-CVG-01"],
  "base": { "unprocessed.md": "${UNPROC}" },
  "ops": [
    { "op": "write_note", "source": "${SRC}", "note_id": "${NOTE_ID}",
      "title": "RAG：参数化记忆 + 非参数化记忆（2020）",
      "sections": {
        "材料提炼": "- 参数化记忆负责语言与推理，非参数化记忆负责事实。\\n- 事实可在不重训的前提下替换与订正。\\n",
        "Agent 分析": "- 该结论解释了知识库为何必须具备失效 / 替代机制。\\n"
      },
      "output_cards": [
        { "card": "${KA}", "mode": "新建" },
        { "card": "${KB}", "mode": "新建" },
        { "card": "${KC}", "mode": "新建" },
        { "card": "${KD}", "mode": "新建" }
      ],
      "coverage_gaps": ["counterexample"] },
    { "op": "create_card", "card_id": "${KA}",
      "title": "参数化记忆与非参数化记忆组合优于单一参数化记忆",
      "tags": ["ai", "rag"],
      "sources": [ { "source": "${SRC}", "note": "${NOTE_ID}", "rel": "support",
        "reason": "原文在知识密集型任务上给出该结论的直接依据" } ],
      "sections": {
        "知识内容": "在知识密集型任务上，把可检索的外部文档库与预训练权重组合使用，比单纯扩大参数化记忆更有效。\\n",
        "解释与依据": "- 事实层可替换，语言层保持稳定。\\n",
        "条件与边界": "- 依赖检索器召回质量。\\n",
        "理解自检": "- 外部库陈旧时该结论是否仍成立？\\n"
      } },
    { "op": "create_card", "card_id": "${KB}",
      "title": "非参数化记忆可在不重训模型的前提下被订正",
      "tags": ["ai", "rag"],
      "sources": [ { "source": "${SRC}", "note": "${NOTE_ID}", "rel": "support",
        "reason": "原文明确指出外部库可替换、增补与订正" } ],
      "sections": {
        "知识内容": "外部文档库承载事实，可被替换、增补与订正，无需重新训练模型。\\n",
        "解释与依据": "- 事实与权重解耦。\\n",
        "条件与边界": "- 订正动作本身需要可追溯的写入通道。\\n",
        "理解自检": "- 订正过程如何留痕？\\n"
      } },
    { "op": "create_card", "card_id": "${KC}",
      "title": "检索器召回质量决定组合方案的上限",
      "tags": ["ai", "retrieval"],
      "sources": [ { "source": "${SRC}", "note": "${NOTE_ID}", "rel": "support",
        "reason": "原文把召回质量列为上限约束" } ],
      "sections": {
        "知识内容": "组合方案的效果上限由检索器召回质量决定。\\n",
        "解释与依据": "- 召回不到的事实无法被利用。\\n",
        "条件与边界": "- 与生成模型规模无关。\\n",
        "理解自检": "- 召回率提升是否总能转化为准确率？\\n"
      } },
    { "op": "create_card", "card_id": "${KD}",
      "title": "外部库陈旧会被组合方案放大成错误事实",
      "tags": ["ai", "risk"],
      "sources": [ { "source": "${SRC}", "note": "${NOTE_ID}", "rel": "support",
        "reason": "原文指出陈旧或冲突条目会被一并放大" } ],
      "sections": {
        "知识内容": "当外部库陈旧或含冲突条目时，组合方案会把错误事实一并放大。\\n",
        "解释与依据": "- 检索命中即被当作事实使用。\\n",
        "条件与边界": "- 需要显式失效与替代机制兜底。\\n",
        "理解自检": "- 冲突条目如何被发现？\\n"
      } },
    { "op": "add_open_question", "note": "${NOTE_ID}",
      "question": "在冲突条目无法被自动发现时，失效机制应由谁触发？" }
  ]
}
JSON
run apply-ingest 0 apply --plan "${WORK}/plan-ingest.json" --json
cp "${WORK}/plan-ingest.json" "${OUT}/plan-ingest.json"

# ── S05 Agent 提出知识候选的删除提案（可提不可执：不带 --user-request）
run proposal-new 0 proposal new --type logical_delete --target "${KC}" \
  --reason "该卡结论已被 ${KA} 覆盖，建议逻辑删除" --json
PID="$(grep -oE 'p-[0-9]{8}-[0-9]{3}' "$(outf proposal-new)" | head -1)"
note "proposal_id=${PID}"

# ── S06 只读查看
run proposal-list 0 proposal list --json
run proposal-show 0 proposal show "${PID}" --json

# ── S07 approve 不带 --confirm → 期望退 6，权威 Markdown 完全不变
snap "${OUT}/sha256-before-exit6.txt"
GITLOG_BEFORE="$(git -C "${VAULT}" log --oneline | head -1)"
run approve-noconfirm 6 proposal approve "${PID}" --user-request --json
snap "${OUT}/sha256-after-exit6.txt"
if diff -u "${OUT}/sha256-before-exit6.txt" "${OUT}/sha256-after-exit6.txt" >"${OUT}/sha256-exit6-diff.txt"; then
  note "退 6：全库 .md sha256 清单逐行相等（$(wc -l <"${OUT}/sha256-before-exit6.txt") 个文件），diff 为空"
else
  note "退 6：sha256 清单出现差异（见 sha256-exit6-diff.txt）"; echo "UNEXPECTED:exit6-bytes" >>"${OUT}/unexpected.txt"
fi
note "退 6 前后 git 顶端 commit：${GITLOG_BEFORE} → $(git -C "${VAULT}" log --oneline | head -1)"
note "退 6 后 git status --porcelain 行数：$(git -C "${VAULT}" status --porcelain | wc -l)"

# ── S08 approve 带 --confirm + --user-request → 真实生效
run approve 0 proposal approve "${PID}" --confirm --user-request --json
grep -o '"status":"[a-z_]*"' "$(outf approve)" | head -3 | tee -a "${LOG}"
note "提案 frontmatter status：$(fm "proposals/${PID}.md" status)"

# ── S09 rel add / rel remove（物理移除 + 不删文件）
MD_BEFORE="$(cd "${VAULT}" && find . -name '*.md' | wc -l)"
run rel-add 0 rel add "${KA}" supports "${KB}" --reason "组合方案的事实层由非参数化记忆承载" --json
REL_AFTER_ADD="$(grep -c 'target:' "${VAULT}/${KDIR}/${KA}.md" || true)"
run rel-remove 0 rel remove "${KA}" supports "${KB}" --reason "关系方向记错，先移除" --json
REL_AFTER_RM="$(grep -c "target: ${KB}" "${VAULT}/${KDIR}/${KA}.md" || true)"
MD_AFTER="$(cd "${VAULT}" && find . -name '*.md' | wc -l)"
note "relations 条目：add 后含 target 行 ${REL_AFTER_ADD} → remove 后匹配 ${KB} 的行 ${REL_AFTER_RM}；.md 文件数 ${MD_BEFORE} → ${MD_AFTER}"
grep -q '墓碑\|tombstone\|removed_at' "${VAULT}/${KDIR}/${KA}.md" && { note "发现墓碑字样（不符 A-24）"; echo "UNEXPECTED:tombstone" >>"${OUT}/unexpected.txt"; } || note "无墓碑字样（A-24 物理移除成立）"
# 再建一条关系，留给后面的 ChangePlan 用 remove_relation 移除
run rel-add-2 0 rel add "${KA}" supports "${KB}" --reason "重新按正确方向建立支持关系" --json

# ── S10 eg edit 修改「知识内容」（验证三维不牵连）
S_BEFORE="$(fm "${KDIR}/${KA}.md" status)$(fm "${KDIR}/${KA}.md" deleted_at)$(fm "${KDIR}/${KA}.md" reviewed_at)"
run edit-knowledge 0 edit --target "${KA}" --section 知识内容 \
  --content "在知识密集型任务上，可检索外部文档库 + 预训练权重的组合，优于单纯扩大参数化记忆；事实层因此可被独立订正。" \
  --user-request --json
S_AFTER="$(fm "${KDIR}/${KA}.md" status)$(fm "${KDIR}/${KA}.md" deleted_at)$(fm "${KDIR}/${KA}.md" reviewed_at)"
[ "${S_BEFORE}" = "${S_AFTER}" ] && note "edit 后 status/deleted_at/reviewed_at 三维逐字不变：「${S_AFTER}」" || { note "三维被牵连：${S_BEFORE} → ${S_AFTER}"; echo "UNEXPECTED:edit-3dim" >>"${OUT}/unexpected.txt"; }
grep -q "事实层因此可被独立订正" "${VAULT}/${KDIR}/${KA}.md" && note "新正文逐字生效" || echo "UNEXPECTED:edit-content" >>"${OUT}/unexpected.txt"

# ── S11 eg edit --section 用户补充 → 恒拒
snap "${WORK}/before-userappend.txt"
run edit-user-append 2 edit --target "${KA}" --section 用户补充 --content "任何内容" --user-request --json
snap "${WORK}/after-userappend.txt"
diff -q "${WORK}/before-userappend.txt" "${WORK}/after-userappend.txt" >/dev/null && note "「用户补充」被拒且零写入" || echo "UNEXPECTED:userappend-write" >>"${OUT}/unexpected.txt"
grep -o '"code":"E[0-9]*"' "$(outf edit-user-append)" | head -2 | tee -a "${LOG}"

# ── S12 deprecate / restore（只动 status）
D_BEFORE="$(fm "${KDIR}/${KD}.md" deleted_at)|$(fm "${KDIR}/${KD}.md" reviewed_at)"
run deprecate 0 deprecate --target "${KD}" --reason "结论需按新证据重述" --json
note "deprecate 后 status：$(fm "${KDIR}/${KD}.md" status)；删除/过目维度：$(fm "${KDIR}/${KD}.md" deleted_at)|$(fm "${KDIR}/${KD}.md" reviewed_at)"
run restore 0 restore --target "${KD}" --reason "失效判断有误，恢复" --json
note "restore 后 status：$(fm "${KDIR}/${KD}.md" status)"
D_AFTER="$(fm "${KDIR}/${KD}.md" deleted_at)|$(fm "${KDIR}/${KD}.md" reviewed_at)"
[ "${D_BEFORE}" = "${D_AFTER}" ] && note "deprecate/restore 全程未碰删除与过目维度" || echo "UNEXPECTED:dep-3dim" >>"${OUT}/unexpected.txt"

# ── S13 replaced-by（单向）
run deprecate-kc 0 deprecate --target "${KC}" --reason "结论已被 ${KA} 覆盖" --json
KA_HASH_BEFORE="$(filehash "${KDIR}/${KA}.md")"
run replaced-by 0 replaced-by --target "${KC}" --to "${KA}" --reason "${KA} 覆盖了该卡结论" --json
KA_HASH_AFTER="$(filehash "${KDIR}/${KA}.md")"
[ "${KA_HASH_BEFORE}" = "${KA_HASH_AFTER}" ] && note "replaced-by 单向：被指向卡 ${KA} 字节不变" || echo "UNEXPECTED:replacedby-bidir" >>"${OUT}/unexpected.txt"
grep -A2 '^replaced_by:' "${VAULT}/${KDIR}/${KC}.md" | tee -a "${LOG}"

# ── S14 delete（三件齐备）+ K-041-01 观测
COMMITS_BEFORE="$(git -C "${VAULT}" log --oneline | wc -l)"
run delete 0 delete --target "${KC}" --reason "结论重复且已有替代卡" --proposal "${PID}" --confirm --user-request --json
COMMITS_AFTER="$(git -C "${VAULT}" log --oneline | wc -l)"
git -C "${VAULT}" status --porcelain >"${OUT}/k-041-01-porcelain.txt"
git -C "${VAULT}" diff -- "proposals/${PID}.md" >"${OUT}/k-041-01-diff.txt"
note "delete 后 commits ${COMMITS_BEFORE} → ${COMMITS_AFTER}；脏变更行数 $(wc -l <"${OUT}/k-041-01-porcelain.txt")：$(cat "${OUT}/k-041-01-porcelain.txt" | tr '\n' ';')"
note "脏 diff 变更键（六键核对）：$(grep -oE '^[+-] *[a-z_]+:' "${OUT}/k-041-01-diff.txt" | sed 's/^[+-] *//;s/:$//' | sort -u | tr '\n' ' ')"
note "脏 diff 统计：$(git -C "${VAULT}" diff --numstat -- "proposals/${PID}.md" | tr '\t' ' ')；变更文件数 $(git -C "${VAULT}" diff --name-only | wc -l)"
note "KC 删除维度：$(fm "${KDIR}/${KC}.md" deleted_at) / $(fm "${KDIR}/${KC}.md" deleted_reason)；status：$(fm "${KDIR}/${KC}.md" status)"
KA_REL_BEFORE_UNDEL="$(grep -c 'target:' "${VAULT}/${KDIR}/${KA}.md" || true)"

# ── S15 search 默认排除已删除 / --include-deleted 纳入
run search-default 0 search 召回 --json
grep -o "${KC}" "$(outf search-default)" | head -1 >"${WORK}/hit1.txt" || true
run search-include 0 search 召回 --include-deleted --json
note "默认搜索命中 ${KC} 次数：$(grep -o "${KC}" "$(outf search-default)" | wc -l)；--include-deleted：$(grep -o "${KC}" "$(outf search-include)" | wc -l)"
grep -o '\[已删除\]' "$(outf search-include)" | head -1 | tee -a "${LOG}"

# ── S16 undelete（只动删除维度、关系不删）
run undelete 0 undelete --target "${KC}" --reason "删除决策撤回" --json
note "undelete 后 KC status：$(fm "${KDIR}/${KC}.md" status)；deleted_at：$(fm "${KDIR}/${KC}.md" deleted_at)（空即已清）"
KA_REL_AFTER_UNDEL="$(grep -c 'target:' "${VAULT}/${KDIR}/${KA}.md" || true)"
[ "${KA_REL_BEFORE_UNDEL}" = "${KA_REL_AFTER_UNDEL}" ] && note "delete/undelete 全程关系条目数不变（${KA_REL_AFTER_UNDEL}）" || echo "UNEXPECTED:rel-cascade" >>"${OUT}/unexpected.txt"

# ── S17 mark-reviewed / unreviewed
run unreviewed-before 0 unreviewed --json
run mark-reviewed 0 mark-reviewed --target "${KB}" --json
note "KB reviewed_at：$(fm "${KDIR}/${KB}.md" reviewed_at)"
run unreviewed-after 0 unreviewed --json
note "unreviewed 命中 ${KB}：before=$(grep -o "${KB}" "$(outf unreviewed-before)" | wc -l) after=$(grep -o "${KB}" "$(outf unreviewed-after)" | wc -l)"

# ── S18 门禁 8：Agent 自主生成含 ≥3 个 M3 新增 op 的 ChangePlan，经 eg apply 真实执行
cat >"${WORK}/m3-ops-plan.json" <<JSON
{
  "plan_version": 1,
  "verb": "process",
  "domain": "ai-infra",
  "reason": "按用户显式请求：失效一张过时卡、移除一条方向记错的关系、标记一张卡已过目",
  "requirement_ids": ["EG-EDIT-04", "EG-KNW-07"],
  "base": {
    "${KDIR}/${KD}.md": "$(filehash "${KDIR}/${KD}.md")",
    "${KDIR}/${KA}.md": "$(filehash "${KDIR}/${KA}.md")"
  },
  "ops": [
    { "op": "deprecate", "target": "${KD}", "reason": "陈旧语料风险已并入 ${KA}", "initiator": "user" },
    { "op": "remove_relation", "from": "${KA}", "type": "supports", "target": "${KB}",
      "reason": "该支持关系应挂在材料层，不应卡到卡", "initiator": "user" },
    { "op": "mark_reviewed", "target": "${KA}", "reason": "本轮复核完成", "initiator": "user" }
  ]
}
JSON
COMMITS_B4_PLAN="$(git -C "${VAULT}" log --oneline | wc -l)"
run apply-m3-ops 0 apply --plan "${WORK}/m3-ops-plan.json" --json --user-request
cp "${WORK}/m3-ops-plan.json" "${OUT}/m3-ops-plan.json"
note "M3 op plan 执行后 commits $(git -C "${VAULT}" log --oneline | wc -l)（执行前 ${COMMITS_B4_PLAN}）"
note "KD status：$(fm "${KDIR}/${KD}.md" status)；KA 关系条目 target 行数：$(grep -c 'target:' "${VAULT}/${KDIR}/${KA}.md" || true)"

# ── S19 report --last --json
run report-last 0 report --last --json
python3 - "$(outf report-last)" <<'PY' | tee -a "${LOG}"
import json,sys
d=json.load(open(sys.argv[1]))
print("     · 信封顶层键：", sorted(d.keys()))
print("     · ok=%s exit_code=%s status=%s" % (d.get("ok"), d.get("exit_code"), d.get("status")))
data=d.get("data",{})
print("     · data 键：", sorted(data.keys()))
for k in ("verb","stages","execution","reconcile"):
    if k in data: print("     · data.%s=%s" % (k, json.dumps(data[k],ensure_ascii=False)[:300]))
PY

# ── 收尾留痕
git -C "${VAULT}" log --oneline >"${OUT}/git-log.txt"
git -C "${VAULT}" status --porcelain >"${OUT}/git-status-final.txt"
snap "${OUT}/sha256-final-vault.txt"
cp "${WORK}/plan-ingest.json" "${OUT}/plan-ingest.json"
echo "=== 会话结束；未达预期项：$(cat "${OUT}/unexpected.txt" 2>/dev/null | tr '\n' ' ') ===" | tee -a "${LOG}"
echo "commits=$(git -C "${VAULT}" log --oneline | wc -l) dirty=$(wc -l <"${OUT}/git-status-final.txt")" | tee -a "${LOG}"

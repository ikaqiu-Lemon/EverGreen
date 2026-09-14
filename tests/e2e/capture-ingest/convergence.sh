#!/usr/bin/env bash
# `convergence[]` 收敛体验端到端脚本（T-evergreen.s1_main_flow-158614-026）。
#
# 判据来源：
#   ChangePlan 合同 `2026-09-08-changeplan-contract.md` §2（convergence[] 条目字段）
#                                                       §2.1（处理关系七值封闭枚举）
#   CLI 合同 `2026-09-01-eg-cli-contract.md` §1.5（eg apply）/ §1.9（eg report --last）/ §3（envelope）
#   里程碑 `M-002-m2.md` 完成判据 6 / 9
#
# 三组断言（Acceptance 逐条对应）：
#   ① apply 与 report --last 的收敛行块 **逐字相等**（同源渲染，diff 断言）
#   ② 七值关系 **全覆盖**：恰 7 个卡段、每个关系名逐字出现
#   ③ `eg init` 落盘的 SKILL.md 与 evergreen/skill/SKILL.md **字节相同**（T-…-017 守护不回归）
# 附带：唯一渲染实现的正反证（convergenceLines 调用点 + 渲染字面量全 internal/ 唯一产地）、
#       缺字段退 0、只读命令零副作用。
#
# 约束：离线、可重复执行、无外部依赖（只用 bash / coreutils / git / go）；
# 一切写操作只发生在 mktemp -d 目录内，仓库工作区零污染。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/capture-ingest/convergence.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m2-convergence.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

# 渲染前缀按运行期拼接：保证「唯一渲染实现」的 grep 反证不被本脚本自身干扰。
MARK="收敛""："

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}";  eg_staged_cleanup; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }
gitv() { git -C "${VAULT}" "$@"; }

# 抽出收敛行块：标题行「逐卡收敛记录」起，至「数据：」/「警告（」前止。
converge_block() { awk '
  /^逐卡收敛记录/ { inblk = 1 }
  inblk && (/^数据：/ || /^警告（/) { exit }
  inblk { print }
' "$1"; }

SRC_ID="s-20260930-convergence"
NOTE_ID="n-20260930-convergence"

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 唯一渲染实现（正反证）
step "唯一渲染实现：apply.go / report.go 各调用 convergenceLines；渲染字面量只在 convergence.go"
for f in internal/cli/apply.go internal/cli/report.go; do
  n=$(grep -c "convergenceLines" "${REPO_ROOT}/${f}" || true)
  [ "${n}" -ge 1 ] || die "${f} 未调用 convergenceLines（两条路径必须同源）"
done
grep_mark() { (cd "${REPO_ROOT}" && grep -rn -- "${MARK}" internal/ || true) | { grep -v "convergence.go" || true; }; }
HITS=$(grep_mark | wc -l | tr -d ' ')
[ "${HITS}" = "0" ] || {
  grep_mark >&2
  die "internal/ 下除 convergence.go 外还有 ${HITS} 处渲染字面量（禁止第二套渲染）"
}
ok "convergenceLines 调用点齐备，渲染字面量在 internal/ 下唯一"

# ---------------------------------------------------------------- 2. seed vault
step "seed vault：eg init + default_domain + 一篇原文"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set default_domain 失败"
printf '注意力机制把序列建模的成本从递归改为并行，这是本篇的核心主张。\n' >"${WORK}/body.txt"
[ "$(eg_code capture --url https://example.com/attn --title "Attention 综述" \
  --body-file "${WORK}/body.txt" --reason "收敛体验端到端" \
  --captured-at 2026-09-30T10:00:00+08:00)" = "0" ] || die "eg capture 失败"
SRC_ID="$(eg capture --url https://example.com/attn --title "Attention 综述" \
  --body-file "${WORK}/body.txt" --reason "取回 source_id" --json |
  tr ',' '\n' | grep -o '"source_id":"[^"]*"' | head -1 | cut -d'"' -f4)"
[ -n "${SRC_ID}" ] || die "未取到 source_id"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
ok "vault 就绪，原文 ${SRC_ID}"

# ---------------------------------------------------------------- 3. 七值关系全覆盖的 plan
step "eg apply：七值关系各一条 + 一条只给 card/relation 的缺字段记录"
cat >"${WORK}/plan.json" <<JSON
{
  "plan_version": 1, "verb": "process", "domain": "ai-infra",
  "reason": "收敛体验端到端：七值关系全覆盖 + 缺字段如实标注",
  "requirement_ids": ["EG-CVG-01", "EG-CVG-02"],
  "convergence": [
    { "card": "k-20260930-c1", "relation": "independent_new",
      "core_knowledge": "different", "conditions": "different", "reuse_purpose": "different",
      "note": "三维度全不同，独立新增" },
    { "card": "k-20260930-c2", "relation": "same_semantics",
      "core_knowledge": "same", "conditions": "same", "reuse_purpose": "same",
      "note": "三维度全同，复用已有卡" },
    { "card": "k-20260930-c3", "relation": "non_core_supplement",
      "core_knowledge": "same", "conditions": "different", "reuse_purpose": "same",
      "note": "补了一条成立条件" },
    { "card": "k-20260930-c4", "relation": "core_change",
      "core_knowledge": "different", "conditions": "same", "reuse_purpose": "same",
      "note": "核心变化，另建新卡并指向原卡" },
    { "card": "k-20260930-c5", "relation": "conflict_coexist",
      "core_knowledge": "different", "conditions": "same", "reuse_purpose": "same",
      "note": "结论相反，两卡并存" },
    { "card": "k-20260930-c6", "relation": "uncertain",
      "core_knowledge": "different", "conditions": "different", "reuse_purpose": "same",
      "note": "信息不足，判不出" },
    { "card": "k-20260930-c7", "relation": "deprecated",
      "core_knowledge": "different", "conditions": "same", "reuse_purpose": "same",
      "note": "由用户提出的失效判断，Agent 不生成状态类 op" },
    { "card": "k-20260930-c8", "relation": "uncertain" }
  ],
  "ops": [
    { "op": "write_note", "source": "${SRC_ID}", "note_id": "${NOTE_ID}",
      "title": "Attention 综述",
      "sections": { "材料提炼": "- 原文主张：并行化把序列建模成本降下来。\n" } }
  ]
}
JSON
code=$(eg_code apply --plan "${WORK}/plan.json")
[ "${code}" = "0" ] || { cat "${WORK}/err.txt" >&2; die "apply 退出码 ${code}，期望 0" ; }
cp "${WORK}/out.txt" "${WORK}/apply.txt"
converge_block "${WORK}/apply.txt" >"${WORK}/apply.block"
SEG=$(grep -c -- "${MARK}" "${WORK}/apply.block" || true)
[ "${SEG}" = "8" ] || { cat "${WORK}/apply.block" >&2; die "卡段数 ${SEG}，期望 8（七值各一 + 缺字段一）"; }
for rel in independent_new same_semantics non_core_supplement core_change \
           conflict_coexist uncertain deprecated; do
  grep -Fq -- "${rel}" "${WORK}/apply.block" || die "收敛行块缺关系名 ${rel}"
done
SEVEN=$(grep -c -- "${MARK}" "${WORK}/apply.block" || true)
[ "${SEVEN}" -ge 7 ] || die "七值关系未全覆盖"
ok "七值关系全覆盖，恰 8 个卡段（${SEVEN} 行以「${MARK}」开头）"

# ---------------------------------------------------------------- 4. 缺字段如实标注
step "缺字段如实标注「未给出」，不编造"
grep -Fq "k-20260930-c8" "${WORK}/apply.block" || die "缺字段记录未出现在输出里"
MISS=$(grep -c "未给出" "${WORK}/apply.block" || true)
[ "${MISS}" -ge 4 ] || die "「未给出」仅 ${MISS} 处，期望 ≥ 4（缺字段记录的三维度 + note）"
# 输出里出现的卡 ID 集合 == 输入里出现的卡 ID 集合（反证「不编造」）。
grep -o 'k-[0-9]\{8\}-[a-z0-9-]*' "${WORK}/plan.json" | sort -u >"${WORK}/ids.in"
grep -o 'k-[0-9]\{8\}-[a-z0-9-]*' "${WORK}/apply.block" | sort -u >"${WORK}/ids.out"
diff "${WORK}/ids.in" "${WORK}/ids.out" >/dev/null || {
  diff "${WORK}/ids.in" "${WORK}/ids.out" >&2 || true
  die "输出的卡 ID 集合与输入不等：渲染不得编造、不得丢条目"
}
ok "缺字段处为「未给出」，卡 ID 集合与输入逐一相等"

# ---------------------------------------------------------------- 5. 两条路径逐字相等
step "eg report --last：收敛行块与 apply 输出逐字相等（同源渲染）"
SNAP="$(gitv rev-parse HEAD)"
[ "$(eg_code report --last)" = "0" ] || die "report --last 退出码非 0"
cp "${WORK}/out.txt" "${WORK}/report.txt"
converge_block "${WORK}/report.txt" >"${WORK}/report.block"
[ -s "${WORK}/report.block" ] || die "report --last 未渲染收敛行块"
diff "${WORK}/apply.block" "${WORK}/report.block" || die "两条路径的收敛行块必须逐字相等"
[ -z "$(gitv status --porcelain)" ] || die "report --last 必须零文件变化"
[ "$(gitv rev-parse HEAD)" = "${SNAP}" ] || die "report --last 必须零 commit"
ok "apply 与 report --last 的收敛行块 diff 为空，且只读零副作用"

# ---------------------------------------------------------------- 6. 顺序稳定 + 重复渲染稳定
step "顺序稳定：输出段顺序 == convergence[] 原始顺序；重复回放逐字相等"
grep -o 'k-[0-9]\{8\}-c[0-9]' "${WORK}/plan.json" >"${WORK}/order.in"
grep -- "${MARK}" "${WORK}/apply.block" | grep -o 'k-[0-9]\{8\}-c[0-9]' >"${WORK}/order.out"
diff "${WORK}/order.in" "${WORK}/order.out" >/dev/null || {
  diff "${WORK}/order.in" "${WORK}/order.out" >&2 || true
  die "渲染顺序必须与 convergence[] 原始顺序逐一对应（不重排、不去重、不合并）"
}
[ "$(eg_code report --last)" = "0" ] || die "report --last 第二次退出码非 0"
converge_block "${WORK}/out.txt" >"${WORK}/report2.block"
diff "${WORK}/report.block" "${WORK}/report2.block" >/dev/null || die "重复回放输出不稳定"
ok "顺序逐一对应，重复回放逐字相等"

# ---------------------------------------------------------------- 7. --json 键集合不变
step "--json：data 顶层三键 + 每条收敛记录六键（M1 基线逐字不变）"
eg apply --plan "${WORK}/plan.json" --dry-run --json >"${WORK}/dry.json" ||
  die "apply --dry-run --json 退出码非 0"
for k in '"report"' '"domain"' '"convergence"'; do
  grep -Fq "${k}" "${WORK}/dry.json" || die "data 缺键 ${k}"
done
for k in '"card"' '"relation"' '"core_knowledge"' '"conditions"' '"reuse_purpose"' '"note"'; do
  grep -Fq "${k}" "${WORK}/dry.json" || die "data.convergence 条目缺键 ${k}"
done
[ -z "$(gitv status --porcelain)" ] || die "--dry-run 必须零写入"
ok "data 顶层键与收敛条目键集合与 M1 一致，--dry-run 零写入零 commit"

# ---------------------------------------------------------------- 8. 缺字段的落盘记录退 0
step "落盘记录不含 convergence 时：report --last 退 0 并如实说明"
python3 - "${VAULT}/.eg/last-report.json" <<'PY'
import json, sys
p = sys.argv[1]
with open(p, encoding="utf-8") as f:
    rec = json.load(f)
rec["data"].pop("convergence", None)
with open(p, "w", encoding="utf-8") as f:
    json.dump(rec, f, ensure_ascii=False)
PY
[ "$(eg_code report --last)" = "0" ] || die "缺字段时必须照常退 0"
grep -Fq "该记录未包含逐卡收敛结论" "${WORK}/out.txt" || die "缺字段时必须如实说明"
grep -q -- "${MARK}" "${WORK}/out.txt" && die "缺字段时不得凭空渲染卡段"
ok "缺字段 → 退 0 + 一行如实说明 + 零卡段"

# ---------------------------------------------------------------- 9. SKILL.md 同源
step "eg init 落盘的 SKILL.md 与 evergreen/skill/SKILL.md 字节相同（T-…-017 守护）"
VAULT2="${WORK}/vault2"
"${EG}" --vault "${VAULT2}" init --domain ai-infra </dev/null >/dev/null || die "第二个 vault 的 eg init 失败"
cmp "${VAULT2}/SKILL.md" "${REPO_ROOT}/skill/SKILL.md" || die "落盘 SKILL.md 与源文件字节不同"
ok "SKILL.md 内嵌副本与源文件字节相同"

# **T-…-044 重钉**：本段原先要求「SKILL.md 的 rel remove 措辞与 --help 的占位文案逐字相等」，
# 且断言调用退非 0。占位被接管为真实写入后两条都必然失效，故改判为**零占位**断言：
# SKILL.md 全文与 `eg rel --help` 都不得再出现「未实现」，且 rel remove 真实可用。
step "SKILL.md 与实现一致：全文零「未实现」；rel remove 已是真实写入"
STALE=$({ grep -n "未实现" "${REPO_ROOT}/skill/SKILL.md" || true; } | wc -l | tr -d ' ')
[ "${STALE}" = "0" ] || {
  { grep -n "未实现" "${REPO_ROOT}/skill/SKILL.md" || true; } >&2
  die "SKILL.md 仍把命令写成「未实现」（M3 起九命令 + 写子命令全部真实可用）"
}
"${EG}" rel --help </dev/null >"${WORK}/relhelp.txt" 2>&1  # 退出码不吞：--help 合同退 0
if grep -Fq '未实现' "${WORK}/relhelp.txt"; then die "eg rel --help 不得再宣告未实现"; fi
grep -Fq 'rel remove' "${WORK}/relhelp.txt" || die "eg rel --help 应列出 rel remove 的真实用法"
grep -Fq '物理移除' "${REPO_ROOT}/skill/SKILL.md" ||
  die "SKILL.md 必须写明 rel remove 的落地语义（A-24 物理移除）"
# 真实调用：缺 --reason 属参数非法退 1、零写入（本段不制造语料，只验参数分级与零副作用）。
"${EG}" --vault "${VAULT2}" rel remove k-20260930-c1 supports k-20260930-c2 </dev/null \
  >"${WORK}/rm.txt" 2>&1 && die "缺 --reason 必须退非 0"
[ -z "$(git -C "${VAULT2}" status --porcelain)" ] || die "参数非法必须零写入"
ok "SKILL.md 零「未实现」；rel remove 已是真实写入；参数非法仍零副作用"

# ---------------------------------------------------------------- 10. 文档样例即用例
step "SKILL.md 两份 ChangePlan 样例仍能被 eg apply --dry-run 接受"
(cd "$(eg_staged_root)" && go test ./internal/cli/... -run 'Skill|Convergence' -count=1) >/dev/null ||
  die "SKILL.md / 收敛用例未全绿"
ok "SKILL.md 与收敛用例全绿"

printf '\n全部 %d 组断言通过（共 %d 步）。\n' "${PASS}" "${STEP}"

#!/usr/bin/env bash
# 反向关系「两条读路径等价 + 降级如实」实跑门禁
# （system_assurance · T-…-002；源 m2_acceptance.sh L183-260 判据 4 的②③格）。
#
# 判据本体（M2 判据 4 + M5 索引合同）：Markdown 是唯一权威，索引只是可重建派生物，
# 两条读路径（全量扫描 / 索引后端）结果必须**等价**；索引缺失时必须**降级**并**如实留痕**。
# 历史脚本用「internal/query 内 .index grep 计数」作代理量，M5 把索引读路径正式发放给
# internal/query 后该代理量必然自红 —— 代理量已由 tests/contract/static-boundaries/symbol_locality.sh
# 接管为集合封闭断言，本脚本接管**实跑等价性**（比 grep 代理更强）。
#
# 三态：
#   ① 无索引（.index/ 不存在）→ 全量 Markdown 扫描；
#   ② 索引在位（eg index build 之后）→ 索引后端；
#   ③ 删索引（rm -rf .index/）→ 降级回全量扫描。
# 断言：三态 `data` 段**逐字节相等**；反向关系正确（B 收到来自 A 的 supports）；
#       ①③ 诊断码序列逐字恰 `W23 Q5`，②为空；三态一律退 0。
#
# 等价性复算只用 bash / coreutils（不引入 python3 / jq）：信封键序由 Envelope.MarshalJSON
# 固定为 ok,data,warnings,exit_code,status 且为紧凑单行，故 data 段可精确切出并逐字节比较
# —— 这比「反序列化后比对象」更严：键序或空白差异也会判不等。
#
# 约束：离线、零交互；只在 mktemp scratch 内建 vault，不碰仓库工作区。
# 用法：cd evergreen && bash tests/e2e/index-search/rel_index_equivalence.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
cd "${REPO_ROOT}"
eg_require_disk 512

WORK="$(eg_scratch eg-rel-equiv)"
trap 'rm -rf "${WORK}"' EXIT

FAIL=0
pass() { printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; FAIL=1; }
sec()  { printf '\n=== %s ===\n' "$1"; }

# ---------------------------------------------------------------- 构建 eg
sec "步骤0：构建 eg 并准备 vault 夹具"
EG="${WORK}/eg"
CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg || { printf '[FAIL] go build ./cmd/eg 失败\n' >&2; exit 1; }
pass "eg 已构建（$(basename "${EG}")）"

VAULT="${WORK}/vault"
K="${VAULT}/domains/ai-infra/knowledge"
A='k-20260901-acc-a'
B='k-20260901-acc-b'
REASON='前者为后者提供论证支持：两条结论在同一前提下互为佐证，理由字段写足以通过写前校验。'

"${EG}" --vault "${VAULT}" init --domain ai-infra </dev/null >/dev/null 2>&1 \
  || { printf '[FAIL] eg init 失败\n' >&2; exit 1; }
mkdir -p "${K}"
{
  printf '%s\n' '---' "id: ${A}" 'status: active' "created_at: '2026-09-01'" \
    "updated_at: '2026-09-01T10:00:00+08:00'" 'title: 判据 4 夹具 A' 'tags: [ai]' 'sources: []' \
    'relations:' '  - type: supports' "    target: ${B}" "    reason: ${REASON}" '---' '' \
    '## 知识内容' '' '正文占位：A 支持 B。' ''
} >"${K}/${A}.md"
{
  printf '%s\n' '---' "id: ${B}" 'status: active' "created_at: '2026-09-01'" \
    "updated_at: '2026-09-01T10:00:00+08:00'" 'title: 判据 4 夹具 B' 'tags: [ai]' 'sources: []' \
    '---' '' '## 知识内容' '' '正文占位：B 被 A 支持。' ''
} >"${K}/${B}.md"
git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg add -A >/dev/null 2>&1
git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg commit -q -m 'seed: 等价性夹具' >/dev/null 2>&1
pass "vault 夹具就绪（A --supports--> B，已入 git）"
[ ! -e "${VAULT}/.index" ] && pass "无索引前提锁成立（.index/ 不存在）" || die "夹具自带 .index/，①态前提被破坏"

# ---------------------------------------------------------------- 三态取样
rel_run() { # rel_run <tag>：跑 eg rel B --json；必须退 0
  local tag="$1" rc=0
  "${EG}" --vault "${VAULT}" rel "${B}" --json </dev/null >"${WORK}/rel.${tag}.json" \
    2>"${WORK}/rel.${tag}.err" || rc=$?
  [ "${rc}" = "0" ] || die "${tag} 态：eg rel 退出码 ${rc}（三态一律必须退 0）"
}
rel_seg() { # rel_seg <json> <data|codes>
  if [ "$({ grep -o ',"warnings":\[' "$1" || true; } | wc -l | tr -d ' ')" != "1" ]; then
    printf 'ENVELOPE-SHAPE-BAD\n'; return 0
  fi
  if [ "$2" = "data" ]; then
    sed -e 's/^{"ok":[^,]*,"data"://' -e 's/,"warnings":\[.*$//' "$1"
  else
    sed -e 's/^.*,"warnings":\[//' -e 's/\],"exit_code":.*$//' "$1" \
      | { grep -o '"code":"[A-Z][0-9]*"' || true; } \
      | sed -e 's/^"code":"//' -e 's/"$//' | tr '\n' ' ' | sed -e 's/ *$//'
  fi
}

sec "步骤1：①无索引 → ②索引在位 → ③删索引 三态取样"
rel_run noidx
"${EG}" --vault "${VAULT}" index build --json </dev/null >/dev/null 2>&1 || die "eg index build 失败"
[ -d "${VAULT}/.index" ] && pass "②态前提成立（.index/ 已建）" || die "index build 后 .index/ 不存在"
rel_run idx
rm -rf "${VAULT}/.index"
rel_run degraded
for tag in noidx idx degraded; do
  rel_seg "${WORK}/rel.${tag}.json" data  >"${WORK}/d.${tag}"
  rel_seg "${WORK}/rel.${tag}.json" codes >"${WORK}/c.${tag}"
done
pass "三态样本已取（noidx / idx / degraded）"

# ---------------------------------------------------------------- 等价性
sec "步骤2：data 段逐字节等价"
[ -s "${WORK}/d.idx" ] && pass "②态 data 段非空" || die "②态 data 段为空"
grep -q 'ENVELOPE-SHAPE-BAD' "${WORK}/d.idx" && die "信封形态异常（warnings 键不唯一）" \
  || pass "信封形态正常（warnings 键唯一，可精确切段）"
cmp -s "${WORK}/d.noidx" "${WORK}/d.idx" \
  && pass "①无索引 == ②索引在位（两条读路径等价 ⇒ 权威仍是 Markdown）" \
  || die "①②data 段不等：$(head -c 200 "${WORK}/d.noidx") ≠ $(head -c 200 "${WORK}/d.idx")"
cmp -s "${WORK}/d.degraded" "${WORK}/d.idx" \
  && pass "③删索引降级 == ②索引在位（降级不改结果）" \
  || die "②③data 段不等（降级污染了结果）"

# ---------------------------------------------------------------- 反向关系正确性
sec "步骤3：反向关系内容正确（不是「都空所以相等」）"
RIN="$(sed -e 's/^.*"relations_in":\[//' -e 's/\].*$//' "${WORK}/d.idx")"
n_in="$(printf '%s' "${RIN}" | { grep -o '{' || true; } | wc -l | tr -d ' ')"
[ "${n_in}" = "1" ] && pass "relations_in 恰 1 条（B 收到唯一入边）" || die "relations_in 条数为 ${n_in}，期望 1"
printf '%s' "${RIN}" | grep -q "\"from\":\"${A}\"" && pass "入边来源逐字为 ${A}" || die "入边来源不是 ${A}"
printf '%s' "${RIN}" | grep -q '"type":"supports"' && pass "入边类型逐字为 supports" || die "入边类型不是 supports"

# ---------------------------------------------------------------- 降级留痕
sec "步骤4：降级留痕逐字（W23 → Q5），健康态零留痕"
[ "$(cat "${WORK}/c.idx")" = "" ] && pass "②索引在位：诊断码序列为空（无假降级留痕）" \
  || die "②态出现诊断码 [$(cat "${WORK}/c.idx")]，索引在位不应有降级痕迹"
[ "$(cat "${WORK}/c.noidx")" = "W23 Q5" ] && pass "①无索引：诊断码序列逐字恰 [W23 Q5]" \
  || die "①态诊断码序列为 [$(cat "${WORK}/c.noidx")]，期望逐字 [W23 Q5]"
[ "$(cat "${WORK}/c.degraded")" = "W23 Q5" ] && pass "③删索引：诊断码序列逐字恰 [W23 Q5]" \
  || die "③态诊断码序列为 [$(cat "${WORK}/c.degraded")]，期望逐字 [W23 Q5]"

# ---------------------------------------------------------------- 只读零副作用
sec "步骤5：只读命令零副作用"
dirty="$(git -C "${VAULT}" status --porcelain | grep -v '^?? \.index/' || true)"
[ -z "${dirty}" ] && pass "vault 工作区零变更（rel 是纯读命令）" || die "rel 污染了工作区：${dirty}"

printf '\n'
[ "${FAIL}" -eq 0 ] || { printf '[FAIL] 反向关系读路径等价性门禁未通过\n' >&2; exit 1; }
printf '[PASS] 反向关系读路径等价性门禁通过（三态 data 逐字节等价 + 降级留痕逐字 W23→Q5）\n'

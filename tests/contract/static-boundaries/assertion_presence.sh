#!/usr/bin/env bash
# 断言在场反证门禁（system_assurance · T-…-002 / 合同 D5 + D6）。
#
# 来源：m2_acceptance.sh L265-285（只读脚本含 `status --porcelain`、查询脚本含诚实诊断码、
#       PPE 留痕六件齐备）。路径按迁移后的 tests/ 布局重钉，判据本体不放宽。
#
# 为什么需要这条门禁：上层 suite 的绿色只能证明「跑过了」，不能证明「断言还在」。
# 有人把关键断言注释掉、或把 `--porcelain` 检查删掉，e2e 依然全绿。本脚本反证**断言本身在场**，
# 让「掏空断言」这类退化立刻变红。这也是合同 D5「禁止 skip / allowlist / frozen-red / 吞失败」的
# 静态侧配套：D5 管「不许屏蔽失败」，本脚本管「不许删掉判据」。
#
# 约束：离线、只读、零副作用。
# 用法：cd evergreen && bash tests/contract/static-boundaries/assertion_presence.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
cd "${REPO_ROOT}"

FAIL=0
pass() { printf '  [ok] %s\n' "$1"; }
bad()  { printf '  [FAIL] %s\n' "$1" >&2; FAIL=1; }
sec()  { printf '\n=== %s ===\n' "$1"; }

# ------------------------------------------------------- ① 只读脚本零副作用断言在场
sec "①只读命令 e2e 必须自带「工作区零变更」断言"
READONLY_SUITES=(
  tests/e2e/card-query/search.sh
  tests/e2e/card-query/card_show.sh
  tests/e2e/card-query/context_polish.sh
  tests/e2e/index-search/read_path_index.sh
  tests/e2e/ops-diagnostics/cmd_check.sh
)
for s in "${READONLY_SUITES[@]}"; do
  if [ ! -f "${s}" ]; then bad "只读 suite 缺失：${s}"; continue; fi
  if grep -Fq 'status --porcelain' "${s}"; then
    pass "${s##*/} 含 git status --porcelain 零副作用断言"
  else
    bad "${s} 丢失「只读零副作用」断言（status --porcelain）"
  fi
done

# ------------------------------------------------------- ② 查询脚本诚实诊断断言在场
sec "②查询类 e2e 必须断言诚实诊断码（Q 族）"
for s in tests/e2e/card-query/search.sh tests/e2e/card-query/card_show.sh \
         tests/e2e/index-search/degrade_fallback.sh; do
  if [ ! -f "${s}" ]; then bad "查询 suite 缺失：${s}"; continue; fi
  # 允许两种写法：直接断言 `"code":"Q…"` 字面量，或经 jcodes/ccount 抽码后断言裸 `Q<n>`。
  if grep -qE '"code":"Q|\bQ[0-9]\b' "${s}"; then
    pass "${s##*/} 含 Q 族诊断码断言（不完整/降级必须如实留痕）"
  else
    bad "${s} 丢失诚实诊断码断言（\"code\":\"Q…\"）"
  fi
done

# ------------------------------------------------------- ③ 降级留痕断言在场
sec "③索引降级 suite 必须断言 W22/W23/W24 + Q5 同现"
deg=tests/e2e/index-search/degrade_fallback.sh
if [ -f "${deg}" ]; then
  ok=1
  grep -qE 'W2[234]' "${deg}" || ok=0
  grep -q 'Q5' "${deg}" || ok=0
  [ "${ok}" = "1" ] && pass "degrade_fallback.sh 含原因码 + Q5 同现断言" \
    || bad "degrade_fallback.sh 丢失「原因码与 Q5 同现」断言"
else
  bad "缺失 ${deg}"
fi

# ------------------------------------------------------- ④ PPE 留痕六件齐备
sec "④PPE 真实会话留痕六件齐备（判据 8）"
PPE="${EG_FIXTURES}/ppe"
for f in plan.json commands.log query-before.txt query-after.txt git-log.txt session.md; do
  [ -s "${PPE}/${f}" ] && pass "PPE 留痕在场且非空：${f}" || bad "PPE 留痕缺失或为空：${f}"
done
if grep -Fq "真实 Agent Harness E2E 会话未执行" "${PPE}/session.md" 2>/dev/null; then
  printf '  [note] PPE 留痕自证「真实会话未执行」——历史结论按 M2 原样保留，本门禁只核留痕齐备\n'
else
  pass "PPE 留痕自证真实会话已执行"
fi

# ------------------------------------------------------- ⑤ 禁止屏蔽手段（D5 静态侧）
sec "⑤tests/ 下禁止 skip / allowlist / frozen-red / 吞失败"
BAN_PATHS=(tests/e2e tests/contract tests/perf tests/runner tests/lib)
pat='^[[:space:]]*(#[[:space:]]*)?(SKIP|skip_if|allowlist|ALLOWLIST|FROZEN_RED|frozen_red|EXPECTED_FAIL)='
hitfiles="$({ grep -rlE "${pat}" "${BAN_PATHS[@]}" 2>/dev/null || true; } | sort -u | tr '\n' ' ')"
[ -z "${hitfiles}" ] && pass "无 skip / allowlist / frozen-red 开关" \
  || bad "出现屏蔽开关：[${hitfiles}]"
# `|| true` 只允许出现在 grep / 计数等取值语境，不允许直接吞掉被测命令的退出码
# 允许的取值语境：命令替换赋值 `X="$(... || true)"`、能力探测 `--help` / `--version`
# （这些行的退出码本身不是判据）。其余「被测命令 || true」一律视为吞失败。
#
# 判定前先做两步归一化，避免把"写在字符串字面量里的反例文本"误判成真实吞错：
#   1) 把 EG 调用占位成 __EGCMD__（先于剥引号，否则 `"${EG}"` 会随双引号一起被剥掉）；
#   2) 剥掉单/双引号内的字符串内容——门禁自身的合成反证（printf '… || true' > 假脚本）
#      属于被写入的数据，不是本文件在执行时吞错。真实吞错行位于引号之外，归一化后仍在。
scan_swallow() {  # scan_swallow <路径...> → 打印命中行（已归一化）
  { grep -rnIE '^' "$@" 2>/dev/null || true; } \
    | sed -E 's/"\$\{EG\}"/__EGCMD__/g; s/\$\{EG\}/__EGCMD__/g; s/\$EG([^A-Za-z0-9_])/__EGCMD__\1/g' \
    | sed -E "s/'[^']*'//g; s/\"[^\"]*\"//g" \
    | grep -E '__EGCMD__[^|]*\|\|[[:space:]]*true' \
    | grep -vE 'rc=\$\?|\|\| rc=|# |\$\(|--help|--version' || true
}
swallow="$(scan_swallow "${BAN_PATHS[@]}" | head -5 | tr '\n' ' ')"
[ -z "${swallow}" ] && pass "被测命令的退出码未被 || true 吞掉" \
  || bad "疑似吞失败（被测命令 || true）：${swallow}"

# ------------------------------------------------------- ⑥ 吞失败判据自身的双侧反证
# 归一化不得把判据削弱成"永不报警"：正例必须命中，反例必须不命中。
sec "⑥吞失败判据的双侧反证（判据本身不得被归一化掏空）"
PROBE="$(eg_scratch eg-assert-probe)"
mkdir -p "${PROBE}/d"
{ printf '#!/usr/bin/env bash\nset -Eeuo pipefail\n'
  printf '"${EG}" add note.md || true\n'; } > "${PROBE}/d/positive.sh"
{ printf '#!/usr/bin/env bash\nset -Eeuo pipefail\n'
  printf 'printf %s > fake.sh\n' "'\"\${EG}\" rel --json || true'"
  printf 'CNT="$("${EG}" search x --json || true)"\n'
  printf '"${EG}" --help >/dev/null || true\n'; } > "${PROBE}/d/negative.sh"
[ -n "$(scan_swallow "${PROBE}/d/positive.sh")" ] \
  && pass "正例（裸 EG 调用被 || true 吞掉）被判据抓住" \
  || bad "判据失效：真实吞失败未被抓住"
[ -z "$(scan_swallow "${PROBE}/d/negative.sh")" ] \
  && pass "反例（字符串字面量 / 取值语境 / --help 探测）不误报" \
  || bad "判据误报：合法语境被判为吞失败：$(scan_swallow "${PROBE}/d/negative.sh" | tr '\n' ' ')"
rm -rf "${PROBE}"

printf '\n'
[ "${FAIL}" -eq 0 ] || { printf '[FAIL] 断言在场反证未通过\n' >&2; exit 1; }
printf '[PASS] 断言在场反证通过（关键判据在场 + 无屏蔽手段）\n'

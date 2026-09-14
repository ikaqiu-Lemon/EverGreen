#!/usr/bin/env bash
# 退出码与诊断码合同门禁（system_assurance · T-…-002 / 合同 D5 + D7）。
#
# 来源与形态转换（migration.tsv 中 4 条 merge 行的落点）：
#   · m2_acceptance.sh L147-155 —— 占位标记门禁（`Placeholder: true` == 0、`PlaceholderNotice` 落点封闭）
#   · m4_acceptance.sh L184     —— `os.Exit(5)` 字面量 == 0
#   · m5_acceptance.sh L134-178 —— 退出码全集 {0..6} + 取值 5 常量唯一 + 无 W21
#   · m6_acceptance.sh L105-125 —— **最严版本作为规范实现**：退出码全集逐字、取值 5 常量恰 1 且名
#                                   ExitPrecheckOrLock、os.Exit(5) == 0、E15/E16/W26/W27/W28 在册、"W21" == 0
#
# 形态转换：历史脚本按「阶段冻结计数」判定；本脚本改判**取值集合逐字相等**与**符号唯一性 / 在册性**，
# 语义等价但不冻结阶段数字（差异登记 I-…-009，历史脚本原样留档 tests/archive/）。
#
# 约束：离线、只读、零副作用。
# 用法：cd evergreen && bash tests/contract/static-boundaries/exit_codes_and_diagnostics.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
cd "${REPO_ROOT}"

FAIL=0
pass() { printf '  [ok] %s\n' "$1"; }
bad()  { printf '  [FAIL] %s\n' "$1" >&2; FAIL=1; }
sec()  { printf '\n=== %s ===\n' "$1"; }

# ------------------------------------------------------- ① 退出码取值全集逐字 {0..6}
sec "①退出码常量取值全集逐字为 {0,1,2,3,4,5,6}"
consts="$({ grep -rhnE '^[[:space:]]*(const[[:space:]]+)?Exit[A-Za-z]+[[:space:]]*=[[:space:]]*[0-9]+' \
             internal/cli/exit.go internal/cli/exitcode.go || true; } \
          | sed -E -e 's/^[0-9]+:[[:space:]]*//' -e 's/^const[[:space:]]+//' | tr -d ' \t')"
vals="$(printf '%s\n' "${consts}" | cut -d= -f2 | sort -u | tr '\n' ' ' | sed 's/ *$//')"
if [ "${vals}" = "0 1 2 3 4 5 6" ]; then
  pass "退出码取值集合 = [${vals}]"
else
  bad "退出码取值集合越界（期望 [0 1 2 3 4 5 6]，实际 [${vals}]）"
fi

# ------------------------------------------------------- ② 取值 5 的常量恰 1 且名固定
sec "②取值 5 的退出码常量恰一个，且名为 ExitPrecheckOrLock"
five="$(printf '%s\n' "${consts}" | grep '=5$' || true)"
n5="$(printf '%s\n' "${five}" | grep -c . || true)"
if [ "${n5}" = "1" ] && [ "${five}" = "ExitPrecheckOrLock=5" ]; then
  pass "取值 5 的常量唯一且逐字为 ExitPrecheckOrLock（M6 写前校验 / 锁不可用合同）"
else
  bad "取值 5 的退出码常量不唯一或命名漂移（实际 [$(printf '%s' "${five}" | tr '\n' ' ')]，条数 ${n5}）"
fi

# ------------------------------------------------------- ③ 禁止裸 os.Exit(5)
sec "③禁止裸 os.Exit(5)（退出码只许经信封 / 常量出口）"
n="$({ grep -rn 'os\.Exit(5)' internal/ cmd/ --include='*.go' || true; } | grep -vc '_test\.go:' || true)"
[ "${n}" = "0" ] && pass "os.Exit(5) 字面量零命中" || bad "存在 ${n} 处裸 os.Exit(5)（绕过信封出口）"

# ------------------------------------------------------- ④ 占位标记门禁（源 m2）
sec "④占位标记：非测试源零 Placeholder: true，PlaceholderNotice 落点封闭"
npt="$({ grep -rn 'Placeholder:[[:space:]]*true' internal/ cmd/ --include='*.go' || true; } \
        | grep -vc '_test\.go:' || true)"
[ "${npt}" = "0" ] && pass "Placeholder: true 零命中（无未实现占位被当成能力发布）" \
  || bad "存在 ${npt} 处 Placeholder: true"
pn="$({ grep -rl 'PlaceholderNotice' internal/ cmd/ --include='*.go' || true; } \
      | grep -v '_test\.go$' | sort -u | tr '\n' ' ' | sed 's/ *$//')"
if [ "${pn}" = "internal/cli/placeholder.go internal/cli/root.go" ]; then
  pass "PlaceholderNotice 落点逐字封闭 = [${pn}]"
else
  bad "PlaceholderNotice 落点越界（期望 [internal/cli/placeholder.go internal/cli/root.go]，实际 [${pn}]）"
fi

# ------------------------------------------------------- ⑤ 诊断码在册性与退役码
# C2 · I-…-015 追加：命令层新码 E17–E25 一并纳入「在册 + 写进 SKILL.md」的同一条判据
# （新增 9 个码的等号，是加严；M6 的 6 个码逐字保留、一格不放宽）。
sec "⑤M6 + 命令层新增诊断码在册 + 退役码 W21 零残留"
expected_file() {
  case "$1" in
    E15|E16|W28) printf '%s' "internal/txn/doc.go" ;;
    W26) printf '%s' "internal/txn/recover.go" ;;
    W27) printf '%s' "internal/mdfile/block_merge.go" ;;
    W23) printf '%s' "internal/index/corrupt.go" ;;
    E17|E18|E19|E20|E21|E22|E23|E24|E25) printf '%s' "internal/cli/codes.go" ;;
    *) return 1 ;;
  esac
}
for code in E15 E16 W23 W26 W27 W28 E17 E18 E19 E20 E21 E22 E23 E24 E25; do
  expected="$(expected_file "${code}")"
  where="$({ grep -rl "\"${code}\"" internal/ --include='*.go' || true; } | grep -v '_test\.go$' | sort -u | tr '\n' ' ' | sed 's/ *$//')"
  if [ -z "${where}" ]; then
    bad "诊断码 ${code} 未在册（产品侧无任何常量定义）"
  elif printf '%s' "${where}" | grep -qF "${expected}"; then
    pass "诊断码 ${code} 在册（定义处含 ${expected}）"
  else
    bad "诊断码 ${code} 定义处漂移（期望含 ${expected}，实际 [${where}]）"
  fi
  grep -q "\`${code}\`" skill/SKILL.md \
    || bad "诊断码 ${code} 未写入 skill/SKILL.md（Agent 侧无法如实转述）"
done
# ------------------------------------------------------- ⑥ error 级诊断禁止空码（全域静态门禁）
# I-…-015：合同 §5 只授权「未编号 **warning** 填 `""`」。error 级空码会让接入方无法按码分支
# （只能对随时可改的中文 message 做子串匹配），故此处按**构造性**判据封死。空码有两副面孔，
# 都要封：
#   a) 显式写了 `Code: ""`；
#   b) 整条 Diagnostic 字面量**压根没有 Code 字段** —— Go 零值同样落成 `code:""`，
#      这一副面孔单靠 grep `Code:\s*""` 抓不到，必须靠花括号配对做字面量级判断。
sec "⑥error 级诊断零空码（显式 Code: \"\" 与省略 Code 字段一并封死）"
badsites="$(python3 - <<'PYEOF'
import glob, io, re

def enclosing_literal(text, pos):
    """返回包含 text[pos] 的最内层 {...} 片段（花括号配对，跨行安全）。"""
    depth, i, start = 0, pos, None
    while i > 0:
        i -= 1
        c = text[i]
        if c == '}':
            depth += 1
        elif c == '{':
            if depth == 0:
                start = i
                break
            depth -= 1
    if start is None:
        return None
    depth, j = 0, start
    while j < len(text):
        c = text[j]
        if c == '{':
            depth += 1
        elif c == '}':
            depth -= 1
            if depth == 0:
                return text[start:j + 1]
        j += 1
    return None

hits = []
for path in sorted(glob.glob('internal/**/*.go', recursive=True)
                   + glob.glob('cmd/**/*.go', recursive=True)):
    if path.endswith('_test.go'):
        continue
    text = io.open(path, encoding='utf-8').read()
    lines = text.split('\n')
    # a) 显式空码：本行 + 后 5 行窗口内出现 LevelError
    for i, line in enumerate(lines):
        if re.search(r'Code:\s*""', line) and 'LevelError' in ' '.join(lines[i:i + 6]):
            hits.append(f'{path}:{i + 1}（显式 Code: ""）')
    # b) 省略 Code 字段：LevelError 所在的诊断字面量内一个 Code: 都没有
    idx = 0
    while True:
        k = text.find('LevelError', idx)
        if k < 0:
            break
        idx = k + 1
        lit = enclosing_literal(text, k)
        if lit is None or 'Level:' not in lit or 'Code:' in lit:
            continue
        hits.append(f'{path}:{text[:k].count(chr(10)) + 1}（省略 Code 字段 → 零值空码）')
print('\n'.join(hits))
PYEOF
)"
nbad="$(printf '%s' "${badsites}" | grep -c . || true)"
if [ "${nbad}" = "0" ]; then
  pass "error 级诊断零空码（显式 + 省略两副面孔的全域构造性判据）"
else
  bad "存在 ${nbad} 处 error 级空码诊断（合同 §5 只对 warning 开放空码）：$(printf '%s' "${badsites}" | tr '\n' ' ')"
fi

# ------------------------------------------------------- ⑦ cause 枚举不得占用 code 位
sec "⑦skipped[].kind / cause 枚举不得出现在 Code: 位（编号域封闭）"
ncause="$({ grep -rnE 'Code:[[:space:]]*(s\.Cause|[a-z]+\.Cause|"(file_changed|content_hash_mismatch|user_block_unsafe|user_block_not_preserved)")' \
             internal/ cmd/ --include='*.go' || true; } | grep -vc '_test\.go:' || true)"
if [ "${ncause}" = "0" ]; then
  pass "cause / kind 枚举零占用 code 位（cause 的家是 report.skipped[].cause）"
else
  bad "存在 ${ncause} 处把 skipped[].cause / kind 塞进 code 位（越出 E*/W*/I*/Q* 编号域）"
fi

# ------------------------------------------------------- ⑧ 命令层码集合封闭（双侧等号）
# 沿用 M4→reconcile、M5→index/query、M6→txn/mdfile 的「按域封闭」先例：命令层新码的**字面量**
# 只许落在 internal/cli/codes.go 一个文件，且该文件的码集合恰 {E17..E25} —— 多一码 / 少一码 /
# 挪个地方都当场红（是加严，不是放宽：其余文件一律引用常量名，不得再写码字面量）。
sec "⑧命令层码字面量落点封闭 + codes.go 码集合恰 E17–E25"
where_cmd="$({ grep -rlE '"E(1[7-9]|2[0-5])"' internal/ cmd/ --include='*.go' || true; } \
             | grep -v '_test\.go$' | sort -u | tr '\n' ' ' | sed 's/ *$//')"
if [ "${where_cmd}" = "internal/cli/codes.go" ]; then
  pass "命令层码字面量落点逐字封闭 = [${where_cmd}]"
else
  bad "命令层码字面量落点越界（期望 [internal/cli/codes.go]，实际 [${where_cmd}]）"
fi
got_cmd="$(grep -ohE '"E(1[7-9]|2[0-5])"' internal/cli/codes.go | tr -d '"' | sort -u | tr '\n' ' ' | sed 's/ *$//')"
if [ "${got_cmd}" = "E17 E18 E19 E20 E21 E22 E23 E24 E25" ]; then
  pass "codes.go 码集合逐字恰 {E17…E25}（九值，无空洞无扩张）"
else
  bad "codes.go 码集合漂移（期望 [E17 E18 E19 E20 E21 E22 E23 E24 E25]，实际 [${got_cmd}]）"
fi

n21="$({ grep -rn '"W21"' internal/ cmd/ skill/ docs/ 2>/dev/null || true; } | grep -vc '_test\.go:' || true)"
[ "${n21}" = "0" ] && pass "退役诊断码 W21 零残留" || bad "W21 仍有 ${n21} 处残留"

printf '\n'
[ "${FAIL}" -eq 0 ] || { printf '[FAIL] 退出码与诊断码合同门禁未通过\n' >&2; exit 1; }
printf '[PASS] 退出码与诊断码合同门禁通过（取值集合逐字 + 常量唯一 + 诊断码在册）\n'

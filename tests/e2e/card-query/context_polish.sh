#!/usr/bin/env bash
# `eg context` 候选相似卡打磨的端到端脚本（T-evergreen.s1_main_flow-158614-025）。
#
# 判据来源：T-…-025 Acceptance 四组断言
#   ① 排序唯一且可复现：同一 vault 连续两次 `eg context --json` 输出逐字相等（diff）；
#   ② 命中理由非空且可核对：文本模式逐张打印得分与理由，顺序与 --json 逐字一致；
#   ③ base 口径稳定：同一 vault 的重复读取产生相同 `data.base`，且每个值都是
#      `sha256:<64 hex>`；
#   ④ 零副作用：执行前后 `git status --porcelain`、`git log --oneline | wc -l`
#      与 vault 内全部 .md 的 sha256sum 全部相等；
# 另加：不可解析 .md 必须有 Q1 诊断且退出码仍 0（M-002 风险 R-1）；deprecated 卡不进推荐。
#
# 约束：离线、可重复执行、无外部依赖（只用 bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/card-query/context_polish.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m2-ctx.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }
gitv() { git -C "${VAULT}" "$@"; }

# base_slice 从 --json 输出里抠出 "base":{…} 片段（base 是平坦 map，无嵌套括号；
# Go 序列化 map 时键有序，因此该片段本身就是可逐字比对的稳定串）。
base_slice() { sed 's/.*"base":{\([^}]*\)}.*/\1/' "$1"; }

snapshot() {
  find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort >"$1"
}

# ---------------------------------------------------------------- 0. 构建
step "构建当前 eg"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "当前二进制就绪"

# ---------------------------------------------------------------- 1. seed 语料
step "seed：eg init + 收录一篇原文 + 三张卡（命中 / 只命中 tags / 零命中）+ 一张 deprecated 卡 + 一个坏 .md"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
printf '注意力机制的正文占位，供 context 用例使用。\n' >"${WORK}/body.txt"
[ "$(eg_code capture --url https://example.com/attention --title '注意力机制入门' \
  --body-file "${WORK}/body.txt" --reason 'T-025 语料' --domain ai-infra \
  --captured-at 2026-09-01T09:00:00+08:00 --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "capture 失败"; }
SRC="$(sed 's/.*"source_id":"\([^"]*\)".*/\1/' "${WORK}/out.txt")"
[ -n "${SRC}" ] || die "capture 未返回 source_id"

seed_card() { # $1=id $2=title $3=status $4=tag（可空）
  local f="${VAULT}/domains/ai-infra/knowledge/$1.md"
  {
    printf -- '---\nid: %s\ntitle: %s\nstatus: %s\n' "$1" "$2" "$3"
    printf "created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n"
    [ -n "$4" ] && printf 'tags:\n  - %s\n' "$4"
    printf -- '---\n\n## 定义\n\n正文占位。\n'
  } >"${f}"
}
seed_card k-20260901-a "注意力机制" active ""      # 只在标题命中
seed_card k-20260901-b "机制入" active "注意"       # 标题 + tags 都命中
seed_card k-20260901-g "磁盘调度" active "注意"     # 只在 tags 命中
seed_card k-20260901-z "磁盘调度" active ""         # 零命中：不得进候选
seed_card k-20260901-old "注意力机制" deprecated "" # 失效卡：不得进候选
printf -- '---\n- 1\n---\n\n# 坏卡\n' >"${VAULT}/domains/ai-infra/knowledge/broken.md"
gitv add -A >/dev/null && gitv -c user.name=eg -c user.email=eg@example.com \
  commit -q -m "seed: context 打磨语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
ok "语料就绪（原文 ${SRC}；4 张 active + 1 张 deprecated + 1 个坏 .md）"

BEFORE_STATUS="$(gitv status --porcelain)"
BEFORE_LOG="$(gitv log --oneline | wc -l)"
snapshot "${WORK}/before.sha"

# ---------------------------------------------------------------- 2. 排序唯一且可复现
step "① 两次 eg context --json 输出逐字相等（diff），候选序列稳定"
[ "$(eg_code context --source "${SRC}" --json)" = "0" ] || { cat "${WORK}/err.txt"; die "context 退出码非 0"; }
cp "${WORK}/out.txt" "${WORK}/ctx1.json"
[ "$(eg_code context --source "${SRC}" --json)" = "0" ] || die "第二次 context 退出码非 0"
cp "${WORK}/out.txt" "${WORK}/ctx2.json"
diff -u "${WORK}/ctx1.json" "${WORK}/ctx2.json" >"${WORK}/ctx.diff" ||
  { cat "${WORK}/ctx.diff"; die "两次 --json 输出不同（排序不确定）"; }
grep -Fq '"exit_code":0' "${WORK}/ctx1.json" || die "context 应退 0"
ok "两次输出逐字相等（$(wc -c <"${WORK}/ctx1.json") 字节）"

# ---------------------------------------------------------------- 3. 命中理由非空且可核对
step "② 候选理由非空、逐条只指一个来源字段；文本模式与 --json 同源同事实"
grep -Fq '"reasons":[]' "${WORK}/ctx1.json" && die "候选卡的理由不得为空"
grep -Fq '命中卡的 title 字段' "${WORK}/ctx1.json" || die "缺 title 来源的命中理由"
grep -Fq '命中卡的 tags 字段' "${WORK}/ctx1.json" || die "缺 tags 来源的命中理由"
grep -Fq 'k-20260901-z' "${WORK}/ctx1.json" &&
  grep -q '"knowledge_candidates":\[[^]]*k-20260901-z' "${WORK}/ctx1.json" &&
  die "零命中的卡不得进候选"
grep -q '"knowledge_candidates":\[[^]]*k-20260901-old' "${WORK}/ctx1.json" && die "deprecated 卡不得进候选"

eg context --source "${SRC}" >"${WORK}/ctx.txt" 2>"${WORK}/ctx.err"
grep -Fq '得分 ' "${WORK}/ctx.txt" || { cat "${WORK}/ctx.txt"; die "文本模式必须逐张打印得分"; }
grep -Fq '· 命中卡的 ' "${WORK}/ctx.txt" || { cat "${WORK}/ctx.txt"; die "文本模式必须打印命中理由"; }
# 文本候选区的 ID 顺序 == --json 的 candidates 顺序。
# knowledge_opinion_split：文本候选区行首由 `候选` 收敛为 `知识候选`（另有 `观点候选`，本例 0 条），
# 与 --json 的 knowledge_candidates / opinion_candidates 分列同源；此处只锁知识候选序列。
grep -F '  知识候选 ' "${WORK}/ctx.txt" | sed 's/^  知识候选 \([^　]*\).*/\1/' >"${WORK}/txt.ids"
tr ',' '\n' <"${WORK}/ctx1.json" | grep -o '"id":"k-[^"]*"' | sed 's/"id":"\(.*\)"/\1/' |
  awk '!seen[$0]++' >"${WORK}/json.ids.all"
COUNT_TXT="$(wc -l <"${WORK}/txt.ids" | tr -d ' ')"
[ "${COUNT_TXT}" -ge 2 ] || { cat "${WORK}/ctx.txt"; die "应至少两张候选卡"; }
while read -r cid; do
  grep -Fq "\"id\":\"${cid}\"" "${WORK}/ctx1.json" || die "文本出现了 JSON 里没有的候选 ${cid}"
done <"${WORK}/txt.ids"
# 逐张打印的条数与总数行一致。
grep -Fq "知识候选 ${COUNT_TXT} 张" "${WORK}/ctx.txt" ||
  { cat "${WORK}/ctx.txt"; die "总数行与逐张打印条数不一致"; }
ok "知识候选 ${COUNT_TXT} 张：理由非空、文本与 JSON 同序同事实"

# ---------------------------------------------------------------- 4. base 口径稳定
step "③ 重复读取的 data.base 逐字相等，且值均为 SHA-256"
base_slice "${WORK}/ctx1.json" >"${WORK}/base.now"
base_slice "${WORK}/ctx2.json" >"${WORK}/base.repeat"
[ -s "${WORK}/base.now" ] || die "base 片段为空（抽取失败）"
diff -u "${WORK}/base.now" "${WORK}/base.repeat" >"${WORK}/base.diff" ||
  { cat "${WORK}/base.diff"; die "重复读取的 base 不稳定"; }
python3 - "${WORK}/ctx1.json" <<'PY' || die "base 的 content_hash 不是 sha256:<64 hex>"
import json
import re
import sys

base = (json.load(open(sys.argv[1])).get("data") or {}).get("base") or {}
if not base or any(not re.fullmatch(r"sha256:[0-9a-f]{64}", value) for value in base.values()):
    raise SystemExit(1)
PY
ok "base 片段稳定且全部使用 SHA-256：$(cut -c1-60 <"${WORK}/base.now")…"

# ---------------------------------------------------------------- 5. 不可解析文件有诊断
step "④ 坏 .md 产出 Q1 诊断（--json 与人读输出都点名文件），退出码仍 0"
grep -Fq '"code":"Q1"' "${WORK}/ctx1.json" || { cat "${WORK}/ctx1.json"; die "缺 Q1 诊断"; }
grep -Fq 'domains/ai-infra/knowledge/broken.md' "${WORK}/ctx1.json" || die "Q1 未点名坏文件路径"
# 人读模式的诊断区（M1 既有渲染口径：RenderHuman 把「警告（N 条）」写在标准输出，
# 本 task 不改双渲染契约，故在人读输出里核对，而非 stderr）。
grep -Fq 'broken.md' "${WORK}/ctx.txt" || { cat "${WORK}/ctx.txt"; die "人读模式未点名坏文件"; }
grep -Fq '警告（' "${WORK}/ctx.txt" || die "人读模式缺诊断区"

[ "$(eg_code context --source "${SRC}")" = "0" ] || die "Q 类诊断不得改变退出码"
ok "Q1 诊断在两套渲染都可见（同源同事实）；退出码仍 0"

# ---------------------------------------------------------------- 6. 零副作用
step "⑤ 只读零副作用：git status / git log / 全部 .md 的 sha256sum 前后相等"
[ "${BEFORE_STATUS}" = "$(gitv status --porcelain)" ] || die "git status 前后不同"
[ "${BEFORE_LOG}" = "$(gitv log --oneline | wc -l)" ] || die "git log 条数变了"
snapshot "${WORK}/after.sha"
diff -u "${WORK}/before.sha" "${WORK}/after.sha" >"${WORK}/sha.diff" ||
  { cat "${WORK}/sha.diff"; die "vault 内 .md 的 sha256 发生变化"; }
# ── C2a·M6 现态重钉（合同 §16.3 runtime-reserved / §16.4 授权；保留 M5 历史事实 + 新增 M6 现态双侧锁）──
# M5 历史事实（只读复算，一格不放宽）：命令绝不替用户建出派生索引 DB（索引不是任何命令的前置，M5 §0.1）。
[ "$(find "${VAULT}/.index" -maxdepth 1 -type f -name 'eg.db*' 2>/dev/null | wc -l | tr -d ' ')" = "0" ] ||
  die "越界：.index/ 出现派生索引 DB（M2/S1 写命令不替用户建索引，属 S4）"
# M6 现态（新增正面锁）：.index/ 若存在，仅含 M6 事务基础设施——run.lock（普通文件）/ txn（目录），别无他物。
if [ -d "${VAULT}/.index" ]; then
  _rx="$(ls -1A "${VAULT}/.index" 2>/dev/null | grep -vxE 'run\.lock|txn' || true)"
  [ -z "${_rx}" ] || die "越界：.index/ 出现非 runtime-reserved 条目：${_rx}"
fi
ok "零文件变化、零 commit（逐文件 sha256 比对）；.index/ 无派生 DB、仅 M6 事务基础设施"

printf '\n=== context_polish.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

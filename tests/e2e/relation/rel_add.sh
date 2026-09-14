#!/usr/bin/env bash
# `eg rel add` 写路径端到端脚本（T-evergreen.s1_main_flow-158614-024）。
#
# 判据来源：M2 查询与关系写入合同 `2026-09-19-m2-query-contract.md`
#   §4.1 参数与校验（type 封闭四值 / --reason 必填 / E2 / E3 / W2）
#   §4.2 走 ChangePlan → plan → executor → store → git 的既有写入链路（禁止绕过）
#   §4.3 opposing 字典序单向归一 + 同对去重（W8）
#   §4.4 报告与**恰一次** `relate` commit
#   §4.5 退出码 0 / 1 / 2 / 3 / 4
#   §7   `eg rel remove` 的阶段占位已由 T-…-044 接管为真实写入（本脚本第 6 步同步改判）
# 另：技术方案 §9 的 B1（默认只追加）/ B2（用户内容逐字保留）/ B3（content_hash）。
#
# 约束：离线、可重复执行、无外部依赖（只用 bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/relation/rel_add.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m2-rel-add.XXXXXX")"
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
log_count() { gitv log --oneline | wc -l | tr -d ' '; }

CARD_A="${VAULT}/domains/ai-infra/knowledge/k-20260901-attention.md"
CARD_B="${VAULT}/domains/ai-infra/knowledge/k-20260902-rnn.md"
CARD_C="${VAULT}/domains/ops/knowledge/k-20260903-ops.md"

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed 语料
step "seed 三张无关系的干净卡（A / B 在 ai-infra，C 在 ops）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set domains ai-infra,ops)" = "0" ] || die "config set domains 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set default_domain 失败"

mkdir -p "${VAULT}/domains/ai-infra/knowledge" "${VAULT}/domains/ops/knowledge"
seed_card() { # $1=路径 $2=id $3=标题
  cat >"$1" <<CARD
---
id: $2
status: active
created_at: '2026-09-01'
updated_at: '2026-09-12T10:00:00+08:00'
title: $3
sources: []
---

## 知识内容

用户手写的正文段落，B2 要求逐字保留。

## 我的思考

这一段也必须原样不动。
CARD
}
seed_card "${CARD_A}" k-20260901-attention "注意力机制的计算代价"
seed_card "${CARD_B}" k-20260902-rnn "RNN 的长序列表现"
seed_card "${CARD_C}" k-20260903-ops "值班经验：长序列请求的超时"
gitv add -A >/dev/null && gitv -c user.name=eg -c user.email=eg@example.com \
  commit -q -m "seed: rel add 语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
cp "${CARD_A}" "${WORK}/a.before"
BODY_BEFORE="$(sed -n '/^---$/,$p' "${CARD_A}" | tail -n +2 | sed -n '/^---$/,$p')"
ok "语料就绪；工作区干净；HEAD=$(gitv rev-parse --short HEAD)"

# ---------------------------------------------------------------- 2. 恰一次 relate commit
step "eg rel add A supports B：写入生效 + 恰一次 relate commit（§4.4）"
BEFORE="$(log_count)"
[ "$(eg_code --json rel add k-20260901-attention supports k-20260902-rnn \
  --reason '注意力那张卡支持 RNN 的结论')" = "0" ] ||
  { cat "${WORK}/err.txt"; die "rel add 应退 0"; }
AFTER="$(log_count)"
[ "$((AFTER - BEFORE))" = "1" ] || die "一次 rel add 必须恰产生一次 commit，实际 +$((AFTER - BEFORE))"
SUBJECT="$(gitv log -1 --pretty=%s)"
case "${SUBJECT}" in
  "relate(ai-infra): "*) ;;
  *) die "commit 主题必须是 relate(<domain>): …，实际：${SUBJECT}" ;;
esac
grep -Fq '"verb":"relate"' "${WORK}/out.txt" || die "--json 回带的 plan 缺 verb=relate"
grep -Fq '"op":"add_relation"' "${WORK}/out.txt" || die "--json 回带的 plan 缺 add_relation op"
grep -Fq '"exit_code":0' "${WORK}/out.txt" || die "信封 exit_code 应为 0"
! grep -Fq '"code":"W5"' "${WORK}/out.txt" ||
  die "专用 rel add 计划不应因空 convergence[] 产生 W5"
! grep -Fq 'convergence[] 缺条目' "${WORK}/out.txt" ||
  die "专用 rel add 计划不应报告 convergence[] 缺条目"
[ -z "$(gitv status --porcelain)" ] || die "commit 后工作区应干净（写入已入库）"
ok "退 0；commit +1；主题 ${SUBJECT}"

step "关系确实落盘且读路径能读回；B1 / B2 不回归"
grep -q "type: 'supports'" "${CARD_A}" || die "A 卡缺 supports 关系"
grep -q "target: 'k-20260902-rnn'" "${CARD_A}" || die "A 卡关系目标不对"
[ "$(grep -c '^  - type:' "${CARD_A}")" = "1" ] || die "relations[] 应恰一条"
grep -Fq '用户手写的正文段落，B2 要求逐字保留。' "${CARD_A}" || die "B2：正文被改动"
grep -Fq '这一段也必须原样不动。' "${CARD_A}" || die "B2：第二个分区被改动"
[ "$(sed -n '/^---$/,$p' "${CARD_A}" | tail -n +2 | sed -n '/^---$/,$p')" = "${BODY_BEFORE}" ] ||
  die "B1 / B2：frontmatter 之后的正文字节必须逐字不变"
[ "$(eg_code --json rel k-20260901-attention)" = "0" ] || die "eg rel A 应退 0"
grep -Fq '"type":"supports","target":"k-20260902-rnn"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "读路径应读回新写入的正向关系"; }
[ "$(eg_code --json rel k-20260902-rnn)" = "0" ] || die "eg rel B 应退 0"
grep -Fq '"relations_in"' "${WORK}/out.txt" || die "B 的信封缺 relations_in"
grep -Fq '"from":"k-20260901-attention"' "${WORK}/out.txt" || die "B 的反向应出现 A"
ok "关系落盘、正文逐字保留、读路径正反向都能读回"

# ---------------------------------------------------------------- 3. 幂等：无实际改动不产生空 commit
step "重复写同一条关系：幂等、零新增条目、零空 commit"
BEFORE="$(log_count)"
[ "$(eg_code rel add k-20260901-attention supports k-20260902-rnn \
  --reason '注意力那张卡支持 RNN 的结论')" = "0" ] || die "重复写入应退 0"
[ "$(log_count)" = "${BEFORE}" ] || die "无实际改动不得产生空 commit"
[ "$(grep -c '^  - type:' "${CARD_A}")" = "1" ] || die "重复写入不得新增条目"
[ -z "$(gitv status --porcelain)" ] || die "幂等路径不得留下工作区改动"
ok "第二次写入退 0、commit 数不变、条目数不变"

# ---------------------------------------------------------------- 4. opposing 字典序单向归一 + 去重
step "eg rel add C opposing A：按字典序归一到 A 侧，记 W8（§4.3）"
BEFORE="$(log_count)"
# 'k-20260901-attention' < 'k-20260903-ops'：方向必须被翻转到 A 为 from。
[ "$(eg_code --json rel add k-20260903-ops opposing k-20260901-attention \
  --reason '值班经验与该结论互斥')" = "0" ] || { cat "${WORK}/err.txt"; die "opposing 写入应退 0"; }
AFTER="$(log_count)"; [ "$((AFTER - BEFORE))" = "1" ] || die "opposing 写入应恰一次 commit"
grep -Fq '"code":"W8"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "方向归一必须记 W8"; }
grep -q "type: 'opposing'" "${CARD_A}" || die "opposing 必须落在字典序在前的 A 卡"
grep -q "target: 'k-20260903-ops'" "${CARD_A}" || die "A 卡的 opposing 目标应为 C"
! grep -q "type: 'opposing'" "${CARD_C}" || die "字典序在后的 C 卡不得留 opposing（单向存储）"
TOTAL="$(grep -c "type: 'opposing'" "${CARD_A}" || true)"
[ "${TOTAL}" = "1" ] || die "全库 opposing 记录必须恰一条，实际 ${TOTAL}"
ok "opposing 单向落在 A 侧；W8 已记；commit +1"

step "反向再写同一对 opposing：幂等跳过、零 commit、零新增条目"
BEFORE="$(log_count)"
[ "$(eg_code --json rel add k-20260901-attention opposing k-20260903-ops \
  --reason '值班经验与该结论互斥')" = "0" ] || die "同对 opposing 重复写入应退 0"
[ "$(log_count)" = "${BEFORE}" ] || die "同对已存在不得产生空 commit"
grep -Fq '"code":"W8"' "${WORK}/out.txt" || die "同对已存在必须记 W8"
[ "$(grep -c "type: 'opposing'" "${CARD_A}")" = "1" ] || die "同对 opposing 不得出现第二条"
[ -z "$(gitv status --porcelain)" ] || die "幂等路径不得留下工作区改动"
ok "同对去重生效：退 0、W8、条目与 commit 数均不变"

# ---------------------------------------------------------------- 5. 校验失败零写入零 commit
step "参数 / 校验失败：一律零写入、零 commit（§4.1 / §4.5）"
LOG_BEFORE="$(log_count)"
find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort >"${WORK}/before.sha"

[ "$(eg_code rel add k-20260901-attention frobnicate k-20260902-rnn --reason r)" = "1" ] ||
  die "type 出四值应退 1"
grep -Fq 'derives' "${WORK}/err.txt" || die "type 非法时应列出合法四值"
[ "$(eg_code rel add k-20260901-attention supports k-20260902-rnn)" = "1" ] || die "缺 --reason 应退 1"
[ "$(eg_code rel add k-20260901-attention supports)" = "1" ] || die "位置参数不足应退 1"
[ "$(eg_code rel add k-20260901-attention supports k-20260902-rnn extra --reason r)" = "1" ] ||
  die "位置参数过多应退 1"
[ "$(eg_code --json rel add k-20260901-attention supports s-20260901-x --reason r)" = "2" ] ||
  die "target 写成原文 ID 应退 2（E3）"
grep -Fq '"code":"E3"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "应产出 E3"; }
[ "$(eg_code --json rel add k-20260901-attention supports k-20260909-missing --reason r)" = "2" ] ||
  die "目标卡不存在应退 2（E2）"
grep -Fq '"code":"E2"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "应产出 E2"; }

[ "$(log_count)" = "${LOG_BEFORE}" ] || die "校验失败不得产生 commit"
[ -z "$(gitv status --porcelain)" ] || die "校验失败必须零写入"
find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort >"${WORK}/after.sha"
diff -u "${WORK}/before.sha" "${WORK}/after.sha" >/dev/null || die "校验失败不得改动任何字节"
ok "六类失败路径退 1 / 2；字节、工作区、commit 数全部不变"

step "给了空 --reason：W2 只提示不拦截（§4.1）"
BEFORE="$(log_count)"
[ "$(eg_code --json rel add k-20260902-rnn limits k-20260903-ops --reason '')" = "0" ] ||
  { cat "${WORK}/err.txt"; die "空 reason 应照写并退 0"; }
grep -Fq '"code":"W2"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "空 reason 必须记 W2"; }
AFTER="$(log_count)"; [ "$((AFTER - BEFORE))" = "1" ] || die "空 reason 仍应正常写入并提交一次"
grep -q "type: 'limits'" "${CARD_B}" || die "W2 不拦截：关系仍须写入"
ok "空 reason：退 0、记 W2、关系照写"

# ---------------------------------------------------------------- 6. rel remove 已被 M3 接管
# **T-…-044 重钉**：本段原先断言 §7 的阶段占位（退 1 + 逐字「未实现」+ 零写入零 commit）。
# 占位被接管后该断言必然自相矛盾，故改为**真实行为**断言：命中即删一条、恰一次 relate commit，
# 且输出里不得再出现任何阶段未实现宣告。W10 / Agent 路径 / 重跑幂等的完整取证在 m3_rel_remove.sh。
step "eg rel remove：真实写入——命中删除、恰一次 relate commit、无未实现宣告"
LOG_BEFORE="$(log_count)"
# 先补一条可删的关系（本步之前 A→B 的 supports 已在第 2 步写入，这里直接删它）。
grep -q "target: 'k-20260902-rnn'" "${CARD_A}" || die "前置条件：A 卡应已有指向 B 的关系"
[ "$(eg_code rel remove k-20260901-attention supports k-20260902-rnn --reason '该支持关系已不成立')" = "0" ] ||
  { cat "${WORK}/err.txt"; die "命中删除应退 0"; }
if grep -Fq '未实现' "${WORK}/out.txt" "${WORK}/err.txt"; then die "rel remove 不得再宣告未实现"; fi
[ "$((LOG_BEFORE + 1))" = "$(log_count)" ] || die "一次成功删除应恰产生一次 commit"
gitv log -1 --pretty=%s | grep -q '^relate(' || die "commit 主题应以 relate( 开头"
[ -z "$(gitv status --porcelain)" ] || die "写入必须一并提交，工作区应干净"
if grep -q "type: 'supports'" "${CARD_A}"; then die "匹配的关系记录应被物理移除"; fi
if grep -Fq 'removed_at' "${CARD_A}"; then die "不得留墓碑字段"; fi
grep -Fq '用户手写的正文段落，B2 要求逐字保留。' "${CARD_A}" || die "正文必须逐字保留（B2）"
ok "rel remove：退 0、恰一次 relate commit、物理移除且不留墓碑、正文逐字保留"

# ---------------------------------------------------------------- 7. 收口复核
step "收口复核：不生成 .index/；每次写入恰一次 relate commit；--help 口径一致"
# ── C2a·M6 现态重钉（合同 §16.3 runtime-reserved / §16.4 授权；保留 M5 历史事实 + 新增 M6 现态双侧锁）──
# M5 历史事实（只读复算，一格不放宽）：写命令绝不替用户建出派生索引 DB（属 S4）。
[ "$(ls -1 "${VAULT}/.index"/eg.db* 2>/dev/null | wc -l | tr -d ' ')" = "0" ] ||
  die "越界：.index/ 出现派生索引 DB（M2/S1 写命令不替用户建索引，属 S4）"
# M6 现态（新增正面锁）：.index/ 若存在，仅含 M6 事务基础设施——run.lock（普通文件）/ txn（目录）。
if [ -d "${VAULT}/.index" ]; then
  _rx="$(ls -1A "${VAULT}/.index" 2>/dev/null | grep -vxE 'run\.lock|txn' || true)"
  [ -z "${_rx}" ] || die "越界：.index/ 出现非 runtime-reserved 条目：${_rx}"
fi
RELATE_COMMITS="$(gitv log --oneline --pretty=%s | grep -c '^relate(' || true)"
[ "${RELATE_COMMITS}" = "4" ] ||
  die "本脚本共 4 次有效写入（3 次 add + 1 次 remove），relate commit 应恰 4 个，实际 ${RELATE_COMMITS}"
[ "$(gitv log --oneline --pretty=%s | grep -c '^process(' || true)" = "0" ] ||
  die "relate 不得退化成 process"
eg rel --help >"${WORK}/help.txt" 2>&1  # 退出码不吞：--help 合同退 0，非 0 由 set -e 直接失败
grep -Fq 'rel add' "${WORK}/help.txt" || die "--help 应列出 rel add"
grep -Fq 'rel remove' "${WORK}/help.txt" || die "--help 应列出 rel remove 的真实用法"
if grep -Fq '未实现' "${WORK}/help.txt"; then die "--help 不得再宣告任何子命令未实现"; fi
ok "无 .index/；relate commit 恰 4 个（3 次 add + 1 次 remove）、无 process 退化；--help 口径一致"

printf '\n=== rel_add.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

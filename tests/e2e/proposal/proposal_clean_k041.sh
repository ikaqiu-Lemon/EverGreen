#!/usr/bin/env bash
# K-041-01 / A-32：一次成功的 `eg delete --confirm` 之后**零脏提案**的端到端脚本
# （T-evergreen.s1_main_flow-158614-060）。
#
# 裁决 A-32「整键删除」把提案 execution.git_commit 退役：succeeded 不再回写 git_commit，
# execution 回写因此不必等 SHA，被提前到 commit **之前**——它与各文件的 deleted_at 落进
# **同一次** commit。于是删除成功后工作区不再残留「commit 之后的 execution 二次脏写」。
#
# 严格断言（全部针对真实用户 vault、真实 eg 二进制）：
#   ① eg delete --confirm --user-request → 退 0；
#   ② git status --porcelain 恰 **0 行**（零脏提案，K-041-01 的核心）；
#   ③ 本次 delete 的 git log 增量恰 **+1**（仍是单次 commit）；
#   ④ 提案文件里 git_commit **0 行**（A-32 整键删除，写侧不再产出该键）；
#   ⑤ HEAD commit 的 name-only stat **命中提案文件**（execution 回写进了历史，而非留在盘上）；
#   ⑥ 提案 execution.status = succeeded（回写确实发生）。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / go）；零交互（stdin 接 /dev/null）；
# 任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/proposal/proposal_clean_k041.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-k041.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/ai-infra/knowledge'
NDIR_REL='domains/ai-infra/notes'
CARD='k-20260901-attention'
NOTE='n-20260901-attention'
SRC='s-20260901-attention'
CARD_REL="${KDIR_REL}/${CARD}.md"
NOTE_REL="${NDIR_REL}/${NOTE}.md"

STEP=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }
gitv() { git -C "${VAULT}" "$@"; }
porcelain_count() { gitv status --porcelain | wc -l | tr -d ' '; }
logcount() { gitv log --oneline | wc -l | tr -d ' '; }
fmvalue() { awk -v k="$2:" '$1==k{print $2; exit}' "${VAULT}/$1" | tr -d "'\""; }
# execstatus 取 execution 块里的 status（区别于顶层 status）。
execstatus() { awk '/^execution:/{f=1;next} f&&/^  status:/{print $2;exit}' "${VAULT}/$1" | tr -d "'\""; }

# ---------------------------------------------------------------- 0. 构建与建库
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

step "建库：eg init + 原文 / 材料笔记 / 一张卡（卡的 sources[] 上恰一条 support 指向该笔记）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}" "${VAULT}/${NDIR_REL}" "${VAULT}/sources"
cat >"${VAULT}/sources/${SRC}.md" <<SRC_EOF
---
id: ${SRC}
url: https://example.com/attention
title: 注意力机制原文
saved_at: '2026-09-01T09:00:00+08:00'
---

# 注意力机制原文

正文占位（收录后不因加工改写）。
SRC_EOF
cat >"${VAULT}/${NOTE_REL}" <<NOTE_EOF
---
id: ${NOTE}
source: ${SRC}
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
---

# 注意力机制材料笔记

## 材料提炼

提炼占位。

## Agent 分析

分析占位。

## 用户补充

## 存疑与待验证

## 产出知识卡

- ${CARD}
NOTE_EOF
cat >"${VAULT}/${CARD_REL}" <<CARD_EOF
---
id: ${CARD}
title: 注意力机制
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: ${SRC}
    note: ${NOTE}
    rel: support
    reason: 该结论由这份材料支持
relations:
  - type: derives
    target: k-20260902-other
    reason: 由上位结论推出
---

# 注意力机制

## 知识内容

正文占位。

## 解释与依据

依据占位。

## 条件与边界

边界占位。

## 用户补充

## 理解自检

- 自检问题占位？
CARD_EOF
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m4 k041 语料"
[ "$(porcelain_count)" = "0" ] || die "前置条件：工作区必须干净"
ok "库就绪：干净工作区"

# ---------------------------------------------------------------- 1. 提案 new + approve
step "eg proposal new --type logical_delete（目标=材料笔记）→ 退 0，落盘 pending"
[ "$(eg_code proposal new --type logical_delete --target "${NOTE}" --reason '该材料已被更权威的版本取代' --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "proposal new 未退 0"; }
PID="$(grep -oE 'p-[0-9]{8}-[0-9]{3}' "${WORK}/out.txt" | head -1)"
[ -n "${PID}" ] || { cat "${WORK}/out.txt"; die "new 未返回提案 ID"; }
PREL="proposals/${PID}.md"
[ -f "${VAULT}/${PREL}" ] || die "提案未落盘：${PREL}"
[ "$(fmvalue "${PREL}" status)" = "pending" ] || die "新提案 status 必须是 pending"
# A-32：新写提案的 execution 块不得再产出 git_commit 键。
[ "$(grep -c 'git_commit' "${VAULT}/${PREL}")" = "0" ] ||
  die "新提案里出现了 git_commit 键：A-32 整键删除后新模板不得再产出该键"
ok "提案 ${PID} 已创建（pending，且无 git_commit 键）"

step "eg proposal approve ${PID} --confirm --user-request → 退 0"
[ "$(eg_code proposal approve "${PID}" --confirm --user-request)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "approve 未退 0"; }
[ "$(fmvalue "${PREL}" status)" = "approved" ] || die "approve 后 status 必须是 approved"
[ "$(porcelain_count)" = "0" ] || die "approve 之后工作区必须干净（前置提交完整）"
ok "提案 ${PID} 已批准（approved），工作区干净"

# ---------------------------------------------------------------- 2. delete + 零脏提案断言
step "① eg delete --confirm --user-request → 退 0"
LOGN_BEFORE="$(logcount)"
[ "$(eg_code delete --target "${NOTE}" --reason '该材料已被更权威的版本取代' --proposal "${PID}" --confirm --user-request --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/out.txt"; die "eg delete 未退 0"; }
# 目标笔记确实写上了删除标记（回写真的发生了，不是空跑）。
grep -q '^deleted_at:' "${VAULT}/${NOTE_REL}" || die "目标笔记缺 deleted_at：逻辑删除必须写这两个键"
ok "① eg delete 退 0，目标笔记已写 deleted_at"

step "② git status --porcelain 恰 0 行（K-041-01 零脏提案）"
PORC_AFTER="$(porcelain_count)"
[ "${PORC_AFTER}" = "0" ] ||
  { gitv status --porcelain; die "delete 之后未提交变更数 = ${PORC_AFTER}，A-32 后必须恰 0（不得有 commit 之后的二次脏写）"; }
ok "② porcelain = 0：删除后工作区零脏改动"

step "③ 本次 delete 的 git log 增量恰 +1"
DELTA=$(( $(logcount) - LOGN_BEFORE ))
[ "${DELTA}" = "1" ] || die "一次成功的 delete 必须恰 +1 条 commit，实际 +${DELTA}"
SUBJECT="$(gitv log -1 --pretty=%s)"
case "${SUBJECT}" in
  delete\(*) : ;;
  *) die "commit 主题 = ${SUBJECT}，期望以 delete( 开头（verb = delete）" ;;
esac
ok "③ git log 恰 +1，主题 = ${SUBJECT}"

step "④ 提案文件里 git_commit 0 行（A-32 整键删除，写侧不再产出该键）"
GC="$(grep -c 'git_commit' "${VAULT}/${PREL}" || true)"
[ "${GC}" = "0" ] ||
  { grep -n 'git_commit' "${VAULT}/${PREL}"; die "提案里仍出现 git_commit ${GC} 处：A-32 后 succeeded 不得回写该键"; }
ok "④ 提案 ${PREL} 内 git_commit 0 行"

step "⑤ HEAD commit 的 name-only stat 命中提案文件（execution 回写进了历史）"
gitv show --name-only --pretty=format: HEAD | grep -Fxq "${PREL}" ||
  { gitv show --name-only --pretty=format: HEAD; die "HEAD commit 未覆盖 ${PREL}：execution 回写必须与 deleted_at 同进一次历史"; }
ok "⑤ HEAD stat 命中 ${PREL}"

step "⑥ 提案 execution.status = succeeded（回写确实发生）"
[ "$(execstatus "${PREL}")" = "succeeded" ] ||
  die "execution.status = $(execstatus "${PREL}")，期望 succeeded"
ok "⑥ execution.status = succeeded"

printf '\n=== 全部 %d 步通过：A-32 / K-041-01 —— 成功 delete 后零脏提案、单次 commit、无 git_commit 键 ===\n' "${STEP}"
exit 0

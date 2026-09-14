#!/usr/bin/env bash
# `eg reconcile` 命令的端到端脚本（T-evergreen.s1_main_flow-158614-058 · 阶段 3）。
#
# 判据来源：`docs/specs/2026-11-12-m4-reconcile-contract.md` §12（`eg reconcile [--json]
# [--dry-run]` 全文：写入=是、commit **恰 0 或 1 次**、五档退出码 `0` 成功（含只有 warning 级
# finding）/ `1` 参数非法 / `2` 存在 error 级 finding（**必须先完成 R1 纳管 commit 再退 2**）/
# `3` 有写入被 B3 跳过 / `4` Git 提交失败、`--dry-run` 零写入零 commit 且 `ran=true`
# `commit=null`、退出码 `5` / `6` **不使用**、**不接受** `--target` / `--domain`）、
# **§0.1 第 3 条**（对账**不作为任何写命令的前置**）、**§11**（报告 `reconcile` 恰三键
# `ran` / `commit` / `findings`，`findings` 空时为 `[]` 永不为 `null`）；
# `2026-11-12-m4-visibility-and-execution-contract.md` **§3.4**（`eg reconcile` 属
# `--include-deprecated` 的**不作用面**，传入即退 `1`）；`milestones/M-004-m4.md`
# 完成判据 11 / 12 与风险 R-15 / R-19。
#
# 七步断言（task deliverable 逐字要求 + 施工卡 §3 阶段 3 的七步）：
#   ① 干净 vault 跑 `eg reconcile` → 退 **0** 且 `git log --oneline` 条数 **+0**、
#      `git status --porcelain` 行数不变（合同 §4「零改动即零 commit」在命令级的落点）；
#   ② 造**外部编辑**（直接改 Markdown，不经 `eg`）→ 退 **0** 且 commit 数**恰 +1**
#      （R1 纳管；提交主题 verb 恰 `reconcile`，用户那一行字节不变）；
#   ③ 造**重复 ID**（error 级 finding）→ 退 **2** 且纳管 commit 相对执行前**恰 +1**
#      （「先 commit 再退 2」的反证），`--json` 的 `.data.reconcile.commit` 非 null 且恰是 `HEAD`；
#   ④ `--dry-run` → `git status --porcelain` 行数**与内容**、`git log --oneline` 行数**均不变**，
#      `.data.reconcile.ran == true`、`.data.reconcile.commit == null`（且退出码不因预演而变软）；
#   ⑤ 收窄参数被拒：`--target k-x` / `--domain d` / `--include-deprecated` 三种**各退 1**，
#      且零 commit、工作区逐字不变（合同 §12 + 可见性合同 §3.4 的不作用面）；
#   ⑥ `eg report --last` **逐字节**复现本次 `reconcile` 三键；`--json` 信封**恒五键**且键序固定、
#      `data` 首键是 `reconcile`、`data.reconcile` **恰三键**、`findings` 恒非 `null`（空时 `[]`）；
#   ⑦ 越界反证：`eg reconcile` **不是任何写命令的前置** —— `grep` 反证（`internal/cli/*.go` 里
#      除 `reconcile*.go` 与 `root.go` 那**唯一一行** Wire 外零 `runReconcile`）+ 真实跑写命令
#      （`eg mark-reviewed` / `eg capture`）确认 commit 数各只 **+1**，且带报告体的那次输出里
#      `reconcile` 逐字是占位 `{"ran":false,"commit":null,"findings":[]}`。
#
# 为什么本脚本直接跑 `eg reconcile` 而不像 `m4_r1_takeover.sh` / `m4_report_reconcile.sh`
# 那样用 `go test` 驱动壳：那两个 task（T-…-050 / T-…-057）明确**不注册命令**，只能驱动能力层；
# 本 task 交付的**就是命令本体**，因此一切事实都从**真实 eg 二进制的退出码与 `--json` 输出**
# 以及 **git 自己**回读，不看实现自报、不引驱动壳。
#
# 约束：离线、零交互、可重复执行；**无 `jq` 依赖**（照 `m2_rel_query.sh` / `m3_proposal_cli.sh`
# 的先例，用紧凑 JSON 的 grep 正则 + awk 状态机取值，见 §JSON 工具）；只依赖
# bash / coreutils / awk / git / go；**一切写操作只发生在 `mktemp -d` 目录内**（无 `/tmp` 之外的
# 固定路径，退出时清理），仓库工作区一个字节都不碰；任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/reconcile-takeover/cmd_reconcile.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-cmd-reconcile.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/ai-infra/knowledge'
NDIR_REL='domains/ai-infra/notes'
CARD='k-20261201-attention'
NOTE='n-20261201-attention'
SRC='s-20261201-attention'
CARD_REL="${KDIR_REL}/${CARD}.md"
DUP_REL="${KDIR_REL}/${CARD}-dup.md"
EDIT_ONE="${CARD_REL}"             # 外部编辑 ①：已跟踪的知识卡（追加一条自检问题）
EDIT_TWO="${NDIR_REL}/${NOTE}.md"  # 外部编辑 ②：已跟踪的材料笔记
# 两处编辑都刻意**只追加分区内的正文行**，不新增 / 不重复任何标准分区标题：
# 本脚本要证的是「用户绕过 eg 直接改 Markdown 之后对账能纳管」，不是「破坏结构后 eg 怎么报错」；
# 收件区 `unprocessed.md` 是**结构化**文件（有自己的条目 schema），拿它当外部编辑语料会连带
# 让后面第 9 步的真实写命令退 3（收件区不可解析），那时 commit 计数那一格就成了空判。
EDIT_ONE_MARK='用户在编辑器里补写的一个自检问题（外部编辑，不经 eg）？'
EDIT_TWO_MARK='用户在编辑器里补的第二处外部编辑（dry-run 不得吞掉它）'
PLACEHOLDER='{"ran":false,"commit":null,"findings":[]}'
ENVELOPE_KEYS='ok,data,warnings,exit_code,status'
RECONCILE_KEYS='ran,commit,findings'

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
commits() { gitv log --oneline | wc -l | tr -d ' '; }
porcelain() { gitv status --porcelain; }
porcelain_count() { porcelain | wc -l | tr -d ' '; }
cnt0() { # cnt0 <说明> <grep 参数...>：期望 0 命中
  local name="$1"; shift
  local n; n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}

# ─────────────────────────────── §JSON 工具（零 jq 依赖）───────────────────────────────
# 本仓的 `--json` 是**紧凑单行** JSON，键序由 `DataOrder` / 结构体字段序钉死（合同要求键序
# 也可断言），因此完全可以只用 awk 的字符串状态机取值：既不引第三方依赖，也不会像
# `grep -o '{[^}]*}'` 那样在嵌套结构上失真。
#
# JSON_KEYS_AWK：打印紧凑 JSON 对象**指定深度**的键序（逗号分隔、按出现序、不排序）。
# 判定「是键」的规则就是 JSON 语法本身：处于对象容器内（`st[depth] == "{"`）、
# 且该字符串的前一个有效字符是 `{` 或 `,`。字符串内的 `\"` 转义逐字节跳过。
JSON_KEYS_AWK='
{
  s = $0; n = length(s); depth = 0; instr = 0; esc = 0; pc = ""; grab = 0; key = ""; out = ""
  for (i = 1; i <= n; i++) {
    c = substr(s, i, 1)
    if (instr) {
      if (esc) { esc = 0; if (grab) key = key c; continue }
      if (c == "\\") { esc = 1; if (grab) key = key c; continue }
      if (c == "\"") { instr = 0; pc = "\""
        if (grab) { out = (out == "" ? key : out "," key); grab = 0 }
        continue }
      if (grab) key = key c
      continue
    }
    if (c == " " || c == "\t" || c == "\n" || c == "\r") continue
    if (c == "\"") { instr = 1; key = ""
      grab = (depth == DEPTH && st[depth] == "{" && (pc == "{" || pc == ",")) ? 1 : 0
      continue }
    if (c == "{" || c == "[") { depth++; st[depth] = c; pc = c; continue }
    if (c == "}" || c == "]") { delete st[depth]; depth--; pc = c; continue }
    pc = c
  }
  print out
}
'
# JSON_SLICE_AWK：按大括号配平切出 `PFX` 之后紧跟的那一整个 JSON 对象（含首尾大括号）。
# `PFX` 由调用方写成**逐字前缀**（例如 `"data":{"reconcile":`），因此这个切片本身
# 也顺带断言了「`data` 的首键就是 `reconcile`」这件事 —— 前缀不在即退非零。
JSON_SLICE_AWK='
{
  p = index($0, PFX); if (p == 0) { exit 1 }
  s = substr($0, p + length(PFX)); n = length(s)
  depth = 0; instr = 0; esc = 0; out = ""
  for (i = 1; i <= n; i++) {
    c = substr(s, i, 1); out = out c
    if (instr) {
      if (esc) { esc = 0; continue }
      if (c == "\\") { esc = 1; continue }
      if (c == "\"") instr = 0
      continue
    }
    if (c == "\"") { instr = 1; continue }
    if (c == "{") depth++
    else if (c == "}") { depth--; if (depth == 0) { print out; exit 0 } }
  }
  exit 1
}
'
# env_keys <信封文件>：信封顶层键序（应恒 `ok,data,warnings,exit_code,status`）。
env_keys() { awk -v DEPTH=1 "${JSON_KEYS_AWK}" "$1"; }
# rc_obj <信封文件>：`data.reconcile` 那一整个对象的紧凑原文。
rc_obj() { awk -v PFX='"data":{"reconcile":' "${JSON_SLICE_AWK}" "$1"; }
# rc_keys <信封文件>：`data.reconcile` 的键序（应恒 `ran,commit,findings`）。
rc_keys() { rc_obj "$1" >"${WORK}/rc.json" && awk -v DEPTH=1 "${JSON_KEYS_AWK}" "${WORK}/rc.json"; }
# rc_commit <信封文件>：`data.reconcile.commit` 的原始值（`null` 或 40 位 sha，不去引号解析）。
rc_commit() { rc_obj "$1" | sed -E 's/^\{"ran":(true|false),"commit":(null|"[0-9a-f]{40}").*$/\2/'; }
# rpt_rc <信封文件>：报告体里 `reconcile` 那一整个对象（`eg report --last` 的复现落点）。
rpt_rc() { awk -v PFX='"reconcile":' "${JSON_SLICE_AWK}" "$1"; }
# assert_rc_shape <信封文件> <说明>：`data.reconcile` 恒三键 + 键序固定 + findings 非 null。
assert_rc_shape() {
  local f="$1" what="$2" obj keys
  obj="$(rc_obj "${f}")" || { cat "${f}"; die "${what}：data 首键必须是 reconcile（切片前缀不命中）"; }
  keys="$(rc_keys "${f}")"
  [ "${keys}" = "${RECONCILE_KEYS}" ] ||
    { printf '%s\n' "${obj}"; die "${what}：data.reconcile 键序必须逐字为 ${RECONCILE_KEYS}，实得 ${keys}"; }
  # 三键之外不得有第四键：键序等式已锁住前三个，这里再锁「findings 数组一闭合对象就闭合」。
  case "${obj}" in
    '{"ran":'*'"commit":'*'"findings":['*']}') ;;
    *) printf '%s\n' "${obj}"; die "${what}：data.reconcile 出现第四键或 findings 不是数组（空时必须是 []，永不为 null）" ;;
  esac
  printf '%s\n' "${obj}" | grep -Fq '"findings":null' &&
    { printf '%s\n' "${obj}"; die "${what}：findings 恒非 null（空时为 []）"; }
  [ "$(env_keys "${f}")" = "${ENVELOPE_KEYS}" ] ||
    { die "${what}：信封键序必须逐字为 ${ENVELOPE_KEYS}，实得 $(env_keys "${f}")"; }
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库与语料
step "eg init + 一份原文 / 一篇材料笔记 / 一张卡（都由 eg 口径入库，工作区干净）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}" "${VAULT}/${NDIR_REL}" "${VAULT}/sources"
cat >"${VAULT}/sources/${SRC}.md" <<SRC_EOF
---
id: ${SRC}
url: https://example.com/attention
title: 注意力机制原文
saved_at: '2026-12-01T09:00:00+08:00'
---

# 注意力机制原文

正文占位（收录后不因加工改写）。
SRC_EOF
cat >"${VAULT}/${EDIT_TWO}" <<NOTE_EOF
---
id: ${NOTE}
source: ${SRC}
created_at: '2026-12-01'
updated_at: '2026-12-01T10:00:00+08:00'
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
created_at: '2026-12-01'
updated_at: '2026-12-01T10:00:00+08:00'
tags:
  - llm
sources:
  - source: ${SRC}
    note: ${NOTE}
    rel: support
    reason: 该结论由这份材料支持
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
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m4 eg reconcile 命令语料"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "前置条件：工作区必须干净"; }
BASE_COMMITS="$(commits)"
ok "库就绪：1 原文 / 1 笔记 / 1 卡，工作区干净，commit 数 ${BASE_COMMITS}"

# --------------------------------------- 2. 干净 vault：退 0、commit +0、工作区行数不变
step "干净 vault 跑 eg reconcile：退 0（只有 warning 级 finding）+ commit 恰 +0 + porcelain 行数不变"
P_BEFORE="$(porcelain_count)"
CODE="$(eg_code reconcile --json)"
cp "${WORK}/out.txt" "${WORK}/clean.json"
[ "${CODE}" = "0" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/clean.json"; die "干净 vault 的对账必须退 0（合同 §12：只有 warning 级 finding 也退 0），实得 ${CODE}"; }
[ "$(commits)" = "${BASE_COMMITS}" ] ||
  die "零改动却产生了 commit：${BASE_COMMITS} → $(commits)（合同 §4「零改动即零 commit」）"
[ "$(porcelain_count)" = "${P_BEFORE}" ] ||
  { porcelain; die "干净 vault 的对账改变了 git status --porcelain 行数"; }
assert_rc_shape "${WORK}/clean.json" "干净 vault"
[ "$(rc_commit "${WORK}/clean.json")" = "null" ] ||
  { rc_obj "${WORK}/clean.json"; die "零 commit 时 reconcile.commit 必须为 null（不是空串）"; }
grep -Fq '"data":{"reconcile":{"ran":true,' "${WORK}/clean.json" ||
  { cat "${WORK}/clean.json"; die "ran 必须为 true（跑过就是跑过，零改动不等于没跑）"; }
# 反证「退 0 不是空判」：本次确实产出了 finding，且其中**没有** error 级（否则该退 2）。
FIND_N="$( { grep -o '"check":"' "${WORK}/clean.json" || true; } | wc -l | tr -d ' ')"
[ "${FIND_N}" -ge 1 ] ||
  { rc_obj "${WORK}/clean.json"; die "干净库这份语料应至少产出 1 条 warning 级 finding，否则「只有 warning 也退 0」是空判"; }
cnt0 '干净库出现 error 级 finding' -F '"severity":"error"' "${WORK}/clean.json"
ok "退 0 / commit ${BASE_COMMITS} → $(commits)（+0）/ porcelain ${P_BEFORE} 行不变 / findings 非空且零 error"

# --------------------------------------- 3. 外部编辑：退 0、commit 恰 +1
step "造外部编辑（直接改 Markdown，不经 eg）跑 eg reconcile：退 0 + commit 恰 +1（R1 纳管）"
printf '\n- %s\n' "${EDIT_ONE_MARK}" >>"${VAULT}/${EDIT_ONE}"
[ "$(porcelain_count)" = "1" ] ||
  { porcelain; die "外部编辑后 git status 应恰 1 条，实得 $(porcelain_count)"; }
BEFORE_LOG="$(commits)"
CODE="$(eg_code reconcile --json)"
cp "${WORK}/out.txt" "${WORK}/edited.json"
[ "${CODE}" = "0" ] ||
  { cat "${WORK}/err.txt"; die "外部编辑只产 warning 级 finding（W13），对账应退 0，实得 ${CODE}"; }
AFTER_LOG="$(commits)"
[ "${AFTER_LOG}" = "$((BEFORE_LOG + 1))" ] ||
  die "纳管 commit 应恰 +1：${BEFORE_LOG} → ${AFTER_LOG}"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "纳管后 git status --porcelain 应为空"; }
SUBJECT="$(gitv log -1 --pretty=%s)"
case "${SUBJECT}" in
  reconcile*) ;;
  *) die "提交主题 = ${SUBJECT}，verb 应恰 reconcile（裁决 A-30 / KnownVerbs 恒 8）" ;;
esac
SHA_EDIT="$(rc_commit "${WORK}/edited.json")"
[ "${SHA_EDIT}" = "\"$(gitv rev-parse HEAD)\"" ] ||
  { rc_obj "${WORK}/edited.json"; die "reconcile.commit 必须逐字是本次 HEAD，实得 ${SHA_EDIT}"; }
grep -Fq '"check":"git_uncommitted"' "${WORK}/edited.json" ||
  { rc_obj "${WORK}/edited.json"; die "外部编辑必须产出 git_uncommitted（W13）finding"; }
# R1 只把既有事实记进历史：用户那一行字节不变（纳管不改写用户的文件）。
grep -Fq "${EDIT_ONE_MARK}" "${VAULT}/${EDIT_ONE}" ||
  die "纳管改写了用户手写的内容（R1 只做 Git 纳管，一个字节都不改）"
gitv show --stat --oneline HEAD | grep -Fq "${EDIT_ONE}" || die "本次 commit 未含 ${EDIT_ONE}"
ok "退 0 / commit ${BEFORE_LOG} → ${AFTER_LOG}（恰 +1）/ verb=reconcile / 用户字节不变"

# --------------------------------------- 4. 重复 ID：退 2 且纳管 commit 仍恰 +1
step "造重复 ID（error 级 finding）跑 eg reconcile：退 2 且纳管 commit 恰 +1（先 commit 再退 2）"
cp "${VAULT}/${CARD_REL}" "${VAULT}/${DUP_REL}"
[ "$(porcelain_count)" = "1" ] ||
  { porcelain; die "手放重复 ID 文件后 git status 应恰 1 条"; }
BEFORE_LOG="$(commits)"
CODE="$(eg_code reconcile --json)"
cp "${WORK}/out.txt" "${WORK}/dup.json"
[ "${CODE}" = "2" ] ||
  { cat "${WORK}/err.txt"; rc_obj "${WORK}/dup.json"; die "存在 error 级 finding 必须退 2，实得 ${CODE}"; }
AFTER_LOG="$(commits)"
[ "${AFTER_LOG}" = "$((BEFORE_LOG + 1))" ] ||
  die "「先完成 R1 纳管 commit 再退 2」被违反：commit ${BEFORE_LOG} → ${AFTER_LOG}，应恰 +1"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "退 2 也必须先把工作区纳管干净"; }
DUP_SHA="$(rc_commit "${WORK}/dup.json")"
[ "${DUP_SHA}" != "null" ] ||
  { rc_obj "${WORK}/dup.json"; die "退 2 时 .data.reconcile.commit 不得为 null（纳管确实发生了）"; }
[ "${DUP_SHA}" = "\"$(gitv rev-parse HEAD)\"" ] ||
  { rc_obj "${WORK}/dup.json"; die "退 2 时登记的 sha 必须逐字是 HEAD，实得 ${DUP_SHA}"; }
assert_rc_shape "${WORK}/dup.json" "退 2 路径"
grep -Fq '"check":"duplicate_id"' "${WORK}/dup.json" ||
  { rc_obj "${WORK}/dup.json"; die "重复 ID 必须产出 duplicate_id finding"; }
grep -Fq '"severity":"error"' "${WORK}/dup.json" || die "duplicate_id 必须是 error 级"
grep -Fq '"exit_code":2' "${WORK}/dup.json" || die "信封 exit_code 必须与进程退出码同真（2）"
ok "退 2 / commit ${BEFORE_LOG} → ${AFTER_LOG}（恰 +1）/ commit 键 = HEAD ${DUP_SHA:1:8}…"

# --------------------------------------- 5. --dry-run：零写入零 commit
step "--dry-run：porcelain 行数与内容 + git log 行数均不变；ran=true、commit=null"
printf '\n%s\n' "${EDIT_TWO_MARK}" >>"${VAULT}/${EDIT_TWO}"
DRY_LOG_BEFORE="$(commits)"
DRY_P_BEFORE="$(porcelain)"
DRY_PN_BEFORE="$(porcelain_count)"
[ "${DRY_PN_BEFORE}" = "1" ] || { porcelain; die "预演前应恰 1 处未提交改动（让 dry-run 有事可做）"; }
# 刻意选「既有 error 级 finding（重复 ID 仍在）又有待纳管改动」的语料：
# 预演必须**既不写盘也不软化退出码** —— 零写入这一格由三项快照锁死，
# 退出码这一格锁死「dry-run 不是把 2 变成 0 的开关」（合同 §12 只免除写入，不免除判定）。
CODE="$(eg_code reconcile --dry-run --json)"
cp "${WORK}/out.txt" "${WORK}/dry.json"
[ "$(commits)" = "${DRY_LOG_BEFORE}" ] ||
  die "dry-run 产生了 commit：${DRY_LOG_BEFORE} → $(commits)"
[ "$(porcelain_count)" = "${DRY_PN_BEFORE}" ] ||
  { porcelain; die "dry-run 改变了 git status --porcelain 行数"; }
[ "${DRY_P_BEFORE}" = "$(porcelain)" ] ||
  { porcelain; die "dry-run 改变了 git status --porcelain 的内容"; }
grep -Fq "${EDIT_TWO_MARK}" "${VAULT}/${EDIT_TWO}" || die "dry-run 改写了用户那处编辑"
assert_rc_shape "${WORK}/dry.json" "dry-run"
grep -Fq '"data":{"reconcile":{"ran":true,"commit":null,' "${WORK}/dry.json" ||
  { rc_obj "${WORK}/dry.json"; die "dry-run 必须 ran=true 且 commit=null（合同 §12）"; }
[ "$(rc_commit "${WORK}/dry.json")" = "null" ] || die "dry-run 的 commit 键必须为 null"
[ "${CODE}" = "2" ] ||
  { cat "${WORK}/err.txt"; die "dry-run 不软化判定：error 级 finding 仍在，应仍退 2，实得 ${CODE}"; }
grep -Fq '"check":"duplicate_id"' "${WORK}/dry.json" ||
  { rc_obj "${WORK}/dry.json"; die "dry-run 必须照报 finding（只免除写入，不免除检查）"; }
ok "dry-run：commit ${DRY_LOG_BEFORE} 不变 / porcelain ${DRY_PN_BEFORE} 行逐字不变 / ran=true / commit=null / 仍退 2"

# --------------------------------------- 6. 收窄参数被拒：各退 1、零 commit 零工作区变化
step "收窄参数被拒：--target k-x / --domain d / --include-deprecated 各退 1 且零 commit 零变化"
REJ_LOG="$(commits)"
REJ_P="$(porcelain)"
for args in "--target k-x" "--domain d" "--include-deprecated"; do
  # shellcheck disable=SC2086
  CODE="$(eg_code reconcile ${args})"
  [ "${CODE}" = "1" ] ||
    { cat "${WORK}/err.txt"; die "eg reconcile ${args} 必须退 1（全库对账不收窄；可见性合同 §3.4 的不作用面），实得 ${CODE}"; }
  # 错误信息必须点名那个参数（不是笼统的「参数非法」）。
  FLAG="${args%% *}"; FLAG="${FLAG#--}"
  grep -Fq "${FLAG}" "${WORK}/err.txt" ||
    { cat "${WORK}/err.txt"; die "拒绝 ${args} 时错误信息未点名 ${FLAG}"; }
  [ "$(commits)" = "${REJ_LOG}" ] || die "eg reconcile ${args} 产生了 commit（参数非法必须零写入）"
  [ "${REJ_P}" = "$(porcelain)" ] || { porcelain; die "eg reconcile ${args} 改动了工作区"; }
done
# 三个收窄参数名在命令源码里一次都不出现（未声明 → 参数解析当场判非法，task Acceptance 的 grep 恒 0）。
cnt0 '命令源码出现范围收窄参数字面量' -nE -- '--target|--domain|--include-deprecated' \
  "${REPO_ROOT}/internal/cli/reconcile.go"
ok "三种收窄参数各退 1、逐个点名、commit 恒 ${REJ_LOG}、工作区逐字不变；源码零收窄参数字面量"

# --------------------------------------- 7. report --last 复现三键 + 信封形态
step "eg report --last 逐字节复现本次 reconcile 三键；信封恒五键 / 三键 / findings 空时为 []"
BEFORE_LOG="$(commits)"
CODE="$(eg_code reconcile --json)"
cp "${WORK}/out.txt" "${WORK}/real.json"
[ "${CODE}" = "2" ] || { cat "${WORK}/err.txt"; die "重复 ID 仍在，本次真实对账应退 2，实得 ${CODE}"; }
[ "$(commits)" = "$((BEFORE_LOG + 1))" ] ||
  die "dry-run 之后那处编辑必须仍在并被本次纳管：commit ${BEFORE_LOG} → $(commits)，应恰 +1"
RC_REAL="$(rc_obj "${WORK}/real.json")"
[ "$(eg_code report --last --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg report --last 未退 0"; }
cp "${WORK}/out.txt" "${WORK}/last.json"
[ "$(rc_obj "${WORK}/last.json")" = "${RC_REAL}" ] ||
  { printf 'reconcile:  %s\nreport --last: %s\n' "${RC_REAL}" "$(rc_obj "${WORK}/last.json")"
    die "eg report --last 未逐字节复现本次 reconcile 三键"; }
[ "$(rpt_rc "${WORK}/last.json")" = "${RC_REAL}" ] ||
  die "报告体里的 reconcile 与 data.reconcile 必须同源（逐字节相等）"
assert_rc_shape "${WORK}/last.json" "report --last"
# `findings` 恒非 null：空时逐字是 `[]`（第 2 步那次干净对账就是空集合的落点）。
grep -Fq '"data":{"reconcile":{"ran":true,"commit":null,"findings":[' "${WORK}/clean.json" ||
  die "findings 必须是数组（空时 []，永不 null）"
for f in clean.json edited.json dup.json dry.json real.json last.json; do
  [ "$(env_keys "${WORK}/${f}")" = "${ENVELOPE_KEYS}" ] ||
    die "${f} 的信封键序不是 ${ENVELOPE_KEYS}（实得 $(env_keys "${WORK}/${f}")）"
  [ "$(rc_keys "${WORK}/${f}")" = "${RECONCILE_KEYS}" ] ||
    die "${f} 的 data.reconcile 键序不是 ${RECONCILE_KEYS}"
  cnt0 "${f} 的 findings 为 null" -F '"findings":null' "${WORK}/${f}"
done
ok "report --last 逐字节复现三键；六份信封各恒五键 ${ENVELOPE_KEYS} / 三键 ${RECONCILE_KEYS}"

# --------------------------------------- 8. 越界反证：不作为写命令前置
step "越界反证：eg reconcile 不是任何写命令的前置（grep 反证 + 真实写命令各只 +1 条 commit）"
# ① 源码级：`runReconcile` 的引用只出现在 `reconcile*.go` 与 `root.go` 那**唯一一行** Wire 上。
#    合同 §0.1 第 3 条要的就是这件事：任何写命令的执行路径里都不得插入对账。
WIRE_LINE='_ = r.Wire("reconcile", r.runReconcile)'
[ "$( { grep -rnF "${WIRE_LINE}" "${REPO_ROOT}/internal/cli/" --include='*.go' || true; } |
  wc -l | tr -d ' ')" = "1" ] || die "对账的 Wire 行必须恰 1 处（唯一挂载点）"
[ "$( { grep -rlF "${WIRE_LINE}" "${REPO_ROOT}/internal/cli/" --include='*.go' || true; } |
  sed "s#${REPO_ROOT}/##" | sort | tr '\n' ' ')" = "internal/cli/root.go " ] ||
  die "对账的 Wire 行必须只落在 internal/cli/root.go"
# 2026-09-07（T-…-063 收口，K-063-01）：本反证的语义是「**产品源码级**的 runReconcile
# 引用只出现在 Wire 行」，故须排除 *_test.go —— 测试助手 `runReconcileCLI`（check_test.go）
# 是测试基建，不是产品 wiring；旧写法漏排 _test.go 且以子串命中 `runReconcileCLI`，误报。
# 真实产品不变式经上方 Wire 行「恰 1 且只在 root.go」逐字钉死，未放宽。
cnt0 'runReconcile 在 reconcile*.go 与 root.go 唯一 Wire 行之外被引用（仅产品源码）' \
  -rn 'runReconcile' "${REPO_ROOT}/internal/cli/" --include='*.go' \
  --exclude='*_test.go' \
  --exclude='reconcile.go' --exclude='reconcile_test.go' \
  --exclude='reconcile_commit.go' --exclude='reconcile_repair_reviewed.go' \
  --exclude='reconcile_repair_stale.go' --exclude='reconcile_recap_sample.go' \
  --exclude='reconcile_render.go' --exclude='root.go'
[ "$( { grep -n 'runReconcile' "${REPO_ROOT}/internal/cli/root.go" || true; } |
  grep -vF "${WIRE_LINE}" | wc -l | tr -d ' ')" = "0" ] ||
  { grep -n 'runReconcile' "${REPO_ROOT}/internal/cli/root.go"
    die "root.go 里除那一行 Wire 外不得再引用 runReconcile"; }
# ② 先把重复 ID 撤掉并纳管（用户直接删文件，也是一种外部编辑）：error 级 finding 消失后
#    对账退回 0 —— 这既反证「退出码跟着事实走、不是写死的常量」，也让下面两条真实写命令
#    跑在一个合法库上（重复 ID 会让写命令自己退 2，那时 commit 计数这一格就成了空判）。
rm "${VAULT}/${DUP_REL}"
D_BEFORE="$(commits)"
[ "$(eg_code reconcile --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/out.txt"; die "撤掉重复 ID 后对账应退回 0"; }
[ "$(commits)" = "$((D_BEFORE + 1))" ] ||
  die "删除文件同样是外部编辑：纳管 commit 应恰 +1（${D_BEFORE} → $(commits)）"
cnt0 '撤掉重复 ID 后仍报 duplicate_id' -F '"check":"duplicate_id"' "${WORK}/out.txt"
# ③ 运行时：真实写命令各跑一次 —— commit 数只 +1（没有被对账多加一条），
#    且带报告体的那次输出里 `reconcile` 逐字是占位形态（写命令不触发对账）。
W_BEFORE="$(commits)"
[ "$(eg_code mark-reviewed --target "${CARD}" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/out.txt"; die "eg mark-reviewed 未退 0"; }
cp "${WORK}/out.txt" "${WORK}/marked.json"
[ "$(commits)" = "$((W_BEFORE + 1))" ] ||
  die "eg mark-reviewed 的 commit 应恰 +1（对账不作为前置，不得多出一条纳管 commit）：${W_BEFORE} → $(commits)"
[ "$(rpt_rc "${WORK}/marked.json")" = "${PLACEHOLDER}" ] ||
  { rpt_rc "${WORK}/marked.json"; die "写命令输出里的 reconcile 必须逐字是占位 ${PLACEHOLDER}"; }
cnt0 'mark-reviewed 输出里出现对账 ran=true' -F '"reconcile":{"ran":true' "${WORK}/marked.json"
W_BEFORE="$(commits)"
printf '这是一篇用于验证「写命令不依赖对账前置」的正文。第二句补充说明，确保正文不过短。第三句继续补充说明。\n' \
  >"${WORK}/body.txt"
[ "$(eg_code capture --url https://example.com/second --title 第二篇原文 \
  --body-file "${WORK}/body.txt" --reason 验证写命令不依赖对账前置 --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/out.txt"; die "eg capture 未退 0"; }
[ "$(commits)" = "$((W_BEFORE + 1))" ] ||
  die "eg capture 的 commit 应恰 +1：${W_BEFORE} → $(commits)"
# `eg capture` 的信封 data 是收录回执（无报告体），故这一格只锁 commit 计数 ——
# 占位形态那一格由上面 `mark-reviewed`（真实写命令 + 带报告体）承担，不拿不存在的键凑断言。
cnt0 'capture 输出里出现对账字段' -F '"reconcile"' "${WORK}/out.txt"
ok "Wire 行恰 1 且只在 root.go；写命令路径零 runReconcile 引用；mark-reviewed / capture 各恰 +1 条 commit 且 reconcile 为占位"

printf '\n=== cmd_reconcile.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

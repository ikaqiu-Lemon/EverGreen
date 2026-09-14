#!/usr/bin/env bash
# 逻辑删除十一步时序 + `eg undelete` 的端到端脚本（T-evergreen.s1_main_flow-158614-041 收尾）。
#
# 判据来源：提案与状态合同 §5.3（时序十一步）/ §5.5（历史保留）/ §9（状态不被自动改变），
# 授权合同 §2 矩阵「逻辑删除」行与 #6「清空删除标记」行、§10.3 的 V9 / V10、U-01 / U-02。
#
# 断言顺序（与 §5.3 十一步一一对应，编号即步号）：
#   ① eg proposal new（**Agent 路径**，不带 --user-request，--type logical_delete）→ 退 0
#   ② commit 主题以 `proposal(` 开头
#   ③ 无 --confirm 的 eg delete → 退 **6**，且 porcelain 计数 / git log --oneline -1 /
#      提案字节三样逐字不变
#   ④ eg proposal approve <pid> --confirm --user-request → 退 0
#   ⑤ 执行前重算影响面（approve 内已做，断言其发生）
#   ⑥ eg delete --target … --reason … --proposal <pid> --confirm --user-request → 退 0，
#      **逐文件**写 deleted_at + deleted_reason
#   ⑦ commit 主题以 `delete(` 开头，git log --oneline | wc -l 恰 +1
#   ⑧ 报告含逐字串「本次删除没有自动改变任何知识卡的状态」
#   ⑨ support_check[] 建议清单非空，且 jq -e '.data.report.support_check[]?.recommendation'
#      能读到并命中「建议标记 `deprecated`」
#   ⑩ status 逐字未变（删除不自动改状态）
#   ⑪ 关系记录一条不删：relations[] / sources[] 条目数逐条不变
# 之后：⑫ 工作区洁净度复核（本次 task 的隐患复核项）、⑬ undelete、⑭ 重复 undelete 幂等。
#
# 关于第 ⑫ 步的**实测结论**（2026-09-07 依 A-23 派生裁决 A-32 / K-041-01 重钉旧事实）：
#   【已作废的旧事实】早前本步曾钉住：delete 之后 `git status --porcelain | wc -l` = **1**，
#   那一处是提案 `execution` 块的**二次回写**，且 `execution = succeeded` 必带真实 `git_commit`
#   （§4.3）；因自指哈希无不动点，该回写被判为「不可能进同一次 commit」，故留一处脏变更。
#   【新合同】A-32 整键退役 `execution.git_commit`（放宽合同 §4.3：不再持久化该 SHA 字段），
#   K-041-01 据此要求 delete 的 `execution = succeeded` 回写**与目标写入同进一次 commit**：
#   既然不再记 SHA，就没有自指哈希的不动点问题，回写可在 commit 之前完成。因此新事实为：
#   一次成功的 delete 之后 `git log --oneline | wc -l` 恰 +1；`git status --porcelain | wc -l` = **0**；
#   提案文件里 `execution.git_commit` = **0 行**；`execution.status` = **succeeded**；且 HEAD 那次
#   commit **包含**该提案文件。一旦哪天有人让知识/提案写入漏出 commit、留下脏变更、
#   多出第二条 commit、或 succeeded 又冒出 `git_commit`，这一步立刻红。
#
# 关于第 ③ 步的**真实判定顺序**（如实记录，不含糊）：
#   eg delete 的裁决次序被 ExitCodeForConfirm 锁死为「参数 1 → 校验 2 → 仅缺确认 6」，
#   而「必须引用 status=approved 的提案」属**校验**（W7 后半 ≡ V10）。因此提案仍是 pending 时
#   缺 --confirm 只能退 2（校验先失败），退 6 的前提是「校验全过、只差确认」。
#   本脚本据此把第 ③ 步拆成前后两半、两半都断言三样逐字不变：
#     ③-前半 提案 pending + 无 --confirm → 退 2；
#     ③-后半 提案 approved + 无 --confirm → 退 **6**（第 ④ 步之后立刻做，位置紧随不改语义）。
#   把退 6 硬塞到 approve 之前只能靠放宽提案守卫来实现，那是拆判据，不是通过判据。
#
# 越界负向断言（本层一律不做）：无 `[已删除]` / `[未过目]` 标记与检索过滤（T-…-043）、
# 无材料充分性自动判定（S3）、无物理删除（关系记录一条不删）、不启用退出码 5。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / go / jq）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/lifecycle/logical_delete.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-logical-delete.XXXXXX")"
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
rawhash() { sha256sum "${VAULT}/$1" | cut -d' ' -f1; }
# fmvalue 取 frontmatter 标量值并剥掉引号：断言比的是语义值，不是引号风格。
fmvalue() { awk -v k="$2:" '$1==k{print $2; exit}' "${VAULT}/$1" | tr -d "'\""; }
# statusline 取 `status:` 那**一整行**（含引号与空格）：逐字比对用。
statusline() { grep -m1 '^status:' "${VAULT}/$1" || true; }
# seqcount 数某个 frontmatter 序列键下的顶层条目数（`  - ` 开头的行）。
seqcount() {
  awk -v k="$2:" '
    $0==k {f=1; next}
    f && /^[A-Za-z_]+:/ {exit}
    f && /^  - / {n++}
    END {print n+0}' "${VAULT}/$1"
}

# ---------------------------------------------------------------- 0. 构建与建库
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
command -v jq >/dev/null || die "本脚本第 ⑨ 步用 jq 读 .data.report.support_check[]"
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
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m3 逻辑删除语料"
[ "$(porcelain_count)" = "0" ] || die "前置条件：工作区必须干净"
CARD_STATUS_BEFORE="$(statusline "${CARD_REL}")"
[ -n "${CARD_STATUS_BEFORE}" ] || die "前置条件：卡必须有 status 行，否则第 ⑩ 步失去判据"
CARD_SOURCES_BEFORE="$(seqcount "${CARD_REL}" sources)"
CARD_RELATIONS_BEFORE="$(seqcount "${CARD_REL}" relations)"
CARD_HASH_BEFORE="$(rawhash "${CARD_REL}")"
ok "库就绪：卡 status 行 =「${CARD_STATUS_BEFORE}」，sources ${CARD_SOURCES_BEFORE} 条 / relations ${CARD_RELATIONS_BEFORE} 条"

# ---------------------------------------------------------------- 1. ① proposal new（Agent 路径）
step "① eg proposal new --type logical_delete（Agent 路径，**不带** --user-request）→ 退 0"
LOG_BEFORE="$(logcount)"
# 删除目标取**材料笔记**：卡因此失去唯一一条有效 support，正是第 ⑨ 步「建议标记 deprecated」的场景。
[ "$(eg_code proposal new --type logical_delete --target "${NOTE}" --reason '该材料已被更权威的版本取代' --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "proposal new 未退 0（提案创建对 Agent 开放：可提不可执）"; }
PID="$(grep -oE 'p-[0-9]{8}-[0-9]{3}' "${WORK}/out.txt" | head -1)"
[ -n "${PID}" ] || { cat "${WORK}/out.txt"; die "new 未返回提案 ID"; }
PREL="proposals/${PID}.md"
[ -f "${VAULT}/${PREL}" ] || die "提案未落盘：${PREL}"
[ "$(fmvalue "${PREL}" status)" = "pending" ] || die "新提案 status 必须是 pending"
ok "① ${PID} 已创建（Agent 路径放行，status: pending）"

# ---------------------------------------------------------------- 2. ② commit 主题 proposal(
step "② commit 主题以 proposal( 开头，且 new 恰 +1 次 commit"
[ "$(( $(logcount) - LOG_BEFORE ))" = "1" ] || die "proposal new 必须产生恰 1 次 commit"
SUBJECT="$(gitv log -1 --pretty=%s)"
case "${SUBJECT}" in
  proposal\(*) : ;;
  *) die "commit 主题 = ${SUBJECT}，期望以 proposal( 开头" ;;
esac
[ "$(porcelain_count)" = "0" ] || die "new 之后工作区必须干净"
ok "② commit 主题 = ${SUBJECT}"

# ---------------------------------------------------------------- 3. ③ 无 --confirm 的 delete
step "③-前半 提案仍 pending + 无 --confirm 的 eg delete → 退 2（校验先失败），三样逐字不变"
PORC_BEFORE="$(porcelain_count)"
LOG1_BEFORE="$(gitv log --oneline -1)"
PHASH_BEFORE="$(rawhash "${PREL}")"
LOGN_BEFORE="$(logcount)"
CODE="$(eg_code delete --target "${NOTE}" --reason '该材料已被更权威的版本取代' --proposal "${PID}" --user-request)"
[ "${CODE}" = "2" ] ||
  { cat "${WORK}/err.txt"; die "pending 提案 + 无 --confirm 的 delete 退出码 = ${CODE}，期望 2（W7 后半 ≡ V10）"; }
[ "$(porcelain_count)" = "${PORC_BEFORE}" ] || die "退 2 后 porcelain 计数变了"
[ "$(gitv log --oneline -1)" = "${LOG1_BEFORE}" ] || die "退 2 后 git log -1 变了"
[ "$(rawhash "${PREL}")" = "${PHASH_BEFORE}" ] || die "退 2 后提案字节变了：必须零写入"
[ "$(rawhash "${NOTE_REL}")" != "" ] || die "目标笔记消失了（本仓无物理删除）"
grep -q 'deleted_at' "${VAULT}/${NOTE_REL}" && die "退 2 时不得写 deleted_at"
ok "③-前半 退 2 且 porcelain / log -1 / 提案字节三样逐字不变"

# ---------------------------------------------------------------- 4. ④ approve
step "④ eg proposal approve ${PID} --confirm --user-request → 退 0"
# 刻意不带 --json：执行前重算那条**事实陈述**在人类可读摘要里（--json 信封只带结构化 report），
# 第 ⑤ 步要断言的正是「重算确实发生过」这件事。
[ "$(eg_code proposal approve "${PID}" --confirm --user-request)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "approve 未退 0"; }
cp "${WORK}/out.txt" "${WORK}/approve.txt"
[ "$(fmvalue "${PREL}" status)" = "approved" ] || die "approve 后 status 必须是 approved"
ok "④ ${PID} 已批准（status: approved）"

# ---------------------------------------------------------------- 5. ③-后半 退 6
step "③-后半 校验全过（approved）+ 无 --confirm 的 eg delete → 退 **6**，三样逐字不变"
PORC_BEFORE="$(porcelain_count)"
LOG1_BEFORE="$(gitv log --oneline -1)"
PHASH_BEFORE="$(rawhash "${PREL}")"
LOGN_BEFORE="$(logcount)"
NHASH_BEFORE="$(rawhash "${NOTE_REL}")"
CODE="$(eg_code delete --target "${NOTE}" --reason '该材料已被更权威的版本取代' --proposal "${PID}" --user-request)"
[ "${CODE}" = "6" ] ||
  { cat "${WORK}/err.txt"; die "approved 提案 + 无 --confirm 的 delete 退出码 = ${CODE}，必须是 6"; }
grep -q -- '--confirm' "${WORK}/err.txt" "${WORK}/out.txt" || die "退 6 必须提示补 --confirm"
[ "$(porcelain_count)" = "${PORC_BEFORE}" ] || die "退 6 后 porcelain 计数变了：${PORC_BEFORE} → $(porcelain_count)"
[ "$(gitv log --oneline -1)" = "${LOG1_BEFORE}" ] || die "退 6 后 git log -1 变了"
[ "$(rawhash "${PREL}")" = "${PHASH_BEFORE}" ] || die "退 6 后提案字节变了：权威 Markdown 必须完全不变"
[ "$(rawhash "${NOTE_REL}")" = "${NHASH_BEFORE}" ] || die "退 6 后目标字节变了"
[ "$(logcount)" = "${LOGN_BEFORE}" ] || die "退 6 不得产生 commit"
ok "③-后半 退 6 且 porcelain 计数 / log -1 / 提案与目标字节四样逐字不变"

# ---------------------------------------------------------------- 6. ⑤ 执行前重算影响面
step "⑤ 执行前重算影响面（approve 内已做，断言其发生）"
grep -Fq '执行前重算影响面' "${WORK}/approve.txt" ||
  { cat "${WORK}/approve.txt"; die "approve 输出未提及执行前重算：§10.2 第 8 步不得省略"; }
grep -Fq 'impact 逐项一致' "${WORK}/approve.txt" ||
  die "approve 未如实说明重算结果与提案 impact 的比对结论"
ok "⑤ approve 报告如实记载执行前重算已发生"

# ---------------------------------------------------------------- 7. ⑥ delete 执行
step "⑥ eg delete --confirm --user-request → 退 0，逐文件写 deleted_at + deleted_reason"
LOGN_BEFORE="$(logcount)"
[ "$(eg_code delete --target "${NOTE}" --reason '该材料已被更权威的版本取代' --proposal "${PID}" --confirm --user-request --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/out.txt"; die "eg delete 未退 0"; }
cp "${WORK}/out.txt" "${WORK}/delete.json"
for key in 'deleted_at:' 'deleted_reason:'; do
  grep -q "^${key}" "${VAULT}/${NOTE_REL}" || die "目标笔记缺 ${key}：逻辑删除必须写这两个键"
done
[ "$(grep -c '^deleted_at:' "${VAULT}/${NOTE_REL}")" = "1" ] || die "deleted_at 必须恰 1 行（单键覆盖）"
grep -q '该材料已被更权威的版本取代' "${VAULT}/${NOTE_REL}" || die "deleted_reason 必须落盘"
[ -f "${VAULT}/${NOTE_REL}" ] || die "目标文件被物理删除：U-01 明令不得物理删除"
[ -f "${VAULT}/sources/${SRC}.md" ] || die "原文被物理删除：U-01"
ok "⑥ 逐文件写入完成：${NOTE_REL} 上 deleted_at + deleted_reason 各恰一行，无物理删除"

# ---------------------------------------------------------------- 8. ⑦ commit 主题与计数
step "⑦ commit 主题以 delete( 开头，git log --oneline | wc -l 恰 +1"
DELTA=$(( $(logcount) - LOGN_BEFORE ))
[ "${DELTA}" = "1" ] || die "一次成功的 delete 必须恰 +1 条 commit，实际 +${DELTA}"
SUBJECT="$(gitv log -1 --pretty=%s)"
case "${SUBJECT}" in
  delete\(*) : ;;
  *) die "commit 主题 = ${SUBJECT}，期望以 delete( 开头（verb = delete）" ;;
esac
ok "⑦ commit 主题 = ${SUBJECT}，git log 恰 +1"

# ---------------------------------------------------------------- 9. ⑧ 报告逐字串
step "⑧ 报告含逐字串「本次删除没有自动改变任何知识卡的状态」"
grep -Fq '本次删除没有自动改变任何知识卡的状态' "${WORK}/delete.json" ||
  { cat "${WORK}/delete.json"; die "报告缺那句逐字说明（§5.5 / §9）"; }
ok "⑧ 逐字串在报告里（一字不差）"

# ---------------------------------------------------------------- 10. ⑨ support_check[]
step "⑨ support_check[] 非空，jq -e '.data.report.support_check[]?.recommendation' 命中「建议标记 deprecated」"
jq -e '.data.report.support_check | length > 0' "${WORK}/delete.json" >/dev/null ||
  { cat "${WORK}/delete.json"; die "support_check[] 为空：失去有效支持的卡必须进清单"; }
RECS="$(jq -re '.data.report.support_check[]?.recommendation' "${WORK}/delete.json")"
[ -n "${RECS}" ] || die "support_check[].recommendation 读不到（信封真实路径是 .data.report.*）"
printf '%s\n' "${RECS}" | grep -Fq '建议标记 `deprecated`' ||
  die "建议清单未命中「建议标记 \`deprecated\`」，实际：${RECS}"
CARD_IN_CHECK="$(jq -re '.data.report.support_check[]?.id' "${WORK}/delete.json")"
printf '%s\n' "${CARD_IN_CHECK}" | grep -Fq "${CARD}" || die "受影响的卡 ${CARD} 未进 support_check[]"
ok "⑨ 建议清单非空且命中「建议标记 \`deprecated\`」（受影响卡 ${CARD}）"

# ---------------------------------------------------------------- 11. ⑩ status 逐字未变
step "⑩ status 逐字未变：删除不自动改状态"
[ "$(statusline "${CARD_REL}")" = "${CARD_STATUS_BEFORE}" ] ||
  die "受影响卡的 status 行变了：「${CARD_STATUS_BEFORE}」→「$(statusline "${CARD_REL}")」"
[ "$(fmvalue "${CARD_REL}" status)" = "active" ] ||
  die "受影响卡 status = $(fmvalue "${CARD_REL}" status)，用户不处理时必须仍是 active"
# 被删的材料笔记按 EG-NOTE-01 本就没有 status 这一格：删除也不得给它长出一个。
grep -q '^status:' "${VAULT}/${NOTE_REL}" &&
  die "删除给材料笔记长出了 status 键（笔记没有状态维度，EG-NOTE-01）"
[ "$(rawhash "${CARD_REL}")" = "${CARD_HASH_BEFORE}" ] ||
  die "受影响卡字节发生变化：删除既不改状态也不级联改关系"
ok "⑩ 受影响卡字节逐字未变（status 仍 active），被删笔记未长出 status 键"

# ---------------------------------------------------------------- 12. ⑪ 关系记录一条不删
step "⑪ 关系记录一条不删：relations[] / sources[] 条目数逐条不变"
[ "$(seqcount "${CARD_REL}" sources)" = "${CARD_SOURCES_BEFORE}" ] ||
  die "sources[] 条目数变了：${CARD_SOURCES_BEFORE} → $(seqcount "${CARD_REL}" sources)"
[ "$(seqcount "${CARD_REL}" relations)" = "${CARD_RELATIONS_BEFORE}" ] ||
  die "relations[] 条目数变了：${CARD_RELATIONS_BEFORE} → $(seqcount "${CARD_REL}" relations)"
grep -Fq "note: ${NOTE}" "${VAULT}/${CARD_REL}" ||
  die "指向被删笔记的 sources[] 记录被抹掉了：关系过滤靠端点有效性，记录一条不删"
ok "⑪ sources[] 恰 ${CARD_SOURCES_BEFORE} 条、relations[] 恰 ${CARD_RELATIONS_BEFORE} 条，指向被删对象的记录仍在册"

# ---------------------------------------------------------------- 13. ⑫ 工作区洁净度复核
# 2026-09-07 依 A-32 / K-041-01 重钉：delete 成功后回写与目标写入同进一次 commit，工作区必须干净。
step "⑫ 洁净度复核：一次成功的 eg delete 之后 porcelain=0、git_commit=0、execution.status=succeeded、log 恰 +1 且 HEAD 含提案"
PORC_AFTER="$(porcelain_count)"
DIRTY="$(gitv status --porcelain)"
HEAD_SHA="$(gitv rev-parse HEAD)"
EXEC_STATUS="$(awk '/^execution:/{f=1;next} f&&/^  status:/{print $2;exit}' "${VAULT}/${PREL}" | tr -d "'\"")"
GITCOMMIT_LINES="$(grep -c '^  git_commit:' "${VAULT}/${PREL}" || true)"
printf '  实测：git status --porcelain | wc -l = %s；本次 delete 的 git log 增量 = +%s\n' \
  "${PORC_AFTER}" "${DELTA}"
printf '  实测：porcelain 内容 =「%s」；HEAD = %s；execution.status = %s；提案里 git_commit 行数 = %s\n' \
  "${DIRTY}" "${HEAD_SHA}" "${EXEC_STATUS}" "${GITCOMMIT_LINES}"
# git log 恰 +1 是硬判据：一次成功的 delete 恰一次 commit（第 ⑦ 步已断言，这里再钉一遍）。
[ "${DELTA}" = "1" ] || die "git log 增量 = ${DELTA}，必须恰 +1"
# A-32 新合同：execution=succeeded，且**不再**持久化 git_commit（§4.3 SHA 已退役）。
[ "${EXEC_STATUS}" = "succeeded" ] || die "execution.status = ${EXEC_STATUS}，期望 succeeded"
[ "${GITCOMMIT_LINES}" = "0" ] || die "提案里仍有 ${GITCOMMIT_LINES} 行 git_commit：A-32 已整键退役，succeeded 不得再写 git_commit"
# K-041-01：回写与目标写入同进一次 commit，工作区**必须干净**，任何知识/提案产物都不得变脏。
[ "${PORC_AFTER}" = "0" ] ||
  { gitv status --porcelain; die "delete 之后未提交变更数 = ${PORC_AFTER}，新合同要求恰 0（回写已同进本次 commit）"; }
[ -z "${DIRTY}" ] || die "工作区必须干净，实际残留「${DIRTY}」"
# HEAD 那次 delete commit 必须**包含**提案文件本身（证明 execution 回写确实进了这次 commit）。
gitv show --name-only --pretty=format: HEAD | grep -qx "${PREL}" ||
  { gitv show --name-only --pretty=format: HEAD; die "HEAD 的 delete commit 未包含提案文件 ${PREL}：回写没进本次 commit"; }
ok "⑫ 实测：git log 恰 +1；porcelain = 0；execution.status = succeeded 且 git_commit 0 行；HEAD 含 ${PREL}"

step "⑫-补 delete commit 已含提案回写，无残留可提交（证明回写「已进 commit」而非「写坏/漏写」）"
# 新合同下 porcelain 已为 0，此处再确认一次：不存在需要额外提交的脏变更。
[ "$(porcelain_count)" = "0" ] || { gitv status --porcelain; die "仍有脏变更：说明回写没有随 delete commit 落盘"; }
ok "⑫-补 delete 之后无任何待提交变更，回写确已随本次 commit 落盘"

# ---------------------------------------------------------------- 14. ⑬ undelete
step "⑬ eg undelete --target … --reason … → 退 0，deleted_at 已清空，status 仍逐字未变"
LOGN_BEFORE="$(logcount)"
[ "$(eg_code undelete --target "${NOTE}" --reason '误删：该材料仍是唯一依据' --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg undelete 未退 0（本命令不做二次确认、不启用退出码 6）"; }
grep -q '^deleted_at:' "${VAULT}/${NOTE_REL}" && die "undelete 后仍有 deleted_at：必须整行清空"
grep -q '^deleted_reason:' "${VAULT}/${NOTE_REL}" && die "undelete 后仍有 deleted_reason：两键同生同灭"
[ "$(statusline "${CARD_REL}")" = "${CARD_STATUS_BEFORE}" ] ||
  die "undelete 改了受影响卡的 status 行：删除维度与状态维度正交"
[ "$(rawhash "${CARD_REL}")" = "${CARD_HASH_BEFORE}" ] || die "undelete 动了别的产物：只改目标一个文件"
[ "$(( $(logcount) - LOGN_BEFORE ))" = "1" ] || die "undelete 必须产生恰 1 次 commit"
[ "$(porcelain_count)" = "0" ] || { gitv status --porcelain; die "undelete 之后工作区必须干净"; }
ok "⑬ undelete 退 0：两键整行清空、status 逐字未变、恰 +1 次 commit、工作区干净"

# ---------------------------------------------------------------- 15. ⑭ 重复 undelete → W11 幂等
step "⑭ 重复 eg undelete → W11 幂等：退 0、零写入、**零 commit**"
LOGN_BEFORE="$(logcount)"
NHASH_BEFORE="$(rawhash "${NOTE_REL}")"
[ "$(eg_code undelete --target "${NOTE}" --reason '再次恢复' --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "重复 undelete 必须退 0（幂等 no-op 不是失败）"; }
grep -Fq '幂等 no-op' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "报告缺 W11 幂等说明"; }
[ "$(rawhash "${NOTE_REL}")" = "${NHASH_BEFORE}" ] || die "幂等 no-op 必须零写入"
[ "$(logcount)" = "${LOGN_BEFORE}" ] || die "幂等 no-op 不得产生 commit（空 commit 会污染历史）"
[ "$(porcelain_count)" = "0" ] || die "幂等 no-op 之后工作区必须干净"
ok "⑭ 重复 undelete：退 0、字节不变、零 commit"

# ---------------------------------------------------------------- 16. 越界反证
step "越界反证：无标记输出与检索过滤（T-…-043）、无材料充分性自动判定（S3）、不启用退出码 5"
grep -q '已删除\]' "${VAULT}/${CARD_REL}" && die "本层不得产出「已删除」标记（T-…-043）"
[ "$(eg_code search 注意力 --json)" = "0" ] || { cat "${WORK}/err.txt"; die "search 只读必须退 0"; }
grep -q '未过目\]' "${WORK}/out.txt" && die "本层不得输出「未过目」标记（T-…-043）"
[ "$(eg_code undelete --target "${NOTE}")" = "1" ] || die "缺 --reason 的 undelete 必须退 1"
[ "$(eg_code undelete --reason '无目标')" = "1" ] || die "缺 --target 的 undelete 必须退 1"
[ "$(porcelain_count)" = "0" ] || die "退 1 必须零写入"
ok "越界项全部为负；缺必填参数的 undelete 退 1 且零写入"

printf '\n=== 全部 %d 步通过：§5.3 十一步时序 + undelete（含 W11 幂等）端到端可用 ===\n' "${STEP}"
exit 0

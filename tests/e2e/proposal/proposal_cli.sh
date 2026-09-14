#!/usr/bin/env bash
# `eg proposal` 五条子命令的端到端脚本（T-evergreen.s1_main_flow-158614-040 第三层）。
#
# 判据来源：提案合同 `2026-10-10-m3-proposal-state-contract.md`
#   §7.1 命令表 S2 行（new / list / show / approve / reject）
#   §10.2 时序第 5 步（new 与 reject 各恰一次 commit，verb = proposal）
#   §10.2 时序第 7 步（**无 --confirm → 退 6 且权威 Markdown 完全不变**）
#   §10.3「可提不可执」（new 允许 Agent 路径，不要求 --user-request）
#   授权合同 §6 U-12（approve 必须由用户显式发起；N-1：文件内容不能自证）
#
# 五段断言（task Acceptance 逐字）：
#   ① new 走 **Agent 路径**（不带 --user-request）→ 退 0，落盘 status: pending /
#      execution: not_started，且产生 **1 次** `proposal(` 开头的 commit；
#   ② list → 只读：退 0，`git status --porcelain` 计数不变；
#   ③ show <id> → 只读：退 0，输出含 status / execution / decision / impact 与**七个 H2**；
#   ④ **无 --confirm 的 approve → 退 6 且零变化**：`git status --porcelain | wc -l`、
#      `git log --oneline -1` 与提案字节均与执行前**逐字相同**（顺带证 U-12：缺
#      --user-request 的 Agent 路径退 2，同样零变化）；
#   ⑤ reject <id> --reason → 退 0，status: rejected 且 decision.result 与之一致。
#
# 为什么第 ④ 段要同时比对三样（porcelain 计数 / log -1 / 文件字节）：
#   退出码 6 的语义边界是「权威 Markdown 完全不变」，单看退出码无法区分
#   「什么都没做」与「写了又提交了但报了个 6」。三样同时不变才是零写入的真判据。
# 为什么第 ① 段刻意不带 --user-request：提案的**创建**对 Agent 开放（可提不可执），
#   一旦有人给 new 也加上授权门，这一步立刻红。
#
# 越界负向断言（本层不做）：无 eg delete / undelete、无 deleted_at 写入（T-…-041）、
#   批准后 execution 仍 not_started（三态回写属 T-…-036）。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/proposal/proposal_cli.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-proposal-cli.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/ai-infra/knowledge'
CARD='k-20260901-attention'

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
# fmvalue 取 frontmatter 标量值并**剥掉引号**：写口对标量一律加单引号（可解析优先），
# 断言比的是语义值，不是引号风格。
fmvalue() { awk -v k="$2:" '$1==k{print $2; exit}' "${VAULT}/$1" | tr -d "'\""; }

# ---------------------------------------------------------------- 0. 构建与建库
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

step "建库：eg init + default_domain + 一张删除目标卡（提案的 --target）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}"
{
  printf -- '---\nid: %s\ntitle: 注意力机制\nstatus: active\n' "${CARD}"
  printf "created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n"
  printf 'sources: []\n'
  printf -- '---\n\n# 注意力机制\n\n## 知识内容\n\n正文占位。\n'
  printf '\n## 解释与依据\n\n依据占位。\n\n## 条件与边界\n\n边界占位。\n'
  printf '\n## 用户补充\n\n## 理解自检\n\n- 自检问题占位？\n'
} >"${VAULT}/${KDIR_REL}/${CARD}.md"
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m3 提案 CLI 语料"
[ "$(porcelain_count)" = "0" ] || die "前置条件：工作区必须干净"
ok "库就绪：${KDIR_REL}/${CARD}.md（status: active），工作区干净"

# ---------------------------------------------------------------- 1. ① new
step "① new 走 Agent 路径（不带 --user-request）→ 退 0，pending / not_started，恰 1 次 proposal( commit"
BEFORE_LOG="$(logcount)"
[ "$(eg_code proposal new --type logical_delete --target "${CARD}" --reason '该卡已过时' --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg proposal new 未退 0（Agent 路径必须放行：§10.3 可提不可执）"; }
PID="$(grep -oE 'p-[0-9]{8}-[0-9]{3}' "${WORK}/out.txt" | head -1)"
[ -n "${PID}" ] || { cat "${WORK}/out.txt"; die "new 未返回提案 ID"; }
PREL="proposals/${PID}.md"
[ -f "${VAULT}/${PREL}" ] || die "提案未落盘：${PREL}"
[ "$(fmvalue "${PREL}" status)" = "pending" ] ||
  die "status = $(fmvalue "${PREL}" status)，期望 pending"
grep -qE "^  status: '?not_started'?$" "${VAULT}/${PREL}" ||
  die "execution.status 必须是 not_started"
[ "$(( $(logcount) - BEFORE_LOG ))" = "1" ] || die "new 必须产生恰 1 次 commit"
SUBJECT="$(gitv log -1 --pretty=%s)"
case "${SUBJECT}" in
  proposal\(*) : ;;
  *) die "commit 主题 = ${SUBJECT}，期望以 proposal( 开头（verb = proposal）" ;;
esac
[ "$(porcelain_count)" = "0" ] || die "new 之后工作区必须干净（已提交）"
ok "① ${PID} 已创建：pending / not_started，commit 主题 ${SUBJECT}"

# ---------------------------------------------------------------- 2. ② list
step "② list → 只读：退 0，git status --porcelain 计数不变"
BEFORE_PORC="$(porcelain_count)"
BEFORE_LOG="$(logcount)"
[ "$(eg_code proposal list --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg proposal list 未退 0"; }
grep -q "${PID}" "${WORK}/out.txt" || die "list 输出未含 ${PID}"
[ "$(eg_code proposal list --status pending --execution not_started)" = "0" ] ||
  die "eg proposal list 带过滤未退 0"
[ "$(porcelain_count)" = "${BEFORE_PORC}" ] ||
  die "list 改变了工作区：porcelain 计数 ${BEFORE_PORC} → $(porcelain_count)"
[ "$(logcount)" = "${BEFORE_LOG}" ] || die "list 不得产生 commit"
ok "② list 只读：退 0，porcelain 计数恒 ${BEFORE_PORC}，零 commit"

# ---------------------------------------------------------------- 3. ③ show
step "③ show <id> → 只读：退 0，输出含 status / execution / decision / impact 与七个 H2"
[ "$(eg_code proposal show "${PID}" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg proposal show 未退 0"; }
for key in status execution decision impact; do
  grep -q "\"${key}\"" "${WORK}/out.txt" || die "show 的 --json 输出缺 ${key}"
done
for key in exits_default_view cards_losing_support affected_material_rels affected_relations; do
  grep -q "\"${key}\"" "${WORK}/out.txt" || die "show 的 impact 缺 ${key}"
done
[ "$(eg_code proposal show "${PID}")" = "0" ] || die "eg proposal show（人类可读）未退 0"
N_H2=0
for sec in 推荐修改 理由与证据 '影响的文件、领域与关系' 执行后状态 不执行的影响 替代方案 可应用内容; do
  grep -Fq "## ${sec}" "${WORK}/out.txt" || die "show 输出缺 H2「${sec}」"
  N_H2=$((N_H2 + 1))
done
[ "${N_H2}" = "7" ] || die "H2 分区数 = ${N_H2}，必须恰 7"
[ "$(porcelain_count)" = "${BEFORE_PORC}" ] || die "show 改变了工作区"
[ "$(logcount)" = "${BEFORE_LOG}" ] || die "show 不得产生 commit"
ok "③ show 只读：四键齐备 + 七个 H2 齐备，零文件变化零 commit"

# ---------------------------------------------------------------- 4. ④ approve 无 --confirm → 6
step "④ 无 --confirm 的 approve → 退 6 且**零变化**（porcelain 计数 / log -1 / 提案字节逐字相同）"
BEFORE_PORC="$(porcelain_count)"
BEFORE_LOG1="$(gitv log --oneline -1)"
BEFORE_HASH="$(sha256sum "${VAULT}/${PREL}" | cut -d' ' -f1)"
BEFORE_LOGN="$(logcount)"
CODE="$(eg_code proposal approve "${PID}" --user-request)"
[ "${CODE}" = "6" ] || { cat "${WORK}/err.txt"; die "无 --confirm 的 approve 退出码 = ${CODE}，必须是 6"; }
grep -q -- '--confirm' "${WORK}/err.txt" "${WORK}/out.txt" ||
  die "退 6 时必须提示补 --confirm"
[ "$(porcelain_count)" = "${BEFORE_PORC}" ] ||
  die "退 6 后 porcelain 计数变了：${BEFORE_PORC} → $(porcelain_count)"
[ "$(gitv log --oneline -1)" = "${BEFORE_LOG1}" ] || die "退 6 后 git log -1 变了"
[ "$(sha256sum "${VAULT}/${PREL}" | cut -d' ' -f1)" = "${BEFORE_HASH}" ] ||
  die "退 6 后提案字节变了：权威 Markdown 必须完全不变"
[ "$(logcount)" = "${BEFORE_LOGN}" ] || die "退 6 不得产生 commit"
[ "$(fmvalue "${PREL}" status)" = "pending" ] || die "退 6 后 status 必须仍是 pending"
# U-12：Agent 路径（缺 --user-request 命令行佐证）→ 退 2，同样零变化（N-1 反伪造）。
CODE="$(eg_code proposal approve "${PID}" --confirm)"
[ "${CODE}" = "2" ] || die "缺 --user-request 的 approve 退出码 = ${CODE}，必须是 2（U-12）"
grep -q 'U-12' "${WORK}/err.txt" "${WORK}/out.txt" || die "U-12 拒绝必须点名 U-12"
[ "$(sha256sum "${VAULT}/${PREL}" | cut -d' ' -f1)" = "${BEFORE_HASH}" ] ||
  die "U-12 拒绝时必须零写入"
[ "$(logcount)" = "${BEFORE_LOGN}" ] || die "U-12 拒绝不得产生 commit"
ok "④ 无 --confirm → 6 且三样逐字不变；U-12 的 Agent 路径 → 2 且零写入"

# ---------------------------------------------------------------- 5. ⑤ reject
step "⑤ reject <id> --reason → 退 0，status: rejected 且 decision.result 一致"
BEFORE_LOGN="$(logcount)"
[ "$(eg_code proposal reject "${PID}" --reason '目标卡仍有引用，暂不删除' --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg proposal reject 未退 0"; }
[ "$(fmvalue "${PREL}" status)" = "rejected" ] ||
  die "status = $(fmvalue "${PREL}" status)，期望 rejected"
RESULT="$(awk '/^decision:/{f=1;next} f&&/^  result:/{print $2;exit}' "${VAULT}/${PREL}" | tr -d "'\"")"
[ "${RESULT}" = "rejected" ] ||
  die "decision.result = ${RESULT}，必须与 status 逐字一致（M-5 / A-20）"
[ "$(( $(logcount) - BEFORE_LOGN ))" = "1" ] || die "reject 必须产生恰 1 次 commit"
grep -q '目标卡仍有引用' "${VAULT}/${PREL}" || die "reject 的理由必须落盘（十项必备 ⑨）"
ok "⑤ ${PID} 已拒绝：status / decision.result 双向一致，恰 1 次 commit"

# ---------------------------------------------------------------- 6. 越界反证
step "越界反证：本层不做删除类逐文件写入（T-…-041）、不回写 execution 三态（T-…-036）"
[ "$(fmvalue "${PREL}" status)" = "rejected" ] || die "状态被意外改写"
grep -qE "^  status: '?not_started'?$" "${VAULT}/${PREL}" ||
  die "execution.status 必须仍是 not_started（三态回写属 T-…-036）"
grep -q 'deleted_at' "${VAULT}/${KDIR_REL}/${CARD}.md" &&
  die "本层不得写 deleted_at（逻辑删除的逐文件写入属 T-…-041）"
[ "$(eg_code delete "${CARD}")" != "0" ] || die "eg delete 不该在本层可用"
ok "越界项全部为负：无 deleted_at、execution 未被回写、eg delete 未启用"

printf '\n=== 全部 %d 步通过：eg proposal new|list|show|approve|reject 端到端可用 ===\n' "${STEP}"
exit 0

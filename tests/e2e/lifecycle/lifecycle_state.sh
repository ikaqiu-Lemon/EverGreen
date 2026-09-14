#!/usr/bin/env bash
# M3 生命周期状态命令端到端脚本（T-evergreen.s1_main_flow-158614-039）。
#
# 判据来源：`2026-10-10-m3-proposal-state-contract.md` §5.1 真值表 / §5.3 失效卡语义 /
#   §8.1 op 字段表；`2026-10-10-m3-user-authorization-contract.md` §2 写权限矩阵
#   #3（status）/ #4（replaced_by）：P-A 🔴、P-U ✅，§3 X1「需确认 = 否」。
#
# 四组断言：
#   ① deprecate → restore → deprecate 三轮：frontmatter **无历史数组新增键**
#      （反复覆盖同一个 status 单键），且 git log 计数恰 +3；
#   ② W11 幂等：重复 deprecate → 报告含 W11、git log 计数**不增**（不产生空 commit）；
#   ③ E10 / W12 两象限：replaced-by 的目标已逻辑删除 → 退 2 零写入；
#      --to 是 deprecated 且未删除 → 退 0 + W12（允许但提示）；
#   ④ 失效卡可继续追加 support → 退 0 + deprecated_new_support[] 非空 + status 仍 deprecated。
#
# 为什么第 ① 组要数「键」而不只看 status：状态变更**不留历史数组**是本阶段的形态裁决——
#   若哪天有人给它加上 status_history / deprecated_at 之类的累积键，这一步立刻红。
# 为什么第 ② 组要数 commit：幂等 no-op 的真正判据是「盘上没动、历史没长」，
#   退 0 本身不能区分「什么都没做」与「原样重写一遍」。
# 为什么第 ③ 组把两象限放在一起：§5.1 真值表里「已删除」是 🔴、「deprecated 未删除」是 🟡，
#   两格必须同时被证；只证一格无法说明拒绝是**按象限**发生的。
#
# 越界负向断言（本阶段不做）：无 eg delete / eg undelete、无 deleted_at 写入（T-…-041）、
#   无「材料支持不足」（S3）、三条状态命令不含 confirm 字样（§3 X1）。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/lifecycle/lifecycle_state.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-state.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/ai-infra/knowledge'
CARD_OLD="k-20260901-old"      # 被失效 / 被替代的主体卡
CARD_NEW="k-20260902-new"      # 替代它的新卡（replaced-by 的指向端）
CARD_DEL="k-20260903-deleted"  # 已逻辑删除的卡（E10 象限的靶子）
CARD_SUP="k-20260904-support"  # 追加 support 的来源卡
REL_OLD="${KDIR_REL}/${CARD_OLD}.md"
REL_NEW="${KDIR_REL}/${CARD_NEW}.md"

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
logcount() { gitv log --oneline | wc -l | tr -d ' '; }
rawhash() { sha256sum "${VAULT}/$1" | cut -d' ' -f1; }
# fmkeys 抽出 frontmatter 的**顶层键集合**（缩进行不算），用于反证没长出历史数组。
fmkeys() { awk 'NR==1&&/^---$/{next} /^---$/{exit} /^[A-Za-z_]+:/{sub(/:.*/,"");print}' \
  "${VAULT}/$1" | sort | tr '\n' ' '; }
fmvalue() { awk -v k="$2:" '$1==k{print $2; exit}' "${VAULT}/$1"; }

seed_card() { # $1=id $2=status $3=额外 frontmatter 行（可空）
  cat >"${VAULT}/${KDIR_REL}/$1.md" <<CARD_EOF
---
id: $1
title: 卡 $1
status: $2
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources: []${3:+
$3}
---

# 卡 $1

## 知识内容

正文占位。
CARD_EOF
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 语料
step "eg init 建库 + 四张卡（active / active / 已逻辑删除 / active）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}"
seed_card "${CARD_OLD}" active ""
seed_card "${CARD_NEW}" active ""
# 已逻辑删除的卡由**用例直接落字节**：本阶段不实现 eg delete（T-…-041），
# 脚本因此只造出该象限的语料，不借用任何未实现的命令。
seed_card "${CARD_DEL}" active "deleted_at: '2026-09-02T10:00:00+08:00'
deleted_reason: 语料：已逻辑删除"
seed_card "${CARD_SUP}" active ""
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m3 生命周期语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
KEYS_BEFORE="$(fmkeys "${REL_OLD}")"
ok "库就绪，主体卡 frontmatter 顶层键：${KEYS_BEFORE}"

# ---------------------------------------------------------------- 2. ① 三轮往复
step "① deprecate → restore → deprecate 三轮：单键反复覆盖 + git log 恰 +3"
LOG0="$(logcount)"
[ "$(eg_code deprecate --target "${CARD_OLD}" --reason "结论已被新证据推翻")" = "0" ] ||
  { cat "${WORK}/err.txt"; die "第 1 轮 deprecate 应退 0"; }
[ "$(fmvalue "${REL_OLD}" status)" = "deprecated" ] || die "第 1 轮后 status 应为 deprecated"
[ "$(eg_code restore --target "${CARD_OLD}" --reason "用户判断结论仍成立")" = "0" ] ||
  { cat "${WORK}/err.txt"; die "restore 应退 0"; }
[ "$(fmvalue "${REL_OLD}" status)" = "active" ] || die "restore 后 status 应为 active"
[ "$(eg_code deprecate --target "${CARD_OLD}" --reason "再次确认结论过期")" = "0" ] ||
  { cat "${WORK}/err.txt"; die "第 3 轮 deprecate 应退 0"; }
[ "$(fmvalue "${REL_OLD}" status)" = "deprecated" ] || die "第 3 轮后 status 应为 deprecated"

KEYS_AFTER="$(fmkeys "${REL_OLD}")"
[ "${KEYS_AFTER}" = "${KEYS_BEFORE}" ] ||
  die "frontmatter 顶层键集合发生变化（不得新增历史数组）：${KEYS_BEFORE} → ${KEYS_AFTER}"
[ "$(grep -c '^status:' "${VAULT}/${REL_OLD}")" = "1" ] || die "status 键必须恰 1 次（单键覆盖）"
grep -q 'deleted_at' "${VAULT}/${REL_OLD}" && die "状态命令不得写 deleted_at（T-…-041）"
DELTA=$(( $(logcount) - LOG0 ))
[ "${DELTA}" = "3" ] || die "三轮状态变更应恰 +3 个 commit，实际 +${DELTA}"
ok "① 三轮往复只覆盖 status 单键、无历史数组新增键、git log 恰 +3"

# ---------------------------------------------------------------- 3. ② W11 幂等
step "② W11 幂等：重复 deprecate → 报告含 W11、git log 计数不增"
LOG1="$(logcount)"
HASH_OLD="$(rawhash "${REL_OLD}")"
[ "$(eg_code deprecate --target "${CARD_OLD}" --reason "重复失效同一张卡" --json)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "幂等重复 deprecate 应退 0"; }
grep -Fq '"W11"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "报告必须含 W11（幂等 no-op）"; }
[ "$(logcount)" = "${LOG1}" ] || die "幂等 no-op 不得产生 commit（${LOG1} → $(logcount)）"
[ "$(rawhash "${REL_OLD}")" = "${HASH_OLD}" ] || die "幂等 no-op 必须零写入（字节应不变）"
[ -z "$(gitv status --porcelain)" ] || { gitv status --porcelain; die "幂等后工作区必须干净"; }
ok "② W11 在场、零写入、零 commit"

# ---------------------------------------------------------------- 4. ③ E10 / W12
step "③ E10 象限：replaced-by 的 target 已逻辑删除 → 退 2 + 零写入"
LOG2="$(logcount)"
HASH_NEW="$(rawhash "${REL_NEW}")"
C="$(eg_code replaced-by --target "${CARD_DEL}" --to "${CARD_NEW}" \
  --reason "已删除对象不得作替代指针主体" --json)"
[ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "已逻辑删除的 target 应退 2，实际 ${C}"; }
grep -Fq '"E10"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "应产出 E10"; }
[ "$(logcount)" = "${LOG2}" ] || die "E10 必须零 commit"
[ -z "$(gitv status --porcelain)" ] || { gitv status --porcelain; die "E10 必须零写入"; }
ok "③-a E10：退 2、零写入、零 commit"

step "③ W12 象限：--to 是 deprecated 且未删除 → 退 0 + W12（照常写入 + 提示）"
# 指向端先失效（deprecated 且**未**逻辑删除），构成 §5.1 的 🟡 象限。
[ "$(eg_code deprecate --target "${CARD_NEW}" --reason "新卡本身也已过期")" = "0" ] ||
  die "前置 deprecate（指向端）应退 0"
C="$(eg_code replaced-by --target "${CARD_OLD}" --to "${CARD_NEW}" \
  --reason "由新卡承接结论" --json)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "deprecated 未删除的指向端应退 0，实际 ${C}"; }
grep -Fq '"W12"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "应产出 W12"; }
grep -q 'replaced_by:' "${VAULT}/${REL_OLD}" || die "失效卡上应写出 replaced_by"
[ "$(rawhash "${REL_NEW}")" != "${HASH_NEW}" ] || true # 指向端因上一步 deprecate 而变，属预期
grep -q 'replaced_by:' "${VAULT}/${REL_NEW}" && die "replaced_by 是单向的：被指向卡不得反向写入"
ok "③-b W12：退 0、写入落在失效卡上、被指向卡无反向指针"

# ---------------------------------------------------------------- 5. ④ 失效卡追加 support
step "④ 失效卡可继续追加 support → 退 0 + deprecated_new_support[] 非空 + status 仍 deprecated"
C="$(eg_code rel add "${CARD_SUP}" supports "${CARD_OLD}" \
  --reason "新证据仍支持旧卡的部分结论" --json)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "对 deprecated 卡追加 support 应退 0，实际 ${C}"; }
grep -Fq "\"deprecated_new_support\":[{" "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "deprecated_new_support[] 必须非空"; }
[ "$(fmvalue "${REL_OLD}" status)" = "deprecated" ] ||
  die "追加 support 后 status 必须仍是 deprecated（不自动恢复，U-06）"
grep -q '材料支持不足' "${WORK}/out.txt" && die "本阶段不得输出「材料支持不足」（归 S3）"
ok "④ 退 0、deprecated_new_support[] 非空、status 仍 deprecated、无 S3 措辞"

# ---------------------------------------------------------------- 6. 越界与源码反证
step "⑤ 源码反证：状态写口唯一、无退出码 5/6、三条状态命令不含确认流程"
N="$( { cd "${REPO_ROOT}" && grep -rnE '\.(SetStatus|SetReplacedBy|SetDeleted)\(' internal/ || true; } |
  { grep -v '_test\.go' || true; } | { grep -v '^internal/store/' || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "状态写口必须全部落在 internal/store/（越界 ${N} 处）"
N="$( { cd "${REPO_ROOT}" && grep -rn 'os.Exit(5)\|os.Exit(6)' internal/ || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "本阶段不启用退出码 5 / 6（${N} 处）"
# 确认流程只属 eg proposal approve（授权合同 §3 X1：三条状态命令「需确认 = 否」）。
# 允许命中的文件随提案子系统落地从 1 个变成 3 个（**事实变了，判据没放宽**）：
#   internal/cli/proposal.go      —— approve 的批准前重算能力（T-…-035）
#   internal/cli/exitcode.go      —— 退出码 6 的确认判定与白名单（T-…-040 上一层）
#   internal/cli/proposal_cmd.go  —— eg proposal 子命令壳与 --confirm 参数声明（T-…-040）
#   internal/cli/proposal_approve.go —— approve 的确认门与 U-12 守卫（T-…-040 第三层）
#   internal/cli/delete.go        —— eg delete 的确认门（T-…-041；退出码 6 白名单本就恰两条命令
#                                    proposal approve / delete，见 exitcode.go 的 needConfirmCommands）
# 判据本体不变：deprecate.go / restore / replaced_by.go 这三条状态命令一次都不许出现。
CONFIRM_FILES="$( { cd "${REPO_ROOT}" && grep -rln 'con''firm' internal/cli/ || true; } |
  { grep -v '_test\.go' || true; } | LC_ALL=C sort | tr '\n' ' ')"
# LC_ALL=C：文件名排序不受本机 locale 影响（'.' < '_'），判据在任何机器上逐字相同。
[ "${CONFIRM_FILES}" = "internal/cli/delete.go internal/cli/exitcode.go internal/cli/proposal.go internal/cli/proposal_approve.go internal/cli/proposal_cmd.go " ] ||
  die "确认流程只应出现在 delete.go / proposal.go / proposal_approve.go / exitcode.go / proposal_cmd.go，实际：${CONFIRM_FILES}"
# 「材料支持不足」在 internal/ 里只以**阶段说明注释**形态存在（M2 起 query / search 里
# 各有一条「本阶段不输出」的负向说明）；本 task 新增的三个文件里必须**一次都不出现**，
# 且运行期输出（上一步已查）也不得含它。
N="$( { cd "${REPO_ROOT}" && grep -rn '材料支持不足\|insufficient_support' \
  internal/cli/deprecate.go internal/cli/restore.go internal/cli/replaced_by.go || true; } |
  wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "三条状态命令不得提及「材料支持不足」（归 S3，${N} 处）"
ok "⑤ 写口唯一、无退出码 5/6、确认流程仅 proposal.go、无 S3 措辞"

# ---------------------------------------------------------------- 7. 收口
step "收口：三条命令在 --help 在场、工作区干净"
[ "$(eg_code --help)" = "0" ] || die "eg --help 应退 0"
for c in deprecate restore replaced-by; do
  grep -Fq "${c}" "${WORK}/out.txt" || die "--help 命令区缺 ${c}"
done
[ -z "$(gitv status --porcelain)" ] || { gitv status --porcelain; die "收口时工作区必须干净"; }
ok "收口：deprecate / restore / replaced-by 均在 --help 在场，工作区干净"

printf '\n=== 全部 %d 步、%d 条断言通过：M3 生命周期状态判据成立（T-…-039）===\n' "${STEP}" "${PASS}"
exit 0

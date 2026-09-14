#!/usr/bin/env bash
# `reviewed_at` / `eg mark-reviewed` / `eg unreviewed` 的端到端脚本
# （T-evergreen.s1_main_flow-158614-042 的 verify.run 逐字调用本文件）。
#
# 判据来源：提案与状态合同 §6.1 全表（「一律不更新 reviewed_at 的动作」四项、
# 「无 reviewed_at 的产物计入 eg unreviewed」、ADR-20 硬约束行）、§4.6 末尾
# 「结果报告同样不更新它」、§14.4 EG-VIEW-08；授权合同 §2 矩阵 #7 / #22 与 §9 A-15 / A-16。
#
# 四组断言：
#   A. mark-reviewed 前后 unreviewed 的**差集**恰为被标记的那一个产物；
#      mark-reviewed 只写 reviewed_at 一个键（status / updated_at / 删除维度逐字不变），
#      恰 +1 次 commit（verb=process），工作区干净。
#   B. 四类动作一律不更新 reviewed_at：被动查看（eg card show）/ 结果报告（eg report --last）/
#      索引读取（eg search）/ Agent 写入（eg apply，无 --user-request）——每类都断言
#      目标文件 reviewed_at 那一行**逐字不变**。其中 Agent 写入还额外钉一条事实：
#      它更新了 updated_at，于是该卡**重新**进入未过目清单（这正是该信号的用途）。
#   C. 无 reviewed_at 的产物计入：材料笔记全程没有该键，却始终在清单里；
#      且 --json 的 reviewed_at 字段如实为空串（不回填任何默认时刻）。
#   D. 零副作用：eg unreviewed 前后 git status --porcelain 计数 = 0、commit 数不变、
#      目标字节不变；输出不含催促类文案（反证按 grep -c → 0）。
#
# 越界反证（本层一律不做）：不输出「未过目」文本标记（T-…-043）、不做对账补齐（S3/M4）、
# 不启用退出码 6（mark-reviewed 不在 6 的白名单里）、不做过期自动判定（S3）。
#
# 约束：离线、可重复执行、依赖仅 bash / coreutils / git / go / jq；
# 任何一条断言不成立立刻非零退出；全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/lifecycle/reviewed.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-reviewed.XXXXXX")"
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
# chash 算 base 用的 content_hash（与 store.ContentHash 同口径：sha256: + 十六进制）。
chash() { printf 'sha256:%s\n' "$(rawhash "$1")"; }
# fmline 取某个 frontmatter 顶层键所在的**整行**（含引号与空格）：逐字比对用。
fmline() { grep -m1 "^$2:" "${VAULT}/$1" || true; }
# fmcount 数某个 frontmatter 顶层键出现的行数（单键覆盖 → 恒为 0 或 1）。
fmcount() { grep -c "^$2:" "${VAULT}/$1" || true; }
# strip_fm_value 从 `key: '值'` 整行里取出裸值（去掉键名与包裹引号）。
strip_fm_value() { printf '%s' "$1" | sed 's/^[a-z_]*: *//' | tr -d "'\""; }
# fmvalue_of 直接取某个 frontmatter 顶层键的裸值。
fmvalue_of() { strip_fm_value "$(fmline "$1" "$2")"; }
# stamp_gt <新> <旧>：两个带时区 RFC3339 时刻，严格晚于则退 0（与 c2_updated_at_refresh.sh 同源口径）。
stamp_gt() {
  python3 - "$1" "$2" <<'PY_STAMP'
import sys
from datetime import datetime
def p(v):
    return datetime.fromisoformat(v.replace("Z", "+00:00"))
sys.exit(0 if p(sys.argv[1]) > p(sys.argv[2]) else 1)
PY_STAMP
}
# ids 从 --json 信封里取未过目清单的 id（只读命令的清单挂在 .data.unreviewed 上）。
ids() { jq -re '.data.unreviewed[]?.id' "$1" | sort; }
# unreviewed_json 跑一次 eg unreviewed 并把信封落到指定文件。
unreviewed_json() {
  local out="$1"; shift
  [ "$(eg_code unreviewed --json "$@")" = "0" ] ||
    { cat "${WORK}/err.txt"; die "eg unreviewed 未退 0（零命中也必须退 0）"; }
  cp "${WORK}/out.txt" "${out}"
}

# ---------------------------------------------------------------- 0. 构建与建库
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
command -v jq >/dev/null || die "本脚本用 jq 读 .data.unreviewed[]"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

step "建库：eg init + 一份原文 / 一篇材料笔记 / 一张卡（三者**都没有** reviewed_at）"
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
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m3 reviewed_at 语料"
[ "$(porcelain_count)" = "0" ] || die "前置条件：工作区必须干净"
grep -q '^reviewed_at:' "${VAULT}/${CARD_REL}" && die "前置条件：语料不得自带 reviewed_at（不回填默认值）"
grep -q '^reviewed_at:' "${VAULT}/${NOTE_REL}" && die "前置条件：笔记不得自带 reviewed_at"
CARD_STATUS_BEFORE="$(fmline "${CARD_REL}" status)"
CARD_UPDATED_BEFORE="$(fmline "${CARD_REL}" updated_at)"
[ -n "${CARD_STATUS_BEFORE}" ] && [ -n "${CARD_UPDATED_BEFORE}" ] ||
  die "前置条件：卡必须同时有 status 与 updated_at 行，否则 A 组失去判据"
ok "库就绪：卡与笔记都没有 reviewed_at（缺省即缺省）"

# ================================================================ A 组
step "A-1 标记前：eg unreviewed 计入**两个**产物（缺 reviewed_at 即从未过目）"
unreviewed_json "${WORK}/before.json"
BEFORE_IDS="$(ids "${WORK}/before.json")"
[ "$(printf '%s\n' "${BEFORE_IDS}" | wc -l | tr -d ' ')" = "2" ] ||
  { cat "${WORK}/before.json"; die "标记前清单应恰含 2 个产物，实得：${BEFORE_IDS}"; }
printf '%s\n' "${BEFORE_IDS}" | grep -qx "${CARD}" || die "标记前清单缺卡 ${CARD}"
printf '%s\n' "${BEFORE_IDS}" | grep -qx "${NOTE}" || die "标记前清单缺笔记 ${NOTE}"
[ "$(jq -re '.data.total' "${WORK}/before.json")" = "2" ] || die "total 与清单长度不一致"
ok "A-1 标记前清单 = ${BEFORE_IDS//$'\n'/ }"

step "A-2 eg mark-reviewed --target ${CARD} → 退 0；只写 reviewed_at 单键、恰 +1 次 commit"
LOG_BEFORE="$(logcount)"
NOTE_HASH_BEFORE="$(rawhash "${NOTE_REL}")"
[ "$(eg_code mark-reviewed --target "${CARD}" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg mark-reviewed 未退 0"; }
[ "$(fmcount "${CARD_REL}" reviewed_at)" = "1" ] ||
  die "reviewed_at 行数 = $(fmcount "${CARD_REL}" reviewed_at)，期望恰 1（单键覆盖，不累积历史）"
[ "$(fmline "${CARD_REL}" status)" = "${CARD_STATUS_BEFORE}" ] ||
  die "mark-reviewed 改了 status 行：status 与过目维度正交"
[ "$(fmline "${CARD_REL}" updated_at)" = "${CARD_UPDATED_BEFORE}" ] ||
  die "mark-reviewed 改了 updated_at：过目不是对内容的修改"
grep -q '^deleted_at:' "${VAULT}/${CARD_REL}" && die "mark-reviewed 写出了 deleted_at：删除维度不得被碰"
grep -q '^deleted_reason:' "${VAULT}/${CARD_REL}" && die "mark-reviewed 写出了 deleted_reason"
[ "$(rawhash "${NOTE_REL}")" = "${NOTE_HASH_BEFORE}" ] || die "mark-reviewed 动了别的产物：只写目标一个文件"
[ "$(( $(logcount) - LOG_BEFORE ))" = "1" ] || die "mark-reviewed 必须产生恰 1 次 commit"
SUBJECT="$(gitv log -1 --pretty=%s)"
case "${SUBJECT}" in process\(*) : ;; *) die "commit 主题 = ${SUBJECT}，期望以 process( 开头" ;; esac
[ "$(porcelain_count)" = "0" ] || { gitv status --porcelain; die "mark-reviewed 之后工作区必须干净"; }
grep -Fq '只写 reviewed_at 一个键' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "报告缺「只写一个键」的说明"; }
CARD_REVIEWED_LINE="$(fmline "${CARD_REL}" reviewed_at)"
ok "A-2 卡的 reviewed_at 行 =「${CARD_REVIEWED_LINE}」，commit 主题 = ${SUBJECT}"

step "A-3 标记后：清单与标记前的**差集**恰为 ${CARD}"
unreviewed_json "${WORK}/after.json"
AFTER_IDS="$(ids "${WORK}/after.json")"
DIFF="$(comm -23 <(printf '%s\n' "${BEFORE_IDS}") <(printf '%s\n' "${AFTER_IDS}"))"
[ "${DIFF}" = "${CARD}" ] || die "差集 =「${DIFF}」，期望恰「${CARD}」"
printf '%s\n' "${AFTER_IDS}" | grep -qx "${NOTE}" || die "笔记未过目，必须仍在清单里"
printf '%s\n' "${AFTER_IDS}" | grep -qx "${CARD}" && die "已过目的卡不得留在清单里"
ok "A-3 差集恰 ${DIFF}；标记后清单 = ${AFTER_IDS//$'\n'/ }"

step "A-4 重复 mark-reviewed：仍只有一行 reviewed_at（覆盖而非累积），status 逐字不变"
[ "$(eg_code mark-reviewed --target "${CARD}" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "重复 mark-reviewed 未退 0"; }
[ "$(fmcount "${CARD_REL}" reviewed_at)" = "1" ] || die "重复标记后 reviewed_at 不止一行"
[ "$(fmline "${CARD_REL}" status)" = "${CARD_STATUS_BEFORE}" ] || die "重复标记改了 status"
[ "$(porcelain_count)" = "0" ] || die "重复标记之后工作区必须干净"
CARD_REVIEWED_LINE="$(fmline "${CARD_REL}" reviewed_at)"
ok "A-4 reviewed_at 恒为一行：${CARD_REVIEWED_LINE}"

# ================================================================ B 组
step "B-1 被动查看 eg card show → reviewed_at 逐字不变"
[ "$(eg_code card show "${CARD}" --json)" = "0" ] || { cat "${WORK}/err.txt"; die "card show 未退 0"; }
[ "$(fmline "${CARD_REL}" reviewed_at)" = "${CARD_REVIEWED_LINE}" ] ||
  die "被动查看更新了 reviewed_at（§5.5 明令一律不更新）"
[ "$(porcelain_count)" = "0" ] || die "card show 是只读命令，工作区必须干净"
ok "B-1 card show 之后 reviewed_at 一字未动"

step "B-2 结果报告 eg report --last → reviewed_at 逐字不变（§4.6 末尾逐字约束）"
[ "$(eg_code report --last --json)" = "0" ] || { cat "${WORK}/err.txt"; die "report --last 未退 0"; }
[ "$(fmline "${CARD_REL}" reviewed_at)" = "${CARD_REVIEWED_LINE}" ] ||
  die "结果报告更新了 reviewed_at（报告同样不更新它）"
ok "B-2 report --last 之后 reviewed_at 一字未动"

step "B-3 索引 / 查询读取 eg search → reviewed_at 逐字不变"
[ "$(eg_code search 注意力 --json)" = "0" ] || { cat "${WORK}/err.txt"; die "search 未退 0"; }
[ "$(fmline "${CARD_REL}" reviewed_at)" = "${CARD_REVIEWED_LINE}" ] ||
  die "查询读取更新了 reviewed_at"
grep -q '未过目\]' "${WORK}/out.txt" && die "本层不得输出「未过目」文本标记（T-…-043）"
ok "B-3 search 之后 reviewed_at 一字未动，且未输出标记文本"

step "B-4 Agent 写入 eg apply（**不带** --user-request）→ reviewed_at 逐字不变"
cat >"${WORK}/plan.json" <<JSON
{
  "plan_version": 1, "verb": "process", "domain": "ai-infra",
  "reason": "Agent 自动路径追加一条依据（用于反证它不更新 reviewed_at）",
  "requirement_ids": ["EG-CFM-06"],
  "base": { "${CARD_REL}": "$(chash "${CARD_REL}")" },
  "ops": [
    { "op": "append_card", "card": "${CARD}",
      "sections": { "解释与依据": "- Agent 追加的一条依据。\n" } }
  ]
}
JSON
[ "$(eg_code apply --plan "${WORK}/plan.json" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/out.txt"; die "Agent apply 未退 0"; }
[ "$(fmline "${CARD_REL}" reviewed_at)" = "${CARD_REVIEWED_LINE}" ] ||
  die "Agent 写入更新了 reviewed_at（矩阵 #7 的 P-A 是 🔴：一律不更新）"
[ "$(fmcount "${CARD_REL}" reviewed_at)" = "1" ] || die "Agent 写入改动了 reviewed_at 的行数"
grep -Fq 'Agent 追加的一条依据' "${VAULT}/${CARD_REL}" || die "Agent 写入没落盘，本步失去判据"
# 内容时间戳的两条路径无差别（授权合同 §2.1 矩阵第 8 行 / I-…-009）：Agent 自动路径
# 追加正文也是一次**实际写入**，因此既有 updated_at 必须就地刷新成本次写入时刻
# （恰一行、严格变新）。「过目 / 内容」两条时间轴由此各归各位：reviewed_at 一字未动，
# updated_at 跟着内容走 —— 复核闭环不再静默失效。
[ "$(fmcount "${CARD_REL}" updated_at)" = "1" ] || die "updated_at 必须恰一行（整行覆盖，不长出第二行）"
CARD_UPDATED_AFTER_AGENT="$(fmvalue_of "${CARD_REL}" updated_at)"
stamp_gt "${CARD_UPDATED_AFTER_AGENT}" "$(strip_fm_value "${CARD_UPDATED_BEFORE}")" ||
  die "Agent 写入必须刷新既有 updated_at（旧 ${CARD_UPDATED_BEFORE} → 实得 ${CARD_UPDATED_AFTER_AGENT}）"
ok "B-4 Agent 写入只追加正文：reviewed_at 一字未动，updated_at 随实际写入刷新"

step "B-5 Agent 路径**直接**提 mark_reviewed op → 退 2、零写入（矩阵 #7 的 P-A 是 🔴）"
CARD_HASH_BEFORE="$(rawhash "${CARD_REL}")"
LOG_BEFORE="$(logcount)"
cat >"${WORK}/plan_agent_mark.json" <<JSON
{
  "plan_version": 1, "verb": "process", "domain": "ai-infra",
  "reason": "Agent 自动路径尝试标记已过目（必须被矩阵拦下）",
  "requirement_ids": ["EG-CFM-06"], "base": {},
  "ops": [ { "op": "mark_reviewed", "target": "${CARD}" } ]
}
JSON
CODE="$(eg_code apply --plan "${WORK}/plan_agent_mark.json" --json)"
[ "${CODE}" = "2" ] || { cat "${WORK}/out.txt"; die "Agent 路径的 mark_reviewed 退出码 = ${CODE}，期望 2"; }
[ "$(rawhash "${CARD_REL}")" = "${CARD_HASH_BEFORE}" ] || die "被拦下的 op 必须零写入"
[ "$(logcount)" = "${LOG_BEFORE}" ] || die "被拦下的 op 不得产生 commit"
ok "B-5 Agent 路径写不了 reviewed_at：退 2、字节不变、零 commit"

step "B-6 用户在编辑器里改卡并把 updated_at 推新 → 该卡**重新**计入清单，reviewed_at 仍一字未动"
awk '{ if ($1 == "updated_at:") print "updated_at: '\''2026-09-20T10:00:00+08:00'\''"; else print }' \
  "${VAULT}/${CARD_REL}" >"${WORK}/card.md" && cp "${WORK}/card.md" "${VAULT}/${CARD_REL}"
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "edit: 用户在编辑器里改了这张卡"
[ "$(fmline "${CARD_REL}" reviewed_at)" = "${CARD_REVIEWED_LINE}" ] ||
  die "前置不成立：外部编辑不该动 reviewed_at 行"
unreviewed_json "${WORK}/after_agent.json"
ids "${WORK}/after_agent.json" | grep -qx "${CARD}" ||
  { cat "${WORK}/after_agent.json"; die "updated_at 推新后该卡必须重新计入未过目清单"; }
ok "B-6 updated_at > reviewed_at 重新成立：该卡回到清单，reviewed_at 一字未动"

# ================================================================ C 组
step "C 无 reviewed_at 的产物计入：笔记全程无该键，却始终在清单里，且 JSON 里如实为空串"
grep -q '^reviewed_at:' "${VAULT}/${NOTE_REL}" && die "笔记不得被任何动作回填 reviewed_at"
unreviewed_json "${WORK}/c.json"
ids "${WORK}/c.json" | grep -qx "${NOTE}" || die "无 reviewed_at 的笔记必须计入清单"
NOTE_FIELD="$(jq -re --arg id "${NOTE}" '.data.unreviewed[] | select(.id==$id) | .reviewed_at' "${WORK}/c.json")"
[ "${NOTE_FIELD}" = "" ] || die "笔记的 reviewed_at 字段 =「${NOTE_FIELD}」，期望空串（缺省不回填）"
[ "$(jq -re --arg id "${NOTE}" '.data.unreviewed[] | select(.id==$id) | .unreviewed' "${WORK}/c.json")" = "true" ] ||
  die "清单行的 unreviewed 判定值必须为 true（T-…-043 读的就是这个值）"
grep -Fq '(无)' <(eg unreviewed) ||
  { eg unreviewed; die "人类可读渲染应把缺省显示为 (无)，不编造时刻"; }
ok "C 缺省即缺省：笔记计入清单，reviewed_at 字段为空串、判定值为 true"

# ================================================================ D 组
step "D 零副作用：eg unreviewed 前后 porcelain = 0、commit 数不变、字节不变、无催促文案"
LOG_BEFORE="$(logcount)"
CARD_HASH_BEFORE="$(rawhash "${CARD_REL}")"
NOTE_HASH_BEFORE="$(rawhash "${NOTE_REL}")"
unreviewed_json "${WORK}/d1.json"
[ "$(porcelain_count)" = "0" ] || { gitv status --porcelain; die "eg unreviewed 之后 porcelain 计数必须为 0"; }
[ "$(logcount)" = "${LOG_BEFORE}" ] || die "只读命令不得产生 commit"
[ "$(rawhash "${CARD_REL}")" = "${CARD_HASH_BEFORE}" ] || die "eg unreviewed 改了卡的字节"
[ "$(rawhash "${NOTE_REL}")" = "${NOTE_HASH_BEFORE}" ] || die "eg unreviewed 改了笔记的字节"
# 催促类文案零命中（字面量拼接构造，避免源码 / 脚本守卫按完整字面量误伤）。
BANNED="$(printf '请%s\\|%s办' '尽快' '催')"
[ "$(grep -c "${BANNED}" "${WORK}/d1.json" || true)" = "0" ] ||
  { cat "${WORK}/d1.json"; die "清单输出出现催促类文案：清单只陈述事实"; }
[ "$(fmline "${CARD_REL}" status)" = "${CARD_STATUS_BEFORE}" ] || die "eg unreviewed 改了 status"
# 两次执行输出逐字相同（确定性；同时再证一次零副作用）。
unreviewed_json "${WORK}/d2.json"
cmp -s "${WORK}/d1.json" "${WORK}/d2.json" || { diff "${WORK}/d1.json" "${WORK}/d2.json" || true; die "两次执行输出不同"; }
ok "D eg unreviewed 只读、确定性、无催促文案"

# ================================================================ 越界与参数反证
step "越界反证：不启用退出码 6、参数缺失退 1 且零写入、领域未登记退 1"
CARD_HASH_BEFORE="$(rawhash "${CARD_REL}")"
LOG_BEFORE="$(logcount)"
[ "$(eg_code mark-reviewed)" = "1" ] || die "缺 --target 的 mark-reviewed 必须退 1"
[ "$(eg_code mark-reviewed --target k-20990101-absent)" = "2" ] || die "目标解析不到必须退 2"
[ "$(eg_code unreviewed --domain not-registered --json)" = "1" ] || die "领域未登记必须退 1"
[ "$(eg_code unreviewed --since 2026/09/01 --json)" = "1" ] || die "--since 形态非法必须退 1"
[ "$(eg_code unreviewed --since 2026-09-05 --until 2026-09-01 --json)" = "1" ] || die "--since 晚于 --until 必须退 1"
[ "$(rawhash "${CARD_REL}")" = "${CARD_HASH_BEFORE}" ] || die "失败路径必须零写入"
[ "$(logcount)" = "${LOG_BEFORE}" ] || die "失败路径必须零 commit"
[ "$(porcelain_count)" = "0" ] || die "失败路径之后工作区必须干净"
# mark-reviewed 不在退出码 6 的白名单里（白名单恰 proposal approve 与 delete 两条）：
# 不带任何确认参数也必须直接退 0，绝不返回需确认。
[ "$(eg_code mark-reviewed --target "${NOTE}" --json)" = "0" ] || die "mark-reviewed 不得要求二次确认"
[ "$(fmcount "${NOTE_REL}" reviewed_at)" = "1" ] || die "笔记的 reviewed_at 应恰一行（四类产物同构）"
[ "$(porcelain_count)" = "0" ] || die "标记笔记之后工作区必须干净"
unreviewed_json "${WORK}/final.json"
ids "${WORK}/final.json" | grep -qx "${NOTE}" && die "刚标记过目的笔记不得留在清单里"
ok "越界项全部为负；四类产物同构可标记，且不产生退出码 6"

printf '\n=== 全部 %d 步通过：reviewed_at 单键写入 + eg unreviewed 只读筛选端到端可用 ===\n' "${STEP}"
exit 0

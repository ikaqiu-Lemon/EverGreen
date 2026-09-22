#!/usr/bin/env bash
# M3 八个 op 与诊断码端到端脚本（T-evergreen.s1_main_flow-158614-037）。
#
# 判据来源：提案合同 `2026-10-10-m3-proposal-state-contract.md`
#   §8.1 八个 op 表（字段与授权）/ §8.2.1 E7–E10 / §8.2.2 W9–W12
#   §8.2.3 W7 自 S2 起升 error、W3 起判定 / §8.2.4 编号占用总览
#   §8.3 `skipped[].kind` 恰两值（块级冲突复用 file_changed / content_hash_mismatch，
#        **不新造** block_conflict）
# 以及 `2026-10-13-m3-prestart-adjudication.md` §7 的 A-23 / A-24 裁决
#   （A-24：remove_relation 物理移除 relations[] 条目，不留墓碑），
# 与 `2026-10-10-m3-user-authorization-contract.md` §9 的 A-14 / A-15
#   （状态类 op 窄口径 = 五个；另三个缺 initiator=user 仍是 warning）。
#
# 四组断言（task deliverable 逐字要求）：
#   ① **8 个 op 逐个可执行**：deprecate / restore / set_replaced_by / delete / undelete /
#      mark_reviewed 六个走校验链后**本次零字节改动、零 commit**（其中四个已落盘的 op 在
#      base 未覆盖时退 3 并 skipped，另两个退 0；口径重钉见下方 T-…-051 说明），replace_block 与
#      remove_relation 真写盘（前者只动当前有效自检块、历史块逐字不变；后者按 A-24 物理
#      移除），未知 op 仍 error 整条不执行；
#   ② **E7–E10 各触发一次**：四条逐条退 2 + vault 逐字节不变；
#   ③ **W9–W12 各触发一次**：四条逐条退 0，其中 W10 / W11 断言零写入 + 不产生空 commit；
#      并给 W7 升 error 的反证（五个状态类 op 缺 initiator=user → 退 2；A-15 窄口径的
#      另三个 op 缺 initiator=user → warning、退 0）；
#   ④ **诊断码集合闭合的四组 grep**：全库码集合恰 23 值且 ⊆ E1..E10 ∪ W1..W12 ∪ {I1}；
#      越界编号 0 命中；`skipped[].kind` 恰两值（SkipReason 恰 3 个常量、非空取值恰两个）
#      且 block_conflict / block_hash_changed 在 internal/ 与报告 JSON 全文均 0；
#      未知归属 op（set_tags / reprocess_note / save_review）0 命中 + A-24 留痕 ≥ 1。
#
# 为什么六个 op「退 0 且零写入」而不是落盘：本 task 只交付 plan 层的 op 字段与校验发码，
# deprecate / restore / set_replaced_by 的 frontmatter 落盘属 T-…-039、逻辑删除十一步
# 时序属 T-…-041、mark_reviewed 的 reviewed_at 属 T-…-042；脚本因此断言它们
# **通过校验链 + 本次零字节改动**，而不是断言它们改了文件。
#
# `eg context`（第 16 步只读复核）的加工对象**必填**（M1 合同 §1.4：`--source` 与
# `--note` 二选一，缺参数 = 用法错误退 1）：脚本先 `eg capture` 落一篇原文拿真 ID 再调用，
# 并顺手把「缺参数仍退 1」当成 M1 契约的不回归断言。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/ops-diagnostics/ops_diagnostics.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-ops.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
PLAN="${WORK}/plan.json"

KDIR_REL='domains/ai-infra/knowledge'
CARD_A="k-20260901-attention"   # active，带 limits 关系 + 两个自检块
CARD_B="k-20260815-rnn"         # active
CARD_C="k-20260902-old"         # deprecated
CARD_D="k-20260903-gone"        # 已逻辑删除（deleted_at 非空）
# 当前有效自检块的逐字文本：base_block_hash = sha256(规范化块文本)[:16]，
# 规范化 = 统一 LF + 去行尾空白 + 去块尾空行，故单行块的规范化结果就是不带换行的该行。
CUR_BLOCK='- 当前有效块：多头注意力的头数如何选？'
OLD_BLOCK='- 历史块：为什么需要缩放？'

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}";  eg_staged_cleanup; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }
gitv() { git -C "${VAULT}" "$@"; }
snapshot() { find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort >"$1"; }
commits() { gitv log --oneline | wc -l | tr -d ' '; }
filehash() { printf 'sha256:%s' "$(sha256sum "${VAULT}/$1" | cut -d' ' -f1)"; }
blockhash() { printf '%s' "$1" | sha256sum | cut -c1-16; }
# apply：plan JSON 从 stdin 读入，落盘成文件后交给 eg apply --json，回显退出码。
# 本脚本模拟「用户在命令行上敲命令」，故一律带 --user-request：
# plan 里的 initiator: user 只有配上它才构成用户显式路径 P-U（反伪造条款 N-1）。
apply() { cat >"${PLAN}"; eg_code apply --plan "${PLAN}" --json --user-request; }
has_code() { grep -Fq "\"code\":\"$1\"" "${WORK}/out.txt"; }
want_code() { if ! has_code "$1"; then cat "${WORK}/out.txt"; die "$2：报告里缺 $1"; fi; }
deny_code() { if has_code "$1"; then cat "${WORK}/out.txt"; die "$2：报告里不该有 $1"; fi; }
unchanged() { snapshot "${WORK}/now.txt"; diff -u "$1" "${WORK}/now.txt" >"${WORK}/diff.txt" ||
  { cat "${WORK}/diff.txt"; die "$2：vault 内 .md 发生变化"; }; }

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 语料
step "eg init 建库 + 收录一篇原文 + 四张卡 + 五个提案（提案子命令属 T-…-040，用夹具直落）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
printf '注意力机制的正文占位，供 M3 op 与诊断码用例使用。\n' >"${WORK}/body.txt"
[ "$(eg_code capture --url https://example.com/attention --title '注意力机制入门' \
  --body-file "${WORK}/body.txt" --reason 'T-037 语料' --domain ai-infra \
  --captured-at 2026-09-01T09:00:00+08:00 --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "capture 失败"; }
SRC="$(sed 's/.*"source_id":"\([^"]*\)".*/\1/' "${WORK}/out.txt")"
[ -n "${SRC}" ] || die "capture 未返回 source_id"

mkdir -p "${VAULT}/${KDIR_REL}" "${VAULT}/proposals"
cat >"${VAULT}/${KDIR_REL}/${CARD_A}.md" <<CARD_A_EOF
---
id: ${CARD_A}
title: 注意力机制
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
tags:
  - 注意力
relations:
  - type: limits
    target: ${CARD_B}
    reason: 限定了原结论的适用序列长度
---

# 注意力机制

## 知识内容

正文占位。

## 用户补充

我自己的理解：先看 QKV。

## 理解自检

${OLD_BLOCK}

${CUR_BLOCK}
CARD_A_EOF
cat >"${VAULT}/${KDIR_REL}/${CARD_B}.md" <<CARD_B_EOF
---
id: ${CARD_B}
title: 循环网络的注意力局限
status: active
created_at: '2026-08-15'
updated_at: '2026-08-15T10:00:00+08:00'
---

# 循环网络的注意力局限

## 知识内容

正文占位。
CARD_B_EOF
cat >"${VAULT}/${KDIR_REL}/${CARD_C}.md" <<CARD_C_EOF
---
id: ${CARD_C}
title: 注意力旧卡
status: deprecated
created_at: '2026-09-02'
updated_at: '2026-09-02T10:00:00+08:00'
---

# 注意力旧卡

## 知识内容

正文占位。
CARD_C_EOF
cat >"${VAULT}/${KDIR_REL}/${CARD_D}.md" <<CARD_D_EOF
---
id: ${CARD_D}
title: 注意力已删卡
status: active
created_at: '2026-09-03'
updated_at: '2026-09-03T10:00:00+08:00'
deleted_at: '2026-09-03T12:00:00+08:00'
---

# 注意力已删卡

## 知识内容

正文占位。
CARD_D_EOF

# 五个提案夹具：001 完整已批准（供 delete 成功路径）；002/003/004 分别踩 E7/E8/E9；
# 005 已批准但十项必备有缺项（供 W9）。正文恰 7 个 H2（合同 §7.3）。
proposal() { # $1 id / $2 status / $3 decision.result / $4 decision.reason
            # $5 decision.superseded_by / $6 execution.status / $7 空正文分区名（可空）
  local pid="$1" st="$2" res="$3" rsn="$4" sup="$5" exec_st="$6" blank="${7:-}"
  {
    printf -- '---\nid: %s\ntype: logical_delete\nstatus: %s\n' "${pid}" "${st}"
    printf "created_at: '2026-07-01'\n"
    printf 'targets:\n  - %s\n' "${CARD_C}"
    printf 'impact:\n  exits_default_view:\n    - %s\n  cards_losing_support: []\n' "${CARD_C}"
    printf '  affected_material_rels: 0\n  affected_relations: 1\n  stale_reviews: []\n'
    printf "decision:\n  result: '%s'\n  reason: '%s'\n  superseded_by: '%s'\n" "${res}" "${rsn}" "${sup}"
    printf "execution:\n  status: %s\n  attempted_at: ''\n  reason: ''\n  git_commit: ''\n" "${exec_st}"
    printf '  written_paths: []\n  unwritten_paths: []\n'
    printf -- '---\n\n# 提案：逻辑删除一张过时卡\n'
    for sec in 推荐修改 理由与证据 '影响的文件、领域与关系' 执行后状态 不执行的影响 替代方案 可应用内容; do
      if [ "${sec}" = "${blank}" ]; then
        printf '\n## %s\n\n' "${sec}"
      else
        printf '\n## %s\n\n%s 的正文占位。\n' "${sec}" "${sec}"
      fi
    done
  } >"${VAULT}/proposals/${pid}.md"
}
proposal p-20260701-001 approved   approved '用户已批准删除' ''  not_started
proposal p-20260701-002 superseded superseded '已被新提案取代' '' not_started
proposal p-20260701-003 pending    approved ''               ''  not_started
proposal p-20260701-004 pending    ''       ''               ''  succeeded
proposal p-20260701-005 approved   approved '用户已批准删除' ''  not_started 替代方案

gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m3 op 与诊断码语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
BASE_COMMITS="$(commits)"
snapshot "${WORK}/before.txt"
ok "库就绪：4 张卡（active / active / deprecated / 已删）+ 5 个提案 + 原文 ${SRC}；已取基线快照"

# ---------------------------------------------------------------- 2. ① 六个零写入 op
step "① 六个 op 逐个通过校验链且本次零写入：deprecate / restore / set_replaced_by / delete / undelete / mark_reviewed"
# **事实重钉（T-…-039 落盘之后）**：deprecate / restore / set_replaced_by 的 frontmatter
# 落盘已经实现，因此它们不再是「校验通过后什么都不做」。本组的 plan 一律 `base: {}`，
# 于是 B3 在写前比对时判 base 未覆盖 → W6 + skipped（kind=file_changed）→ **退 3**。
# 结论不变的是本组真正要证的两件事：**零字节改动 + 零 commit**（下方 unchanged 与 commits 断言）。
# 另三个（delete / undelete / mark_reviewed）的落盘归 T-…-041 / 042，仍是退 0 的零写入形态。
#
# **事实重钉二（2026-09-06 · M4 · T-…-051，A-33 裁决落地）**：`mark_reviewed` 由 M3 期的
# pendingWrite（校验通过后不落盘）转为 **真实写入 op**（`internal/plan/execute_m4.go` 接线，
# 只写 `reviewed_at` 一个键），因此它与上面三个已落盘 op **同形态**：本组 `base: {}` → B3
# 写前比对判 base 未覆盖 → W6 + skipped（kind=file_changed）→ **退 3**。
# 故此处期望值按实测由 0 重钉为 **3**（`run_op mark_reviewed 3`）。
# **只改期望值这一格（判据形态），判据本体一格未放宽**：本组真正要证的
# 「零字节改动 + 零 commit」两条断言（下方 unchanged / BASE_COMMITS）逐字未动；
# `run_op` 在 want=3 时**额外**要求产出 W6 且跳过 kind 恰 file_changed（比 want=0 更严）；
# R2 侧「写入面封闭、只写 `reviewed_at`」由 T-…-051 的 `m4_r2_reviewed_backfill.sh`
# 与 `TestR2OnlyReviewedAtWritten` / `TestR2WrittenKeySetClosed` 正面守卫，未在此放宽。
run_op() { # $1 说明 / $2 期望退出码（默认 0）/ stdin plan
  local name="$1" want="${2:-0}" c
  c="$(apply)"
  [ "${c}" = "${want}" ] || { cat "${WORK}/out.txt"; die "${name} 应退 ${want}，实退 ${c}"; }
  deny_code E7 "${name}"; deny_code W7 "${name}"
  if [ "${want}" = "3" ]; then
    grep -Fq '"W6"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "${name} 应产出 W6（base 未覆盖）"; }
    grep -Fq '"file_changed"' "${WORK}/out.txt" ||
      { cat "${WORK}/out.txt"; die "${name} 的跳过 kind 应是 file_changed"; }
  fi
}
run_op deprecate 3 <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"用户显式失效一张过时卡","base":{},
 "ops":[{"op":"deprecate","target":"${CARD_A}","reason":"结论已被新综述取代","initiator":"user"}]}
JSON
run_op restore 3 <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"恢复一张误失效卡","base":{},
 "ops":[{"op":"restore","target":"${CARD_C}","reason":"失效判断有误，恢复","initiator":"user"}]}
JSON
run_op set_replaced_by 3 <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"给失效卡挂替代指针","base":{},
 "ops":[{"op":"set_replaced_by","target":"${CARD_C}","initiator":"user","reason":"新卡覆盖旧结论",
         "replaced_by":{"target":"${CARD_A}","reason":"新综述覆盖了旧结论"}}]}
JSON
run_op delete <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"按已批准提案删除","base":{},
 "ops":[{"op":"delete","target":"${CARD_C}","reason":"内容重复且无引用","initiator":"user",
         "proposal":"p-20260701-001"}]}
JSON
run_op undelete <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"撤销一次逻辑删除","base":{},
 "ops":[{"op":"undelete","target":"${CARD_D}","reason":"误删恢复","initiator":"user"}]}
JSON
run_op mark_reviewed 3 <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"季度复核","base":{},
 "ops":[{"op":"mark_reviewed","target":"${CARD_A}","reason":"季度复核完成","initiator":"user"}]}
JSON
unchanged "${WORK}/before.txt" "六个零写入 op"
[ "${BASE_COMMITS}" = "$(commits)" ] || die "六个零写入 op 不得产生 commit"
ok "① 六个 op 逐条如实发码（四个已落盘的在 base 未覆盖时退 3 并跳过，另两个退 0），全程零字节改动、零 commit"

# ---------------------------------------------------------------- 3. ① replace_block
step "① replace_block 只动当前有效自检块：历史块逐字不变，且必带 base_block_hash"
BH="$(blockhash "${CUR_BLOCK}")"
[ "$(printf '%s' "${BH}" | wc -c | tr -d ' ')" = "16" ] || die "base_block_hash 必须是 16 位十六进制"
NEW_BLOCK='- 当前有效块：多头注意力的头数与序列长度如何权衡？'
C="$(apply <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"更新当前有效自检问题块",
 "base":{"${KDIR_REL}/${CARD_A}.md":"$(filehash "${KDIR_REL}/${CARD_A}.md")"},
 "ops":[{"op":"replace_block","target":"${CARD_A}","section":"理解自检","initiator":"user",
         "reason":"自检问题升级","base_block_hash":"${BH}","block":"${NEW_BLOCK}\n"}]}
JSON
)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "replace_block 应退 0，实退 ${C}"; }
grep -Fq -- "${NEW_BLOCK}" "${VAULT}/${KDIR_REL}/${CARD_A}.md" || die "新块未写入"
grep -Fq -- "${OLD_BLOCK}" "${VAULT}/${KDIR_REL}/${CARD_A}.md" || die "历史记录块必须逐字不变（只追加、永不改写）"
if grep -Fq -- "${CUR_BLOCK}" "${VAULT}/${KDIR_REL}/${CARD_A}.md"; then die "旧的当前有效块未被替换"; fi
grep -Fq '我自己的理解：先看 QKV。' "${VAULT}/${KDIR_REL}/${CARD_A}.md" || die "用户分区必须逐字保留（B2）"
[ "$((BASE_COMMITS + 1))" = "$(commits)" ] || die "replace_block 应恰产生 1 个 commit"
ok "① replace_block 写入成功：当前有效块被替换、历史块与用户分区逐字保留"

step "① replace_block 冲突复用封闭两值：kind=file_changed / cause=content_hash_mismatch（不新造第三值）"
snapshot "${WORK}/after_rb.txt"
AFTER_RB_COMMITS="$(commits)"
C="$(apply <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"用过期的 base_block_hash 复写",
 "base":{"${KDIR_REL}/${CARD_A}.md":"$(filehash "${KDIR_REL}/${CARD_A}.md")"},
 "ops":[{"op":"replace_block","target":"${CARD_A}","section":"理解自检","initiator":"user",
         "reason":"用陈旧凭据复写","base_block_hash":"${BH}","block":"- 当前有效块：应当被拒绝。\n"}]}
JSON
)"
[ "${C}" = "3" ] || { cat "${WORK}/out.txt"; die "块级冲突应退 3（部分跳过），实退 ${C}"; }
grep -Fq '"kind":"file_changed"' "${WORK}/out.txt" || die "冲突的 kind 必须是 file_changed"
grep -Fq '"cause":"content_hash_mismatch"' "${WORK}/out.txt" || die "冲突的 cause 必须是 content_hash_mismatch"
grep -Fq "${CARD_A}#理解自检#" "${WORK}/out.txt" || die "detail 必须带块 locator"
grep -Fq "${BH}" "${WORK}/out.txt" || die "detail 必须带期望的 base_block_hash"
for bad in block_conflict block_hash_changed; do
  if grep -Fq "${bad}" "${WORK}/out.txt"; then die "报告 JSON 出现禁止字面量 ${bad}"; fi
done
unchanged "${WORK}/after_rb.txt" "块级冲突"
[ "${AFTER_RB_COMMITS}" = "$(commits)" ] || die "块级冲突不得产生 commit"
ok "① 冲突落在 skipped{file_changed / content_hash_mismatch}，locator 与 hash 只在自由文本 detail 里"

# ---------------------------------------------------------------- 4. ① remove_relation
step "① remove_relation 按 A-24 物理移除 relations[] 条目（不留墓碑）"
C="$(apply <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"移除一条建错的关系",
 "base":{"${KDIR_REL}/${CARD_A}.md":"$(filehash "${KDIR_REL}/${CARD_A}.md")"},
 "ops":[{"op":"remove_relation","from":"${CARD_A}","type":"limits","target":"${CARD_B}",
         "reason":"关系建错","initiator":"user"}]}
JSON
)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "remove_relation 应退 0，实退 ${C}"; }
if grep -Fq "limits" "${VAULT}/${KDIR_REL}/${CARD_A}.md"; then die "关系必须被物理移除"; fi
for tomb in removed_at removed deleted tombstone; do
  if grep -Fq "${tomb}" "${VAULT}/${KDIR_REL}/${CARD_A}.md"; then die "A-24 要求不留墓碑，却出现 ${tomb}"; fi
done
[ "$((AFTER_RB_COMMITS + 1))" = "$(commits)" ] || die "remove_relation 应恰产生 1 个 commit"
snapshot "${WORK}/after_w.txt"
AFTER_W_COMMITS="$(commits)"
ok "① relations[] 条目已物理移除、无墓碑字段；8 个 op 至此逐个可执行"

# ---------------------------------------------------------------- 5. ① 未知 op
step "① 未知 op 仍报 error 且整条 plan 不执行（§4.5 开放集合口径）"
C="$(apply <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"混入一个未知 op","base":{},
 "ops":[{"op":"deprecate","target":"${CARD_A}","reason":"合法 op","initiator":"user"},
        {"op":"totally_unknown_op","target":"${CARD_A}","reason":"未知 op"}]}
JSON
)"
[ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "未知 op 应退 2，实退 ${C}"; }
unchanged "${WORK}/after_w.txt" "未知 op"
[ "${AFTER_W_COMMITS}" = "$(commits)" ] || die "未知 op 不得产生 commit"
ok "① 未知 op → 校验失败族退 2，同 plan 内的合法 op 一并不执行"

# ---------------------------------------------------------------- 6. ② E7–E10
step "② E7 / E8 / E9 / E10 各触发一次：逐条退 2 + vault 逐字节不变"
err_case() { # $1 期望码 / $2 说明 / stdin plan
  local want="$1" name="$2" c
  c="$(apply)"
  [ "${c}" = "2" ] || { cat "${WORK}/out.txt"; die "${name} 应退 2，实退 ${c}"; }
  want_code "${want}" "${name}"
  unchanged "${WORK}/after_w.txt" "${name}"
  [ "${AFTER_W_COMMITS}" = "$(commits)" ] || die "${name} 不得产生 commit"
}
err_case E7 'E7：superseded 但 decision.superseded_by 为空' <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"E7","base":{},
 "ops":[{"op":"delete","target":"${CARD_C}","reason":"重复卡","initiator":"user",
         "proposal":"p-20260701-002"}]}
JSON
err_case E8 'E8：decision.result 与 status 不一致（pending 时非空）' <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"E8","base":{},
 "ops":[{"op":"delete","target":"${CARD_C}","reason":"重复卡","initiator":"user",
         "proposal":"p-20260701-003"}]}
JSON
err_case E9 'E9：status × execution 命中不可达矩阵（pending × succeeded）' <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"E9","base":{},
 "ops":[{"op":"delete","target":"${CARD_C}","reason":"重复卡","initiator":"user",
         "proposal":"p-20260701-004"}]}
JSON
err_case E10 'E10：set_replaced_by 的目标已被逻辑删除' <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"E10","base":{},
 "ops":[{"op":"set_replaced_by","target":"${CARD_C}","initiator":"user","reason":"指向替代卡",
         "replaced_by":{"target":"${CARD_D}","reason":"已删卡不得作替代目标"}}]}
JSON
ok "② E7–E10 逐条退 2、零写入、零 commit"

# ---------------------------------------------------------------- 7. ③ W9–W12
step "③ W9 / W10 / W11 / W12 各触发一次：逐条退 0，不拦截其余判定"
# **事实重钉（T-…-039 落盘之后）**：W12 所在的 set_replaced_by 现在真的会写盘，
# 因此在 `base: {}` 下它的退出码来自 **B3 跳过**（W6 + file_changed → 退 3），
# **不是**来自 W12——W12 本身仍是 warning、仍不拦截。$3 因此显式声明期望退出码，
# 让「warning 不改退出码」与「B3 未覆盖就跳过」两件事各自可辨。
warn_case() { # $1 期望诊断码 / $2 说明 / $3 期望退出码（默认 0）/ stdin plan
  local want="$1" name="$2" wantcode="${3:-0}" c
  c="$(apply)"
  [ "${c}" = "${wantcode}" ] ||
    { cat "${WORK}/out.txt"; die "${name} 应退 ${wantcode}，实退 ${c}"; }
  want_code "${want}" "${name}"
}
warn_case W9 'W9：已批准提案十项必备有缺项（照常放行）' <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"W9","base":{},
 "ops":[{"op":"delete","target":"${CARD_C}","reason":"重复卡","initiator":"user",
         "proposal":"p-20260701-005"}]}
JSON
warn_case W10 'W10：remove_relation 未命中任何既有关系（幂等 no-op）' <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"W10","base":{},
 "ops":[{"op":"remove_relation","from":"${CARD_A}","type":"limits","target":"${CARD_B}",
         "reason":"关系已不存在","initiator":"user"}]}
JSON
warn_case W11 'W11：restore 目标已是 active（幂等 no-op）' <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"W11","base":{},
 "ops":[{"op":"restore","target":"${CARD_B}","reason":"目标已是 active","initiator":"user"}]}
JSON
warn_case W12 'W12：replaced_by 目标是 deprecated 且未删除（允许但提示）' 3 <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"W12","base":{},
 "ops":[{"op":"set_replaced_by","target":"${CARD_B}","initiator":"user","reason":"指向替代卡",
         "replaced_by":{"target":"${CARD_C}","reason":"旧卡暂作替代目标"}}]}
JSON
unchanged "${WORK}/after_w.txt" "W9–W12"
[ "${AFTER_W_COMMITS}" = "$(commits)" ] || die "W10 / W11 的幂等 no-op 不得产生空 commit"
ok "③ W9–W12 逐条在场；W12 的退 3 来自 B3 跳过（W6）而非 warning 本身；W10 / W11 零写入 + 不产生空 commit"

# ---------------------------------------------------------------- 8. ③ W7 升 error 反证
step "③ W7 升 error 反证：五个状态类 op 缺 initiator=user → 退 2、零写入"
w7_error() { # $1 op 名 / stdin plan
  local name="$1" c
  c="$(apply)"
  [ "${c}" = "2" ] || { cat "${WORK}/out.txt"; die "${name} 缺 initiator=user 应退 2，实退 ${c}"; }
  want_code W7 "${name} 缺 initiator"
  unchanged "${WORK}/after_w.txt" "${name} 缺 initiator"
}
w7_error deprecate <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"W7","base":{},
 "ops":[{"op":"deprecate","target":"${CARD_A}","reason":"缺 initiator"}]}
JSON
w7_error restore <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"W7","base":{},
 "ops":[{"op":"restore","target":"${CARD_C}","reason":"缺 initiator"}]}
JSON
w7_error set_replaced_by <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"W7","base":{},
 "ops":[{"op":"set_replaced_by","target":"${CARD_C}","reason":"缺 initiator",
         "replaced_by":{"target":"${CARD_A}","reason":"新卡覆盖旧结论"}}]}
JSON
w7_error delete <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"W7","base":{},
 "ops":[{"op":"delete","target":"${CARD_C}","reason":"缺 initiator","proposal":"p-20260701-001"}]}
JSON
w7_error undelete <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"W7","base":{},
 "ops":[{"op":"undelete","target":"${CARD_D}","reason":"缺 initiator"}]}
JSON
# 同族反证：delete 给了 initiator=user 但未引用提案，同样是 W7 的 error 形态。
w7_error 'delete 未引用提案' <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"W7","base":{},
 "ops":[{"op":"delete","target":"${CARD_C}","reason":"未引用提案","initiator":"user"}]}
JSON
[ "${AFTER_W_COMMITS}" = "$(commits)" ] || die "W7 拒绝路径不得产生 commit"
ok "③ 五个状态类 op 缺 initiator=user 与 delete 未引用已批准提案，逐条退 2、零写入"

step "③ A-15 窄口径：mark_reviewed / replace_block / remove_relation 缺 initiator=user 的 W7 仍是 warning"
# 分工（授权合同 §1 + §2）：W7 只管「授权表达完整吗」的分级，写权限矩阵管
# 「这条路径能不能写这一格」。因此三个 op 的 W7 恒为 warning，但退出码不同：
#   mark_reviewed  → 矩阵 #7  P-A 🔴 → E6 拦住退 2、零写入
#   remove_relation→ 矩阵 #11 P-A 🔴 → E6 拦住退 2、零写入
#   replace_block  → 矩阵 #17 两格 ✅ → 照常写入退 0
narrow() { # $1 op 名 / $2 期望退出码 / stdin plan
  local name="$1" want="$2" c
  c="$(apply)"
  [ "${c}" = "${want}" ] || { cat "${WORK}/out.txt"; die "${name} 缺 initiator 应退 ${want}，实退 ${c}"; }
  grep -q '"code":"W7","level":"warning"' "${WORK}/out.txt" ||
    { cat "${WORK}/out.txt"; die "${name} 的 W7 必须是 warning（A-15 窄口径）"; }
  if [ "${want}" != "0" ]; then
    grep -q '"code":"E6","level":"error"' "${WORK}/out.txt" ||
      { cat "${WORK}/out.txt"; die "${name} 退 ${want} 必须由写权限矩阵的 E6 拦下"; }
  fi
}
narrow mark_reviewed 2 <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"A-15","base":{},
 "ops":[{"op":"mark_reviewed","target":"${CARD_A}","reason":"复核完成"}]}
JSON
# replace_block 这条给足 base 且新块与当前有效块逐字相同：写回同样的字节，
# 于是既能走到真正的写路径，又不改任何字节（下一步的快照比对因此仍成立）。
narrow replace_block 0 <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"A-15",
 "base":{"${KDIR_REL}/${CARD_A}.md":"$(filehash "${KDIR_REL}/${CARD_A}.md")"},
 "ops":[{"op":"replace_block","target":"${CARD_A}","section":"理解自检",
         "base_block_hash":"$(blockhash "${NEW_BLOCK}")","block":"${NEW_BLOCK}\n"}]}
JSON
narrow remove_relation 2 <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"A-15","base":{},
 "ops":[{"op":"remove_relation","from":"${CARD_A}","type":"limits","target":"${CARD_B}",
         "reason":"缺 initiator"}]}
JSON
ok "③ 另三个 op 缺 initiator=user 的 W7 只是 warning；能不能写由矩阵 #7 / #11 / #17 各自决定"

# ---------------------------------------------------------------- 9. ④ 四组 grep
step "④ grep 组 1：全库诊断码集合恰 23 值且闭合在 E1..E10 ∪ W1..W12 ∪ {I1}"
# 重钉（M4 · T-…-049，只改形态不放宽本体）：对账合同 §3 把 E11–E14 / W13–W20 正式发放给
# internal/reconcile 的十二值 check 枚举，那批码在该包内是合同要求存在的事实。因此把「全库
# 一张表」重钉为「按包分域的两张表」：reconcile **之外**仍恰 23 值逐字相等（原判据一格不放宽），
# reconcile **之内**恰 12 值逐字相等（新增等号，是加严）。
# 2026-09-08 随 M5 · T-…-065 按同一先例再分一域（只改**形态**，本体一格不放宽）：
# M5 索引合同把 W23 / W24 发放给 `internal/index`（索引缺失 / 索引不可用），因此分域从两张表
# 变成三张表 —— 非 reconcile **且非 index** 仍恰 23 值逐字相等（原判据不放宽），
# reconcile 内恰 12 值，index 内恰 2 值（后两个都是**新增等号**，是加严不是放宽）。
# 2026-09-08 随 M5 · T-…-069 再按同一先例分第四域（只改**形态**，本体一格不放宽）：
# M5 检索合同把 **W25（结果被 --limit 截断）** 与 Q1–Q5 一并发放给读路径包 `internal/query`
# （W25 落在 `internal/query/page.go` 的分页截断处），这是 T-…-068 的合同事实，不是越界。
# 因此分域从三张表变成四张表 —— 非 reconcile / 非 index / **非 query** 仍恰 23 值逐字相等
# （原判据一格不放宽），并对 query 域新增一条**封闭双侧等号**：该包 (E|W|I|Q) 码集合恰
# {Q1,Q2,Q3,Q4,Q5,W25} 六值逐字相等（既挡住 query 域私造 E/W/I 码，也挡住 Q 号段扩张），
# 是新增等号，是加严不是放宽。
# ── C2a·M6 现态重钉（合同 §16.3 / §16.5 M6 新增恰 5 码 E15/E16/W26/W27/W28；沿用本脚本既有「按包分域」先例，
#    非放宽：base 域一格不放宽仍恰 23 值，另新增两条封闭双侧等号锁住 M6 两域，是加严）──
# M6 把恰 5 个新码发放给两个专属包：`internal/txn`（E15 写前复核 / E16 锁忙 / W26 恢复留痕 / W28 锁重试）
# 与 `internal/mdfile`（W27 块级合并冲突）。二者均**只**含 M6 新码、零 base 码（实测为准），故按 M4→reconcile、
# M5→index/query 的同一先例把这两个包从 base 域剔除，base 域历史事实（恰 23 值）逐字保留、一格不放宽。
# ── C2 · I-…-015 现态重钉（CLI 合同 §5 现态脚注；沿用同一「按域封闭」先例，**加严不放宽**）──
# 命令层补码把恰 9 个新码 E17–E25 发放给**一个文件** internal/cli/codes.go（其余文件零码字面量、
# 只引用常量名）。因此从 base 域剔除的粒度是**该单个文件**而非整个 internal/cli 目录 ——
# base 域历史事实（恰 23 值：E1–E10 / W1–W12 / I1）逐字保留、一格不放宽；并对该文件新增一条
# 封闭双侧等号，把命令层码集合逐字锁成 {E17…E25}（多一码 / 少一码都当场红）。
# ── C2 · plan 现态重钉（ChangePlan schema v2 · commit 4b36712 把 W21 发放给 `internal/plan`；沿用同一
#    「按域封闭」先例，**加严不放宽**）── `internal/plan/diagnostics.go` 同时是 base 码 E7–E10 / W9–W12
#    的定义处（与同包 diagnostic.go 合成 base 全集），无法按目录/文件从 base 域剔除；故改为**按号段**分域：
#    先对 plan 域新增一条封闭双侧等号，把该包铸造的**越 base（E11+ / W13+ / I2+）码集合**逐字锁成恰 {W21}
#    （write_note 结构覆盖诊断，认领全局唯一空号；多一码 / 少一码都当场红），再在 base 组把这唯一越界码按 comm
#    从全库集合里精确剔除 —— base 历史事实（恰 23 值：E1–E10 / W1–W12 / I1）逐字保留、一格不放宽。
grep -rhoE '"(E|W|I)[0-9]+"' "${REPO_ROOT}/internal/plan/" | tr -d '"' | sort -u |
  { grep -E '^(E1[1-9]|E[2-9][0-9]|W1[3-9]|W[2-9][0-9]|I[2-9])$' || true; } >"${WORK}/codes_plan_extra.txt"
printf 'W21\n' >"${WORK}/want_codes_plan_extra.txt"
diff -u "${WORK}/want_codes_plan_extra.txt" "${WORK}/codes_plan_extra.txt" ||
  die "internal/plan 越 base 号段码集合应恰 {W21}（ChangePlan write_note 结构覆盖，认领全局唯一空号）"
grep -rhoE '"(E|W|I)[0-9]+"' --exclude-dir=reconcile --exclude-dir=index --exclude-dir=query \
  --exclude-dir=txn --exclude-dir=mdfile --exclude=codes.go \
  "${REPO_ROOT}/internal/" | tr -d '"' | sort -u |
  comm -23 - "${WORK}/codes_plan_extra.txt" >"${WORK}/codes.txt"
{ for i in $(seq 1 10); do printf 'E%d\n' "${i}"; done
  for i in $(seq 1 12); do printf 'W%d\n' "${i}"; done
  printf 'I1\n'; } | sort -u >"${WORK}/want_codes.txt"
diff -u "${WORK}/want_codes.txt" "${WORK}/codes.txt" || die "诊断码集合与合同 §8.2.4 不逐字相等"
grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/cli/codes.go" | tr -d '"' | sort -u >"${WORK}/codes_cmd.txt"
printf 'E17\nE18\nE19\nE20\nE21\nE22\nE23\nE24\nE25\n' | sort -u >"${WORK}/want_codes_cmd.txt"
diff -u "${WORK}/want_codes_cmd.txt" "${WORK}/codes_cmd.txt" ||
  die "命令层码集合应恰 {E17…E25}（CLI 合同 §5 现态脚注 / I-…-015）"
[ "$(wc -l <"${WORK}/codes.txt" | tr -d ' ')" = "23" ] || die "诊断码不是恰 23 值"
# ── C2a·M6 现态重钉（合同 §9 A-56 strict 升级面；沿用「按包/按文件分域」先例，非放宽）──
# M6 · T-074 把 `eg check --strict` 的升级策略落在 `internal/reconcile/strict.go`（产品码零码字面量，
# 全用常量）＋ `strict_test.go`（反证 strict 升级面）。strict_test.go 里逐字出现 base 警告码 W1–W6，
# 那是「strict 下升为 error 的 base 码」的**引用**（W1/W2/W3/W4/W6 升级、W5 明确不升的负向反证），
# **不是** reconcile 自己铸造的新码。故按先例把 strict 特性文件从「reconcile 铸造码」域剔除——
# reconcile 铸造码历史事实随 A-62 从十二值扩为**十三值**：对账合同 §3 冻结面（E11–E14 + W13–W20）
# 逐字保留，A-62 正式修订该冻结、追加 R3·W29（opinion_unsupported_validated），是加严不是放宽。
grep -rhoE '"(E|W|I)[0-9]+"' --exclude='strict*.go' "${REPO_ROOT}/internal/reconcile/" |
  tr -d '"' | sort -u >"${WORK}/codes_m4.txt"
{ for i in $(seq 11 14); do printf 'E%d\n' "${i}"; done
  for i in $(seq 13 20); do printf 'W%d\n' "${i}"; done
  printf 'W29\n'; } | sort -u >"${WORK}/want_codes_m4.txt"
diff -u "${WORK}/want_codes_m4.txt" "${WORK}/codes_m4.txt" ||
  die "internal/reconcile 的 M4 码集合与对账合同 §3（含 A-62 修订 W29）不逐字相等"
[ "$(wc -l <"${WORK}/codes_m4.txt" | tr -d ' ')" = "13" ] || die "M4 诊断码不是恰 13 值（含 A-62 W29）"
# M6 现态新增等号（加严）：strict 特性文件引用的 base 升级码集合恰 {W1,W2,W3,W4,W5,W6} 六值逐字相等
# （既锁住 strict 升级面 W1/W2/W3/W4/W6 五条 + W5 负向反证，也挡住 strict 面私造 E/新 W 码或号段扩张）。
# 测试树外置（ADR-T1）：strict_test.go 的权威位在 tests/_staged/internal/reconcile/，
# 判据不放宽——扫描面 = 产品位 ∪ 测试权威位，集合仍必须恰 {W1..W6} 逐字相等。
grep -rhoE '"(E|W|I)[0-9]+"' --include='strict*.go' \
  "${REPO_ROOT}/internal/reconcile/" "${EG_STAGED_TESTS}/internal/reconcile/" |
  tr -d '"' | sort -u >"${WORK}/codes_m6_strict.txt"
printf 'W1\nW2\nW3\nW4\nW5\nW6\n' | sort -u >"${WORK}/want_codes_m6_strict.txt"
diff -u "${WORK}/want_codes_m6_strict.txt" "${WORK}/codes_m6_strict.txt" ||
  die "internal/reconcile strict 特性文件引用的 base 升级码集合与合同 §9 不逐字相等（应恰 W1–W6）"
[ "$(wc -l <"${WORK}/codes_m6_strict.txt" | tr -d ' ')" = "6" ] || die "strict 升级面引用码不是恰 6 值"
# M5：`internal/index` 恰 3 值（W22 索引陈旧 / W23 索引缺失 / W24 索引不可用）。
# 2026-09-08 随 M5 · T-…-066 阶段 B 精确重钉：W22 = index_stale 已按索引合同 §9 正式启用
# （陈旧判定 + 写后同步失败的降级提示），故等号从 2 值改为 3 值；
# Q5（检索降级）属 T-…-067：未落地即恒 0，等号会当场拦住提前出现。
grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/index/" |
  tr -d '"' | sort -u >"${WORK}/codes_m5.txt"
printf 'W22\nW23\nW24\n' >"${WORK}/want_codes_m5.txt"
diff -u "${WORK}/want_codes_m5.txt" "${WORK}/codes_m5.txt" ||
  die "internal/index 的 M5 码集合与索引合同 §9 不逐字相等（Q5 属 T-…-067，未落地即不许出现）"
# M5 第四域：`internal/query` 在 Storage v3 m8 后恰 6 值 —— Q1–Q5（检索诊断，
# Q5 = 降级读）与 W25（结果被 --limit 截断）。D-3 的 candidates 弃用 I1 已随字段删除。
grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/query/" |
  tr -d '"' | sort -u >"${WORK}/codes_m5_query.txt"
printf 'Q1\nQ2\nQ3\nQ4\nQ5\nW25\n' | sort -u >"${WORK}/want_codes_m5_query.txt"
diff -u "${WORK}/want_codes_m5_query.txt" "${WORK}/codes_m5_query.txt" ||
  die "internal/query 的码集合与检索/分页合同不逐字相等（应恰 Q1–Q5 + W25）"
[ "$(wc -l <"${WORK}/codes_m5_query.txt" | tr -d ' ')" = "6" ] || die "internal/query 码不是恰 6 值"
# 反向封闭：W25 只许落在 query 域（reconcile / index / 其余包内恒 0），
# 且 index 域不得出现任何 Q 码之外的越界（W22–W24 的等号已在上一格锁死）。
N="$( { grep -rn '"W25"' --exclude-dir=query "${REPO_ROOT}/internal/" || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "W25 出现在 internal/query 之外（${N} 处）"
# ── C2a·M6 现态重钉：M6 两域封闭双侧等号（加严，非放宽）──
# M6 域一：`internal/txn` 恰 4 值 —— E15（写前复核失败）/ E16（锁忙）/ W26（恢复留痕）/ W28（锁重试）。
# （W27 块级合并冲突落在 mdfile，不在 txn；双侧精确：多一码、少一码都当场红。）
grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/txn/" |
  tr -d '"' | sort -u >"${WORK}/codes_m6_txn.txt"
printf 'E15\nE16\nW26\nW28\n' | sort -u >"${WORK}/want_codes_m6_txn.txt"
diff -u "${WORK}/want_codes_m6_txn.txt" "${WORK}/codes_m6_txn.txt" ||
  die "internal/txn 的 M6 码集合与原子性合同 §12/§16.3 不逐字相等（应恰 E15/E16/W26/W28）"
[ "$(wc -l <"${WORK}/codes_m6_txn.txt" | tr -d ' ')" = "4" ] || die "internal/txn 码不是恰 4 值"
# M6 域二：`internal/mdfile` 恰 1 值 —— W27（块级合并冲突，合同 §7 块级安全合并）。
grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/mdfile/" |
  tr -d '"' | sort -u >"${WORK}/codes_m6_mdfile.txt"
printf 'W27\n' >"${WORK}/want_codes_m6_mdfile.txt"
diff -u "${WORK}/want_codes_m6_mdfile.txt" "${WORK}/codes_m6_mdfile.txt" ||
  die "internal/mdfile 的 M6 码集合与块级合并合同 §7 不逐字相等（应恰 W27）"
[ "$(wc -l <"${WORK}/codes_m6_mdfile.txt" | tr -d ' ')" = "1" ] || die "internal/mdfile 码不是恰 1 值"
ok "④-1 码集合八域等号：非 reconcile/index/query/txn/mdfile 且剔除 plan 越界码后恰 23 值（E1–E10 + W1–W12 + I1）/ reconcile 恰 13 值（E11–E14 + W13–W20 + A-62 W29）/ index 恰 3 值（W22–W24）/ query 恰 6 值（Q1–Q5 + W25，且 W25 域外恒 0）/ txn 恰 4 值（E15/E16/W26/W28）/ mdfile 恰 1 值（W27）/ cli/codes.go 恰 9 值（E17–E25）/ plan 越 base 恰 1 值（W21）"

step "④ grep 组 2：越界编号（reconcile 外 E11+ / W13+ / I2+；reconcile 内 E15+ / W21–W28 / W30+ / I2+）零命中"
# ── C2a·M6 现态重钉：M6 把 E15/E16 发放给 `internal/txn`（已在上一格由封闭等号逐字锁死为恰 {E15,E16,W26,W28}）。
#    沿用本格既有对 reconcile 的剔除先例，把 M6 两域（txn/mdfile）一并剔除——它们各自的封闭双侧等号已在 ④-1 锁住，
#    此处再计入即与那两条等号重复且互斥。base 越界判据（其余包 E11+/W13+/I2+ 恒 0）一格不放宽。
# ── C2 · I-…-015 现态重钉（同一先例、同一理由，**加严不放宽**）：命令层 9 码只落在单个文件
#    internal/cli/codes.go，且已在 ④-1 由封闭双侧等号逐字锁成 {E17…E25}；此处按**文件**粒度剔除
#    （不是整个 internal/cli 目录），internal/cli 其余文件的越界仍恒 0。
N="$( { grep -rnE '"(E1[1-9]|W1[3-9]|I[2-9])"' --exclude-dir=reconcile --exclude-dir=txn \
  --exclude-dir=mdfile --exclude=codes.go "${REPO_ROOT}/internal/" || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "出现越界编号（${N} 处）"
N="$( { grep -rnE '"(E1[5-9]|E[2-9][0-9]|W2[1-8]|W[3-9][0-9]|I[2-9])"' \
  "${REPO_ROOT}/internal/reconcile/" || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "internal/reconcile 出现 M5–M6 号段编号（W21–W28 / W30+，${N} 处；A-62 只解冻 W29）"
# 2026-09-07 随 M4 · T-…-055 阶段 4a 按实测重钉（只改判据**形态**，本体一格不放宽）：
# 阶段 1 在 `internal/plan/validate_m4.go` 留了**一行边界说明注释**（逐字写「顺序判定属 R6
# 检查器（`internal/reconcile`，阶段 2），本文件只做取值封闭校验，不自造第二套顺序」）——
# 那是划清包边界的**反证注释**，不是把 M4 能力搬进 `internal/plan`。判据本体是
# 「`internal/plan` 不得承担 M4–M6 的能力」，故照 T-050 / T-056 的先例拆成四格，
# 其中前三格是**新增的加严**（比原来的一条裸词 grep 更强）：
#   ① `internal/plan` 对 `internal/reconcile` 的 import 恰 0（真正的越界是包依赖）；
#   ② `internal/plan` **非注释行**出现 `reconcile` 字样恰 0（能力字样不得进代码行）；
#   ③ 裸词落点集合恰 `{internal/plan/validate_m4.go}` 且该文件内恰 1 处
#      （集合 + 行数双等号锁死，**不**设通配白名单：多写一行说明也要重新登记）；
#   ④ `flock` / `txn/` / `run.lock` / `FTS5` / `.index/` 五个词仍恒 0（逐字未动）。
N="$( { grep -rn 'internal/reconcile"' "${REPO_ROOT}/internal/plan/" || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "internal/plan 出现对 internal/reconcile 的 import（${N} 处）"
# ── C2·Schema v2 现态重钉（write_note_v2.go 的 opinionItem 在 I1 提示文案里写了一句用户可读引导
#    「悬空引用由 eg reconcile 的关系 / 结构检查负责检出」——那是**字符串字面量里的 UX 提示**，不是 import
#    更不是能力调用（① import==0 已锁死 plan 不依赖 reconcile）。故 ② 从「非注释行恒 0」重钉为
#    「write_note_v2.go 之外的非注释行 reconcile 恒 0；write_note_v2.go 内恰 1 处且必在双引号字符串里」，
#    本体（plan 不承担 reconcile 能力）一格不放宽）──
RECON_HINT="internal/plan/write_note_v2.go"
N="$( { { grep -rn 'reconcile' "${REPO_ROOT}/internal/plan/" || true; } |
  { grep -vE ':[0-9]+:[[:space:]]*//' || true; } |
  { grep -vF "${REPO_ROOT}/${RECON_HINT}:" || true; }; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "internal/plan（除 write_note_v2.go 的 UX 提示字符串外）非注释行出现 reconcile 字样（${N} 处）"
HINT_N="$( { grep -n 'reconcile' "${REPO_ROOT}/${RECON_HINT}" || true; } |
  { grep -vE ':[0-9]+:[[:space:]]*//' || true; } | wc -l | tr -d ' ')"
[ "${HINT_N}" = "1" ] || die "write_note_v2.go 非注释 reconcile 提及应恰 1 处（I1 提示文案），实得 ${HINT_N}"
{ grep -n 'reconcile' "${REPO_ROOT}/${RECON_HINT}" || true; } |
  { grep -vE ':[0-9]+:[[:space:]]*//' || true; } | grep -q '"' ||
  die "write_note_v2.go 的 reconcile 提及必须在双引号字符串里（UX 提示，非 import / 非能力调用）"
# ── C2a·M6 现态重钉（合同 §13 plan/reconcile 互不 import；§9/§17.1 写前 strict precheck 落 internal/plan/precheck.go）──
# M6 · T-074 新增 `internal/plan/precheck.go`（写前强校验入口）。它对 `reconcile` 的两处提及**全在注释**里
# （解释 severity 策略为何落零依赖的 model 而非 reconcile，反证 plan 与 reconcile 按 §13 互不 import）——
# 与 validate_m4.go 的边界说明注释同性质，不是能力搬迁、更不是 import 越界（①②两格已分别锁死）。
# 保留历史事实（validate_m4.go 恰 1 处）＋ 新增现态双侧锁（precheck.go 恰 2 处、落点集合恰两文件）。
# ── C2·Schema v2 现态重钉（commit 4b36712 / 97df9cd 的 opinion-lifecycle 收口在三个文件里新增了对
#    reconcile 的**注释 / UX 提示字符串**引用，全非 import、全非能力调用（①格 import==0 已锁死）：
#      · diagnostics.go 恰 1 处（W21 号段占用说明注释，点名 reconcile/r7_support.go 的预留出处）；
#      · strict_exempt.go 恰 2 处（strict 豁免表边界注释，反证 W1–W6 由本包发放、reconcile 侧看不到）；
#      · write_note_v2.go 恰 1 处（I1 提示文案里的 UX 引导字符串，②格已锁在双引号内）。
#    故落点集合从两文件扩为五文件，并对三个新文件各补一条「恰 N 处」双侧锁（多一处 / 挪窝都红）。
PLAN_RECON_SET="$( { grep -rl 'reconcile' "${REPO_ROOT}/internal/plan/" || true; } |
  sed "s#${REPO_ROOT}/##" | sort | tr '\n' ' ')"
[ "${PLAN_RECON_SET}" = "internal/plan/diagnostics.go internal/plan/precheck.go internal/plan/strict_exempt.go internal/plan/validate_m4.go internal/plan/write_note_v2.go " ] ||
  die "internal/plan 的 reconcile 裸词落点集合 = 「${PLAN_RECON_SET}」，应恰 {diagnostics.go, precheck.go, strict_exempt.go, validate_m4.go, write_note_v2.go}"
N="$( { grep -c 'reconcile' "${REPO_ROOT}/internal/plan/validate_m4.go" || true; } | tr -d ' ')"
[ "${N}" = "1" ] || die "validate_m4.go 内 reconcile 字样 ${N} 处，应恰 1（边界说明注释）"
N="$( { grep -c 'reconcile' "${REPO_ROOT}/internal/plan/precheck.go" || true; } | tr -d ' ')"
[ "${N}" = "2" ] || die "precheck.go 内 reconcile 字样 ${N} 处，应恰 2（§13 边界说明注释，全在注释、零 import）"
N="$( { grep -c 'reconcile' "${REPO_ROOT}/internal/plan/diagnostics.go" || true; } | tr -d ' ')"
[ "${N}" = "1" ] || die "diagnostics.go 内 reconcile 字样 ${N} 处，应恰 1（W21 号段占用说明注释）"
N="$( { grep -c 'reconcile' "${REPO_ROOT}/internal/plan/strict_exempt.go" || true; } | tr -d ' ')"
[ "${N}" = "2" ] || die "strict_exempt.go 内 reconcile 字样 ${N} 处，应恰 2（strict 豁免表边界注释，全在注释、零 import）"
N="$( { grep -c 'reconcile' "${REPO_ROOT}/internal/plan/write_note_v2.go" || true; } | tr -d ' ')"
[ "${N}" = "1" ] || die "write_note_v2.go 内 reconcile 字样 ${N} 处，应恰 1（I1 提示文案里的 UX 引导字符串）"
# ── C2a·M6 现态重钉（合同 §8 锁/§7 块级合并；沿用本格既有「注释 vs 非注释」判据形态，非放宽）──
# 历史本体一格不放宽：internal/plan **非注释行**出现 flock/txn//run.lock/FTS5/.index/ 恒 0
# （plan 层绝不**实现** M5–M6 能力）。M6 · T-073/M6 收口在两个 M3 期文件里各加了 1 行**边界说明注释**
# （replace_block.go 说明「锁与 E16 由 internal/txn 的 run.lock 承载、本 op 不启用退出码 5」；
#  m3_test.go 说明 W26/W28 由 internal/txn 产出）——是划清包边界的反证注释，不是把能力搬进 plan。
# 故新增现态双侧锁（加严）：非注释行恒 0 ＋ 注释落点集合恰 {m3_test.go, replace_block.go} 且各恰 1 处。
# 测试树外置（ADR-T1）后，internal/plan 的**测试**文件权威位在 tests/_staged/internal/plan/。
# 判据一格不放宽：扫描面 = 产品位 ∪ 测试权威位，落点集合仍必须恰两个文件、各恰 1 处注释。
PLAN_SCAN="${REPO_ROOT}/internal/plan/ ${EG_STAGED_TESTS}/internal/plan/"
# shellcheck disable=SC2086
N="$( { { grep -rnE 'flock|txn/|run\.lock|FTS5|\.index/' ${PLAN_SCAN} || true; } |
  { grep -vE ':[0-9]+:[[:space:]]*//' || true; }; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "internal/plan（含测试权威位）非注释行出现 M5–M6 能力字样（${N} 处）"
# shellcheck disable=SC2086
PLAN_CAP_SET="$( { grep -rlE 'flock|txn/|run\.lock|FTS5|\.index/' ${PLAN_SCAN} || true; } |
  sed "s#${REPO_ROOT}/##" | sort | tr '\n' ' ')"
[ "${PLAN_CAP_SET}" = "internal/plan/replace_block.go tests/_staged/internal/plan/m3_test.go " ] ||
  die "internal/plan 的 M5–M6 能力字样落点集合 = 「${PLAN_CAP_SET}」，应恰 {internal/plan/replace_block.go, tests/_staged/internal/plan/m3_test.go}（全在注释）"
for f in internal/plan/replace_block.go tests/_staged/internal/plan/m3_test.go; do
  N="$( { grep -cE 'flock|txn/|run\.lock|FTS5|\.index/' "${REPO_ROOT}/${f}" || true; } | tr -d ' ')"
  [ "${N}" = "1" ] || die "${f} 内 M5–M6 能力字样 ${N} 处，应恰 1（边界说明注释）"
done
ok "④-2 越界编号 0 命中；internal/plan 零 reconcile import + 非注释行仅 write_note_v2.go 的 1 处 UX 提示字符串（双引号内）+ 裸词落点恰 {validate_m4.go, precheck.go, write_note_v2.go} + 五个 M5–M6 词 0"

step "④ grep 组 3：skipped[].kind 恰两值（SkipReason 恰 3 常量 / 非空取值恰两个），block_conflict 零命中"
grep -rhoE '(SkipNone|SkipFileChanged|SkipUserBlockUnsafe) SkipReason = "[a-z_]*"' \
  "${REPO_ROOT}/internal/store/receipt.go" >"${WORK}/kinds.txt"
[ "$(wc -l <"${WORK}/kinds.txt" | tr -d ' ')" = "3" ] || die "SkipReason 常量不是恰 3 行"
sed 's/.*"\(.*\)"/\1/' "${WORK}/kinds.txt" | grep -v '^$' | sort -u >"${WORK}/kinds2.txt"
printf 'file_changed\nuser_block_unsafe\n' >"${WORK}/want_kinds.txt"
diff -u "${WORK}/want_kinds.txt" "${WORK}/kinds2.txt" || die "kind 非空取值不是恰 {file_changed, user_block_unsafe}"
N="$( { grep -rn "block_conflict\|block_hash_changed" "${REPO_ROOT}/internal/" || true; } |
  wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "internal/ 出现被禁字面量 block_conflict / block_hash_changed（${N} 处）"
(cd "$(eg_staged_root)" && go test ./internal/store -run TestCauseFor_ExactlyTwoKinds -count=1 \
  </dev/null >"${WORK}/t_kind.txt" 2>&1) || { tail -20 "${WORK}/t_kind.txt";
  die "TestCauseFor_ExactlyTwoKinds 未全绿"; }
ok "④-3 kind 恰两值闭集 + 被禁字面量 0 命中 + TestCauseFor_ExactlyTwoKinds 全绿"

step "④ grep 组 4：未知归属 op 零实现（set_tags / reprocess_note / save_review）+ A-24 留痕在册"
N="$( { grep -rn "set_tags\|reprocess_note\|save_review" "${REPO_ROOT}/internal/plan/" || true; } |
  { grep -v '_test\.go' || true; } | { grep -v 'A-18' || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "出现未知归属 op 的实现（${N} 处）"
N="$(grep -c "2026-10-13-m3-prestart-adjudication" "${REPO_ROOT}/internal/plan/ops_m3.go" || true)"
[ "${N}" -ge 1 ] || die "ops_m3.go 必须逐字留痕 A-24 裁决来源"
M3_OPS='deprecate restore set_replaced_by delete undelete mark_reviewed replace_block remove_relation'
for op in ${M3_OPS}; do
  grep -Fq "\"${op}\"" "${REPO_ROOT}/internal/plan/ops_m3.go" || die "ops_m3.go 缺 op ${op}"
done
ok "④-4 未知归属 op 0 命中；A-24 留痕 ${N} 处；8 个 op 名逐字在册"

# ---------------------------------------------------------------- 10. 只读复核
step "只读复核：提案仍不进知识检索、context 仍只给摘要（033 / 034 不回归）"
[ "$(eg_code search 注意力 --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg search 应退 0"; }
if grep -Fq 'p-20260701-001' "${WORK}/out.txt"; then die "search 命中提案"; fi
# `eg context` 的加工对象必填（M1 合同 §1.4：--source 与 --note 二选一，缺参数退 1）。
[ "$(eg_code context --json)" = "1" ] || die "eg context 缺 --source/--note 必须按用法错误退 1"
grep -Fq -- "--source 与 --note 二选一" "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "缺参数的用法错误文案与合同 §1.4 不一致"; }
CTX="$(eg_code context --source "${SRC}" --json)"
[ "${CTX}" = "0" ] || { cat "${WORK}/err.txt" "${WORK}/out.txt"; die "eg context --source 应退 0，实退 ${CTX}"; }
grep -Fq '"exit_code":0' "${WORK}/out.txt" || die "context 信封的 exit_code 必须为 0"
unchanged "${WORK}/after_w.txt" "只读复核"
[ -z "$(gitv status --porcelain)" ] || die "收口时工作区必须干净"
[ "${AFTER_W_COMMITS}" = "$(commits)" ] || die "只读命令不得产生 commit"
[ "$((BASE_COMMITS + 2))" = "$(commits)" ] || die "全程应恰 2 个写 commit（replace_block + remove_relation）"
ok "全程零意外副作用：恰 2 个写 commit，其余判定路径逐字节不变"

printf '\n=== ops_diagnostics.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

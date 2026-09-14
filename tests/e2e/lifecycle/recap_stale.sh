#!/usr/bin/env bash
# R6 综述失准标记的端到端脚本（T-evergreen.s1_main_flow-158614-055 阶段 3）。
#
# 判据来源：`docs/specs/2026-11-12-m4-reconcile-contract.md` §9（R6 全文：**M4 唯一触发条件**、
# `stale_reason` **封闭三值**与取值顺序、**幂等**、**不自动重算综述 / 不自动清除标记**、
# **综述专属两键**、A-34 指向）、§1.3 写口归属表第 3 行（R6 的写入经**内存 ChangePlan** →
# `internal/plan` → `internal/store`）、§3（check ↔ 诊断码单射：`recap_stale` ↔ `W19`）、
# §11（`skipped[kind=file_changed]` **不新增 kind**）；
# `docs/specs/2026-11-15-m4-prestart-adjudication.md` 的 **A-33**（R6 的写入 op = 新增
# `set_stale`）、**A-34**（R6 写这两键**不需要** `--user-request`，仍走 ChangePlan，矩阵不新增行）、
# **A-35**（R1 纳管与 R2 / R6 修复的写入合并为**恰一次** commit）；`milestones/M-004-m4.md`
# 完成判据 8 与风险 R-16 / R-18。
#
# 七段断言（task deliverable 与 Acceptance 逐字要求）：
#   ① 干净库（2 张卡 / 1 篇笔记 / 1 份原文 / **2 篇综述**，两篇综述的 `updated_at` 与各自
#      引用卡**逐字相等**）→ `recap_stale` **零命中**（合同 §9 的判据是「**晚于**」，
#      相等不算）、零写入零提交；
#   ② **事后更新引用卡**（用户手改卡 1 的正文并把 `updated_at` 改新）→ 只读检查产出
#      `recap_stale` **恰 1 条**（W19 / warning），RepairSpec 恰 1 条且 `keys` 恰
#      `{stale, stale_reason}`，理由落在封闭三值内；未被牵连的综述 2 **零命中**；
#   ③ 修复 + 纳管：综述被改的 frontmatter 键集合**恰** `{stale, stale_reason}`
#      （`git diff` 逐键复算 + 键级 diff 复算），`stale: true`、`stale_reason` ∈ 封闭三值，
#      **综述正文逐字节不变、行数不变**（未重算综述），其余对象字节零变化；
#   ④ **恰一次 commit**（A-35）：`git log --oneline | wc -l` 相对对账前**恰 +1**，
#      提交主题 verb 恰 `reconcile`；
#   ⑤ **幂等**：再跑一整轮 → 零 op、零写入、**零新增 commit**，而 `recap_stale` finding
#      **仍产出**（合同 §9 幂等条），既有标记也**未被清除**；
#   ⑥ **B3 不豁免**：让第二篇综述命中并注入**过期**观测 hash → 进
#      `skipped[kind=file_changed]`、目标字节零变化、对应 finding 仍在且注明已跳过；
#   ⑦ 源码级边界反证：修复桥零 `internal/store`、零 `SetStatus` / `deleted_at` /
#      `replaced_by` / `reviewed_at` 字样、经 `internal/plan` 组装 ChangePlan、A-34 下不带
#      `initiator`、本 task **零命令注册**、`content_hash` 前置未削弱、`kind` 不新增、
#      封闭三值恒 3、e2e 计数只增不减。
#
# 为什么用 `go test` 驱动而不是 `eg reconcile`：
#   `eg reconcile` 命令本体、信封与退出码属 T-…-058（`eg check` 属 T-…-059），本 task 明确
#   **零命令注册**，只交付「R6 只读判定 + 经 ChangePlan 的封闭两键写入」两项能力。脚本因此在
#   真实临时 vault 上用 `test/e2e/m4_r6_recap_stale_test.go` 这一层驱动壳调这两项能力
#   （同 `m4_r2_reviewed_backfill.sh` / `m4_r7_support.sh` 的先例：驱动壳未设
#   `EG_M4_R6_VAULT` 时直接 skip，`go test ./...` 行为一字不变），事实一律回读**文件字节 /
#   git 自己 / eg 自己的只读命令**与驱动壳落盘的 JSON，不看实现自报。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go / jq / python3）；
# **一切写操作只发生在 mktemp -d 目录内**；任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/lifecycle/recap_stale.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-r6.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
FACTS="${WORK}/facts.json"

KDIR_REL='domains/ai-infra/knowledge'
NDIR_REL='domains/ai-infra/notes'
RDIR_REL='domains/ai-infra/reviews'
CARD='k-20260901-attention'           # 综述 1 的引用卡：事后被用户手改 → R6 唯一触发条件成立
CARD2='k-20260902-rnn'                # 综述 2 的引用卡：第 6 步才手改（B3 那一步用）
NOTE='n-20260901-attention'
SRC='s-20260901-attention'
RECAP='r-20260901-attention'          # 命中综述（全程主角）
RECAP2='r-20260902-rnn'               # 前 5 步的对照物：零命中、字节一字不动
CARD_REL="${KDIR_REL}/${CARD}.md"
CARD2_REL="${KDIR_REL}/${CARD2}.md"
NOTE_REL="${NDIR_REL}/${NOTE}.md"
RECAP_REL="${RDIR_REL}/${RECAP}.md"
RECAP2_REL="${RDIR_REL}/${RECAP2}.md"

# 注入的对账时刻（固定值 → 全过程确定性，绝不读本机时钟）。
STAMP='2026-11-30T10:00:00+08:00'
STAMP2='2026-12-02T10:00:00+08:00'
# 两篇综述与两张卡的初始 `updated_at`：**逐字相等** → 造数完成时 R6 恒不命中。
AT1='2026-09-01T10:00:00+08:00'
AT2='2026-09-02T10:00:00+08:00'
# 「事后更新引用卡」后的两个时刻（严格晚于对应综述）。
BUMP1='2026-09-05T09:00:00+08:00'
BUMP2='2026-09-06T09:00:00+08:00'
# 封闭三值（真源在 internal/model；此处逐字复述用于取值校验）。
R1_UPDATED='引用卡已更新'
R2_DELETED='引用卡已逻辑删除'
R3_DEPRECATED='引用卡已失效'

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
commits() { gitv log --oneline | wc -l | tr -d ' '; }
porcelain() { gitv status --porcelain; }
porcelain_count() { porcelain | wc -l | tr -d ' '; }
tree_sum() { find "${VAULT}" -path "${VAULT}/.git" -prune -o -type f -print0 |
  xargs -0 sha256sum | sort; }
rawhash() { sha256sum "${VAULT}/$1" | cut -d' ' -f1; }
linecount() { wc -l <"${VAULT}/$1" | tr -d ' '; }
# fmline 取某个 frontmatter 顶层键所在的**整行**（含引号与空格）：逐字比对用。
fmline() { grep -m1 "^$2:" "${VAULT}/$1" || true; }
# seal <说明>：把造好的数据提交进 vault 自己的仓库，使「检查前工作区为空」成立。
seal() { gitv add -A >/dev/null && gitv -c user.name=e2e -c user.email=e2e@local \
  commit -q -m "e2e(r6): $1" >/dev/null; }
cnt0() { # cnt0 <说明> <grep 参数...>：期望 0 命中
  local name="$1"; shift
  local n; n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}

# drive <mode> [stamp] [tag]：在真实 vault 上跑驱动壳，事实落 ${FACTS}（另存带标签副本）。
drive() {
  local mode="$1" stamp="${2:-${STAMP}}" tag="${3:-$1}"
  rm -f "${FACTS}"
  ( cd "$(eg_staged_root)" &&
    EG_M4_R6_VAULT="${VAULT}" EG_M4_R6_MODE="${mode}" EG_M4_R6_OUT="${FACTS}" \
    EG_M4_R6_DOMAIN='ai-infra' EG_M4_R6_STAMP="${stamp}" \
    go test ./test/e2e/ -run TestM4R6RecapStaleHarness -count=1 -v </dev/null ) \
    >"${WORK}/drive-${tag}.log" 2>&1 ||
    { tail -30 "${WORK}/drive-${tag}.log"; die "驱动壳（${tag}）未全绿"; }
  grep -Fq -- "--- PASS: TestM4R6RecapStaleHarness" "${WORK}/drive-${tag}.log" ||
    { tail -30 "${WORK}/drive-${tag}.log"; die "驱动壳（${tag}）未 PASS（可能被 skip）"; }
  [ -s "${FACTS}" ] || die "驱动壳（${tag}）未落事实文件"
  cp "${FACTS}" "${WORK}/facts-${tag}.json"
}

fact_str() { jq -r ".$1 // \"\"" "${FACTS}"; }
fact_num() { jq -r ".$1 // 0" "${FACTS}"; }
fact_list() { jq -r "(.$1 // []) | join(\",\")" "${FACTS}"; }
fact_count() { grep -o -- "$1" "${FACTS}" | wc -l | tr -d ' '; }

# 「变化的 frontmatter 键 + 正文是否变化」的键级复算器：**反证**修复只动那两个键。
# 输出恰两行：`keys=<变化键的升序逗号串>` 与 `body=same|changed`。
FMDIFF="${WORK}/fmdiff.py"
cat >"${FMDIFF}" <<'PY'
import re
import sys


def split(path):
    """把 md 拆成 (顶层键 -> 该键的整段原文, 正文原文)。"""
    raw = open(path, encoding='utf-8').read()
    if not raw.startswith('---\n'):
        raise SystemExit('缺 frontmatter：' + path)
    end = raw.index('\n---\n', 3)
    fm, body = raw[4:end + 1], raw[end + 5:]
    keys, cur = {}, None
    for line in fm.splitlines(True):
        if re.match(r'^[A-Za-z_][A-Za-z0-9_]*:', line):
            cur = line.split(':', 1)[0]
            keys[cur] = line
            continue
        if cur is not None:
            keys[cur] += line
    return keys, body


a_keys, a_body = split(sys.argv[1])
b_keys, b_body = split(sys.argv[2])
changed = set(a_keys) ^ set(b_keys)
changed |= {k for k in set(a_keys) & set(b_keys) if a_keys[k] != b_keys[k]}
print('keys=' + ','.join(sorted(changed)))
print('body=' + ('same' if a_body == b_body else 'changed'))
PY
fmdiff() { python3 "${FMDIFF}" "$1" "$2"; }
# only_stale_two_keys <before 副本> <after 路径>：键差集恰两键且正文逐字节不变。
only_stale_two_keys() {
  local got; got="$(fmdiff "$1" "$2")"
  [ "$(printf '%s\n' "${got}" | sed -n 1p)" = "keys=stale,stale_reason" ] ||
    { printf '%s\n' "${got}"; die "$2 的 frontmatter 键差集应恰 {stale, stale_reason}"; }
  [ "$(printf '%s\n' "${got}" | sed -n 2p)" = "body=same" ] ||
    { printf '%s\n' "${got}"; die "$2 的正文被改动了（R6 只写两个键，**不重算综述**）"; }
}
# diff_keys <rel>：本次 commit 里该文件**变化了的** frontmatter 键名（升序去重）。
# 只看 +/- 行：上下文行（前导空格）不是改动。
diff_keys() { gitv diff HEAD~1 -- "$1" | grep -E '^[+-][a-z_]+:' |
  sed -E 's/^[+-]([a-z_]+):.*/\1/' | sort -u | paste -sd, -; }
# bearers：整库 frontmatter 里带失准两键之一的文件（相对路径升序）——**综述专属**的判据。
bearers() {
  ( cd "${VAULT}" && grep -rlE '^(stale|stale_reason):' --include='*.md' . 2>/dev/null |
    sed 's|^\./||' | sort ) || true
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
command -v jq >/dev/null || die "本脚本用 jq 读驱动壳事实"
command -v python3 >/dev/null || die "本脚本用 python3 做 frontmatter 键级复算"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 造数
step "建库并造数：2 张卡 + 1 篇笔记 + 1 份原文 + 2 篇综述（综述与引用卡的 updated_at 逐字相等）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}" "${VAULT}/${NDIR_REL}" "${VAULT}/${RDIR_REL}" "${VAULT}/sources"
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
updated_at: '${AT1}'
---

# 注意力机制材料笔记

## 材料提炼

提炼占位。
NOTE_EOF
cat >"${VAULT}/${CARD_REL}" <<CARD_EOF
---
id: ${CARD}
title: 注意力的计算代价
status: active
created_at: '2026-09-01'
updated_at: '${AT1}'
sources:
  - source: ${SRC}
    note: ${NOTE}
    rel: support
    reason: 该结论由这份材料支持
relations: []
---

# 注意力的计算代价

## 知识内容

注意力的计算量随序列长度平方增长。
CARD_EOF
cat >"${VAULT}/${CARD2_REL}" <<CARD2_EOF
---
id: ${CARD2}
title: RNN 的长序列衰减
status: active
created_at: '2026-09-02'
updated_at: '${AT2}'
sources:
  - source: ${SRC}
    note: ${NOTE}
    rel: support
    reason: 该结论由这份材料支持
relations: []
---

# RNN 的长序列衰减

## 知识内容

长序列上信息衰减明显。
CARD2_EOF
cat >"${VAULT}/${RECAP_REL}" <<RECAP_EOF
---
id: ${RECAP}
title: 注意力机制主题综述
status: active
created_at: '2026-09-01'
updated_at: '${AT1}'
generated_at: '${AT1}'
source_cards:
  - ${CARD}
---

## 综述

注意力是一种加权求和；本段正文一个字节都不许动。

## 依据

- 取材自知识卡 ${CARD}
RECAP_EOF
cat >"${VAULT}/${RECAP2_REL}" <<RECAP2_EOF
---
id: ${RECAP2}
title: RNN 主题综述
status: active
created_at: '2026-09-02'
updated_at: '${AT2}'
generated_at: '${AT2}'
source_cards:
  - ${CARD2}
---

## 综述

长序列衰减的成因综述；本段正文一个字节都不许动。
RECAP2_EOF
seal "R6 语料（2 篇综述与各自引用卡的 updated_at 逐字相等）"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "造数后应已入库（工作区为空）"; }
cnt0 '综述不得自带 stale 两键（本 task 首次写入）' -rnE '^(stale|stale_reason):' \
  "${VAULT}/${RECAP_REL}" "${VAULT}/${RECAP2_REL}"
[ "$(bearers | grep -c . || true)" = "0" ] || { bearers; die "造数后整库不该有文件带失准两键"; }
BASE_COMMITS="$(commits)"
RECAP_LINES_BEFORE="$(linecount "${RECAP_REL}")"
RECAP2_HASH_BEFORE="$(rawhash "${RECAP2_REL}")"
NOTE_HASH_BEFORE="$(rawhash "${NOTE_REL}")"
RECAP_STATUS_BEFORE="$(fmline "${RECAP_REL}" status)"
[ -n "${RECAP_STATUS_BEFORE}" ] || die "综述必须有 status 行，否则「维度不牵连」失去判据"
ok "vault 就绪：commit 数 ${BASE_COMMITS}、工作区为空、两篇综述均无失准两键（正文 ${RECAP_LINES_BEFORE} 行）"

# ---------------------------------------------------------------- 2. 反向行：相等不算晚于
step "未更新引用卡时跑只读检查：recap_stale 零命中（判据是「晚于」，相等不算）、零写入零提交"
CLEAN_SUM="$(tree_sum)"
drive probe "${STAMP}" clean
[ "$(fact_num sampled)" = "2" ] ||
  { cat "${FACTS}"; die "综述分区应采到 2 篇，实得 $(fact_num sampled)"; }
[ "$(fact_list sampled_paths)" = "${RECAP_REL},${RECAP2_REL}" ] ||
  { cat "${FACTS}"; die "采样路径应恰是那两篇综述（升序）"; }
[ "$(fact_num recap_stale)" = "0" ] ||
  { cat "${FACTS}"; die "两侧时刻相等时 recap_stale 必须零命中"; }
[ "$(fact_count '"check":"recap_stale"')" = "0" ] ||
  { cat "${FACTS}"; die "不得有 recap_stale finding"; }
[ "$(fact_count '"W19"')" = "0" ] || { cat "${FACTS}"; die "不得出现 W19"; }
[ "$(fact_list targets)" = "" ] || { cat "${FACTS}"; die "零命中时命中综述清单必须为空"; }
grep -Fq '"keys":["stale","stale_reason"]' "${FACTS}" ||
  { cat "${FACTS}"; die "封闭待写键集合恒 {stale, stale_reason}（零命中时也是这两个键）"; }
[ "$(fact_num exit_code)" = "0" ] || { cat "${FACTS}"; die "零 error 级 finding 时退出码应为 0"; }
[ "$(commits)" = "${BASE_COMMITS}" ] || die "只读检查改变了 commit 数"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "只读检查后工作区应仍为空"; }
[ "${CLEAN_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了"
ok "反向行：两侧 updated_at 相等 → 0 条 recap_stale / 0 个 W19；采到 2 篇综述；零写入零提交"

# ---------------------------------------------------------------- 3. 事后更新引用卡 → 恰 1 条
step "事后更新引用卡（用户手改卡 1：正文追加 + updated_at 改新）→ recap_stale 恰 1 条（W19 / warning）"
python3 - "${VAULT}/${CARD_REL}" "${AT1}" "${BUMP1}" <<'PY'
import sys
p, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(p, encoding='utf-8').read()
# 用户在编辑器里手改知识卡：正文追加一段，并把 updated_at 改新（综述因此失准）。
s = s.replace("updated_at: '%s'" % old, "updated_at: '%s'" % new, 1)
open(p, 'w', encoding='utf-8').write(s + '\n用户在编辑器里补写的一段（事后更新引用卡）。\n')
PY
grep -Fq "updated_at: '${BUMP1}'" "${VAULT}/${CARD_REL}" || die "未能把卡 1 的 updated_at 改新"
seal "事后更新引用卡 1（updated_at ${AT1} → ${BUMP1}）"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "本步应已入库（工作区为空）"; }
DIRTY_SUM="$(tree_sum)"
HIT_LOG="$(commits)"
cp "${VAULT}/${RECAP_REL}" "${WORK}/recap.before"
drive probe "${STAMP}" hit
[ "$(fact_num recap_stale)" = "1" ] ||
  { cat "${FACTS}"; die "应恰 1 条 recap_stale，实得 $(fact_num recap_stale)"; }
[ "$(fact_count '"check":"recap_stale"')" = "1" ] ||
  { cat "${FACTS}"; die "recap_stale 的 finding 条数不为 1"; }
[ "$(fact_count '"W19"')" = "1" ] || { cat "${FACTS}"; die "R6 的诊断码应恰 W19 一次（§3 单射）"; }
[ "$(fact_count '"check":"recap_stale","severity":"warning"')" = "1" ] ||
  { cat "${FACTS}"; die "R6 的 severity 应为 warning"; }
# 命中面：恰那一篇综述（综述 2 零命中 —— 它的引用卡一个字都没动）。
[ "$(fact_list targets)" = "${RECAP_REL}" ] ||
  { cat "${FACTS}"; die "命中综述应恰 ${RECAP_REL}，实得 $(fact_list targets)"; }
[ "$(fact_list target_ids)" = "${RECAP}" ] ||
  { cat "${FACTS}"; die "命中综述 ID 应恰 ${RECAP}"; }
grep -Fq "\"targets\":[\"${RECAP_REL}\",\"${RECAP}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "finding 的 targets 应为 [综述路径, 综述 ID]"; }
# 综述 2 出命中面：它的引用卡一个字都没动 → 不在命中综述 / 命中 ID / 意向 / R6 finding 里。
# 只看 R6 自己的四处产出 —— 采样面**本该**含它（采样不等于命中），拿采样面判越界会把
# 「采到了」谎报成「判它失准了」。
r6_hit_facts() {
  jq -r '(.targets[]?), (.target_ids[]?),
    (.repairs[]? | select(.Check=="recap_stale") | .Path),
    (.findings[]? | select(.check=="recap_stale") | .targets[])' "${FACTS}"
}
r6_hit_facts >"${WORK}/r6-hit.txt"
cnt0 '综述 2 进了命中面（它的引用卡未变）' -Fn "${RECAP2}" "${WORK}/r6-hit.txt"
[ "$(grep -c . "${WORK}/r6-hit.txt")" = "5" ] ||
  { cat "${WORK}/r6-hit.txt"; die "R6 命中面事实应恰 5 条（1 命中路径 + 1 命中 ID + 1 意向 Path + 1 条 finding × 2 targets）"; }
# 理由：封闭三值的首个命中值（本步造数恰命中「引用卡已更新」）。
[ "$(fact_list target_reasons)" = "${R1_UPDATED}" ] ||
  { cat "${FACTS}"; die "失准理由应恰 ${R1_UPDATED}，实得 $(fact_list target_reasons)"; }
[ "$(fact_list already_marked)" = "" ] ||
  { cat "${FACTS}"; die "首轮不该有「已标记」综述"; }
# RepairSpec 恰 1 条，keys 恰两键（逐键封闭）。
[ "$(fact_count '"Check":"recap_stale"')" = "1" ] || { cat "${FACTS}"; die "RepairSpec 应恰 1 条"; }
[ "$(fact_count '"Keys":\["stale","stale_reason"\]')" = "1" ] ||
  { cat "${FACTS}"; die "RepairSpec 的 keys 应恰 {stale, stale_reason}"; }
cnt0 'R6 意向里出现第三个待写键' -oE '"Keys":\["stale","stale_reason",' "${FACTS}"
# 检查阶段零写入零提交。
[ "$(commits)" = "${HIT_LOG}" ] || die "只读检查改变了 commit 数"
[ "${DIRTY_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "只读检查改变了工作区状态"; }
[ "$(fact_num ops)" = "0" ] || { cat "${FACTS}"; die "probe 模式不得合成任何 op"; }
[ "$(fact_list written)" = "" ] || { cat "${FACTS}"; die "probe 模式必须零写入"; }
ok "唯一触发条件成立：1 条 W19 / warning、1 条封闭意向（keys 恰两键）、理由=${R1_UPDATED}、零写入零提交"

# ---------------------------------------------------------------- 4. 修复 + 纳管：恰一次 commit
step "修复 + 纳管：改键集合恰 {stale, stale_reason}、正文字节与行数不变、git log 恰 +1（A-35）"
BEFORE_LOG="$(commits)"
drive repair "${STAMP}" repair
[ "$(fact_num ops)" = "1" ] || { cat "${FACTS}"; die "应合成恰 1 条 op，实得 $(fact_num ops)"; }
[ "$(fact_list keys)" = "stale,stale_reason" ] ||
  { cat "${FACTS}"; die "回执的待写键集合应恰 {stale, stale_reason}"; }
[ "$(fact_list written)" = "${RECAP_REL}" ] ||
  { cat "${FACTS}"; die "实际写入路径应恰 ${RECAP_REL}"; }
[ "$(fact_list staled)" = "${RECAP}" ] ||
  { cat "${FACTS}"; die "落盘失准标记的综述应恰 ${RECAP}"; }
[ "$(fact_list idempotent)" = "" ] || { cat "${FACTS}"; die "首轮不该有幂等项"; }
[ "$(jq -r ".reasons[\"${RECAP}\"] // \"\"" "${FACTS}")" = "${R1_UPDATED}" ] ||
  { cat "${FACTS}"; die "回执的机读理由应恰 ${R1_UPDATED}"; }
[ "$(fact_list skipped_kinds)" = "" ] || { cat "${FACTS}"; die "本轮不应有跳过项"; }
[ "$(fact_list failures)" = "" ] || { cat "${FACTS}"; die "本轮不应有落盘失败"; }
# ① 两键的落盘取值：`stale` 是 YAML 布尔真，`stale_reason` ∈ 封闭三值。
[ "$(fmline "${RECAP_REL}" stale)" = "stale: true" ] ||
  { fmline "${RECAP_REL}" stale; die "stale 行应恰 'stale: true'"; }
REASON_LINE="$(fmline "${RECAP_REL}" stale_reason)"
case "${REASON_LINE}" in
  *"${R1_UPDATED}"*|*"${R2_DELETED}"*|*"${R3_DEPRECATED}"*) ;;
  *) die "stale_reason 行 = ${REASON_LINE}，取值必须落在封闭三值内" ;;
esac
grep -Fq "${R1_UPDATED}" "${VAULT}/${RECAP_REL}" || die "本步造数应写 ${R1_UPDATED}"
[ "$(grep -c '^stale:' "${VAULT}/${RECAP_REL}")" = "1" ] || die "stale 应恰 1 行（覆盖而非累积）"
[ "$(grep -c '^stale_reason:' "${VAULT}/${RECAP_REL}")" = "1" ] || die "stale_reason 应恰 1 行"
# ② 键级反证：键差集恰两键、正文逐字节不变；行数只多两行（两键各一行），正文行数不变。
only_stale_two_keys "${WORK}/recap.before" "${VAULT}/${RECAP_REL}"
[ "$(linecount "${RECAP_REL}")" = "$((RECAP_LINES_BEFORE + 2))" ] ||
  die "综述行数 ${RECAP_LINES_BEFORE} → $(linecount "${RECAP_REL}")，只应多出两键那两行"
[ "$(fmline "${RECAP_REL}" status)" = "${RECAP_STATUS_BEFORE}" ] || die "综述的 status 行被改动了"
# ③ diff 级反证（task Acceptance 的那条）：本次 commit 里综述变化的键**恰**那两个。
# ④ 恰一次 commit：R6 的两键写入 → git log 恰 +1。
SHA="$(fact_str commit)"
[ -n "${SHA}" ] || { cat "${FACTS}"; die "有修复写入时必须产生恰一条 commit"; }
[ "$(fact_num commits)" = "1" ] || { cat "${FACTS}"; die "整轮对账的提交条数应恰 1（A-35）"; }
AFTER_LOG="$(commits)"
[ "${AFTER_LOG}" = "$((BEFORE_LOG + 1))" ] ||
  die "git log 条数 ${BEFORE_LOG} → ${AFTER_LOG}，应恰 +1"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "纳管后 git status --porcelain 应为空"; }
[ "$(diff_keys "${RECAP_REL}")" = "stale,stale_reason" ] ||
  { gitv diff HEAD~1 -- "${RECAP_REL}"; die "综述 diff 里变化的键应恰 {stale, stale_reason}"; }
gitv diff HEAD~1 >"${WORK}/repair.diff"
cnt0 '其余维度被牵连（status / deleted_at / deleted_reason / replaced_by / reviewed_at）' \
  -nE '^[+-](status|deleted_at|deleted_reason|replaced_by|reviewed_at):' "${WORK}/repair.diff"
# 未重算综述的 diff 级反证：本次 commit 里**恰 2 个新增行、0 个删除行**，
# 且两个新增行就是那两键（正文一行都没被增删改）。
ADDED="$(grep -cE '^\+[^+]' "${WORK}/repair.diff" || true)"
REMOVED="$(grep -cE '^-[^-]' "${WORK}/repair.diff" || true)"
[ "${ADDED}" = "2" ] && [ "${REMOVED}" = "0" ] ||
  { cat "${WORK}/repair.diff"; die "diff 应恰 2 个新增行 / 0 个删除行，实得 +${ADDED} / -${REMOVED}"; }
[ "$(grep -cE '^\+(stale|stale_reason):' "${WORK}/repair.diff")" = "2" ] ||
  { cat "${WORK}/repair.diff"; die "两个新增行必须恰是失准两键"; }
SUBJECT="$(gitv log -1 --pretty=%s)"
case "${SUBJECT}" in
  "reconcile(ai-infra): "*) ;;
  *) die "提交主题 = ${SUBJECT}，应以 reconcile(<domain>): 开头（A-30 / KnownVerbs 恒 8）" ;;
esac
# ⑤ 综述专属：整库带这两键的文件恰那一篇综述；其余对象字节零变化。
[ "$(bearers | paste -sd, -)" = "${RECAP_REL}" ] ||
  { bearers; die "带失准两键的文件应恰 ${RECAP_REL}（两键是综述专属）"; }
[ "$(rawhash "${RECAP2_REL}")" = "${RECAP2_HASH_BEFORE}" ] ||
  die "未命中综述 ${RECAP2_REL} 被写了"
[ "$(rawhash "${NOTE_REL}")" = "${NOTE_HASH_BEFORE}" ] || die "材料笔记被写了（两键是综述专属）"
ok "只写两键（stale: true / ${R1_UPDATED}）、正文与其余键一字不动、恰 +1 条 commit（${SHA:0:8}）"

# ---------------------------------------------------------------- 5. 幂等
step "幂等：再跑一整轮 → 零 op、零写入、零新增 commit，而 recap_stale finding 仍产出"
AFTER_SUM="$(tree_sum)"
RECAP_HASH_AFTER="$(rawhash "${RECAP_REL}")"
drive repair "${STAMP2}" idem
# finding **仍产出**（触发条件仍成立），但落盘已是同一标记与同一理由 → 零写入零 commit。
[ "$(fact_num recap_stale)" = "1" ] ||
  { cat "${FACTS}"; die "幂等轮的 recap_stale finding 仍应恰 1 条（合同 §9）"; }
[ "$(fact_list already_marked)" = "${RECAP}" ] ||
  { cat "${FACTS}"; die "幂等轮应把 ${RECAP} 标为已标记"; }
[ "$(fact_list idempotent)" = "${RECAP}" ] ||
  { cat "${FACTS}"; die "回执的幂等清单应恰 [${RECAP}]"; }
grep -Fq '零写入、零 commit' "${FACTS}" ||
  { cat "${FACTS}"; die "幂等轮的 finding 应如实注明零写入零 commit" ; }
[ "$(fact_num ops)" = "0" ] || { cat "${FACTS}"; die "幂等轮必须零 op（连 plan 都不组装）"; }
[ "$(fact_list written)" = "" ] || { cat "${FACTS}"; die "幂等轮必须零写入"; }
[ "$(fact_list staled)" = "" ] || { cat "${FACTS}"; die "幂等轮不得有新落盘的失准标记"; }
[ "$(fact_str commit)" = "" ] || { cat "${FACTS}"; die "幂等轮必须零 commit（不产生空提交）"; }
[ "$(fact_num commits)" = "0" ] || { cat "${FACTS}"; die "幂等轮提交条数必须为 0"; }
grep -Fq '"ran":true' "${FACTS}" || { cat "${FACTS}"; die "幂等轮的 ran 仍应为 true"; }
[ "$(commits)" = "${AFTER_LOG}" ] || die "幂等轮改变了 commit 数（应零新增 commit）"
[ "${AFTER_SUM}" = "$(tree_sum)" ] || die "幂等轮写盘了（应零写入）"
[ "$(rawhash "${RECAP_REL}")" = "${RECAP_HASH_AFTER}" ] || die "幂等轮改动了综述字节"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "幂等轮后工作区应仍干净"; }
# 也**不清除**既有标记（合同 §9 第二条「不自动」）。
[ "$(fmline "${RECAP_REL}" stale)" = "stale: true" ] || die "幂等轮清除了 stale（R6 不自动清除）"
grep -Fq "${R1_UPDATED}" "${VAULT}/${RECAP_REL}" || die "幂等轮清除了 stale_reason"
ok "幂等：0 op / 零写入 / 零新增 commit（仍 ${AFTER_LOG}）；finding 仍 1 条且标记未被清除"

# ---------------------------------------------------------------- 6. B3 不豁免
step "B3 不豁免：让综述 2 命中并注入过期观测 hash → skipped[kind=file_changed]、零写入"
python3 - "${VAULT}/${CARD2_REL}" "${AT2}" "${BUMP2}" <<'PY'
import sys
p, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(p, encoding='utf-8').read()
s = s.replace("updated_at: '%s'" % old, "updated_at: '%s'" % new, 1)
open(p, 'w', encoding='utf-8').write(s + '\n用户在编辑器里补写的一段（事后更新引用卡 2）。\n')
PY
grep -Fq "updated_at: '${BUMP2}'" "${VAULT}/${CARD2_REL}" || die "未能把卡 2 的 updated_at 改新"
[ "$(porcelain_count)" = "1" ] || { porcelain; die "本步应恰 1 处未提交改动（R1 的纳管面）"; }
RECAP2_HASH_BEFORE_STALE="$(rawhash "${RECAP2_REL}")"
STALE_LOG="$(commits)"
drive stale "${STAMP2}" stale
# 命中两篇（综述 1 幂等 / 综述 2 待写），注入的过期 hash 让待写那篇进 skipped。
[ "$(fact_num recap_stale)" = "2" ] ||
  { cat "${FACTS}"; die "本步应恰 2 条 recap_stale（综述 1 幂等 + 综述 2 待写）"; }
[ "$(fact_num ops)" = "1" ] ||
  { cat "${FACTS}"; die "应合成恰 1 条 op（幂等那篇不进 op；跳过发生在写前比对）"; }
[ "$(fact_count '"kind":"file_changed"')" = "1" ] ||
  { cat "${FACTS}"; die "hash 过期应进 skipped[kind=file_changed] 且恰 1 条（封闭两值之一）"; }
[ "$(fact_list skipped_kinds)" = "file_changed" ] ||
  { cat "${FACTS}"; die "跳过命名只用封闭两值，不得自造第三种 kind"; }
[ "$(fact_list written)" = "" ] || { cat "${FACTS}"; die "B3 前置比对未通过必须零写入"; }
[ "$(fact_list staled)" = "" ] || { cat "${FACTS}"; die "被跳过的综述不得计入已落盘清单"; }
# finding 仍在且 detail 注明已跳过（不删条目、不降级）。
grep -Fq '已跳过' "${FACTS}" || { cat "${FACTS}"; die "被跳过的 finding 应在 detail 里注明已跳过"; }
grep -Fq 'kind=file_changed' "${FACTS}" || { cat "${FACTS}"; die "注记应写清跳过 kind"; }
[ "$(fact_count '"severity":"warning"')" -ge 2 ] ||
  { cat "${FACTS}"; die "跳过不得改变 finding 的分级"; }
# 目标文件字节零变化：综述 2 一个字节都没被写。
[ "$(rawhash "${RECAP2_REL}")" = "${RECAP2_HASH_BEFORE_STALE}" ] ||
  die "被跳过的综述字节变了（B3 不豁免要求本次不写它）"
cnt0 '被跳过的综述仍长出了失准两键' -nE '^(stale|stale_reason):' "${VAULT}/${RECAP2_REL}"
[ "$(bearers | paste -sd, -)" = "${RECAP_REL}" ] ||
  { bearers; die "B3 跳过后带两键的文件仍应只有 ${RECAP_REL}"; }
# 纳管仍只产生恰一次 commit（把用户那处外部编辑记进历史；R6 一格未写）。
[ "$(fact_num commits)" = "1" ] || { cat "${FACTS}"; die "本轮提交条数应恰 1（A-35）"; }
[ "$(commits)" = "$((STALE_LOG + 1))" ] || die "本轮 commit 数应恰 +1"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "纳管后工作区应为空"; }
ok "B3 不豁免：kind=file_changed 恰 1 条、零写入、finding 仍在并注明已跳过、仍恰一次 commit"

# ---------------------------------------------------------------- 7. 源码级边界反证
step "边界反证：零 internal/store / 走 ChangePlan / A-34 不带 initiator / 零命令注册 / 计数只增不减"
R6SRC="${REPO_ROOT}/internal/reconcile/r6_recap.go"
BRIDGE="${REPO_ROOT}/internal/cli/reconcile_repair_stale.go"
# ① 检查侧三条零（写盘 / 提交 / 子进程）。
cnt0 '检查侧写盘' -rnE 'os\.WriteFile|os\.Create|os\.Remove|os\.Rename' "${R6SRC}"
cnt0 '检查侧提交' -rnE 'Commit\(|git add' "${R6SRC}"
cnt0 '检查侧起子进程' -rnE 'os/exec|exec\.Command' "${R6SRC}"
# ② 修复桥不 import 落盘层、不牵连其余四个维度（task verify.run 的两条 grep 逐字同源）。
cnt0 '修复桥 import 知识数据落盘层' -n 'internal/store' "${BRIDGE}"
cnt0 '修复桥牵连状态 / 删除 / 替代指针 / 过目维度' \
  -nE 'SetStatus|deleted_at|replaced_by|reviewed_at' "${BRIDGE}"
[ "$(grep -cE 'internal/plan|runPlan' "${BRIDGE}")" -ge 1 ] ||
  die "修复桥必须经 internal/plan 落盘（不得裸写知识数据）"
[ "$(grep -c 'ChangePlan' "${BRIDGE}")" -ge 1 ] || die "修复桥必须组装内存 ChangePlan"
# ③ A-34：不需要 `--user-request` —— 合成 plan 逐字不带 initiator，也不给命令行佐证。
cnt0 '修复桥给了 initiator（A-34 判定 R6 不需要 --user-request）' -n 'Initiator' "${BRIDGE}"
cnt0 '修复桥自造 op 名字面量' -nE '"set_stale"|"mark_reviewed"' "${BRIDGE}"
[ "$(grep -c 'OpSetStale' "${BRIDGE}")" -ge 1 ] ||
  die "op 名必须取阶段 1 定稿的 plan.OpSetStale 常量真源（A-33）"
# ④ B3 前置比对未削弱：执行路径逐字带 ExpectedHash，且 content_hash 计数不减（基线 41）。
[ "$(grep -c 'ExpectedHash' "${REPO_ROOT}/internal/plan/execute_m4.go")" -ge 1 ] ||
  die "set_stale 的执行路径必须带 ExpectedHash（B3 不豁免）"
C_HASH="$(grep -rn 'ContentHash\|content_hash' "${REPO_ROOT}/internal/store" "${REPO_ROOT}/internal/plan" \
  --include=*.go | grep -v _test.go | wc -l | tr -d ' ')"
[ "${C_HASH}" -ge 41 ] || die "content_hash 前置比对计数 ${C_HASH} < 41（基线不得减少）"
cnt0 '出现跳过 hash 比对的口子' -rn '跳过 hash 比对' "${BRIDGE}" "${REPO_ROOT}/internal/plan/execute_m4.go"
# ⑤ 不新增 kind、不新增命令：`eg reconcile` / `eg check` 在整个命令层零注册。
KINDS="$( { grep -rhoE 'kind\":\s*\"[a-z_]+\"' "${REPO_ROOT}/internal/" || true; } |
  sort -u | wc -l | tr -d ' ')"
[ "${KINDS}" -le 2 ] || die "skipped[].kind 取值数 ${KINDS} > 2（封闭两值不得新增）"
# 落盘层的封闭两值本体（真源）：`file_changed` / `user_block_unsafe` 之外零取值。
KIND_SRC="$( { grep -rhoE 'SkipReason = "[a-z_]+"' "${REPO_ROOT}/internal/store" ||
  true; } | sort -u | wc -l | tr -d ' ')"
[ "${KIND_SRC}" = "2" ] || die "落盘层 SkipReason 的非空取值数 ${KIND_SRC} ≠ 2（封闭两值不得新增）"
# 2026-09-07 随 M4 · T-…-058 阶段 3 按实测重钉判据**形态**（本体一格未放宽，反而多一格）：
# T-…-058 按技术方案 §7.1 / 对账合同 §12 **合法注册** S3 命令 `eg reconcile`（命令数 18 → 19），
# 注册形态 `Name: "reconcile"` 在 `internal/cli/reconcile.go` 落**恰 1** 次，故旧的「全包恒 0」
# 按实测重钉为两格：① 全 `internal/cli/` 恰 1（= M3 期 0 + M4 T-…-058 新增 1，加法等式，
# M3 侧加数 0 逐字保留）；② 唯一落点必须是 `internal/cli/reconcile.go` —— 本 task 的实现文件
# 与其它任何文件内恒 0。「本 task 不注册命令」这一本体由第 ② 格逐字钉死，比旧的一格更严。
T058_REG_ALL="$( { grep -rnE 'Name:[[:space:]]*"reconcile"' "${REPO_ROOT}/internal/cli/" \
  --include='*.go' || true; } | wc -l | tr -d ' ')"
T058_REG_OUT="$( { grep -rnE 'Name:[[:space:]]*"reconcile"' "${REPO_ROOT}/internal/cli/" \
  --include='*.go' | grep -v '/reconcile\.go:' || true; } | wc -l | tr -d ' ')"
[ "${T058_REG_ALL}" = "1" ] ||
  die "reconcile 命令注册形态应恰 1（M3 期 0 + M4 T-…-058 新增 1），实得 ${T058_REG_ALL}"
[ "${T058_REG_OUT}" = "0" ] ||
  die "reconcile 命令注册形态的唯一落点必须是 internal/cli/reconcile.go，别处实得 ${T058_REG_OUT}"
# 2026-09-07 随 M4 · T-…-059 阶段 3 按实测重钉判据**形态**（本体一格未放宽，反而多一格）：
# T-…-059 按技术方案 §7.1 / 对账合同 §13 **合法注册**只读结构体检命令 `eg check`
# （命令数 19 → 20，M4 收口值），注册形态 `Name: "check"` 在 `internal/cli/check.go` 落**恰 1** 次，
# 故旧的「全包恒 0」按实测重钉为两格：① 全 `internal/cli/` 恰 1（= M3 期 0 + M4 T-…-059 新增 1，
# 加法等式，M3 侧加数 0 逐字保留）；② 唯一落点必须是 `internal/cli/check.go` —— 本 task 的实现文件
# 与其它任何文件内恒 0。「本 task 不注册命令」这一本体由第 ② 格逐字钉死，比旧的一格更严。
T059_REG_ALL="$( { grep -rnE 'Name:[[:space:]]*"check"' "${REPO_ROOT}/internal/cli/" \
  --include='*.go' || true; } | wc -l | tr -d ' ')"
T059_REG_OUT="$( { grep -rnE 'Name:[[:space:]]*"check"' "${REPO_ROOT}/internal/cli/" \
  --include='*.go' | grep -v '/check\.go:' || true; } | wc -l | tr -d ' ')"
[ "${T059_REG_ALL}" = "1" ] ||
  die "check 命令注册形态应恰 1（M3 期 0 + M4 T-…-059 新增 1），实得 ${T059_REG_ALL}"
[ "${T059_REG_OUT}" = "0" ] ||
  die "check 命令注册形态的唯一落点必须是 internal/cli/check.go，别处实得 ${T059_REG_OUT}"
# ⑥ 封闭三值恒 3（真源在 model，检查侧逐字复述那三句）。
THREE="$(grep -oE "${R1_UPDATED}|${R2_DELETED}|${R3_DEPRECATED}" "${R6SRC}" | sort -u | wc -l | tr -d ' ')"
[ "${THREE}" = "3" ] || die "r6_recap.go 里的封闭三值命中 ${THREE} 个，应恰 3"
# ⑦ 不重算综述 / 不清除标记：两个能力在写入面结构上不可表达。
cnt0 '修复桥出现重算综述的入口' -nE 'Recompute|Regenerate|正文重算' "${BRIDGE}"
cnt0 '修复桥出现清除标记的反向形态' -nE 'ClearStale|UnsetStale|DropStale' "${BRIDGE}"
# ⑧ 越界能力零引入 + e2e 计数只增不减（本 task 为磁盘上第 32 个 e2e / M4 期第 7 个）。
cnt0 '修复桥出现越界能力' -rnE '\.index/|FTS5|[Ss][Qq][Ll]ite|flock|P95' "${BRIDGE}"
# 测试资产统一迁入 tests/ 并按能力重组后（合同 §2 / ADR-T1），e2e 不再以 test/e2e/m<N>_*.sh 命名。
# 「只增不减」这条历史判据一格不放宽，只改为等价可复算形态：
#   · 总数 = 盘面 tests/e2e/**/*.sh 计数（历史下限 32 继续生效）；
#   · M4 期计数 = 迁移表里 old_path 以 test/e2e/m4_ 开头且 decision=keep 的行数
#     （比数文件名更强：要求每支 M4 期脚本都在迁移表里有登记落点，漏登即判否）。
E2E_TOTAL="$(find "${REPO_ROOT}/tests/e2e" -name '*.sh' | wc -l | tr -d ' ')"
E2E_M4="$(awk -F'\t' 'NR>1 && $1 ~ /^test\/e2e\/m4_/ && $3=="keep"' \
  "${REPO_ROOT}/tests/manifest/migration.tsv" | wc -l | tr -d ' ')"
[ "${E2E_TOTAL}" -ge 32 ] || die "磁盘 e2e 脚本数 ${E2E_TOTAL} < 32（本 task 恰 +1，只增不减）"
[ "${E2E_M4}" -ge 7 ] || die "M4 期 e2e 脚本数 ${E2E_M4} < 7（迁移表登记为准）"
ok "零 internal/store / 经 ChangePlan / 不带 initiator（A-34）/ 零命令注册 / content_hash ${C_HASH} ≥ 41 / kind ${KINDS} ≤ 2 / e2e ${E2E_TOTAL}（M4 期 ${E2E_M4}）"

printf '\n=== recap_stale.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

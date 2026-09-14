#!/usr/bin/env bash
# R2 `reviewed_at` 补齐的端到端脚本（T-evergreen.s1_main_flow-158614-051）。
#
# 判据来源：`docs/specs/2026-11-12-m4-reconcile-contract.md` §5（R2 全文：三条判定同真、
# 动作「补写 `reviewed_at` 为本次对账时刻」、**三维不牵连**、**B3 不豁免**、ADR-20 不放宽）、
# §1.3 写口归属表第 2 行（R2 的写入经**内存 ChangePlan** → `internal/plan` → `internal/store`）、
# §3（check ↔ 诊断码单射：`reviewed_at_missing` ↔ `W14`）；
# `docs/specs/2026-11-15-m4-prestart-adjudication.md` 的 **A-33**（R2 复用**既有** op
# `mark_reviewed`，`AllOpNames` 恒 16）、**A-34**（R2 走 P-U 列：合成 plan 带
# `initiator: user` + 进程边界注入授权佐证）、**A-35**（R1 纳管与 R2 修复的写入合并为
# **恰一次** commit）；`docs/specs/2026-10-10-m3-user-authorization-contract.md` 的
# A-16 / N-7（M3 只做触发② `eg mark-reviewed`，触发①「用户直接编辑后补齐」逐字留给 S3 / M4，
# 即本 task）；`milestones/M-004-m4.md` 完成判据 4 与风险 R-16。
#
# 六段断言（task deliverable 与 Acceptance 逐字要求）：
#   ① 干净库（两张互连卡 / 一篇笔记 / 一份原文）→ 只读检查**零 finding**、零写入零提交：
#      那张缺 `reviewed_at` 的卡虽被 `eg unreviewed` 如实列出（判定第 1、3 条成立），
#      但工作区干净、**没有**第 2 条的外部编辑证据 → R2 一条不命中（三条同真的反向行）；
#   ② 造 3 处外部编辑（不经 `eg`：一张**缺** `reviewed_at` 的卡、一篇 `reviewed_at`
#      **早于** `updated_at` 的笔记、一份**出范围**的 `unprocessed.md`）→ 只读检查产出
#      `reviewed_at_missing` **恰 2 条**（W14 / warning，两种形态各 1：缺失 + 过期），
#      RepairSpec 恰 2 条且 `keys` 恰 `{reviewed_at}`；`unprocessed.md` / `proposals/`
#      **零命中**（范围面）；检查阶段零写入零提交；
#   ③ 修复 + 纳管：`reviewed_at` 被补写为**注入的对账时刻**（逐字可断言），
#      同文件其余 frontmatter 键与**正文**一字不动（键级 diff + 正文逐字节比对 + 未命中
#      对象的 sha256 反证），`status:` / `deleted_at:` / `deleted_reason:` / `replaced_by:`
#      在 `git diff` 中**零变化行**，`eg unreviewed` 输出行数 → 0；
#   ④ **恰一次 commit**（A-35）：R1 有 3 处外部编辑 + R2 有 2 处修复写入，
#      `git log --oneline | wc -l` 相对对账前**恰 +1**（不是 +2），提交主题 verb 恰 `reconcile`；
#   ⑤ **幂等**：干净工作区再跑一整轮 → 零 op、零写入、零 commit（finding 也归零）；
#   ⑥ **B3 不豁免**：注入**过期**观测 hash → 进 `skipped[kind=file_changed]`、目标字节
#      零变化、对应 finding **仍在**且 detail 注明已跳过。
# 另加源码级边界反证（修复桥不 import 落盘层 / 走 ChangePlan / `content_hash` 前置未削弱 /
# 三维字面量零命中 / 检查侧三条零 / 不新增 op 与命令 / ADR-20 四包零引用）。
#
# 为什么用 `go test` 驱动而不是 `eg reconcile`：
#   `eg reconcile` 命令本体、信封与退出码属 T-…-058，本 task 明确不注册命令，只交付
#   「R2 只读判定 + 经 ChangePlan 的封闭键写入」两项能力。脚本因此在真实临时 vault 上用
#   `test/e2e/m4_r2_reviewed_test.go` 这一层驱动壳调这两项能力（同 `m4_r1_takeover.sh` /
#   `m4_r4_structure.sh` 的先例：驱动壳未设 `EG_M4_R2_VAULT` 时直接 skip，
#   `go test ./...` 行为一字不变），事实一律回读**文件字节 / git 自己 / eg 自己的只读命令**
#   与驱动壳落盘的 JSON，不看实现自报。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go / jq / python3）；
# **一切写操作只发生在 mktemp -d 目录内**；任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/lifecycle/reviewed_backfill.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-r2.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
FACTS="${WORK}/facts.json"

KDIR_REL='domains/ai-infra/knowledge'
NDIR_REL='domains/ai-infra/notes'
CARD='k-20260901-attention'          # 缺 reviewed_at → 判定第 3 条的 missing 形态
CARD2='k-20260902-rnn'               # 过目信号齐备且不被外部编辑 → 全程一字不变的对照物
NOTE='n-20260901-attention'          # reviewed_at 早于 updated_at → behind_updated_at 形态
SRC='s-20260901-attention'
CARD_REL="${KDIR_REL}/${CARD}.md"
CARD2_REL="${KDIR_REL}/${CARD2}.md"
NOTE_REL="${NDIR_REL}/${NOTE}.md"
OUT_OF_SCOPE='unprocessed.md'        # 收件区：R1 纳管它，R2 恒不碰它（合同 §5 判定第 1 条）

# 两个**注入**的对账时刻（固定值 → 补写进 frontmatter 的值可逐字断言，绝不读本机时钟）。
STAMP='2026-11-24T10:00:00+08:00'
STAMP_STALE='2026-12-02T10:00:00+08:00'
# 用户手改时写进 updated_at 的两个时刻（用于制造「过目信号过期」）。
# 纪律：UPDATED_LATE **早于** STAMP，因此补齐后 `eg unreviewed` 归零（内容时刻 < 过目时刻）；
# UPDATED_LATER **晚于** STAMP 但早于 STAMP_STALE，用来在 B3 那一步重新制造「过期」形态。
UPDATED_LATE='2026-11-20T10:00:00+08:00'
UPDATED_LATER='2026-12-01T10:00:00+08:00'

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
# fmline 取某个 frontmatter 顶层键所在的**整行**（含引号与空格）：逐字比对用。
fmline() { grep -m1 "^$2:" "${VAULT}/$1" || true; }
# seal <说明>：把造好的数据提交进 vault 自己的仓库，使「检查前工作区为空」成立。
seal() { gitv add -A >/dev/null && gitv -c user.name=e2e -c user.email=e2e@local \
  commit -q -m "e2e(r2): $1" >/dev/null; }
# unreviewed_ids 取 `eg unreviewed` 清单里的对象 ID（只读命令，零命中也必须退 0）。
unreviewed_ids() {
  [ "$(eg_code unreviewed --json)" = "0" ] ||
    { cat "${WORK}/err.txt"; die "eg unreviewed 未退 0（零命中也必须退 0）"; }
  jq -re '.data.unreviewed[]?.id' "${WORK}/out.txt" | sort
}
unreviewed_count() { unreviewed_ids | grep -c . || true; }

# drive <mode> [stamp] [tag]：在真实 vault 上跑驱动壳，事实落 ${FACTS}（另存带标签副本）。
drive() {
  local mode="$1" stamp="${2:-${STAMP}}" tag="${3:-$1}"
  rm -f "${FACTS}"
  ( cd "$(eg_staged_root)" &&
    EG_M4_R2_VAULT="${VAULT}" EG_M4_R2_MODE="${mode}" EG_M4_R2_OUT="${FACTS}" \
    EG_M4_R2_DOMAIN='ai-infra' EG_M4_R2_STAMP="${stamp}" \
    go test ./test/e2e/ -run TestM4R2ReviewedHarness -count=1 -v </dev/null ) \
    >"${WORK}/drive-${tag}.log" 2>&1 ||
    { tail -30 "${WORK}/drive-${tag}.log"; die "驱动壳（${tag}）未全绿"; }
  grep -Fq -- "--- PASS: TestM4R2ReviewedHarness" "${WORK}/drive-${tag}.log" ||
    { tail -30 "${WORK}/drive-${tag}.log"; die "驱动壳（${tag}）未 PASS（可能被 skip）"; }
  [ -s "${FACTS}" ] || die "驱动壳（${tag}）未落事实文件"
  cp "${FACTS}" "${WORK}/facts-${tag}.json"
}

fact_str() { sed -E "s/.*\"$1\":\"([^\"]*)\".*/\1/" "${FACTS}"; }
fact_num() { sed -E "s/.*\"$1\":([0-9]+).*/\1/" "${FACTS}"; }
fact_count() { grep -o -- "$1" "${FACTS}" | wc -l | tr -d ' '; }
cnt0() { # cnt0 <说明> <grep 参数...>：期望 0 命中
  local name="$1"; shift
  local n; n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}
# 「变化的 frontmatter 键 + 正文是否变化」的键级复算器：**反证**修复只动一个键。
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
# only_reviewed_at <before 副本> <after 路径>：键差集恰 {reviewed_at} 且正文逐字节不变。
only_reviewed_at() {
  local got; got="$(fmdiff "$1" "$2")"
  [ "$(printf '%s\n' "${got}" | sed -n 1p)" = "keys=reviewed_at" ] ||
    { printf '%s\n' "${got}"; die "$2 的 frontmatter 键差集应恰 {reviewed_at}"; }
  [ "$(printf '%s\n' "${got}" | sed -n 2p)" = "body=same" ] ||
    { printf '%s\n' "${got}"; die "$2 的正文被改动了（R2 只写一个键，正文一字不动）"; }
}
# diff_keys <rel>：本次 commit 里该文件**变化了的** frontmatter 键名（升序去重）。
# 只看 +/- 行：上下文行（前导空格）不是改动，把它算进来会把「diff 里出现过 status:」
# 误判成「R2 改了 status」——判据本体是「status 等三维**未被改动**」。
diff_keys() { gitv diff HEAD~1 -- "$1" | grep -E '^[+-][a-z_]+:' |
  sed -E 's/^[+-]([a-z_]+):.*/\1/' | sort -u | paste -sd, -; }

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
command -v jq >/dev/null || die "本脚本用 jq 读 .data.unreviewed[]"
command -v python3 >/dev/null || die "本脚本用 python3 做 frontmatter 键级复算"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 造干净数据
step "建库并造数据：2 张互连卡 + 1 篇笔记 + 1 份原文（过目信号齐备 → 干净库零 finding）"
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
reviewed_at: '2026-09-01T10:00:00+08:00'
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
updated_at: '2026-09-01T10:00:00+08:00'
tags:
  - llm
sources:
  - source: ${SRC}
    note: ${NOTE}
    rel: support
    reason: 该结论由这份材料支持
relations:
  - type: supports
    target: ${CARD2}
    reason: 支持 RNN 那张卡的结论
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
updated_at: '2026-09-02T10:00:00+08:00'
reviewed_at: '2026-09-02T10:00:00+08:00'
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
seal "R2 语料（一张卡缺 reviewed_at、一篇笔记的过目信号早于 updated_at）"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "造数后应已入库（工作区为空）"; }
cnt0 '卡不得自带 reviewed_at（不回填默认值）' -n '^reviewed_at:' "${VAULT}/${CARD_REL}"
grep -Fq "reviewed_at: '2026-09-01T10:00:00+08:00'" "${VAULT}/${NOTE_REL}" ||
  die "笔记必须自带一个**早于**后续 updated_at 的 reviewed_at（过期形态的判据基础）"
BASE_COMMITS="$(commits)"
CARD2_HASH_BEFORE="$(rawhash "${CARD2_REL}")"
CARD_STATUS_BEFORE="$(fmline "${CARD_REL}" status)"
[ -n "${CARD_STATUS_BEFORE}" ] || die "卡必须有 status 行，否则三维不牵连失去判据"
ok "vault 就绪：commit 数 ${BASE_COMMITS}、工作区为空、卡无 reviewed_at、笔记过目信号齐备"

# ---------------------------------------------------------------- 2. 干净库：零 finding
step "干净库跑只读检查：零 finding（只满足两条不命中）、零 RepairSpec、零写入零提交"
CLEAN_SUM="$(tree_sum)"
# 三条同真的**反向行**：这张卡满足判定第 1 条（是知识卡）与第 3 条（reviewed_at 缺失，
# 因此 `eg unreviewed` 如实把它列出来），但**没有**第 2 条的外部编辑证据（工作区干净）
# → R2 一条 finding 都不许产出。
[ "$(unreviewed_count)" = "1" ] ||
  { unreviewed_ids; die "干净库的 unreviewed 清单应恰列出那张缺 reviewed_at 的卡"; }
[ "$(unreviewed_ids)" = "${CARD}" ] ||
  { unreviewed_ids; die "unreviewed 清单里应恰是 ${CARD}"; }
drive probe "${STAMP}" clean
[ "$(fact_count '"check":')" = "0" ] || { cat "${FACTS}"; die "干净库不得有任何 finding"; }
[ "$(fact_num reviewed_at_missing)" = "0" ] ||
  { cat "${FACTS}"; die "干净库不得有 reviewed_at_missing"; }
grep -Eq '"repairs":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "零命中时不得有 RepairSpec"; }
grep -Fq '"keys":["reviewed_at"]' "${FACTS}" ||
  { cat "${FACTS}"; die "封闭待写键集合恒 {reviewed_at}（零命中时也是这一个键）"; }
[ "$(fact_num exit_code)" = "0" ] || { cat "${FACTS}"; die "零 error 级 finding 时退出码应为 0"; }
[ "$(commits)" = "${BASE_COMMITS}" ] || die "只读检查改变了 commit 数"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "只读检查后工作区应仍为空"; }
[ "${CLEAN_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了"
ok "干净库：0 finding / 0 repair / 零写入零提交；只满足两条判定（缺 reviewed_at 但无外部编辑证据）不命中"

# ---------------------------------------------------------------- 3. 造 3 处外部编辑
step "造 3 处外部编辑（不经 eg）：卡正文追加、笔记正文追加 + updated_at 变新、收件区追加"
printf '\n用户在编辑器里手写的一段（外部编辑 1）。\n' >>"${VAULT}/${CARD_REL}"
python3 - "${VAULT}/${NOTE_REL}" "${UPDATED_LATE}" <<'PY'
import sys
p, late = sys.argv[1], sys.argv[2]
s = open(p, encoding='utf-8').read()
# 用户手改笔记：正文追加一段，并把 updated_at 改新（reviewed_at 因此变成「过期」）。
s = s.replace("updated_at: '2026-09-01T10:00:00+08:00'", "updated_at: '%s'" % late, 1)
open(p, 'w', encoding='utf-8').write(s + '\n用户在编辑器里手写的一段（外部编辑 2）。\n')
PY
grep -Fq "updated_at: '${UPDATED_LATE}'" "${VAULT}/${NOTE_REL}" || die "未能把笔记的 updated_at 改新"
printf -- '- 用户手放进收件区的一行（外部编辑 3，出 R2 范围）\n' >>"${VAULT}/${OUT_OF_SCOPE}"
[ "$(porcelain_count)" = "3" ] ||
  { porcelain; die "外部编辑后 git status 应恰 3 条，实得 $(porcelain_count)"; }
DIRTY_BEFORE="$(porcelain)"
DIRTY_SUM="$(tree_sum)"
cp "${VAULT}/${CARD_REL}" "${WORK}/card.before"
cp "${VAULT}/${NOTE_REL}" "${WORK}/note.before"
[ "$(unreviewed_count)" -ge 1 ] || die "外部编辑后至少那张缺 reviewed_at 的卡应计入 unreviewed"
ok "工作区恰 3 处未提交改动（卡 / 笔记 / 收件区），其中恰 2 个在 R2 范围内"

# ---------------------------------------------------------------- 4. 只读检查：恰 2 条 W14
step "只读检查：reviewed_at_missing 恰 2 条（W14 / warning，缺失 + 过期各 1），出范围零命中"
drive probe "${STAMP}" dirty
[ "$(fact_num reviewed_at_missing)" = "2" ] ||
  { cat "${FACTS}"; die "应恰 2 条 reviewed_at_missing，实得 $(fact_num reviewed_at_missing)"; }
[ "$(fact_count '"check":"reviewed_at_missing"')" = "2" ] ||
  { cat "${FACTS}"; die "reviewed_at_missing 的 finding 条数不为 2"; }
[ "$(fact_count '"W14"')" = "2" ] || { cat "${FACTS}"; die "R2 的诊断码应恰 W14 两次（§3 单射）"; }
[ "$(fact_count '"check":"reviewed_at_missing","severity":"warning"')" = "2" ] ||
  { cat "${FACTS}"; die "R2 的 severity 应为 warning"; }
# 两种形态各 1 条（判定第 3 条的封闭两形态：缺失 / 早于 updated_at）。
# finding 的 detail 用「**且**过目信号…」措辞，同源 RepairSpec 的 reason 用「**后**过目信号…」，
# 因此两个前缀各自恰 1 次 = 「每种形态恰 1 条 finding + 恰 1 条同源意向」。
[ "$(fact_count '且过目信号缺失')" = "1" ] ||
  { cat "${FACTS}"; die "缺失形态的 finding 应恰 1 条"; }
[ "$(fact_count '后过目信号缺失')" = "1" ] ||
  { cat "${FACTS}"; die "缺失形态的修复意向应恰 1 条"; }
[ "$(fact_count '且过目信号早于内容更新时刻')" = "1" ] ||
  { cat "${FACTS}"; die "过期（早于 updated_at）形态的 finding 应恰 1 条"; }
[ "$(fact_count '后过目信号早于内容更新时刻')" = "1" ] ||
  { cat "${FACTS}"; die "过期形态的修复意向应恰 1 条"; }
# targets = [路径, 对象 ID]（去重升序）；命中集合恰那两个对象。
grep -Fq "\"targets\":[\"${CARD_REL}\",\"${CARD}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "缺失形态的 targets 应为 [卡路径, 卡 ID]"; }
grep -Fq "\"targets\":[\"${NOTE_REL}\",\"${NOTE}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "过期形态的 targets 应为 [笔记路径, 笔记 ID]"; }
grep -Fq "\"targets\":[\"${CARD_REL}\",\"${NOTE_REL}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "命中对象清单应恰是那两个（升序）"; }
grep -Fq "\"target_ids\":[\"${CARD}\",\"${NOTE}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "命中对象 ID 清单应恰是那两个（升序）"; }
# RepairSpec 恰 2 条，keys 逐条恰 {reviewed_at}（逐键封闭）。
[ "$(fact_count '"Check":"reviewed_at_missing"')" = "2" ] ||
  { cat "${FACTS}"; die "RepairSpec 应恰 2 条"; }
[ "$(fact_count '"Keys":\["reviewed_at"\]')" = "2" ] ||
  { cat "${FACTS}"; die "每条 RepairSpec 的 keys 应恰 {reviewed_at}"; }
cnt0 'R2 意向里出现第二个待写键' -oE '"Keys":\["reviewed_at","' "${FACTS}"
# 范围面：收件区与提案面恒不进 **R2** 的命中集合（合同 §5 判定第 1 条）。
# 判定只看 R2 自己的四处产出（命中路径 / 命中 ID / R2 finding 的 targets / 修复意向的 Path）——
# R1 的 `git_uncommitted` **本该**把收件区那处外部编辑列进它自己的 targets，那是 R1 的事实，
# 拿它来判 R2 越界会把两项检查的事实搅在一起。
r2_scope_facts() {
  jq -r '(.targets[]?), (.target_ids[]?), (.repairs[]?.Path),
    (.findings[]? | select(.check=="reviewed_at_missing") | .targets[])' "${FACTS}"
}
r2_scope_facts >"${WORK}/r2-scope.txt"
cnt0 'R2 命中了收件区（出范围）' -Fn "${OUT_OF_SCOPE}" "${WORK}/r2-scope.txt"
cnt0 'R2 命中了提案面（出范围）' -Fn 'proposals/' "${WORK}/r2-scope.txt"
[ "$(grep -c . "${WORK}/r2-scope.txt")" = "10" ] ||
  { cat "${WORK}/r2-scope.txt"; die "R2 的命中面事实应恰 10 条（2 命中路径 + 2 命中 ID + 2 意向 Path + 2 条 finding × 2 targets）"; }
# R1 仍如实报它自己的那一条（3 处外部编辑一并纳管），两项检查互不吞并。
[ "$(fact_count '"check":"git_uncommitted"')" = "1" ] ||
  { cat "${FACTS}"; die "R1 的 git_uncommitted 应恰 1 条（R2 不吞并 R1 的事实）"; }
# 检查阶段零写入零提交。
[ "$(commits)" = "${BASE_COMMITS}" ] || die "只读检查改变了 commit 数"
[ "${DIRTY_BEFORE}" = "$(porcelain)" ] || die "只读检查改变了工作区状态"
[ "${DIRTY_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了"
[ "$(fact_num ops)" = "0" ] || { cat "${FACTS}"; die "probe 模式不得合成任何 op"; }
grep -Eq '"written":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "probe 模式必须零写入" ; }
ok "检查侧：2 条 W14 / warning（缺失 + 过期各 1）、2 条封闭意向、出范围零命中、零写入零提交"

# ---------------------------------------------------------------- 5. 修复 + 纳管：恰一次 commit
step "修复 + 纳管：只写 reviewed_at 一个键、正文一字不动、git log 恰 +1（A-35）"
BEFORE_LOG="$(commits)"
drive repair "${STAMP}" repair
[ "$(fact_num ops)" = "2" ] || { cat "${FACTS}"; die "应合成恰 2 条 op，实得 $(fact_num ops)"; }
[ "$(fact_str stamp)" = "${STAMP}" ] ||
  { cat "${FACTS}"; die "补写时刻应恰是注入的对账时刻 ${STAMP}，实得 $(fact_str stamp)"; }
grep -Fq "\"written\":[\"${CARD_REL}\",\"${NOTE_REL}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "实际写入路径应恰是那两个对象（升序去重）"; }
grep -Fq "\"reviewed\":[\"${CARD}\",\"${NOTE}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "被补齐过目信号的对象 ID 应恰是那两个"; }
grep -Eq '"skipped":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "本轮不应有跳过项"; }
grep -Eq '"failures":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "本轮不应有落盘失败" ; }
# ① 补写的值逐字可断言（时刻由注入给定，不读本机时钟）。
grep -Fq "reviewed_at: '${STAMP}'" "${VAULT}/${CARD_REL}" ||
  { fmline "${CARD_REL}" reviewed_at; die "卡的 reviewed_at 未补成注入的对账时刻"; }
grep -Fq "reviewed_at: '${STAMP}'" "${VAULT}/${NOTE_REL}" ||
  { fmline "${NOTE_REL}" reviewed_at; die "笔记的 reviewed_at 未覆盖成注入的对账时刻"; }
# ② 键级反证：键差集恰 {reviewed_at}，正文逐字节不变（同文件其他键与正文一字不动）。
only_reviewed_at "${WORK}/card.before" "${VAULT}/${CARD_REL}"
only_reviewed_at "${WORK}/note.before" "${VAULT}/${NOTE_REL}"
[ "$(fmline "${CARD_REL}" status)" = "${CARD_STATUS_BEFORE}" ] || die "卡的 status 行被改动了"
# ③ 未命中对象（过目信号齐备的第二张卡）字节零变化。
[ "$(rawhash "${CARD2_REL}")" = "${CARD2_HASH_BEFORE}" ] ||
  die "未命中对象 ${CARD2_REL} 被写了（R2 只碰命中对象）"
# ④ 恰一次 commit：3 处外部编辑 + 2 处修复写入 → git log 恰 +1（不是 +2）。
SHA="$(fact_str commit)"
[ -n "${SHA}" ] || { cat "${FACTS}"; die "有修复与外部编辑时必须产生恰一条 commit"; }
[ "$(fact_num commits)" = "1" ] || { cat "${FACTS}"; die "整轮对账的提交条数应恰 1（A-35）"; }
AFTER_LOG="$(commits)"
[ "${AFTER_LOG}" = "$((BEFORE_LOG + 1))" ] ||
  die "git log 条数 ${BEFORE_LOG} → ${AFTER_LOG}，应恰 +1（R1 与 R2 的写入合并为一条）"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "纳管后 git status --porcelain 应为空"; }
SUBJECT="$(gitv log -1 --pretty=%s)"
case "${SUBJECT}" in
  "reconcile(ai-infra): "*) ;;
  *) die "提交主题 = ${SUBJECT}，应以 reconcile(<domain>): 开头（A-30 / KnownVerbs 恒 8）" ;;
esac
# ⑤ 三处外部编辑与两处补写同进这一条 commit（R1 的收件区改动也在内）。
for rel in "${CARD_REL}" "${NOTE_REL}" "${OUT_OF_SCOPE}"; do
  gitv show --stat --oneline HEAD | grep -Fq "${rel}" || die "本次 commit 未含 ${rel}"
done
# ⑥ diff 级反证（task Acceptance 的那条 grep）：卡的 diff 里变化的 frontmatter 键**恰**
#    `reviewed_at`（用户对卡只改了正文，因此这条 diff 里除 reviewed_at 外不该有第二个键）。
[ "$(diff_keys "${CARD_REL}")" = "reviewed_at" ] ||
  { gitv diff HEAD~1 -- "${CARD_REL}"; die "卡的 diff 里变化的键应恰 reviewed_at"; }
# 笔记的 diff 里另有一处 `updated_at` —— 那是**用户自己**手改的（外部编辑 2），
# 不是 R2 写的：证据是修复**之前**的快照 note.before 里已经是新值。R2 的写入面仍只有
# 一个键（键级反证 only_reviewed_at 上面已逐字复算：其余键与正文一字不动）。
grep -Fq "updated_at: '${UPDATED_LATE}'" "${WORK}/note.before" ||
  die "note.before 里应已含用户手改的 updated_at（否则这处 diff 的归属不成立）"
[ "$(diff_keys "${NOTE_REL}")" = "reviewed_at,updated_at" ] ||
  { gitv diff HEAD~1 -- "${NOTE_REL}"; die "笔记的 diff 里变化的键应恰 {reviewed_at, 用户手改的 updated_at}"; }
gitv diff HEAD~1 -- "${CARD_REL}" "${NOTE_REL}" >"${WORK}/repair.diff"
cnt0 '三维被牵连（status / deleted_at / deleted_reason / replaced_by 出现变化行）' \
  -nE '^[+-](status|deleted_at|deleted_reason|replaced_by):' "${WORK}/repair.diff"
# 卡侧（用户只改正文）：`updated_at` 零变化行 —— 补写过目信号**不**顺手更新内容时刻。
gitv diff HEAD~1 -- "${CARD_REL}" >"${WORK}/repair-card.diff"
cnt0 'updated_at 被本 task 额外指定' -nE '^[+-]updated_at:' "${WORK}/repair-card.diff"
# ⑦ 补齐之后 `eg unreviewed` 输出行数 → 0（task Acceptance 的那条判据）。
[ "$(unreviewed_count)" = "0" ] ||
  { unreviewed_ids; die "补齐后 eg unreviewed 输出行数应为 0"; }
ok "只写 reviewed_at=${STAMP}、正文与其他键一字不动、恰 +1 条 commit（${SHA:0:8}）、unreviewed 归零"

# ---------------------------------------------------------------- 6. 幂等
step "幂等：干净工作区再跑一整轮 → 零 finding、零 op、零写入、零 commit"
AFTER_SUM="$(tree_sum)"
drive repair "${STAMP}" idem
[ "$(fact_num reviewed_at_missing)" = "0" ] ||
  { cat "${FACTS}"; die "补齐后不得再报 reviewed_at_missing"; }
[ "$(fact_count '"check":')" = "0" ] || { cat "${FACTS}"; die "干净工作区不得有任何 finding"; }
[ "$(fact_num ops)" = "0" ] || { cat "${FACTS}"; die "无需修复时必须零 op（连 plan 都不组装）"; }
grep -Eq '"written":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "无需修复时必须零写入"; }
[ "$(fact_str commit)" = "" ] || { cat "${FACTS}"; die "无需修复时必须零 commit（不产生空提交）"; }
[ "$(fact_num commits)" = "0" ] || { cat "${FACTS}"; die "无需修复时提交条数必须为 0"; }
grep -Fq '"ran":true' "${FACTS}" || { cat "${FACTS}"; die "零改动分支的 ran 仍应为 true"; }
[ "$(commits)" = "${AFTER_LOG}" ] || die "第二轮改变了 commit 数（应幂等）"
[ "${AFTER_SUM}" = "$(tree_sum)" ] || die "第二轮写盘了（应零写入）"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "第二轮后工作区应仍干净"; }
ok "幂等：0 finding / 0 op / 零写入 / 零 commit（commit 数仍 ${AFTER_LOG}）"

# ---------------------------------------------------------------- 7. B3 不豁免
step "B3 不豁免：注入过期观测 hash → skipped[kind=file_changed]、零写入、finding 仍在"
python3 - "${VAULT}/${CARD_REL}" "${UPDATED_LATER}" <<'PY'
import sys
p, late = sys.argv[1], sys.argv[2]
s = open(p, encoding='utf-8').read()
# 再一次**用户手改**：正文追加一段，并把 updated_at 改新 → 过目信号重新变成「过期」。
s = s.replace("updated_at: '2026-09-01T10:00:00+08:00'", "updated_at: '%s'" % late, 1)
open(p, 'w', encoding='utf-8').write(s + '\n用户在编辑器里手写的又一段（外部编辑 4）。\n')
PY
grep -Fq "updated_at: '${UPDATED_LATER}'" "${VAULT}/${CARD_REL}" || die "未能把卡的 updated_at 改新"
[ "$(porcelain_count)" = "1" ] || { porcelain; die "本步应恰 1 处未提交改动"; }
CARD_HASH_BEFORE_STALE="$(rawhash "${CARD_REL}")"
CARD_REVIEWED_BEFORE_STALE="$(fmline "${CARD_REL}" reviewed_at)"
STALE_LOG="$(commits)"
drive stale "${STAMP_STALE}" stale
[ "$(fact_num reviewed_at_missing)" = "1" ] ||
  { cat "${FACTS}"; die "过期形态应恰 1 条 finding，实得 $(fact_num reviewed_at_missing)"; }
[ "$(fact_num ops)" = "1" ] || { cat "${FACTS}"; die "应合成恰 1 条 op（跳过发生在写前比对）"; }
grep -Fq '"kind":"file_changed"' "${FACTS}" ||
  { cat "${FACTS}"; die "hash 过期应进 skipped[kind=file_changed]（封闭两值之一）"; }
[ "$(fact_count '"kind":"file_changed"')" = "1" ] ||
  { cat "${FACTS}"; die "跳过项应恰 1 条"; }
grep -Fq '"skipped_kinds":["file_changed"]' "${FACTS}" ||
  { cat "${FACTS}"; die "跳过命名只用封闭两值，不得自造第三种 kind"; }
grep -Eq '"written":(\[\]|null)' "${FACTS}" ||
  { cat "${FACTS}"; die "B3 前置比对未通过必须零写入" ; }
grep -Eq '"reviewed":(\[\]|null)' "${FACTS}" ||
  { cat "${FACTS}"; die "被跳过的对象不得计入已补齐清单"; }
# finding 仍在且 detail 注明已跳过（不删条目、不降级）。
grep -Fq '已跳过' "${FACTS}" || { cat "${FACTS}"; die "被跳过的 finding 应在 detail 里注明已跳过"; }
grep -Fq 'kind=file_changed' "${FACTS}" || { cat "${FACTS}"; die "注记应写清跳过 kind"; }
[ "$(fact_count '"severity":"warning"')" -ge 1 ] ||
  { cat "${FACTS}"; die "跳过不得改变 finding 的分级"; }
# 目标文件字节零变化：过目信号仍是上一轮补的那个值，绝不是本轮注入的 STAMP_STALE。
[ "$(rawhash "${CARD_REL}")" = "${CARD_HASH_BEFORE_STALE}" ] ||
  die "被跳过的文件字节变了（B3 不豁免要求本次不写它）"
[ "$(fmline "${CARD_REL}" reviewed_at)" = "${CARD_REVIEWED_BEFORE_STALE}" ] ||
  die "被跳过的文件的 reviewed_at 被改写了"
cnt0 '被跳过的文件仍被写入了本轮时刻' -Fn "${STAMP_STALE}" "${VAULT}/${CARD_REL}"
# 纳管仍只产生恰一次 commit（把用户那处外部编辑记进历史；R2 一格未写）。
[ "$(fact_num commits)" = "1" ] || { cat "${FACTS}"; die "本轮提交条数应恰 1（A-35）"; }
[ "$(commits)" = "$((STALE_LOG + 1))" ] || die "本轮 commit 数应恰 +1"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "纳管后工作区应为空"; }
ok "B3 不豁免：kind=file_changed 恰 1 条、零写入、finding 仍在并注明已跳过、仍恰一次 commit"

# ---------------------------------------------------------------- 8. 源码级边界反证
step "边界反证：走 ChangePlan / 不裸写知识数据 / content_hash 前置未削弱 / 不新增 op 与命令"
R2SRC="${REPO_ROOT}/internal/reconcile/r2_reviewed.go"
BRIDGE="${REPO_ROOT}/internal/cli/reconcile_repair_reviewed.go"
# ① 检查侧三条零（写盘 / 提交 / 子进程）。
cnt0 '检查侧写盘' -rnE 'os\.WriteFile|os\.Create|os\.Remove|os\.Rename' "${R2SRC}"
cnt0 '检查侧提交' -rnE 'Commit\(|git add' "${R2SRC}"
cnt0 '检查侧起子进程' -rnE 'os/exec|exec\.Command' "${R2SRC}"
# ② 修复桥不 import 落盘层、不直呼 setter，写入必须经计划层（task Acceptance 的两条 grep）。
cnt0 '修复桥 import 知识数据落盘层' -n 'internal/store' "${BRIDGE}"
cnt0 '修复桥直呼 setter 或牵连三维' \
  -nE 'SetStatus|SetDeleted|SetReplacedBy|deleted_at|deleted_reason|replaced_by' "${BRIDGE}"
[ "$(grep -cE 'internal/plan|runPlan' "${BRIDGE}")" -ge 1 ] ||
  die "修复桥必须经 internal/plan 落盘（不得裸写知识数据）"
[ "$(grep -c 'ChangePlan' "${BRIDGE}")" -ge 1 ] || die "修复桥必须组装内存 ChangePlan"
[ "$(grep -c 'InitiatorUser' "${BRIDGE}")" -ge 1 ] || die "合成 plan 必须逐字带 initiator=user（A-34）"
# ③ B3 前置比对未削弱：执行路径逐字带 ExpectedHash，且 content_hash 计数不减（基线 41）。
[ "$(grep -c 'ExpectedHash' "${REPO_ROOT}/internal/plan/execute_m4.go")" -ge 1 ] ||
  die "mark_reviewed 的执行路径必须带 ExpectedHash（B3 不豁免）"
C_HASH="$(grep -rn 'ContentHash\|content_hash' "${REPO_ROOT}/internal/store" "${REPO_ROOT}/internal/plan" \
  --include=*.go | grep -v _test.go | wc -l | tr -d ' ')"
[ "${C_HASH}" -ge 41 ] || die "content_hash 前置比对计数 ${C_HASH} < 41（基线不得减少）"
cnt0 '出现跳过 hash 比对的口子' -rn '跳过 hash 比对' "${BRIDGE}" "${REPO_ROOT}/internal/plan/execute_m4.go"
# ④ 不新增 op、不新增命令：op 名恒取既有 mark_reviewed，`eg reconcile` 命令零注册。
[ "$(grep -c 'OpMarkReviewed' "${BRIDGE}")" -ge 1 ] ||
  die "op 名必须取既有 mark_reviewed 的常量真源（A-33，AllOpNames 恒 16）"
cnt0 '修复桥自造 op 名字面量' -nE '"mark_reviewed"|"set_stale"' "${BRIDGE}"
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
# ⑤ ADR-20 文件级隔离不放宽：四个被隔离的包在 internal/reconcile 内零引用。
cnt0 'ADR-20 隔离被放宽' -rn 'query/rank\|query/relations\|rules/converge\|query/review' \
  "${REPO_ROOT}/internal/reconcile/"
# ⑥ 越界能力零引入（.index/ / SQLite / FTS5 / 锁 / 性能门槛）。
cnt0 '检查侧出现越界能力' -rnE '\.index/|FTS5|[Ss][Qq][Ll]ite|flock|P95' "${REPO_ROOT}/internal/reconcile/"
cnt0 '修复桥出现越界能力' -rnE '\.index/|FTS5|[Ss][Qq][Ll]ite|flock|P95' "${BRIDGE}"
# ⑦ R6 的写入（set_stale）属 T-…-055：本 task 的两个实现文件一个字也不涉及。
cnt0 'R2 越界做了 R6 的写入' -rn 'set_stale\|stale_reason' "${R2SRC}" "${BRIDGE}"
ok "走 ChangePlan（零 internal/store 引用）/ ExpectedHash 在链上（content_hash ${C_HASH} ≥ 41）/ 不新增 op 与命令 / ADR-20 未放宽"

printf '\n=== reviewed_backfill.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

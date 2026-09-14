#!/usr/bin/env bash
# R4 结构检查（E11 重复 ID / E12 悬空引用 / W17 孤儿）的端到端脚本
# （T-evergreen.s1_main_flow-158614-052）。
#
# 判据来源：`docs/specs/2026-11-12-m4-reconcile-contract.md` §6（R4 全文：§6.1 重复 ID、
# §6.2 悬空引用「恰两类」、§6.3 孤儿封闭三子类型与**末段的可见性解耦硬约束**）、§1.3 写口
# 归属表第 4 行（R4 归「无人写」→ 零 RepairSpec）、§3（check ↔ 诊断码单射）；
# `docs/specs/2026-11-15-m4-prestart-adjudication.md` 的 **A-31**（有 error 级 finding 退 2）；
# `docs/specs/2026-09-19-m2-query-contract.md` §5.1（Q1 / Q2 是查询域诊断，与 E11 / E12
# 同事实不同域，禁止双计数）；`milestones/M-004-m4.md` 完成判据 5 与风险 R-17。
#
# 五段断言（task deliverable 逐字要求）：
#   ① 造一个**干净**的小 vault（一原文 / 一笔记 / 两张互连卡）→ 只读检查零 finding、
#      按 A-31 换算的退出码为 0；
#   ② 造三处结构问题（同 ID 两文件 + 一条悬空 source + 一张零关系卡）→ 只读检查
#      **E11 / E12 / W17 各恰 1 条**、severity 分别 error / error / warning、
#      孤儿子类型恰 `card_without_relation`、按 A-31 换算的退出码为 **2**（error 级语义）；
#   ③ 零写入：检查前后 `git status --porcelain` 恒为空、commit 数不变、
#      vault 文件树 sha256 逐字不变、零 RepairSpec；
#   ④ 幂等可复算：同一 vault 连跑两次，事实 JSON 逐字节相同；
#   ⑤ 与可见性解耦：把唯一邻居改成 `deprecated`（未删除）→ 该卡**不得**新增孤儿告警；
#      再加一条源码级反证（`VisibleEndpoints` 零引用 / 三子类型串恰 3 / 检查侧三条零 /
#      命令本体零注册）。
#
# 为什么用 `go test` 驱动而不是 `eg check`：
#   `eg check` / `eg reconcile` 命令本体、信封与退出码属 T-…-059 / T-…-058，本 task 明确
#   不注册命令，只交付 R4 三项检查器。脚本因此在真实临时 vault 上用
#   `test/e2e/m4_r4_structure_test.go` 这一层驱动壳调检查器（同 `m4_r1_takeover.sh`
#   的先例），事实一律回读驱动壳落盘的 JSON 与 git 自己，不看实现自报。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go）；
# **一切写操作只发生在 mktemp -d 目录内**；任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/reconcile-takeover/structure.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-r4.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
FACTS="${WORK}/facts.json"

KDIR="${VAULT}/domains/ai-infra/knowledge"
NDIR="${VAULT}/domains/ai-infra/notes"
MLDIR="${VAULT}/domains/ml/knowledge"

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
# seal <说明>：把造好的数据提交进 vault 自己的仓库，使「检查前工作区为空」成立。
seal() { gitv add -A >/dev/null && gitv -c user.name=e2e -c user.email=e2e@local \
  commit -q -m "e2e(r4): $1" >/dev/null; }

# drive <标签>：在真实 vault 上跑驱动壳，事实落 ${FACTS}（另存一份带标签的副本）。
drive() {
  local tag="$1"
  rm -f "${FACTS}"
  ( cd "$(eg_staged_root)" &&
    EG_M4_R4_VAULT="${VAULT}" EG_M4_R4_OUT="${FACTS}" \
    go test ./test/e2e/ -run TestM4R4StructureHarness -count=1 -v </dev/null ) \
    >"${WORK}/drive-${tag}.log" 2>&1 ||
    { tail -30 "${WORK}/drive-${tag}.log"; die "驱动壳（${tag}）未全绿"; }
  grep -Fq -- "--- PASS: TestM4R4StructureHarness" "${WORK}/drive-${tag}.log" ||
    { tail -30 "${WORK}/drive-${tag}.log"; die "驱动壳（${tag}）未 PASS（可能被 skip）"; }
  [ -s "${FACTS}" ] || die "驱动壳（${tag}）未落事实文件"
  cp "${FACTS}" "${WORK}/facts-${tag}.json"
}

fact_num() { sed -E "s/.*\"$1\":([0-9]+).*/\1/" "${FACTS}"; }
fact_count() { grep -o -- "$1" "${FACTS}" | wc -l | tr -d ' '; }
cnt0() { # cnt0 <说明> <grep 参数...>：期望 0 命中
  local name="$1"; shift
  local n; n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 造干净数据
step "建库并造干净数据：1 份原文 + 1 篇笔记 + 2 张互连卡（三类对象都不该被判问题）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${KDIR}" "${NDIR}" "${VAULT}/sources"
cat >"${VAULT}/sources/s-20260901-attention.md" <<'SRC'
---
id: s-20260901-attention
url: https://example.com/attention
title: 注意力机制原文
saved_at: '2026-09-01T09:00:00+08:00'
---

# 注意力机制原文

正文占位（收录后不因加工改写）。
SRC
cat >"${NDIR}/n-20260901-attention.md" <<'NOTE'
---
id: n-20260901-attention
source: s-20260901-attention
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
---

# 注意力机制材料笔记

## 材料提炼

提炼占位。
NOTE
cat >"${KDIR}/k-20260901-attention.md" <<'CARD_A'
---
id: k-20260901-attention
title: 注意力的计算代价
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: s-20260901-attention
    note: n-20260901-attention
    rel: support
    reason: 该结论由这份材料支持
relations:
  - type: supports
    target: k-20260902-rnn
    reason: 支持 RNN 那张卡的结论
---

## 知识内容

注意力的计算量随序列长度平方增长。
CARD_A
cat >"${KDIR}/k-20260902-rnn.md" <<'CARD_B'
---
id: k-20260902-rnn
title: RNN 的长序列衰减
status: active
created_at: '2026-09-02'
updated_at: '2026-09-02T10:00:00+08:00'
sources:
  - source: s-20260901-attention
    note: n-20260901-attention
    rel: support
    reason: 该结论由这份材料支持
relations: []
---

## 知识内容

长序列上信息衰减明显。
CARD_B
seal "干净数据"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "造数后应已入库（工作区为空）"; }
CLEAN_COMMITS="$(commits)"
ok "干净 vault 就绪：commit 数 ${CLEAN_COMMITS}，工作区为空"

# ---------------------------------------------------------------- 2. 干净库：零 finding
step "干净库跑只读检查：零 finding、零 RepairSpec、按 A-31 换算退出码 0"
CLEAN_SUM="$(tree_sum)"
drive clean
[ "$(fact_num cards)" = "2" ] || { cat "${FACTS}"; die "应扫到 2 张卡"; }
[ "$(fact_num notes)" = "1" ] || { cat "${FACTS}"; die "应扫到 1 篇笔记"; }
[ "$(fact_num sources)" = "1" ] || { cat "${FACTS}"; die "应采样到 1 份原文"; }
[ "$(fact_count '"check":')" = "0" ] || { cat "${FACTS}"; die "干净库不得有 finding"; }
[ "$(fact_num duplicate_id)" = "0" ] || { cat "${FACTS}"; die "干净库不得有 E11"; }
[ "$(fact_num dangling_ref)" = "0" ] || { cat "${FACTS}"; die "干净库不得有 E12"; }
[ "$(fact_num orphan)" = "0" ] || { cat "${FACTS}"; die "干净库不得有 W17"; }
grep -Fq '"has_error":false' "${FACTS}" || { cat "${FACTS}"; die "干净库不得有 error 级 finding"; }
[ "$(fact_num exit_code)" = "0" ] || { cat "${FACTS}"; die "零 error 级 finding 时退出码应为 0"; }
grep -Eq '"repairs":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "R4 恒零 RepairSpec"; }
[ "$(porcelain_count)" = "0" ] || { porcelain; die "只读检查后工作区应仍为空"; }
[ "${CLEAN_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了"
ok "干净库：0 finding / 0 repair / exit 0 / 零写入"

# ---------------------------------------------------------------- 3. 造三处结构问题
step "造三处问题：同 ID 两文件（k-dup）+ 悬空 source（n-bad → s-gone）+ 零关系卡（k-lonely）"
mkdir -p "${MLDIR}"
for d in "${KDIR}" "${MLDIR}"; do
  cat >"${d}/k-20260903-dup.md" <<'CARD_DUP'
---
id: k-20260903-dup
title: 同一个 ID 落在两个文件里
status: active
created_at: '2026-09-03'
updated_at: '2026-09-03T10:00:00+08:00'
sources: []
relations:
  - type: supports
    target: k-20260901-attention
    reason: 有落盘出边，因此不是孤儿
---

## 知识内容

同 ID 重复只该被 E11 报一条（全部冲突文件进 targets）。
CARD_DUP
done
cat >"${NDIR}/n-20260904-bad.md" <<'NOTE_BAD'
---
id: n-20260904-bad
source: s-99999999-gone
created_at: '2026-09-04'
updated_at: '2026-09-04T10:00:00+08:00'
---

# 悬空引用的笔记

## 材料提炼

它声明的原文不在 vault 内 → 恰一条 E12（不是孤儿）。
NOTE_BAD
cat >"${KDIR}/k-20260905-lonely.md" <<'CARD_LONELY'
---
id: k-20260905-lonely
title: 一张零关系的卡
status: active
created_at: '2026-09-05'
updated_at: '2026-09-05T10:00:00+08:00'
sources:
  - source: s-20260901-attention
    note: n-20260901-attention
    rel: support
    reason: 有材料来源，因此不是「无来源」那类问题
relations: []
---

## 知识内容

出边入边同时为空 → 恰一条 W17（子类型 card_without_relation）。
CARD_LONELY
seal "三处结构问题"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "造数后应已入库"; }
DIRTY_COMMITS="$(commits)"
DIRTY_SUM="$(tree_sum)"
ok "问题数据入库：commit 数 ${DIRTY_COMMITS}，工作区为空"

# ---------------------------------------------------------------- 4. 三码各恰 1 条
step "只读检查：E11 / E12 / W17 各恰 1 条、单射码与 severity 正确、退出码 2（A-31）"
drive dirty
[ "$(fact_num duplicate_id)" = "1" ] ||
  { cat "${FACTS}"; die "duplicate_id 应恰 1 条，实得 $(fact_num duplicate_id)"; }
[ "$(fact_num dangling_ref)" = "1" ] ||
  { cat "${FACTS}"; die "dangling_ref 应恰 1 条，实得 $(fact_num dangling_ref)"; }
[ "$(fact_num orphan)" = "1" ] ||
  { cat "${FACTS}"; die "orphan 应恰 1 条，实得 $(fact_num orphan)"; }
[ "$(fact_count '"check":')" = "3" ] || { cat "${FACTS}"; die "finding 总数应恰 3"; }
[ "$(fact_count '"E11"')" = "1" ] || { cat "${FACTS}"; die "E11 应恰 1 次（合同 §3 单射）"; }
[ "$(fact_count '"E12"')" = "1" ] || { cat "${FACTS}"; die "E12 应恰 1 次"; }
[ "$(fact_count '"W17"')" = "1" ] || { cat "${FACTS}"; die "W17 应恰 1 次"; }
[ "$(fact_count '"severity":"error"')" = "2" ] || { cat "${FACTS}"; die "error 级应恰 2 条（E11 + E12）"; }
[ "$(fact_count '"severity":"warning"')" = "1" ] || { cat "${FACTS}"; die "warning 级应恰 1 条（W17）"; }
# E11 的 targets：**全部**冲突文件（两条，字典序升序）——不只报第一个。
grep -Fq '"targets":["domains/ai-infra/knowledge/k-20260903-dup.md","domains/ml/knowledge/k-20260903-dup.md"]' \
  "${FACTS}" || { cat "${FACTS}"; die "E11 的 targets 应列出两个冲突文件且升序"; }
# E12 的 targets：[引用方 ID, 缺失目标 ID]。
grep -Fq '"targets":["n-20260904-bad","s-99999999-gone"]' "${FACTS}" ||
  { cat "${FACTS}"; die "E12 的 targets 应为 [引用方 ID, 缺失目标 ID]"; }
# W17 的 targets：恰一个对象 ID；子类型由 detail 承载，本例恰 card_without_relation。
grep -Fq '"targets":["k-20260905-lonely"]' "${FACTS}" ||
  { cat "${FACTS}"; die "W17 的 targets 应恰为 [k-20260905-lonely]"; }
grep -Fq '"orphan_subtypes":["card_without_relation"]' "${FACTS}" ||
  { cat "${FACTS}"; die "本例的孤儿子类型应恰 card_without_relation"; }
cnt0 '悬空的笔记被误判为孤儿' -F '"targets":["n-20260904-bad"]' "${FACTS}"
cnt0 '有派生笔记的原文被误判为孤儿' -F '"targets":["s-20260901-attention"]' "${FACTS}"
grep -Fq '"has_error":true' "${FACTS}" || { cat "${FACTS}"; die "E11 / E12 存在时 has_error 应为 true"; }
[ "$(fact_num exit_code)" = "2" ] ||
  { cat "${FACTS}"; die "有 error 级 finding 时退出码应为 2（A-31），实得 $(fact_num exit_code)"; }
grep -Eq '"repairs":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "R4 只报告，恒零 RepairSpec"; }
ok "三码各恰 1 条、targets 逐字可复算、exit 2、零 RepairSpec"

# ---------------------------------------------------------------- 5. 零写入 + 幂等
step "零写入与幂等：git status 为空、commit 数不变、文件树 sha256 不变、两次事实逐字节相同"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "只读检查后 git status --porcelain 应为空"; }
[ "$(commits)" = "${DIRTY_COMMITS}" ] || die "只读检查改变了 commit 数（R4 零 commit）"
[ "${DIRTY_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了（R4 零写盘、零改名、零删除）"
drive dirty2
cmp -s "${WORK}/facts-dirty.json" "${WORK}/facts-dirty2.json" ||
  { diff "${WORK}/facts-dirty.json" "${WORK}/facts-dirty2.json" || true;
    die "两次检查的事实不同：R4 必须可逐字复算"; }
[ "$(porcelain_count)" = "0" ] || { porcelain; die "第二次检查后工作区应仍为空"; }
[ "${DIRTY_SUM}" = "$(tree_sum)" ] || die "第二次检查写盘了"
ok "零写入零 commit（sha256 逐字不变）+ 两次事实逐字节相同"

# ---------------------------------------------------------------- 6. 与可见性过滤解耦
step "可见性解耦：唯一邻居改成 deprecated（未删除）→ 该卡不得新增孤儿告警（合同 §6.3 末段）"
python3 - "${KDIR}/k-20260902-rnn.md" <<'PY'
import sys
p = sys.argv[1]
s = open(p, encoding='utf-8').read()
# 只改 status 一个键：把「注意力」那张卡的唯一邻居标成失效（deleted_at 保持不存在）。
s = s.replace("status: active", "status: deprecated", 1)
open(p, 'w', encoding='utf-8').write(s)
PY
grep -Fq 'status: deprecated' "${KDIR}/k-20260902-rnn.md" || die "未能把邻居标成 deprecated"
cnt0 '邻居被误加删除标记' -F 'deleted_at' "${KDIR}/k-20260902-rnn.md"
seal "把唯一邻居标成 deprecated"
drive deprecated
[ "$(fact_num orphan)" = "1" ] ||
  { cat "${FACTS}"; die "孤儿条数应仍为 1（deprecated 邻居仍是落盘事实上的边），实得 $(fact_num orphan)"; }
grep -Fq '"orphan_subtypes":["card_without_relation"]' "${FACTS}" ||
  { cat "${FACTS}"; die "孤儿子类型不应变化"; }
cnt0 'deprecated 邻居的持有卡被误判孤儿' -F '"targets":["k-20260901-attention"]' "${FACTS}"
cnt0 'deprecated 卡自身被误判孤儿' -F '"targets":["k-20260902-rnn"]' "${FACTS}"
ok "唯一邻居失效不改变孤儿判定（判定看落盘事实，不看展示面过滤）"

# ---------------------------------------------------------------- 7. 修好即归零
step "修好三处问题 → finding 归零、退出码回 0（判定条件可逆，不留残留告警）"
rm -f "${MLDIR}/k-20260903-dup.md" "${NDIR}/n-20260904-bad.md"
python3 - "${KDIR}/k-20260905-lonely.md" <<'PY'
import sys
p = sys.argv[1]
s = open(p, encoding='utf-8').read()
s = s.replace("relations: []", """relations:
  - type: supports
    target: k-20260901-attention
    reason: 补一条落盘出边，孤儿告警随之消失""", 1)
open(p, 'w', encoding='utf-8').write(s)
PY
seal "修好三处结构问题"
drive fixed
# 2026-09-06 随 M4 · T-…-056（R7 材料支撑不足实时判定）**按实测重钉判据形态**：
# 本驱动壳采样 `sources/` 分区，故同一份输入现在也会跑 R7。三处结构问题修好后，去重只剩一份的
# `k-20260903-dup` **确实**没有任何 `rel=support` 条目 —— 这是 R7 落地后新出现的**真事实**
# （`support_insufficient` / W20 / warning），不是 R4 的回归。判据本体一格不放宽：
# R4 三项 **逐项恒 0**、error 级恒 0、`has_error` 仍为 false、退出码仍回 0；新增三格加严：
# finding 总数恰 1、唯一那条逐字是 W20 / warning、且它不是 R4 的任何一码。
[ "$(fact_num duplicate_id)" = "0" ] || { cat "${FACTS}"; die "三处修好后 E11 应归零"; }
[ "$(fact_num dangling_ref)" = "0" ] || { cat "${FACTS}"; die "三处修好后 E12 应归零"; }
[ "$(fact_num orphan)" = "0" ] || { cat "${FACTS}"; die "三处修好后 W17 应归零"; }
[ "$(fact_count '"severity":"error"')" = "0" ] ||
  { cat "${FACTS}"; die "三处修好后不得有 error 级 finding"; }
[ "$(fact_count '"check":')" = "1" ] ||
  { cat "${FACTS}"; die "三处修好后只应剩 R7 的那 1 条 W20"; }
[ "$(fact_count '"check":"support_insufficient"')" = "1" ] ||
  { cat "${FACTS}"; die "剩下的唯一 finding 必须逐字是 support_insufficient（R7 / T-…-056）"; }
[ "$(fact_count '"W20"')" = "1" ] || { cat "${FACTS}"; die "剩下的唯一诊断码必须是 W20"; }
[ "$(fact_count '"severity":"warning"')" = "1" ] || { cat "${FACTS}"; die "W20 必须是 warning"; }
grep -Fq '"has_error":false' "${FACTS}" || { cat "${FACTS}"; die "零 error 级 finding 时 has_error 应为 false"; }
[ "$(fact_num exit_code)" = "0" ] || { cat "${FACTS}"; die "零 error 级 finding 时退出码应回 0"; }
ok "三处修好 → R4 三项逐项归零 / error 级 0 / exit 0（E11 / E12 / W17 判定条件均可逆），只剩 R7 落地后的 1 条 W20 真事实"

# ---------------------------------------------------------------- 8. 源码级边界反证
step "边界反证：检查侧三条零 / 三子类型恰 3 / VisibleEndpoints 零引用 / 命令本体零注册"
R4SRC="${REPO_ROOT}/internal/reconcile/r4_structure.go"
cnt0 '检查侧写盘或提交' -rnE 'os\.WriteFile|os\.Create|os\.Remove|os\.Rename|Commit\(' "${R4SRC}"
cnt0 '检查侧起子进程' -rnE 'os/exec|exec\.Command' "${R4SRC}"
cnt0 '检查侧调展示面过滤' -rn 'VisibleEndpoints' "${R4SRC}"
cnt0 '检查侧越界依赖' -rnE 'internal/(store|plan|cli|report|proposal)' "${R4SRC}"
cnt0 '检查侧出现越界能力' -rnE '\.index/|FTS5|[Ss][Qq][Ll]ite|flock|P95' "${REPO_ROOT}/internal/reconcile/"
[ "$(grep -oE 'note_without_source|source_without_note|card_without_relation' "${R4SRC}" |
  sort -u | wc -l | tr -d ' ')" = "3" ] || die "孤儿三子类型串应恰 3 个不同取值"
[ "$(grep -cE 'CheckDuplicateID|CheckDanglingRef|CheckOrphan' "${R4SRC}")" -ge 3 ] ||
  die "三项 check 必须引用 check.go 的枚举真源（不得自写字面量）"
# `eg check` / `eg reconcile` 命令本体属 T-…-059 / T-…-058：本 task 不注册命令。
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
# R3 的两个码属 T-…-053：本 task 的实现文件里不得产出它们。
cnt0 'R4 越界产出 R3 的 check' -rnE 'CheckRelationTargetMissing|CheckRelationPrefixInvalid' "${R4SRC}"
ok "检查侧零写盘 / 零 commit / 零子进程 / 零展示面过滤；三子类型恰 3；命令零注册"

printf '\n=== structure.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

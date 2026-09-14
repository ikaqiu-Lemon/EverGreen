#!/usr/bin/env bash
# `eg card show` 端到端脚本（T-evergreen.s1_main_flow-158614-022）。
#
# 判据来源：M2 查询合同 `2026-09-19-m2-query-contract.md`
#   §2.1 定位与退出口径 / §2.2 data 键表 + 五分区键序 + markers
#   §3.2 正向与反向取数（反向 = 全库扫描，不走索引）/ §3.3 两级排序
#   §5.1 Q1 / Q2 诊断（悬空引用与坏文件绝不静默）/ §6 只读零副作用。
#
# 约束：离线、可重复执行、无外部依赖（只用 bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/card-query/card_show.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m2-card.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

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

# 快照：vault 内全部 .md 的「路径 + 字节数 + mtime + sha256」（合同 §6 的 find 快照口径）。
snapshot() {
  eg_snapshot_markdown_metadata "${VAULT}" "$1"
  find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort >>"$1"
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed 语料
step "seed 语料：两张互指的卡 + 一张失效卡（跨领域）+ 一条悬空引用 + 一个坏 .md"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
[ "$(eg_code config set domains ai-infra,ops)" = "0" ] || die "config set domains 失败"

mkdir -p "${VAULT}/domains/ai-infra/knowledge" "${VAULT}/domains/ops/knowledge"
# 三张卡都显式带上 reviewed_at（== updated_at，严格大于才算未过目）：本脚本钉的是 M2 的
# 分区 / 关系 / [失效] 口径，不该被 T-…-043 起启用的过目维度标记连带影响（三维正交）。
# A：五分区齐全 + 一条 sources + 三条正向关系（其中 limits 指向不存在的卡）。
cat >"${VAULT}/domains/ai-infra/knowledge/k-20260901-attention.md" <<'CARD_A'
---
id: k-20260901-attention
status: active
created_at: '2026-09-01'
updated_at: '2026-09-12T10:00:00+08:00'
reviewed_at: '2026-09-12T10:00:00+08:00'
title: 注意力机制的计算代价
tags:
  - attention
sources:
  - source: s-20260901-x
    note: n-20260901-x
    rel: support
    reason: 实测数据支持该结论
relations:
  - type: supports
    target: k-20260902-rnn
    reason: 支持 RNN 那张卡的结论
  - type: opposing
    target: k-20260903-ops
    reason: 与值班经验冲突
  - type: limits
    target: k-20260909-missing
    reason: 指向库中不存在的卡（悬空引用）
---

## 知识内容

注意力的计算量随序列长度平方增长。

## 解释与依据

实测数据见材料笔记。

## 条件与边界

只在自注意力场景成立。

## 用户补充

用户写的内容，任何时候都不许自动改写。

## 理解自检

为什么是平方而不是线性？
CARD_A
# B：只写了「知识内容」一个分区（缺分区必须键仍在、值为空串）+ 反指 A。
cat >"${VAULT}/domains/ai-infra/knowledge/k-20260902-rnn.md" <<'CARD_B'
---
id: k-20260902-rnn
status: active
created_at: '2026-09-02'
updated_at: '2026-09-02T10:00:00+08:00'
reviewed_at: '2026-09-02T10:00:00+08:00'
title: RNN 的长序列衰减
sources: []
relations:
  - type: derives
    target: k-20260901-attention
    reason: 由注意力那张卡推出
---

## 知识内容

长序列上信息衰减明显。
CARD_B
# C：失效卡，且在另一个领域（反向扫描必须跨领域看得见）。
cat >"${VAULT}/domains/ops/knowledge/k-20260903-ops.md" <<'CARD_C'
---
id: k-20260903-ops
status: deprecated
created_at: '2026-09-03'
updated_at: '2026-09-03T10:00:00+08:00'
reviewed_at: '2026-09-03T10:00:00+08:00'
title: 运维值班注意力分配
sources: []
---

## 知识内容

值班时注意力分配的老经验。
CARD_C
printf -- '---\n- 1\n---\n\n# 坏卡\n' >"${VAULT}/domains/ai-infra/knowledge/broken.md"

git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg add -A
git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg commit -q -m "seed: m2 card show 语料"
[ -z "$(git -C "${VAULT}" status --porcelain)" ] || die "前置条件失败：工作区应干净"
ok "4 个 .md（3 张卡 + 1 个坏文件）入库，工作区干净"

# ---------------------------------------------------------------- 2. 零副作用前置快照
step "只读零副作用：取展示前的 git / 文件快照（合同 §6）"
BEFORE_STATUS="$(git -C "${VAULT}" status --porcelain)"
BEFORE_LOG="$(git -C "${VAULT}" log --oneline | wc -l)"
snapshot "${WORK}/before.txt"
ok "git status / git log 条数 / .md 字节与 mtime 快照就绪"

# ---------------------------------------------------------------- 3. 单卡视图 JSON
step "eg card show <k-id> --json：14 键齐备、五分区键序固定、sources / 正向关系"
[ "$(eg_code card show k-20260901-attention --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "card show --json 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/a.json"
for k in '"id"' '"title"' '"domain"' '"status"' '"deprecated"' '"created_at"' '"updated_at"' \
  '"path"' '"tags"' '"markers"' '"sections"' '"sources"' '"relations_out"' '"relations_in"'; do
  grep -Fq -- "${k}" "${WORK}/a.json" || die "--json 缺合同 §2.2 的键 ${k}"
done
# 五分区键序固定为声明序（F5）：用一条正则逐字比对次序。
grep -Eq '"sections":\{"知识内容":.*"解释与依据":.*"条件与边界":.*"用户补充":.*"理解自检":' \
  "${WORK}/a.json" || { cat "${WORK}/a.json"; die "sections 键序不是五分区声明序"; }
grep -Fq '"source":"s-20260901-x"' "${WORK}/a.json" || die "sources[] 未原样透出"
grep -Fq '"rel":"support"' "${WORK}/a.json" || die "sources[].rel 未原样透出"
# 正向关系：type 固定次序（opposing → limits → supports），元素恰五键。
#
# 2026-09-08 随 M5 · T-…-069 **阶段化重钉**（只改形态，M2 §3.3 的结论一格不放宽、不删断言）：
# M4 · T-…-061 的 owner 裁决 ② A-38 / A-39 把「deprecated 对端**默认隐藏**」写进查询合同
# §5.1，语料里的 C（`k-20260903-ops`，status: deprecated）因此不在**默认视图**里。这不是
# M2 §3.3 排序口径变了，而是**可见集合**变了（可见性在排序之前）。故拆成两侧双向锁：
#   ① 历史基线（M2 §3.3 原判据**原样复算**）：把 M4 §5.1 新增的「默认隐藏」摘掉
#      （`--include-deprecated`）后，三条正向关系必须**逐字**仍是
#      opposing → limits → supports，from 恒本卡、元素恰五键 —— 一个字未删；
#   ② 当前值（M4/M5 现状的**正面**断言，新增等号 = 加严）：默认视图恰 2 条且次序逐字
#      limits → supports，C 恰因 deprecated 缺席；同时 `--include-deprecated` 视图必须
#      **恰好**比默认视图多 C 这一条（差集等号），漏隐藏 / 多隐藏都当场红。
# 另加第 ③ 格（M5 · T-…-068 的四级全序**封闭性**静态复算，与本脚本的关系输出同源）：
# `internal/query/rank.go` 的排序键清单必须恰四级、键名与次序逐字，第五键即红。
edges_of() { # $1=只含一个 relations_out 数组的文件 → 逐行 "type→target"（JSON 键序 from,type,target,reason,path）
  grep -oE '"type":"[a-z_]+","target":"[^"]*"' "$1" |
    sed 's/"type":"\([a-z_]*\)","target":"\([^"]*\)"/\1→\2/'
}
# ①-a 默认视图：恰 2 条、次序逐字 limits → supports（deprecated 对端 C 默认隐藏）。
printf 'limits→k-20260909-missing\nsupports→k-20260902-rnn\n' >"${WORK}/want_out_default.txt"
grep -o '"relations_out":\[[^]]*\]' "${WORK}/a.json" >"${WORK}/rel_out_default.json" ||
  die "a.json 缺 relations_out 数组"
edges_of "${WORK}/rel_out_default.json" >"${WORK}/got_out_default.txt"
diff -u "${WORK}/want_out_default.txt" "${WORK}/got_out_default.txt" ||
  { cat "${WORK}/a.json"; die "默认 relations_out 不是恰 2 条 limits → supports（§5.1 deprecated 默认隐藏）"; }
grep -Fq 'k-20260903-ops' "${WORK}/rel_out_default.json" &&
  die "默认视图泄漏了 deprecated 对端 k-20260903-ops（§5.1 失守）"
# ①-b 历史基线原样复算：摘掉「默认隐藏」后，M2 §3.3 的三条次序逐字未动。
[ "$(eg_code card show k-20260901-attention --json --include-deprecated)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "card show --json --include-deprecated 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/a_all.json"
grep -Eq '"relations_out":\[\{"from":"k-20260901-attention","type":"opposing",.*"type":"limits",.*"type":"supports"' \
  "${WORK}/a_all.json" ||
  { cat "${WORK}/a_all.json"; die "relations_out 次序不符合合同 §3.3（历史基线复算失败）"; }
printf 'opposing→k-20260903-ops\nlimits→k-20260909-missing\nsupports→k-20260902-rnn\n' \
  >"${WORK}/want_out_all.txt"
grep -o '"relations_out":\[[^]]*\]' "${WORK}/a_all.json" >"${WORK}/rel_out_all.json" ||
  die "a_all.json 缺 relations_out 数组"
edges_of "${WORK}/rel_out_all.json" >"${WORK}/got_out_all.txt"
diff -u "${WORK}/want_out_all.txt" "${WORK}/got_out_all.txt" ||
  { cat "${WORK}/a_all.json"; die "--include-deprecated 的三条次序与 M2 §3.3 不逐字相等"; }
# ①-c 差集等号：两视图之差**恰**是 C 那一条（不是「少了点什么」而是「少了它」）。
sort "${WORK}/got_out_all.txt" >"${WORK}/s_all.txt"
sort "${WORK}/got_out_default.txt" >"${WORK}/s_def.txt"
DIFF_ONLY="$(comm -23 "${WORK}/s_all.txt" "${WORK}/s_def.txt" | tr '\n' ' ')"
[ "${DIFF_ONLY}" = "opposing→k-20260903-ops " ] ||
  die "两视图差集 = 「${DIFF_ONLY}」，应恰 {opposing→k-20260903-ops}"
# ①-d 元素恰五键（两个视图都不许扩张 data 元素键集合，A-38）。
for f in "${WORK}/rel_out_default.json" "${WORK}/rel_out_all.json"; do
  N="$(tr ',' '\n' <"${f}" | grep -cE '"(from|type|target|reason|path)":' || true)"
  M="$(tr ',' '\n' <"${f}" | grep -cE '"[a-z_]+":' || true)"
  [ "${N}" = "${M}" ] || die "relations_out 元素键集合被扩张（合同键 ${N} / 实际 ${M}）：${f}"
done
# ③ 四级全序封闭性（M5 · T-…-068 / M-005 判据 11）：恰四级、键名与次序逐字，第五键即红。
RANK="${REPO_ROOT}/internal/query/rank.go"
grep -Fq 'const SortKeyCount = 4' "${RANK}" || die "rank.go 的 SortKeyCount 不是恰 4（第五键必须回合同补裁决）"
printf 'SortKeyRelationType\nSortKeyPeerID\nSortKeyPath\nSortKeyEdgeID\n' >"${WORK}/want_sortkeys.txt"
sed -n '/^var sortKeyOrder = \[\]string{/,/^}/p' "${RANK}" |
  grep -oE 'SortKey[A-Za-z]+' >"${WORK}/got_sortkeys.txt"
diff -u "${WORK}/want_sortkeys.txt" "${WORK}/got_sortkeys.txt" ||
  die "rank.go 的四级排序键清单被改名 / 重排 / 增删"
grep -Fq '"relations_in"' "${WORK}/a_all.json" || die "--include-deprecated 视图缺 relations_in 键"
grep -Fq '"exit_code":0' "${WORK}/a.json" || die "正常路径必须退 0"
grep -Fq '"exit_code":0' "${WORK}/a_all.json" || die "--include-deprecated 也必须退 0"
ok "14 键齐备；sections 键序 = 五分区声明序；sources / relations_out 双视图双向锁（默认 2 条 / 放开 3 条 = M2 §3.3 原序）+ 四级键恰 4"

# ---------------------------------------------------------------- 4. 反向关系（全库扫描）
step "反向关系：A 指向 B → card show B 的 relations_in[] 含 from == A（跨领域亦然）"
[ "$(eg_code card show k-20260902-rnn --json)" = "0" ] || die "card show B 应退 0"
grep -Fq '"relations_in":[{"from":"k-20260901-attention","type":"supports"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "relations_in 缺 A 指过来的条目"; }
[ "$(eg_code card show k-20260902-rnn)" = "0" ] || die "card show B（文本）应退 0"
grep -Fq '反向关系：k-20260901-attention --supports--> k-20260902-rnn' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "文本模式缺反向关系条目"; }
# 跨领域：C 在 ops 领域，A 在 ai-infra；opposing 单向存储，读路径不补对称条目。
[ "$(eg_code card show k-20260903-ops --json)" = "0" ] || die "card show C 应退 0"
grep -Fq '"relations_out":[]' "${WORK}/out.txt" || die "opposing 不得在对端补出对称正向条目"
grep -Fq '"relations_in":[{"from":"k-20260901-attention","type":"opposing"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "跨领域反向关系未被全库扫描发现"; }
ok "反向关系来自全库扫描（跨领域可见）；opposing 单向存储口径守住"

# ---------------------------------------------------------------- 5. 缺分区 + 失效标记
step "缺分区键仍在值为空串；失效卡 markers = [失效] 且 deprecated: true（合同 §2.2）"
[ "$(eg_code card show k-20260902-rnn --json)" = "0" ] || die "card show B 应退 0"
grep -Fq '"解释与依据":""' "${WORK}/out.txt" || die "缺分区应「键仍在、值为空串」"
[ "$(eg_code card show k-20260902-rnn)" = "0" ] || die "card show B（文本）应退 0"
grep -Fq '分区 解释与依据：（本分区缺失）' "${WORK}/out.txt" || die "文本模式缺「（本分区缺失）」标注"
[ "$(eg_code card show k-20260903-ops)" = "0" ] || die "card show C（文本）应退 0"
grep -q '^\[失效\]' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "文本模式缺 [失效] 前缀"; }
for m in '[已删除]' '[未过目]' '[材料支持不足]'; do
  if grep -Fq -- "${m}" "${WORK}/out.txt"; then die "M2 不得输出 ${m}（S2 / S3）"; fi
done
[ "$(eg_code card show k-20260903-ops --json)" = "0" ] || die "card show C --json 应退 0"
grep -Fq '"deprecated":true' "${WORK}/out.txt" || die "--json 缺 deprecated: true"
grep -Fq '"markers":["[失效]"]' "${WORK}/out.txt" || die "--json 的 markers 应为 [\"[失效]\"]"
ok "缺分区不丢键；失效卡文本标 [失效]、JSON 标 markers + deprecated，无 S2/S3 标记"

# ---------------------------------------------------------------- 6. Q 类诊断
step "Q 系列：悬空引用记 Q2、坏 .md 记 Q1 + Q3 汇总，退出码仍 0（合同 §5）"
[ "$(eg_code card show k-20260901-attention --json)" = "0" ] || die "带 Q 类 warning 仍必须退 0"
grep -Fq '"code":"Q2"' "${WORK}/out.txt" || die "warnings[] 缺 Q2（悬空引用）"
grep -Fq 'k-20260909-missing' "${WORK}/out.txt" || die "Q2 未点名悬空目标 ID"
grep -Fq '"code":"Q1"' "${WORK}/out.txt" || die "warnings[] 缺 Q1（坏 .md）"
grep -Fq 'domains/ai-infra/knowledge/broken.md' "${WORK}/out.txt" || die "Q1 缺坏文件路径"
grep -Fq '"code":"Q3"' "${WORK}/out.txt" || die "warnings[] 缺 Q3 汇总"
grep -Fq '"exit_code":0' "${WORK}/out.txt" || die "Q 类不得改变退出码"
[ "$(eg_code card show k-20260901-attention)" = "0" ] || die "文本模式应退 0"
grep -Fq 'k-20260909-missing（目标不存在）' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "文本模式应保留悬空条目并标注「目标不存在」"; }
ok "Q1（坏文件路径）+ Q2（悬空目标 ID）+ Q3 汇总俱在，退出码仍 0，悬空条目照实展示"

# ---------------------------------------------------------------- 7. 退出码边界
step "退出码边界：卡不存在 / ID 非法 / 参数个数不对 / 子命令缺失一律退 1（无 2/3/4）"
[ "$(eg_code card show k-20260909-missing)" = "1" ] || die "卡不存在应退 1"
grep -Fq 'k-20260909-missing' "${WORK}/err.txt" || die "stderr 应给出可定位提示"
[ "$(eg_code card show not-an-id)" = "1" ] || die "ID 形态非法应退 1"
[ "$(eg_code card show s-20260901-x)" = "1" ] || die "非 k- 前缀 ID 应退 1"
[ "$(eg_code card show)" = "1" ] || die "缺位置参数应退 1"
[ "$(eg_code card show a b)" = "1" ] || die "多余位置参数应退 1"
[ "$(eg_code card)" = "1" ] || die "缺子命令应退 1"
[ "$(eg_code card list)" = "1" ] || die "未登记的子命令应退 1"
ok "六类错误路径一律退 1（只读命令不产生 2 / 3 / 4）"

# ---------------------------------------------------------------- 8. 零副作用复核
step "只读零副作用复核：git status / git log / 文件字节与 mtime 逐一相等（合同 §6）"
[ "${BEFORE_STATUS}" = "$(git -C "${VAULT}" status --porcelain)" ] || die "git status 前后不同"
[ "${BEFORE_LOG}" = "$(git -C "${VAULT}" log --oneline | wc -l)" ] || die "git log 条数变了"
snapshot "${WORK}/after.txt"
diff -u "${WORK}/before.txt" "${WORK}/after.txt" >"${WORK}/diff.txt" ||
  { cat "${WORK}/diff.txt"; die "vault 内 .md 的字节 / mtime / sha256 发生变化"; }
ok "零文件变化、零 commit（含 sha256 逐文件比对）"

printf '\n=== card_show.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

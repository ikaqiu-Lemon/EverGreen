#!/usr/bin/env bash
# `eg rel` 读路径端到端脚本（T-evergreen.s1_main_flow-158614-023）。
#
# 判据来源：M2 查询合同 `2026-09-19-m2-query-contract.md`
#   §3.1 用法 / data 键表（id / relations_out / relations_in / scanned_files / skipped_files）
#        + 条目五键（from / type / target / reason / path）+ `--to` 过滤口径
#   §3.2 取数：正向 = 本卡 frontmatter relations[]；反向 = **全库 Markdown 扫描**（不走索引）
#   §3.3 排序：type 固定次序 opposing → limits → supports → derives，再对端 ID 升序
#   §5.1 Q1 / Q2 / Q3（坏文件与悬空引用绝不静默）/ §6 只读零副作用。
#
# 约束：离线、可重复执行、无外部依赖（只用 bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/relation/rel_query.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m2-rel.XXXXXX")"
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

# index_entries：`.index/` 下的条目集合（目录不存在即空串）。
index_entries() {
  if [ -d "${VAULT}/.index" ]; then ( cd "${VAULT}/.index" && ls -A | sort | tr '\n' ' ' ); fi
}
# no_derived_index <说明>：M2 读路径**不引索引**（合同 §3.2「反向 = 全库 Markdown 扫描，不走索引」）。
# 现态口径重钉（I-…-030）：I-…-021 之后 A 类写命令（init / config set）按 §16.1/S1 取
# `vault/.index/run.lock`，锁层按 A-53 **按需**建 runtime-reserved 条目 —— 「建 `.index/` 目录」
# ≠「建索引」。因此判据从「`.index/` 必须不存在」收紧成两条更精确的：
#   ① 派生索引 DB 家族恒不存在（eg.db / eg.db-wal / eg.db-shm）——只有显式 `eg index build` 才建；
#   ② `.index/` 若存在，**只许**锁层两项（run.lock / txn），出现任何第三项当场红（§16.3）。
# 口径与 tests/e2e/index-search/index_consistency.sh 的 no_derived_index 同源。
no_derived_index() {
  local why="$1" e
  for e in eg.db eg.db-wal eg.db-shm; do
    [ ! -e "${VAULT}/.index/${e}" ] ||
      die "${why}：M2 读路径建出了派生索引 DB ${e}（建索引只走显式 eg index build，属 S4）"
  done
  [ -e "${VAULT}/.index" ] || return 0
  for e in $( ( cd "${VAULT}/.index" && ls -A | sort ) ); do
    case "${e}" in
      run.lock|txn) ;;
      *) die "${why}：.index/ 出现非 runtime-reserved 条目「${e}」（只许锁层 run.lock / txn，§16.3）" ;;
    esac
  done
  return 0
}

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
step "seed 关系语料：A→B(supports)、A→C(opposing)、B→A(opposing)、C→B(limits)、C→悬空 + 一个坏 .md"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
[ "$(eg_code config set domains ai-infra,ops)" = "0" ] || die "config set domains 失败"

mkdir -p "${VAULT}/domains/ai-infra/knowledge" "${VAULT}/domains/ops/knowledge"
cat >"${VAULT}/domains/ai-infra/knowledge/k-20260901-attention.md" <<'CARD_A'
---
id: k-20260901-attention
status: active
created_at: '2026-09-01'
updated_at: '2026-09-12T10:00:00+08:00'
title: 注意力机制的计算代价
sources: []
relations:
  - type: supports
    target: k-20260902-rnn
    reason: 支持 RNN 那张卡的结论
  - type: opposing
    target: k-20260903-ops
    reason: 与值班经验冲突
---

## 知识内容

注意力的计算量随序列长度平方增长。
CARD_A
cat >"${VAULT}/domains/ai-infra/knowledge/k-20260902-rnn.md" <<'CARD_B'
---
id: k-20260902-rnn
status: active
created_at: '2026-09-02'
updated_at: '2026-09-02T10:00:00+08:00'
title: RNN 的长序列衰减
sources: []
relations:
  - type: opposing
    target: k-20260901-attention
    reason: 与注意力那张卡的结论冲突
---

## 知识内容

长序列上信息衰减明显。
CARD_B
# C 落在另一个领域：反向扫描必须跨领域看得见（ScanOptions.Domains = nil）。
cat >"${VAULT}/domains/ops/knowledge/k-20260903-ops.md" <<'CARD_C'
---
id: k-20260903-ops
status: active
created_at: '2026-09-03'
updated_at: '2026-09-03T10:00:00+08:00'
title: 运维值班注意力分配
sources: []
relations:
  - type: limits
    target: k-20260902-rnn
    reason: 限定 RNN 结论的适用范围
  - type: derives
    target: k-20260909-missing
    reason: 指向库中不存在的卡（悬空引用）
---

## 知识内容

值班时注意力分配的老经验。
CARD_C
printf -- '---\n- 1\n---\n\n# 坏卡\n' >"${VAULT}/domains/ai-infra/knowledge/broken.md"

git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg add -A
git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg commit -q -m "seed: m2 rel query 语料"
[ -z "$(git -C "${VAULT}" status --porcelain)" ] || die "前置条件失败：工作区应干净"
ok "4 个 .md（3 张卡 + 1 个坏文件）入库，工作区干净"

# ---------------------------------------------------------------- 2. 零副作用前置快照
step "只读零副作用：取查询前的 git / 文件快照（合同 §6）"
BEFORE_STATUS="$(git -C "${VAULT}" status --porcelain)"
BEFORE_LOG="$(git -C "${VAULT}" log --oneline | wc -l)"
snapshot "${WORK}/before.txt"
# `.index/` 条目基线：建库阶段的 A 类写命令可能已按 A-53 建出锁层 run.lock；
# 之后的**只读**查询一条都不许让这个集合增长（I-…-030）。
IDX_ENTRIES_BEFORE="$(index_entries)"
no_derived_index "建库之后（查询前）"
ok "git status / git log 条数 / .md 字节与 mtime + sha256 快照就绪；.index/ 条目基线「${IDX_ENTRIES_BEFORE:-（空）}」"

# ---------------------------------------------------------------- 3. 正向查询
step "eg rel <k-id> --json：data 五键序 + 条目五键 + 正向 opposing 先于 supports（§3.1 / §3.3）"
[ "$(eg_code rel k-20260901-attention --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "rel --json 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/a.json"
grep -Eq '"data":\{"id":.*"relations_out":.*"relations_in":.*"scanned_files":.*"skipped_files":' \
  "${WORK}/a.json" || { cat "${WORK}/a.json"; die "data 键序不符合合同 §3.1"; }
grep -Eq '"relations_out":\[\{"from":"k-20260901-attention","type":"opposing","target":"k-20260903-ops","reason":".*","path":"' \
  "${WORK}/a.json" || { cat "${WORK}/a.json"; die "正向首条应为 opposing 且条目键序为五键"; }
grep -Eq '"type":"opposing".*"type":"supports"' "${WORK}/a.json" ||
  { cat "${WORK}/a.json"; die "正向次序必须 opposing → supports（§3.3）"; }
grep -Fq '"exit_code":0' "${WORK}/a.json" || die "正常路径必须退 0"
ok "data 五键序 + 条目五键齐备；正向 = 本卡 relations[] 且 type 次序守合同"

# ---------------------------------------------------------------- 4. 反向全库扫描
step "反向：rel B 的 relations_in[] 恰两条且 limits(C) → supports(A)，跨领域可见（M2 判据 4）"
[ "$(eg_code rel k-20260902-rnn --json)" = "0" ] || die "rel B --json 应退 0"
cp "${WORK}/out.txt" "${WORK}/b.json"
grep -Eq '"relations_in":\[\{"from":"k-20260903-ops","type":"limits","target":"k-20260902-rnn"' \
  "${WORK}/b.json" || { cat "${WORK}/b.json"; die "反向首条应为 limits(C)（跨领域）"; }
grep -Eq '"relations_in":\[.*"from":"k-20260903-ops".*"from":"k-20260901-attention","type":"supports"' \
  "${WORK}/b.json" || { cat "${WORK}/b.json"; die "反向次序必须 limits(C) → supports(A)"; }
# opposing 单向存储：B 自己写的那条只出现在 B 的正向侧，不在 A 的正向侧被补出。
grep -Eq '"relations_out":\[\{"from":"k-20260902-rnn","type":"opposing","target":"k-20260901-attention"' \
  "${WORK}/b.json" || die "B 的正向应恰含自己写的 opposing 条目"
[ "$(eg_code rel k-20260902-rnn)" = "0" ] || die "rel B（文本）应退 0"
grep -Fq '反向关系：k-20260903-ops --limits--> k-20260902-rnn' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "文本模式缺反向条目 limits(C)"; }
grep -Fq '反向关系：k-20260901-attention --supports--> k-20260902-rnn' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "文本模式缺反向条目 supports(A)"; }
[ "$(grep -c '反向关系：' "${WORK}/out.txt")" = "2" ] || die "文本模式的反向条目应恰两条"
ok "反向来自全库扫描（跨领域可见）恰两条且次序守合同；opposing 单向存储口径守住"

# ---------------------------------------------------------------- 5. --to 过滤
step "--to：正反向同时过滤；对端不存在 → 结果为空 + 一条 Q2，退出码仍 0（§3.1）"
[ "$(eg_code rel k-20260902-rnn --to k-20260901-attention --json)" = "0" ] || die "--to 应退 0"
grep -Eq '"relations_in":\[\{"from":"k-20260901-attention","type":"supports"[^]]*\}\]' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "--to 过滤后的反向应恰保留 supports(A) 一条"; }
if grep -Fq '"from":"k-20260903-ops"' "${WORK}/out.txt"; then die "--to 未过滤掉 limits(C)"; fi
[ "$(eg_code rel k-20260903-ops --to k-20260909-missing --json)" = "0" ] ||
  die "--to 指向不存在的卡仍应退 0"
grep -Fq '"relations_out":[]' "${WORK}/out.txt" || die "--to 不存在时正向应为空（§3.1）"
grep -Fq '"relations_in":[]' "${WORK}/out.txt" || die "--to 不存在时反向应为空（§3.1）"
grep -Fq '"code":"Q2"' "${WORK}/out.txt" || die "--to 不存在应记一条 Q2"
grep -Fq 'k-20260909-missing' "${WORK}/out.txt" || die "Q2 未点名 --to 的目标 ID"
ok "--to 正反向同时过滤；对端不存在时结果为空 + Q2，退出码仍 0"

# ---------------------------------------------------------------- 6. Q 类诊断
step "Q 系列：悬空引用 Q2（条目照实展示）、坏 .md 记 Q1 + Q3 汇总，退出码仍 0（合同 §5）"
[ "$(eg_code rel k-20260903-ops --json)" = "0" ] || die "带 Q 类 warning 仍必须退 0"
cp "${WORK}/out.txt" "${WORK}/c.json"
grep -Fq '"code":"Q2"' "${WORK}/c.json" || die "warnings[] 缺 Q2（悬空引用）"
grep -Fq 'k-20260909-missing' "${WORK}/c.json" || die "Q2 未点名悬空目标 ID"
grep -Fq '"code":"Q1"' "${WORK}/c.json" || die "warnings[] 缺 Q1（坏 .md）"
grep -Fq 'domains/ai-infra/knowledge/broken.md' "${WORK}/c.json" || die "Q1 缺坏文件相对路径"
grep -Fq '"code":"Q3"' "${WORK}/c.json" || die "warnings[] 缺 Q3 汇总"
grep -Fq '"scanned_files":4' "${WORK}/c.json" || die "scanned_files 应为 4（含坏文件）"
grep -Fq '"skipped_files":1' "${WORK}/c.json" || die "skipped_files 应为 1（坏文件计入）"
grep -Fq '"exit_code":0' "${WORK}/c.json" || die "Q 类不得改变退出码"
grep -Fq '"target":"k-20260909-missing"' "${WORK}/c.json" || die "悬空条目不得被静默丢弃"
[ "$(eg_code rel k-20260903-ops)" = "0" ] || die "rel C（文本）应退 0"
grep -Fq 'k-20260909-missing（目标不存在）' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "文本模式应保留悬空条目并标注「目标不存在」"; }
# M2 不引索引：反证与源码 grep 互补，运行期也不得建派生索引（现态口径见 no_derived_index / I-…-030）。
no_derived_index "M2 只读查询"
[ "$(index_entries)" = "${IDX_ENTRIES_BEFORE}" ] ||
  die "只读查询让 .index/ 条目集合长了（基线「${IDX_ENTRIES_BEFORE:-（空）}」→ 实得「$(index_entries)」）"
ok "Q1（坏文件路径）+ Q2（悬空目标 ID）+ Q3 汇总俱在；计数守恒；退出码仍 0；无派生索引 DB 且 .index/ 条目零增长"

# ---------------------------------------------------------------- 7. 读路径错误一律退 1
# **T-…-044 重钉**：本段原先断言 `rel remove` 的阶段占位（退 1 + 逐字未实现文案）。
# 占位被接管为真实写路径后，该断言与实现自相矛盾；本脚本是**只读**脚本（第 8 步要求零文件变化），
# 因此这里只保留「写子命令的参数形态错误仍退 1 且不落进读路径」这条，真实写入的取证在
# m3_rel_remove.sh（命中删除 / W10 / Agent 路径 / 重跑幂等四段）。
step "rel remove 的参数形态错误仍退 1 且不落读路径；读路径错误一律退 1（无 2/3/4）"
for sub in "rel remove k-20260901-attention supports k-20260902-rnn"; do
  # 缺 --reason → 参数非法退 1、零写入（本脚本零文件变化的前提）
  # shellcheck disable=SC2086
  [ "$(eg_code ${sub})" = "1" ] || die "eg ${sub} 缺 --reason 应退 1"
  if grep -Fq '未实现' "${WORK}/out.txt" "${WORK}/err.txt"; then
    die "eg ${sub} 不得再宣告阶段未实现"
  fi
  if grep -Fq 'relations_out' "${WORK}/out.txt" "${WORK}/err.txt"; then
    die "eg ${sub} 泄漏了读路径字段（不得落进读路径）"
  fi
done
[ "$(eg_code rel k-20260909-missing)" = "1" ] || die "卡不存在应退 1"
grep -Fq 'k-20260909-missing' "${WORK}/err.txt" || die "stderr 应给出可定位提示"
[ "$(eg_code rel not-an-id)" = "1" ] || die "ID 形态非法应退 1"
[ "$(eg_code rel s-20260901-x)" = "1" ] || die "非 k- 前缀 ID 应退 1"
[ "$(eg_code rel)" = "1" ] || die "缺位置参数应退 1"
[ "$(eg_code rel k-20260901-attention extra)" = "1" ] || die "多余位置参数应退 1"
[ "$(eg_code rel k-20260901-attention --to not-an-id)" = "1" ] || die "--to 形态非法应退 1"
ok "remove 的参数错误退 1 且不落读路径；七类错误路径一律退 1"

# ---------------------------------------------------------------- 8. 零副作用复核
step "只读零副作用复核：git status / git log / 文件字节与 mtime + sha256 逐一相等（合同 §6）"
[ "${BEFORE_STATUS}" = "$(git -C "${VAULT}" status --porcelain)" ] || die "git status 前后不同"
[ "${BEFORE_LOG}" = "$(git -C "${VAULT}" log --oneline | wc -l)" ] || die "git log 条数变了"
snapshot "${WORK}/after.txt"
diff -u "${WORK}/before.txt" "${WORK}/after.txt" >"${WORK}/diff.txt" ||
  { cat "${WORK}/diff.txt"; die "vault 内 .md 的字节 / mtime / sha256 发生变化"; }
ok "零文件变化、零 commit（含 sha256 逐文件比对）"

printf '\n=== rel_query.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

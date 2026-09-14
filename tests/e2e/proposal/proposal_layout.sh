#!/usr/bin/env bash
# 提案落盘形态端到端脚本（T-evergreen.s1_main_flow-158614-033）。
#
# 判据来源：提案合同 `2026-10-10-m3-proposal-state-contract.md`
#   §7.1 目录与文件名 / §7.2 frontmatter 键（含新定的 written_paths / unwritten_paths）
#   §7.3 正文恰 7 个 H2 / §10.3 提案不进知识扫描范围、eg context 只给摘要
# 以及 `2026-10-13-m3-prestart-adjudication.md` §7 的 A-23 裁决
#   （提案控制面走 CLI 直写例外；本 task 只落形态，`eg proposal new` 属 T-…-040，
#    因此本脚本用**夹具直接落**一个提案文件，不调用尚未存在的子命令）。
#
# 四组断言：
#   ① 路径与文件名：proposals/p-<yyyymmdd>-<3d>.md，一项一文件、不属于任何领域；
#   ② frontmatter 键集合：顶层恰 8 键 + 三个嵌套块的子键，且**不含**
#      reviewed_at / deleted_at / deleted_reason；
#   ③ 正文恰 7 个 H2，顺序逐字固定；
#   ④ 提案不进检索（eg search / card show / rel 均看不到它），
#      eg context 只看到摘要（提案正文哨兵串零泄漏），且全程只读零副作用。
#
# 约束：离线、可重复执行、无外部依赖（只用 bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/proposal/proposal_layout.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-proposal.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

# 提案正文哨兵：它一旦出现在任何查询 / 上下文输出里，就说明提案正文泄漏了。
SENTINEL='SENTINEL-PROPOSAL-BODY-MUST-NOT-LEAK'
PID='p-20260701-001'
PREL="proposals/${PID}.md"

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

snapshot() {
  find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort >"$1"
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 语料
step "eg init 建库 + 收录一篇原文 + 一张同领域知识卡"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
# eg init 不创建 proposals/（S2 目录，M1 明确不建）：这里逐条反证它确实没被建出来。
[ ! -d "${VAULT}/proposals" ] || die "eg init 不应创建 proposals/（S2）"
printf '注意力机制的正文占位，供 M3 提案形态用例使用。\n' >"${WORK}/body.txt"
[ "$(eg_code capture --url https://example.com/attention --title '注意力机制入门' \
  --body-file "${WORK}/body.txt" --reason 'T-033 语料' --domain ai-infra \
  --captured-at 2026-09-01T09:00:00+08:00 --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "capture 失败"; }
SRC="$(sed 's/.*"source_id":"\([^"]*\)".*/\1/' "${WORK}/out.txt")"
[ -n "${SRC}" ] || die "capture 未返回 source_id"
mkdir -p "${VAULT}/domains/ai-infra/knowledge"
{
  printf -- '---\nid: k-20260901-a\ntitle: 注意力机制\nstatus: active\n'
  printf "created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n"
  printf -- '---\n\n## 知识内容\n\n正文占位。\n'
} >"${VAULT}/domains/ai-infra/knowledge/k-20260901-a.md"
ok "库就绪（原文 ${SRC} + 卡 k-20260901-a）"

# ---------------------------------------------------------------- 2. ① 夹具落一个提案
step "夹具直接落一个提案（eg proposal new 属 T-…-040，本 task 只判形态）"
mkdir -p "${VAULT}/proposals"
{
  printf -- '---\n'
  printf 'id: %s\n' "${PID}"
  printf 'type: logical_delete\n'
  printf 'status: pending\n'
  printf "created_at: '2026-07-01'\n"
  printf 'targets:\n  - %s\n' "${SRC}"
  printf 'impact:\n'
  printf '  exits_default_view:\n    - %s\n' "${SRC}"
  printf '  cards_losing_support:\n    - k-20260901-a\n'
  printf '  affected_material_rels: 1\n'
  printf '  affected_relations: 0\n'
  printf '  stale_reviews: []\n'
  printf 'decision:\n  result:\n  reason:\n  superseded_by:\n'
  printf 'execution:\n'
  printf "  status: 'not_started'\n"
  printf '  attempted_at:\n  reason:\n  git_commit:\n'
  printf '  written_paths: []\n  unwritten_paths: []\n'
  printf -- '---\n\n'
  printf '# 逻辑删除注意力机制入门原文\n'
  for sec in 推荐修改 理由与证据 '影响的文件、领域与关系' 执行后状态 不执行的影响 替代方案 可应用内容; do
    printf '\n## %s\n\n%s 注意力 attention\n' "${sec}" "${SENTINEL}"
  done
} >"${VAULT}/${PREL}"
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m3 提案形态语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"

# ① 路径与文件名：proposals/p-<yyyymmdd>-<3d>.md，一项一文件、不属于任何领域。
echo "${PREL}" | grep -Eq '^proposals/p-[0-9]{8}-[0-9]{3}\.md$' ||
  die "提案路径 ${PREL} 不匹配 proposals/p-<yyyymmdd>-<3d>.md"
[ -f "${VAULT}/${PREL}" ] || die "提案文件不存在"
[ "$(find "${VAULT}/proposals" -name '*.md' -type f | wc -l)" = "1" ] || die "一项一文件不成立"
[ "$(find "${VAULT}/domains" -name 'p-*.md' -type f | wc -l)" = "0" ] ||
  die "提案不属于任何领域：不得出现在 domains/ 下"
ok "① 路径与文件名成立：${PREL}（一项一文件、不在 domains/ 下）"

# ---------------------------------------------------------------- 3. ② frontmatter 键集合
step "② frontmatter 键集合：顶层恰 8 键 + 子键逐项，且不含 reviewed_at / deleted_at / deleted_reason"
# 顶层键 = frontmatter 区间内**行首无缩进**的 `key:` 行。
sed -n '2,/^---$/p' "${VAULT}/${PREL}" | sed '$d' | grep -E '^[a-z_]+:' |
  sed 's/:.*//' >"${WORK}/topkeys.txt"
printf '%s\n' id type status created_at targets impact decision execution >"${WORK}/wantkeys.txt"
diff -u "${WORK}/wantkeys.txt" "${WORK}/topkeys.txt" ||
  die "顶层键集合与合同 §7.2 不逐键相等"
[ "$(wc -l <"${WORK}/topkeys.txt")" = "8" ] || die "顶层键数不是 8"
for k in exits_default_view cards_losing_support affected_material_rels affected_relations \
  stale_reviews result reason superseded_by attempted_at git_commit; do
  grep -Eq "^  ${k}:" "${VAULT}/${PREL}" || die "缺子键 ${k}"
done
# 本仓合同新定的两键（M-3 / 登记项 A-21）必须逐字存在。
grep -Fq '  written_paths: []' "${VAULT}/${PREL}" || die "缺新定键 execution.written_paths"
grep -Fq '  unwritten_paths: []' "${VAULT}/${PREL}" || die "缺新定键 execution.unwritten_paths"
for bad in reviewed_at deleted_at deleted_reason; do
  if grep -Fq "${bad}" "${VAULT}/${PREL}"; then die "提案不设 ${bad}（合同 M-6）"; fi
done
ok "② 顶层恰 8 键、子键齐备（含两个新定路径键），三个禁用键零出现"

# ---------------------------------------------------------------- 4. ③ 正文恰 7 个 H2
step "③ 正文恰 7 个 H2，顺序逐字固定（合同 §7.3）"
grep -E '^## ' "${VAULT}/${PREL}" | sed 's/^## //' >"${WORK}/gotsecs.txt"
printf '%s\n' 推荐修改 理由与证据 '影响的文件、领域与关系' 执行后状态 不执行的影响 替代方案 可应用内容 \
  >"${WORK}/wantsecs.txt"
diff -u "${WORK}/wantsecs.txt" "${WORK}/gotsecs.txt" || die "正文分区与合同 §7.3 不逐字相等"
[ "$(wc -l <"${WORK}/gotsecs.txt")" = "7" ] || die "H2 分区数不是 7"
ok "③ 正文 H2 恰 7 个且顺序逐字一致"

# ---------------------------------------------------------------- 5. 只读前置快照
step "只读零副作用：取查询前的 git / 文件快照"
BEFORE_STATUS="$(gitv status --porcelain)"
BEFORE_LOG="$(gitv log --oneline | wc -l)"
snapshot "${WORK}/before.txt"
ok "快照就绪（git status / log 条数 / 逐文件 sha256）"

# ---------------------------------------------------------------- 6. ④ 提案不进检索
step "④-1 eg search：提案标题词与正文哨兵串一律零命中，scanned_files 不计入 proposals/"
[ "$(eg_code search 注意力 --json)" = "0" ] || { cat "${WORK}/err.txt"; die "search 应退 0"; }
grep -Fq "${PID}" "${WORK}/out.txt" && die "search 命中了提案 ID"
grep -Fq 'proposals/' "${WORK}/out.txt" && die "search 输出出现 proposals/ 路径"
grep -Fq "${SENTINEL}" "${WORK}/out.txt" && die "search 输出泄漏提案正文"
# 语料里只有 1 张卡（原文在 sources/ 不进知识扫描面）：scanned_files 必须恰为 1，
# 提案的那个文件绝不能被计进去（反证虚假计数）。
grep -Fq '"scanned_files":1' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "scanned_files 不是 1：proposals/ 被计入了"; }
grep -Fq '"skipped_files":0' "${WORK}/out.txt" || die "skipped_files 不是 0"
# 2026-09-08 随 M5 · T-…-069 阶段化重钉（只改判据**形态**，「提案不得产生噪声」本体一格不放宽）：
# 原判据用「`"code":"Q` 零命中」代理「提案零噪声」。M5 · T-…-067 把**降级留痕**正式发放给读路径：
# 本夹具全程不建索引（`.index/` 不存在），因此每条读命令**必须**同时给出 `W23`（索引不可用）
# 与 `Q5`（本次读走降级路径，结果以权威 Markdown 为准）—— 这是合同要求在场的事实，不是噪声。
# 重钉为**封闭双侧精确等号**（比原来的单侧 grep 更严）：
#   前提封闭：`.index/` 确实不存在（降级前提被锁死，不给「顺手建了索引」留后门）；
#   正面等号：本次 search 的 code 序列**逐字恰为** `W23` → `Q5`（顺序 + 个数 + 取值三重锁）；
#   负面等号：提案噪声本体 `Q1` / `Q2` / `Q3` / `Q4` 逐个恒 0（原判据的实质，一格不放宽）；
#   泄漏封闭：两条降级留痕的 message / path 不得出现提案 ID / 提案正文哨兵串。
# 泄漏封闭这一格**只用 bash / coreutils** 实现（文件头约束「无外部依赖」：不引入 python3 / jq）：
#   `warnings` 是标量字段对象的数组（`code` / `level` / `path` / `op_index` / `message`，
#   见 `internal/cli/exit.go` 的 `Diagnostic`），字段值里不含 `]`，故 `[^]]*` 可精确切出整段 ——
#   与本文件既有 `'"cards":\[[^]]*\]'` / `'"base":{[^}]*}'` 是同一个惯例。
#   切段后做三件事：段唯一 + 段内诊断对象恰 2（与上面的 `W23 → Q5` 码序列等号互为反证）+
#   段内逐字零命中 PID / SENTINEL。比原 python 版多了「段唯一」与「对象恰 2」两格。
# ── C2a·M6 现态重钉（合同 §16.3 runtime-reserved / §16.4 授权；保留 M5 历史事实 + 新增 M6 现态双侧锁）──
# M5 历史事实（降级前提锁死，一格不放宽）：确无派生索引 DB —— 本步的 W23→Q5 降级路径必须被真实走到，
# 不给「顺手建了索引」留后门（eg.db / eg.db-wal / eg.db-shm 一律零存在）。
[ "$(ls -1 "${VAULT}/.index"/eg.db* 2>/dev/null | wc -l | tr -d ' ')" = "0" ] ||
  die "夹具前提被破坏：.index/ 出现派生索引 DB，无索引降级路径不再成立"
# M6 现态（新增正面锁）：.index/ 若存在，仅含 M6 事务基础设施——run.lock（普通文件）/ txn（目录）。
if [ -d "${VAULT}/.index" ]; then
  _rx="$(ls -1A "${VAULT}/.index" 2>/dev/null | grep -vxE 'run\.lock|txn' || true)"
  [ -z "${_rx}" ] || die "夹具前提被破坏：.index/ 出现非 runtime-reserved 条目：${_rx}"
fi
grep -oE '"code":"[EWIQ][0-9]+"' "${WORK}/out.txt" | sed 's/^"code":"//; s/"$//' >"${WORK}/codes_search.txt"
printf 'W23\nQ5\n' >"${WORK}/want_codes_search.txt"
diff -u "${WORK}/want_codes_search.txt" "${WORK}/codes_search.txt" ||
  { cat "${WORK}/out.txt"; die "无索引 search 的诊断码序列应逐字恰为 W23 → Q5（降级留痕在场且不扩张）"; }
for q in Q1 Q2 Q3 Q4; do
  N="$( { grep -c "\"code\":\"${q}\"" "${WORK}/out.txt" || true; } | tail -1)"
  [ "${N}" = "0" ] || die "提案不得产生 ${q} 类诊断噪声（命中 ${N} 处）"
done
grep -Fq '"code":"W23"' "${WORK}/out.txt" || die "无索引读路径必须留 W23"
grep -Fq '降级' "${WORK}/out.txt" || die "Q5 / W23 必须如实写明本次读走降级路径"
grep -o '"warnings":\[[^]]*\]' "${WORK}/out.txt" >"${WORK}/warn_seg.txt" ||
  { cat "${WORK}/out.txt"; die "search --json 缺 warnings 段（无索引时降级留痕必须在场）"; }
[ "$(wc -l <"${WORK}/warn_seg.txt")" = "1" ] || die "warnings 段不唯一（信封只允许一处 warnings）"
NWARN="$( { grep -o '{"code":' "${WORK}/warn_seg.txt" || true; } | wc -l | tr -d ' ')"
[ "${NWARN}" = "2" ] || { cat "${WORK}/warn_seg.txt"; die "warnings 段内诊断对象 ${NWARN} 个 ≠ 恰 2（应恰 W23 + Q5）"; }
grep -Fq "${PID}" "${WORK}/warn_seg.txt" && die "降级留痕泄漏了提案 ID"
grep -Fq "${SENTINEL}" "${WORK}/warn_seg.txt" && die "降级留痕泄漏了提案正文"
[ "$(eg_code search 逻辑删除 --json)" = "0" ] || die "search 逻辑删除 应退 0"
grep -Fq '"total":0' "${WORK}/out.txt" || die "提案标题词必须零命中"
# 第二次查询（提案标题词）同样只许有降级留痕，Q1–Q4 仍逐个恒 0。
grep -oE '"code":"[EWIQ][0-9]+"' "${WORK}/out.txt" | sed 's/^"code":"//; s/"$//' >"${WORK}/codes_search2.txt"
diff -u "${WORK}/want_codes_search.txt" "${WORK}/codes_search2.txt" ||
  { cat "${WORK}/out.txt"; die "提案标题词查询的诊断码序列应逐字恰为 W23 → Q5"; }
ok "④-1 提案零命中；scanned_files=1 / skipped_files=0；无索引下诊断码逐字恰 W23 → Q5，Q1–Q4 恒 0 且降级留痕零泄漏"

step "④-2 eg card show / eg rel：按提案 ID 查一律找不到（退 1，零副作用）"
[ "$(eg_code card show "${PID}")" = "1" ] || die "eg card show 提案 ID 应退 1（看不到提案）"
grep -Fq "${SENTINEL}" "${WORK}/out.txt" "${WORK}/err.txt" && die "card show 泄漏提案正文"
[ "$(eg_code rel "${PID}")" = "1" ] || die "eg rel 提案 ID 应退 1（看不到提案）"
grep -Fq "${SENTINEL}" "${WORK}/out.txt" "${WORK}/err.txt" && die "rel 泄漏提案正文"
[ "$(eg_code rel k-20260901-a --json)" = "0" ] || die "eg rel 读卡应退 0"
grep -Fq "${PID}" "${WORK}/out.txt" && die "rel 正反向出现提案 ID"
grep -Fq '"scanned_files":1' "${WORK}/out.txt" || die "rel 的 scanned_files 不是 1"
ok "④-2 card show / rel 都看不到提案；rel 的扫描计数同样不含 proposals/"

step "④-3 eg context：只给「标题 + targets」摘要——正文零泄漏、base 不含 proposals/"
[ "$(eg_code context --source "${SRC}" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg context 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/ctx.json"
grep -Fq "${SENTINEL}" "${WORK}/ctx.json" && die "context 泄漏提案正文（七个 H2 的内容不得进上下文）"
for sec in '## 推荐修改' '## 理由与证据' '## 可应用内容'; do
  grep -Fq -- "${sec}" "${WORK}/ctx.json" && die "context 泄漏提案正文分区 ${sec}"
done
# 摘要面正判：data.proposals 恰一项，且**只有** id / path / title / targets 四键，
# 逐字比对整段 JSON 片段（键顺序 = 结构体字段顺序，多一键即不相等）。
WANT_PSUM="\"proposals\":[{\"id\":\"${PID}\",\"path\":\"${PREL}\",\"title\":\"逻辑删除注意力机制入门原文\",\"targets\":[\"${SRC}\"]}]"
grep -Fq -- "${WANT_PSUM}" "${WORK}/ctx.json" ||
  { cat "${WORK}/ctx.json"; die "data.proposals 不是「id/path/title/targets 四键」的逐字形态：期望 ${WANT_PSUM}"; }
# base 是「本次加工可能被改的文件 → content_hash」：提案不是知识数据，绝不能进 base。
grep -o '"base":{[^}]*}' "${WORK}/ctx.json" >"${WORK}/base.txt" || die "context 缺 base"
grep -Fq 'proposals/' "${WORK}/base.txt" && die "base 不得包含 proposals/（提案不是本次加工会改的文件）"
# 提案也不得混进 cards / candidates（那是知识面）。
grep -o '"cards":\[[^]]*\]' "${WORK}/ctx.json" | grep -Fq "${PID}" && die "cards 里出现提案"
grep -o '"candidates":\[[^]]*\]' "${WORK}/ctx.json" | grep -Fq "${PID}" && die "candidates 里出现提案"
grep -Fq '"code":"Q' "${WORK}/ctx.json" && die "合法提案不得产生 Q 类诊断"
grep -Fq '"exit_code":0' "${WORK}/ctx.json" || die "context 必须退 0"
# 人读模式与 --json 同源同事实：摘要行有 ID / 标题 / targets，正文与分区名一个都没有。
[ "$(eg_code context --source "${SRC}")" = "0" ] || die "eg context 人读模式应退 0"
for want in "${PID}" 逻辑删除注意力机制入门原文 "${SRC}"; do
  grep -Fq -- "${want}" "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "人读摘要缺 ${want}"; }
done
grep -Fq "${SENTINEL}" "${WORK}/out.txt" && die "人读模式泄漏提案正文"
for sec in 推荐修改 理由与证据 可应用内容; do
  grep -Fq -- "${sec}" "${WORK}/out.txt" && die "人读模式泄漏提案分区名 ${sec}"
done
ok "④-3 context 两侧都只给 id/path/title/targets 摘要；正文、base、cards、candidates 均零泄漏"

# ---------------------------------------------------------------- 7. 只读复核
step "只读零副作用复核：git status / git log / 逐文件 sha256 全部不变"
[ "${BEFORE_STATUS}" = "$(gitv status --porcelain)" ] || die "git status 前后不同"
[ "${BEFORE_LOG}" = "$(gitv log --oneline | wc -l)" ] || die "git log 条数变了"
snapshot "${WORK}/after.txt"
diff -u "${WORK}/before.txt" "${WORK}/after.txt" >"${WORK}/diff.txt" ||
  { cat "${WORK}/diff.txt"; die "vault 内 .md 发生变化"; }
ok "零文件变化、零 commit（含提案文件本身逐字节未动）"

# ---------------------------------------------------------------- 8. round-trip 复核
step "round-trip 复核：提案文件字节与夹具写入时逐字节相等（读路径永不改写）"
CUR="$(sha256sum "${VAULT}/${PREL}" | cut -d' ' -f1)"
grep -Fq "${CUR}" "${WORK}/before.txt" || die "提案文件 sha256 与查询前不一致"
ok "提案文件 sha256 未变：${CUR}"

printf '\n=== proposal_layout.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

#!/usr/bin/env bash
# 提案四态状态机端到端脚本（T-evergreen.s1_main_flow-158614-034）。
#
# 判据来源：提案合同 `2026-10-10-m3-proposal-state-contract.md`
#   §2.1 四态定义（不存在第五种）/ §2.3 合法迁移恰 5 条 / §2.4 非法迁移恰 12 条
#   §2.5 终态与 **A-22 临时裁决**（approved 非终态，唯一出边 → superseded）
#   §7.2 末段 status ⟷ decision.result 一致性（M-5 / A-20）
#
# 三组断言（task Acceptance 逐字要求）：
#   ① **四态取值封闭**：产品代码里的 status 取值集合恰 4 个；draft / applied / candidate
#      三个反例被判非法；提案摘要面（eg context）不会把第五态当成合法值悄悄接受；
#   ② **非法迁移零写入**：12 条非法边逐条被拒（表驱动用例真实执行），且**判定全程零写入**
#      —— 脚本用真实 vault 夹具 + 逐文件 sha256 前后比对反证，git 工作区与 log 均不变；
#   ③ **终态不可再迁**：rejected / superseded 的出边数恒 0，approved 的出边恰 1 条
#      且指向 superseded（A-22 留痕在代码里可 grep）。
#
# 只读复核（第 11 步）用 `eg context`，其加工对象**必填**（M1 合同 §1.4：`--source` 与
# `--note` 二选一，缺参数 = 用法错误退 1）。脚本因此先 `eg capture` 落一篇原文再取上下文，
# 并顺手把「缺参数仍退 1」当成 M1 契约的不回归断言 —— 本 task 不放宽 M1 的参数契约。
#
# 为什么用「夹具 + 纯函数层用例 + 只读 CLI」而不是 `eg proposal approve`：
#   状态迁移的 CLI 子命令属 T-…-040，本 task 只交付判定层（纯函数、无写口）。
#   脚本因此把「零写入」判成**结构性反证**：internal/proposal 全包没有任何文件写口，
#   且判定跑完前后 vault 逐字节不变。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/proposal/proposal_state.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-state.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

PID='p-20260701-001'
PREL="proposals/${PID}.md"
SENTINEL='SENTINEL-STATE-BODY-MUST-NOT-LEAK'

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
gotest() { (cd "$(eg_staged_root)" && go test ./internal/proposal/ "$@" </dev/null); }

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. ① 四态封闭
step "① 四态取值封闭：产品代码里的 status 取值恰 4 个，且不存在第五种字面量"
SRC="${REPO_ROOT}/internal/proposal/schema.go"
grep -E '^[[:space:]]+Status[A-Za-z]+[[:space:]]+Status[[:space:]]+=[[:space:]]+"' \
  "${SRC}" | sed 's/.*"\(.*\)".*/\1/' >"${WORK}/states.txt"
printf '%s\n' pending approved rejected superseded >"${WORK}/want_states.txt"
diff -u "${WORK}/want_states.txt" "${WORK}/states.txt" || die "四态集合与合同 §2.1 不逐字相等"
[ "$(wc -l <"${WORK}/states.txt")" = "4" ] || die "status 取值不是恰 4 个"
# 第五态候选词在产品代码里零出现（注释里的反例说明只允许出现在 _test.go）。
for bad in draft applied candidate; do
  n=$( { grep -rn "Status *= *\"${bad}\"\|StatusDraft\|StatusApplied\|StatusCandidate" \
    "${REPO_ROOT}/internal/proposal/" || true; } | { grep -v '_test\.go' || true; } |
    wc -l | tr -d ' ')
  [ "${n}" = "0" ] || die "产品代码出现第五态 ${bad}（${n} 处）"
done
ok "① 四态恰 pending/approved/rejected/superseded；第五态零落点"

step "① 三个反例（draft / applied / candidate）被判非法：表驱动用例真实执行"
gotest -run TestStatus_ExactlyFourValues -count=1 -v >"${WORK}/t1.txt" 2>&1 ||
  { tail -20 "${WORK}/t1.txt"; die "TestStatus_ExactlyFourValues 未全绿"; }
for bad in draft applied candidate; do
  grep -Eq -- "--- PASS: TestStatus_ExactlyFourValues/${bad}" "${WORK}/t1.txt" ||
    { tail -20 "${WORK}/t1.txt"; die "反例 ${bad} 的子用例未通过"; }
done
ok "① 三反例子用例逐个 PASS（枚举越界 → 校验失败族、退 2、零写入）"

# ---------------------------------------------------------------- 2. ② 非法迁移零写入
step "② 建库 + 夹具落一个提案（eg proposal new 属 T-…-040，本 task 只判状态机）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
# 收录一篇原文：第 11 步的 `eg context` 按 M1 合同 §1.4 必须给 `--source` 与 `--note`
# 二选一（缺参数 → UsageError、退 1），所以加工对象必须在**取基线快照之前**先入库。
printf '注意力机制的正文占位，供 M3 状态机用例使用。\n' >"${WORK}/body.txt"
[ "$(eg_code capture --url https://example.com/attention --title '注意力机制入门' \
  --body-file "${WORK}/body.txt" --reason 'T-034 语料' --domain ai-infra \
  --captured-at 2026-09-01T09:00:00+08:00 --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "capture 失败"; }
SRC="$(sed 's/.*"source_id":"\([^"]*\)".*/\1/' "${WORK}/out.txt")"
[ -n "${SRC}" ] || die "capture 未返回 source_id"
mkdir -p "${VAULT}/proposals" "${VAULT}/domains/ai-infra/knowledge"
{
  printf -- '---\nid: %s\ntype: logical_delete\nstatus: pending\n' "${PID}"
  printf "created_at: '2026-07-01'\n"
  printf 'targets:\n  - k-20260901-a\n'
  printf 'impact:\n  exits_default_view: []\n  cards_losing_support: []\n'
  printf '  affected_material_rels: 0\n  affected_relations: 0\n  stale_reviews: []\n'
  printf 'decision:\n  result:\n  reason:\n  superseded_by:\n'
  printf 'execution:\n  status: not_started\n  attempted_at:\n  reason:\n  git_commit:\n'
  printf '  written_paths: []\n  unwritten_paths: []\n'
  printf -- '---\n\n# 逻辑删除一张过时卡\n'
  for sec in 推荐修改 理由与证据 '影响的文件、领域与关系' 执行后状态 不执行的影响 替代方案 可应用内容; do
    printf '\n## %s\n\n%s\n' "${sec}" "${SENTINEL}"
  done
} >"${VAULT}/${PREL}"
{
  printf -- '---\nid: k-20260901-a\ntitle: 注意力机制\nstatus: active\n'
  printf "created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n"
  printf -- '---\n\n## 知识内容\n\n正文占位。\n'
} >"${VAULT}/domains/ai-infra/knowledge/k-20260901-a.md"
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m3 状态机语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
BEFORE_LOG="$(gitv log --oneline | wc -l)"
snapshot "${WORK}/before.txt"
ok "库就绪：${PREL}（status: pending）+ 卡 k-20260901-a + 原文 ${SRC}；已取基线快照"

step "② 非法迁移恰 12 条：逐条被拒（自环 4 + 异向 8），且每条断言退 2 + 字节不变"
gotest -run TestStateMachine_IllegalTransitions -count=1 -v >"${WORK}/t2.txt" 2>&1 ||
  { tail -30 "${WORK}/t2.txt"; die "TestStateMachine_IllegalTransitions 未全绿"; }
N_ILLEGAL=$(grep -cE -- "--- PASS: TestStateMachine_IllegalTransitions/[a-z]+->[a-z]+" \
  "${WORK}/t2.txt" || true)
[ "${N_ILLEGAL}" = "12" ] ||
  { grep -E -- "--- PASS: TestStateMachine_IllegalTransitions/" "${WORK}/t2.txt";
    die "非法边子用例 = ${N_ILLEGAL}，必须恰 12"; }
for tr in approved-\>rejected approved-\>pending rejected-\>pending superseded-\>approved; do
  grep -Fq -- "--- PASS: TestStateMachine_IllegalTransitions/${tr}" "${WORK}/t2.txt" ||
    die "最易写错的边 ${tr} 缺独立子用例"
done
# 12 条非法边由 4×4 减法**算出**而非手写（漏项反证）。
grep -q 'for .* range allStatuses' "${REPO_ROOT}/internal/proposal/state.go" ||
  die "非法边表必须由 allStatuses 全集减法导出"
ok "② 12 条非法边逐条 PASS（含四条最易写错的边），减法导出可 grep"

step "② 合法边恰 5 条（T0–T4）：表驱动逐行，必写字段与 §2.3 逐字一致"
gotest -run TestStateMachine_LegalTransitions -count=1 -v >"${WORK}/t3.txt" 2>&1 ||
  { tail -30 "${WORK}/t3.txt"; die "TestStateMachine_LegalTransitions 未全绿"; }
for tid in T0 T1 T2 T3 T4; do
  grep -Fq -- "--- PASS: TestStateMachine_LegalTransitions/${tid}" "${WORK}/t3.txt" ||
    die "合法边 ${tid} 缺子用例"
done
N_LEGAL=$(grep -cE -- "--- PASS: TestStateMachine_LegalTransitions/T[0-4]" "${WORK}/t3.txt" || true)
[ "${N_LEGAL}" = "5" ] || die "合法边子用例 = ${N_LEGAL}，必须恰 5"
ok "② 合法边 T0–T4 恰 5 条全 PASS"

step "② 零写入反证：状态机判定全程无文件写口，vault 逐字节不变、零 commit"
# 结构性反证：internal/proposal 全包没有任何写盘调用（写口属 T-…-040）。
WR=$( { grep -rnE "os\.(WriteFile|Create|OpenFile|Rename|Remove)" \
  "${REPO_ROOT}/internal/proposal/" || true; } | { grep -v '_test\.go' || true; } |
  wc -l | tr -d ' ')
[ "${WR}" = "0" ] || die "internal/proposal 出现写盘调用（${WR} 处），违反 A-23 的 guarded 写口约束"
snapshot "${WORK}/after.txt"
diff -u "${WORK}/before.txt" "${WORK}/after.txt" >"${WORK}/diff.txt" ||
  { cat "${WORK}/diff.txt"; die "vault 内 .md 发生变化"; }
[ -z "$(gitv status --porcelain)" ] || die "判定后工作区必须干净"
[ "${BEFORE_LOG}" = "$(gitv log --oneline | wc -l)" ] || die "不得产生 commit"
ok "零写盘调用 + vault 逐字节不变 + 零 commit"

# ---------------------------------------------------------------- 3. ③ 终态
step "③ 终态不可再迁：rejected / superseded 出边 0；approved 出边恰 1 → superseded（A-22）"
gotest -run TestTerminalStates -count=1 -v >"${WORK}/t4.txt" 2>&1 ||
  { tail -30 "${WORK}/t4.txt"; die "TestTerminalStates 未全绿"; }
for st in rejected superseded; do
  for to in pending approved rejected superseded; do
    grep -Fq -- "--- PASS: TestTerminalStates/${st}->${to}" "${WORK}/t4.txt" ||
      die "终态出边 ${st}->${to} 缺子用例"
  done
done
N_TERM=$(grep -cE -- "--- PASS: TestTerminalStates/[a-z]+->[a-z]+" "${WORK}/t4.txt" || true)
[ "${N_TERM}" = "8" ] || die "终态出边子用例 = ${N_TERM}，必须恰 8（2 终态 × 4 止态）"
# A-22 临时裁决必须在代码里留痕（owner 回改状态图时便于定位）。
[ "$(grep -c 'A-22' "${REPO_ROOT}/internal/proposal/state.go" || true)" -ge 1 ] ||
  die "state.go 必须逐字留痕 A-22 临时裁决"
ok "③ 8 条终态出边全被拒；A-22 留痕在册"

step "③ 一致性规则（M-5 / A-20）：status 是权威，decision.result 必须逐字相等"
gotest -run TestDecisionResultMatchesStatus -count=1 >"${WORK}/t5.txt" 2>&1 ||
  { tail -20 "${WORK}/t5.txt"; die "TestDecisionResultMatchesStatus 未全绿"; }
ok "③ 一致性三例（不等 / pending 非空 / approved 相等）全绿"

# ---------------------------------------------------------------- 4. 越界与编号
step "越界反证：本 task 不发编号、不引 M4–M6 能力"
[ "$( { grep -rhoE '"(E|W|I)[0-9]+"' "${REPO_ROOT}/internal/proposal/" || true; } |
  sort -u | wc -l | tr -d ' ')" = "0" ] ||
  die "internal/proposal 不得出现诊断码字面量（发码属 T-…-037）"
[ "$( { grep -rnE 'reconcile|flock|txn/|run\.lock|FTS5|\.index/' \
  "${REPO_ROOT}/internal/proposal/" || true; } | wc -l | tr -d ' ')" = "0" ] ||
  die "出现 M4–M6 能力字样"
# 重钉（M4 · T-…-049，只改形态不放宽本体）：E11–E14 / W13–W20 已由对账合同 §3 发放给
# internal/reconcile，故越界扫描按包分域——reconcile 外仍 0 命中，reconcile 内只许那 12 个码
# 且 M5–M6 号段（E15+ / W21+ / I2+）仍 0 命中。
# C2a·M6 现态重钉（原子性合同 §16.3/§16.4/§17.2 授权）：M6 把 E15/E16 发放给 internal/txn、
#   W27 发放给 internal/mdfile；E15/E16 命中旧「reconcile 外」grep，沿用 M4→reconcile 剔除先例把
#   txn/mdfile 一并剔除（历史事实一格不放宽），并新增封闭双侧等号把两域逐字锁死（加严不放宽）。
# ── C2 · I-…-015 现态重钉（CLI 合同 §5 现态脚注授权；**零弱化**：base 域一格不放宽）──
# 命令层补码把恰 9 个新码 E17–E25 发放给**一个文件** internal/cli/codes.go（其余文件一律引用常量名、
# 零码字面量，已由 exit_codes_and_diagnostics.sh ⑧ 双侧锁死）。E17–E19 命中旧「reconcile/txn/mdfile 外」
# grep，沿用 M4→reconcile、M6→txn/mdfile 的同一剔除先例把该**单个文件**（不是整个 internal/cli 目录）
# 剔除 —— internal/cli 其余文件的越界仍恒 0，历史事实逐字保留；并新增一条封闭双侧等号把该文件
# 逐字锁成 {E17…E25}（多一码 / 少一码当场红，是加严不是放宽）。
[ "$( { grep -rnE '"(E1[1-9]|W1[3-9]|I[2-9])"' --exclude-dir=reconcile --exclude-dir=txn \
  --exclude-dir=mdfile --exclude=codes.go "${REPO_ROOT}/internal/" || true; } |
  wc -l | tr -d ' ')" = "0" ] ||
  die "出现越界编号"
[ "$( { grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/cli/codes.go" || true; } |
  tr -d '"' | sort -u | tr '\n' ' ')" = "E17 E18 E19 E20 E21 E22 E23 E24 E25 " ] ||
  die "internal/cli/codes.go 的命令层码集合应恰 {E17…E25}（CLI 合同 §5 现态脚注）"
[ "$( { grep -rnE '"(E1[5-9]|E[2-9][0-9]|W2[1-8]|W[3-9][0-9]|I[2-9])"' \
  "${REPO_ROOT}/internal/reconcile/" || true; } |
  wc -l | tr -d ' ')" = "0" ] ||
  die "internal/reconcile 出现 M5–M6 号段编号（W21–W28 / W30+；A-62 只解冻 W29）"
# M6 域封闭双侧等号：internal/txn 恰 {E15,E16,W26,W28}、internal/mdfile 恰 {W27}。
grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/txn/" | tr -d '"' | sort -u >"${WORK}/codes_m6_txn.txt"
printf 'E15\nE16\nW26\nW28\n' | sort -u >"${WORK}/want_m6_txn.txt"
diff -u "${WORK}/want_m6_txn.txt" "${WORK}/codes_m6_txn.txt" ||
  die "internal/txn 的 M6 码集合应恰 {E15,E16,W26,W28}（原子性合同 §12/§16.3）"
grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/mdfile/" | tr -d '"' | sort -u >"${WORK}/codes_m6_mdfile.txt"
printf 'W27\n' >"${WORK}/want_m6_mdfile.txt"
diff -u "${WORK}/want_m6_mdfile.txt" "${WORK}/codes_m6_mdfile.txt" ||
  die "internal/mdfile 的 M6 码集合应恰 {W27}（块级合并合同 §7）"
ok "零编号字面量、零越界能力字样"

# ---------------------------------------------------------------- 5. 只读复核
step "只读复核：提案仍不进知识检索、context 仍只给摘要（T-…-033 不回归）"
[ "$(eg_code search 注意力 --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg search 应退 0"; }
if grep -Fq "${PID}" "${WORK}/out.txt"; then die "search 命中提案"; fi
if grep -Fq "${SENTINEL}" "${WORK}/out.txt"; then die "search 泄漏提案正文"; fi
# `eg context` 的加工对象是必填的（M1 合同 §1.4：--source 与 --note 二选一）。
# 先逐字反证「缺参数 = 用法错误退 1」这条既有契约不回归，再按契约给 --source 取上下文；
# 本 task 不放宽 M1 的参数契约，也不改 context 的行为。
[ "$(eg_code context --json)" = "1" ] || die "eg context 缺 --source/--note 必须按用法错误退 1"
grep -Fq -- "--source 与 --note 二选一" "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "缺参数的用法错误文案与合同 §1.4 不一致"; }
CTX_CODE="$(eg_code context --source "${SRC}" --json)"
[ "${CTX_CODE}" = "0" ] ||
  { cat "${WORK}/err.txt" "${WORK}/out.txt"; die "eg context --source ${SRC} 应退 0，实退 ${CTX_CODE}"; }
if grep -Fq "${SENTINEL}" "${WORK}/out.txt"; then die "context 泄漏提案正文"; fi
grep -Fq "\"id\":\"${PID}\"" "${WORK}/out.txt" || die "context 的提案摘要缺 ${PID}"
grep -Fq '"exit_code":0' "${WORK}/out.txt" || die "context 信封的 exit_code 必须为 0"
[ "$(eg_code context --source "${SRC}")" = "0" ] || die "eg context 人读模式应退 0"
if grep -Fq "${SENTINEL}" "${WORK}/out.txt"; then die "context 人读模式泄漏提案正文"; fi
grep -Fq "${PID}" "${WORK}/out.txt" || die "context 人读模式缺提案摘要行"
snapshot "${WORK}/after2.txt"
diff -u "${WORK}/before.txt" "${WORK}/after2.txt" >/dev/null || die "只读命令改了文件"
[ -z "$(gitv status --porcelain)" ] || die "只读命令后工作区必须干净"
[ "${BEFORE_LOG}" = "$(gitv log --oneline | wc -l)" ] || die "只读命令不得产生 commit"
ok "提案零命中检索、context 两侧只给摘要、缺参数仍按 §1.4 退 1、全程零副作用"

printf '\n=== proposal_state.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

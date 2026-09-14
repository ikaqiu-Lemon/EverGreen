#!/usr/bin/env bash
# 并发冲突端到端脚本（M6 · T-evergreen.s1_main_flow-158614-073）。
#
# 判据来源：docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md
#   §8 / A-54 / A-58（run.lock 跨进程互斥；后到者在超时内等待并留痕 W28、超时携 E16 且零写入；
#     同一文件上不出现两个 txn_id 交叉写；任一观察者永不见半写文件）
#   §7 / A-57（块级安全合并：锁内 S3 重读重校验后，目标块自读取以来已变 → 跳过并产 W27）
#
# 本脚本以**真实两进程**（两个 `eg apply`）反证并发语义，另以「持锁 + 限时取锁」反证 E16 分支：
#   ① 两个 `eg` 同时写同一 vault 的**同一自检块**（同一 base_block_hash）：run.lock 串行化后
#      锁内 S3 重读 —— 恰一个赢家提交（退 0），恰一个后到者因目标块已变而**跳过**（退 3 + W27
#      block_merge_conflict，零写入）；全程读采样任一时刻文件 ∈ {前像, 赢家目标态}，**零半写**；
#      `.index/txn/` 恰一个已提交事务、零悬挂事务，**零交叉 txn_id**；净增恰 1 个 commit。
#   ② E16 分支（A-54 超时定盘，R7 只断言非零 + E16 + 零写入，**不**断言退 5、**不**断言恰 1）：
#      持锁进程占用 run.lock 期间，另一个真实 `eg apply` 限时取锁必超时 —— 非零退出 + 报告含 E16，
#      且 `git status --porcelain` 与整棵工作树字节与执行前**逐字相等**（权威零写入）。
#
# 约束：离线、零交互、可重复；依赖仅 bash / coreutils / git / go / jq；
#   一切写与构建只发生在 mktemp -d 沙箱内，真实仓库工作区一个字节都不碰（末尾自查）；
#   任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/concurrent_conflict.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-conflict.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
TXNCTL="${WORK}/txnctl"

KDIR_REL='domains/ai-infra/knowledge'
CARD="k-20260901-attention"
CARDPATH="${VAULT}/${KDIR_REL}/${CARD}.md"

OLD_BLOCK='- 历史块：为什么需要缩放点积？'
CUR_BLOCK='- 当前有效块：多头注意力的头数如何选？'
NEW_1='- 赢家 A 写入：头数与维度如何权衡？'
NEW_2='- 赢家 B 写入：注意力的复杂度上界？'
USER_LINE='	用户手写、带 Tab 缩进的补充块，B2 要求逐字保留。'

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

gitv() { git -C "${VAULT}" "$@"; }
commits() { gitv log --oneline | wc -l | tr -d ' '; }
porcelain() { gitv status --porcelain; }
treesum() { find "${VAULT}" -path "${VAULT}/.git" -prune -o -type f -print0 | sort -z | xargs -0 sha256sum; }
filehash() { printf 'sha256:%s' "$(sha256sum "${VAULT}/$1" | cut -d' ' -f1)"; }
blockhash() { printf '%s' "$1" | sha256sum | cut -c1-16; }
cardsha() { sha256sum "${CARDPATH}" | cut -d' ' -f1; }
# cardsha_norm：把 `updated_at` 那一行归一成 seed 值后再取 sha。
# 为什么需要归一：内容时间戳由 CLI 在**实际写入**时刷新（授权合同 §2.1 矩阵第 8 行 /
# I-…-009），赢家写入时刻在起跑前无法预知；除该行外仍**逐字**比对，半写 / 交叉写照样现形。
UPDATED_AT_SEED="updated_at: '2026-09-01T10:00:00+08:00'"
norm_bytes() { sed "s/^updated_at: .*/${UPDATED_AT_SEED}/" "$1" 2>/dev/null; }
cardsha_norm() { norm_bytes "${CARDPATH}" | sha256sum | cut -d' ' -f1; }
# stamp_gt <新> <旧>：带时区 RFC3339 严格晚于则退 0（与 c2_updated_at_refresh.sh 同源口径）。
stamp_gt() {
  python3 - "$1" "$2" <<'PY_STAMP'
import sys
from datetime import datetime
def p(v):
    return datetime.fromisoformat(v.replace("Z", "+00:00"))
sys.exit(0 if p(sys.argv[1]) > p(sys.argv[2]) else 1)
PY_STAMP
}
# 建一张五分区卡（自检分区含 OLD_BLOCK / CUR_BLOCK），提交后工作区干净。
seed_card() {
  rm -f "${CARDPATH}"; mkdir -p "${VAULT}/${KDIR_REL}"
  {
    printf '%s\n' '---' "id: ${CARD}" 'title: 注意力机制' 'status: active' \
      "created_at: '2026-09-01'" "updated_at: '2026-09-01T10:00:00+08:00'" \
      'tags:' '  - 注意力' '---' '' \
      '## 知识内容' '' '正文占位一行。' '' \
      '## 用户补充' '' "${USER_LINE}" '' \
      '## 理解自检' '' "${OLD_BLOCK}" '' "${CUR_BLOCK}"
  } >"${CARDPATH}"
  gitv -c user.email=eg@example.com -c user.name=eg add -A
  gitv -c user.email=eg@example.com -c user.name=eg commit -q -m "seed: m6 并发语料"
  [ -z "$(porcelain)" ] || die "前置：seed 后工作区应干净"
}
# expected_card <new-block-line> <outfile>：把 seed 里的 CUR_BLOCK 换成新块，产出期望全文件。
expected_card() {
  {
    printf '%s\n' '---' "id: ${CARD}" 'title: 注意力机制' 'status: active' \
      "created_at: '2026-09-01'" "updated_at: '2026-09-01T10:00:00+08:00'" \
      'tags:' '  - 注意力' '---' '' \
      '## 知识内容' '' '正文占位一行。' '' \
      '## 用户补充' '' "${USER_LINE}" '' \
      '## 理解自检' '' "${OLD_BLOCK}" '' "$1"
  } >"$2"
}
wait_ready() { local f="$1" i; for i in $(seq 1 200); do [ -f "${f}" ] && return 0; sleep 0.05; done; return 1; }

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 0. 构建
step "构建 eg + txnctl"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "eg 编译失败"
(cd "${REPO_ROOT}" && go build -o "${TXNCTL}" ./tests/lib/txnctl) || die "txnctl 编译失败"
command -v jq >/dev/null || die "本脚本用 jq 读报告"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

step "eg init 建库"
"${EG}" --vault "${VAULT}" init --domain ai-infra </dev/null >/dev/null 2>&1 || die "eg init 失败"
"${EG}" --vault "${VAULT}" config set default_domain ai-infra </dev/null >/dev/null 2>&1 || die "config set 失败"
ok "库就绪"

# ---------------------------------------------------------------- 1. 两个真实 eg 进程写同一自检块
step "① 两个 eg 同时写同一自检块：恰一个赢家提交、恰一个后到者跳过 + W27，零半写 / 零交叉 txn_id"
seed_card
BASE_COMMITS="$(commits)"
BH="$(blockhash "${CUR_BLOCK}")"
FH="$(filehash "${KDIR_REL}/${CARD}.md")"
SEED_SHA="$(cardsha_norm)"
expected_card "${NEW_1}" "${WORK}/exp1.md"; SHA1="$(norm_bytes "${WORK}/exp1.md" | sha256sum | cut -d' ' -f1)"
expected_card "${NEW_2}" "${WORK}/exp2.md"; SHA2="$(norm_bytes "${WORK}/exp2.md" | sha256sum | cut -d' ' -f1)"

for i in 1 2; do
  BLK="NEW_${i}"; BLK="${!BLK}"
  cat >"${WORK}/plan${i}.json" <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"并发写者 ${i}",
 "base":{"${KDIR_REL}/${CARD}.md":"${FH}"},
 "ops":[{"op":"replace_block","target":"${CARD}","section":"理解自检","initiator":"user",
         "reason":"并发写者 ${i}","base_block_hash":"${BH}","block":"${BLK}\n"}]}
JSON
done

# 读采样器：持续对卡文件取 sha，任一样本必 ∈ {前像, 赢家A目标, 赢家B目标}，否则记为半写。
BAD="${WORK}/bad_samples.txt"; : >"${BAD}"
SAMPLE_STOP="${WORK}/sample.stop"; rm -f "${SAMPLE_STOP}"
(
  while [ ! -f "${SAMPLE_STOP}" ]; do
    s="$(cardsha_norm || true)"
    if [ -n "${s}" ] && [ "${s}" != "${SEED_SHA}" ] && [ "${s}" != "${SHA1}" ] && [ "${s}" != "${SHA2}" ]; then
      echo "${s}" >>"${BAD}"
    fi
  done
) &
SAMPLER_PID=$!

# 真实两进程：同时起跑（都给足取锁超时，让后到者等待而非超时），各自写同一自检块。
export EG_LOCK_TIMEOUT_MS=15000
"${EG}" --vault "${VAULT}" apply --plan "${WORK}/plan1.json" --json --user-request </dev/null \
  >"${WORK}/o1.txt" 2>"${WORK}/e1.txt" & P1=$!
"${EG}" --vault "${VAULT}" apply --plan "${WORK}/plan2.json" --json --user-request </dev/null \
  >"${WORK}/o2.txt" 2>"${WORK}/e2.txt" & P2=$!
C1=0; wait "${P1}" || C1=$?
C2=0; wait "${P2}" || C2=$?
unset EG_LOCK_TIMEOUT_MS
touch "${SAMPLE_STOP}"; wait "${SAMPLER_PID}" 2>/dev/null || true

# 恰一个退 0（赢家），恰一个退 3（后到者跳过）。
if { [ "${C1}" = "0" ] && [ "${C2}" = "3" ]; }; then WIN=1; LOSE=2;
elif { [ "${C1}" = "3" ] && [ "${C2}" = "0" ]; }; then WIN=2; LOSE=1;
else cat "${WORK}/o1.txt" "${WORK}/o2.txt"; die "并发两进程退出码应为 {0,3} 各一，实得 C1=${C1} C2=${C2}"; fi
ok "退出码封闭：赢家(进程 ${WIN}) 退 0、后到者(进程 ${LOSE}) 退 3"

# 赢家：确有一次权威写入与 commit。
jq -e '.exit_code == 0' "${WORK}/o${WIN}.txt" >/dev/null || die "赢家信封应 exit_code=0"
jq -e '(.data.report.skipped | length) == 0' "${WORK}/o${WIN}.txt" >/dev/null || die "赢家不应有 skipped[]"
# 后到者：锁内 S3 重读发现目标块已变 → 跳过 + W27 block_merge_conflict，封闭两值 kind。
jq -e '[.data.report.skipped[].kind] | index("file_changed") != null' "${WORK}/o${LOSE}.txt" >/dev/null ||
  { cat "${WORK}/o${LOSE}.txt"; die "后到者必须以 kind=file_changed 跳过（不新造第三值）"; }
grep -Fq 'W27' "${WORK}/o${LOSE}.txt" || { cat "${WORK}/o${LOSE}.txt"; die "后到者跳过自由文本必须含 W27"; }
grep -Fq 'block_merge_conflict' "${WORK}/o${LOSE}.txt" || die "后到者跳过自由文本必须含 block_merge_conflict"
ok "行为封闭：赢家一次写入无跳过；后到者跳过 + W27 block_merge_conflict（零写入）"

# 零半写：采样期间从未见过 {前像, 赢家目标} 之外的字节。
[ ! -s "${BAD}" ] || { echo "非法样本："; cat "${BAD}"; die "并发读采样到半写文件（非原子写入）"; }
# 终态是某个赢家目标态的完整字节，用户补充逐字保留。
FIN="$(cardsha_norm)"
{ [ "${FIN}" = "${SHA1}" ] || [ "${FIN}" = "${SHA2}" ]; } ||
  die "终态卡文件不是任一赢家目标态的完整字节（可能半写 / 交叉写）"
# 赢家那一次写入必须刷新内容时间戳：恰一行且严格晚于 seed（矩阵第 8 行 / I-…-009）。
[ "$(grep -c '^updated_at:' "${CARDPATH}")" = "1" ] || die "updated_at 必须恰一行（整行覆盖）"
FIN_AT="$(sed -n 's/^updated_at: *//p' "${CARDPATH}" | tr -d "'\"")"
stamp_gt "${FIN_AT}" '2026-09-01T10:00:00+08:00' ||
  die "赢家写入必须刷新 updated_at（旧 2026-09-01T10:00:00+08:00 → 实得 ${FIN_AT}）"
grep -Fq -e "${USER_LINE}" "${CARDPATH}" || die "用户补充必须逐字保留（B2）"
grep -Fq -- "${OLD_BLOCK}" "${CARDPATH}" || die "历史块必须逐字保留"
ok "零半写：采样全程文件 ∈ {前像, 赢家目标态}，终态为完整赢家目标 + 用户字节零丢失"

# 零交叉 txn_id：恰一个已提交事务、零悬挂事务；净增恰 1 个 commit。
COMMITTED=0; OPEN=0
if [ -d "${VAULT}/.index/txn" ]; then
  while IFS= read -r d; do
    [ -n "${d}" ] || continue
    if [ -e "${d}/commit" ]; then COMMITTED=$((COMMITTED + 1)); fi
    if [ -e "${d}/intent.json" ] && [ ! -e "${d}/commit" ] && [ ! -e "${d}/abort" ]; then OPEN=$((OPEN + 1)); fi
  done < <(find "${VAULT}/.index/txn" -mindepth 1 -maxdepth 1 -type d)
fi
[ "${COMMITTED}" = "1" ] || die "应恰有 1 个已提交事务（后到者零写入不开事务），实得 ${COMMITTED}"
[ "${OPEN}" = "0" ] || die "不得留下悬挂事务（intent 无 commit/abort），实得 ${OPEN} 个"
[ "$(commits)" = "$((BASE_COMMITS + 1))" ] || die "并发两进程净增应恰 1 个 commit，实得 $(( $(commits) - BASE_COMMITS ))"
ok "零交叉 txn_id：恰 1 个已提交事务、0 个悬挂事务、净增恰 1 个 commit"

# ---------------------------------------------------------------- 2. E16 分支（持锁 + 限时取锁）
step "② E16 分支：持锁期间真实 eg 限时取锁必超时 —— 非零退出 + E16 + 工作树逐字不变（R7：不断言退 5 / 不断言恰 1）"
seed_card
BH2="$(blockhash "${CUR_BLOCK}")"
FH2="$(filehash "${KDIR_REL}/${CARD}.md")"
cat >"${WORK}/plan_e16.json" <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"被锁挡住的写者",
 "base":{"${KDIR_REL}/${CARD}.md":"${FH2}"},
 "ops":[{"op":"replace_block","target":"${CARD}","section":"理解自检","initiator":"user",
         "reason":"应因锁忙而零写入","base_block_hash":"${BH2}","block":"- 被 E16 挡住、不该落盘的块。\n"}]}
JSON
READY="${WORK}/hold.ready"; rm -f "${READY}"
"${TXNCTL}" lock-hold --vault "${VAULT}" --hold-ms 4000 --ready "${READY}" \
  >"${WORK}/hold.out" 2>"${WORK}/hold.err" & HOLD_PID=$!
wait_ready "${READY}" || { cat "${WORK}/hold.out" "${WORK}/hold.err"; die "持锁进程未能取到锁"; }
grep -q 'acquired=1' "${WORK}/hold.out" || die "持锁进程应报 acquired=1"

BEFORE_PORC="$(porcelain)"; BEFORE_TREE="$(treesum)"; BEFORE_COMMITS="$(commits)"
CE=0
EG_LOCK_TIMEOUT_MS=300 "${EG}" --vault "${VAULT}" apply --plan "${WORK}/plan_e16.json" --json --user-request \
  </dev/null >"${WORK}/e16.out" 2>"${WORK}/e16.err" || CE=$?
[ "${CE}" != "0" ] || { cat "${WORK}/e16.out"; die "锁忙时限时取锁必须非零退出"; }
grep -Fq 'E16' "${WORK}/e16.out" || { cat "${WORK}/e16.out"; die "锁不可用报告必须含诊断码 E16"; }
[ "$(porcelain)" = "${BEFORE_PORC}" ] || die "E16 后 git status --porcelain 必须逐字不变（权威零写入）"
[ "$(treesum)" = "${BEFORE_TREE}" ] || die "E16 后整棵工作树字节必须逐字不变"
[ "$(commits)" = "${BEFORE_COMMITS}" ] || die "E16 后不得产生 commit"
if grep -Fq '被 E16 挡住、不该落盘的块。' "${CARDPATH}"; then die "被 E16 挡住的块一个字节都不许落盘"; fi
wait "${HOLD_PID}" 2>/dev/null || true
grep -q 'released=1' "${WORK}/hold.out" || die "持锁进程应正常释放"
ok "E16 分支：非零退出 + E16 + 工作树逐字不变 + 零 commit（后到者零写入）"

# ---------------------------------------------------------------- 3. 真实仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] concurrent_conflict.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"

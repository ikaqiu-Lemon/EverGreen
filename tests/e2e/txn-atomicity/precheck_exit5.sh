#!/usr/bin/env bash
# S3 写前强校验命中 → E15 + 退 5 + 本次请求零权威写入 端到端脚本
# （M6 · T-evergreen.s1_main_flow-158614-074）。
#
# 判据来源：docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md
#   §16.x（临界区内 S3 重新发现 / 重读 / 强校验；命中 → 携 E15、退 5、事务零权威写入、
#           **未分配 txn_id、未写 intent、无 commit、不发 W26**）
#   §9 A-56（--strict 升级面 W1/W2/W3/W4/W6）
#
# 「本次请求零权威写入」是原子性合同里最硬的一条：强校验在**锁内 S3**命中时，必须在任何
# 权威 rename / txn 分配**之前**中止。本脚本以**真实 eg 二进制 + 多条写命令**在**全新 vault**
# 上反证该不变量，同时用「同一 plan 去掉 --strict 就能落盘」证明 plan 本身合法、唯有 strict 拦下它：
#   ① rel add（reason 等于关系名 → W2）：--strict 退 5、携 E15；权威 Markdown 逐字不变、
#      零 commit、`.index/txn/` 全程不存在（未分配 txn_id / 未写 intent）、无 W26、vault 干净；
#      去掉 --strict 同一命令退 0、关系落盘、恰一次 commit（证 plan 合法，唯 strict 拦下）；
#   ② rel remove（reason 等于关系名 → W2）：在「已有一条关系」的 vault 上，--strict 退 5、携 E15、
#      零权威写入 / 零 commit / 无 txn / 无 W26；去掉 --strict 同一命令退 0、关系被物理移除、commit +1；
#   ③ 退出码 5 语义收口：两条写命令 --strict 命中均**恰退 5**（E15 面），
#      且信封 exit_code / status 与进程退出码三者一致。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复；无 jq / sqlite3 外部依赖（只用 bash/coreutils/awk/git/go）；
#   一切写与构建只发生在 mktemp -d 沙箱内，真实仓库工作区一个字节都不碰（末尾自查）；
#   任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/precheck_exit5.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-precheck.XXXXXX")"
EG="${WORK}/eg"

CARD_A='k-20270101-aaa'
CARD_B='k-20270101-bbb'
LONG_REASON='新卡结论支持既有卡的适用范围判断：两者在同一前提下互为佐证，理由字段写足以通过写前校验的长度。'

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

run() {
  local v="$1"; shift
  local c=0
  "${EG}" --vault "${v}" "$@" </dev/null >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?
  echo "${c}"
}
gitv()    { git -C "$1" -c user.email=eg@example.com -c user.name=eg "${@:2}"; }
commits() { gitv "$1" log --oneline | wc -l | tr -d ' '; }
# authority_sha <vault>：整个权威 Markdown 面（domains/ 等）的 sha256 快照，用于反证「零权威写入」。
authority_sha() {
  ( cd "$1" && { find domains sources proposals reviews -type f 2>/dev/null || true; } |
    sort | xargs -r sha256sum )
}
# txn_exists <vault>：`.index/txn/` 下是否已分配任何事务目录（seq 文件不算事务）。
txn_present() {
  [ -d "$1/.index/txn" ] || { echo 0; return; }
  local n
  n="$(find "$1/.index/txn" -mindepth 1 -maxdepth 1 -name 't*' -type d 2>/dev/null | wc -l | tr -d ' ')"
  echo "${n}"
}

seed_vault() {
  local v="$1" k id
  "${EG}" --vault "${v}" init --domain ai </dev/null >/dev/null 2>&1 || die "eg init 失败"
  "${EG}" --vault "${v}" config set default_domain ai </dev/null >/dev/null 2>&1 || die "config set 失败"
  k="${v}/domains/ai/knowledge"; mkdir -p "${k}"
  for id in "${CARD_A}" "${CARD_B}"; do
    printf '%s\n' '---' "id: ${id}" 'status: active' "created_at: '2027-01-01'" \
      "updated_at: '2027-01-01T10:00:00+08:00'" "title: ${id}" 'tags: [ai]' 'sources: []' \
      '---' '' '## 知识内容' '' '正文占位。' '' >"${k}/${id}.md"
  done
  gitv "${v}" add -A >/dev/null
  gitv "${v}" commit -q -m "seed: m6 precheck 语料（两张 active 卡）"
  [ "$(gitv "${v}" status --porcelain)" = "" ] || die "前置：seed 后 vault 应干净"
}

# assert_strict_blocked <vault> <what> <cmd...>：核心不变量断言。
#   跑一次 <cmd> --strict，断言：退 5 / exit_code=5 / status=failed / 携 E15 /
#   权威 Markdown 逐字不变 / 零新增 commit / 本次不新增 txn 目录（未分配 txn_id）/ 无 W26 / vault 干净。
assert_strict_blocked() {
  local v="$1" what="$2"; shift 2
  local sha_before commit_before txn_before c
  sha_before="$(authority_sha "${v}")"
  commit_before="$(commits "${v}")"
  txn_before="$(txn_present "${v}")"   # 之前合法写入可能已留下事务目录；本次命中不得**新增**任何事务
  c="$(run "${v}" "$@" --strict --json)"
  [ "${c}" = "5" ] || { cat "${WORK}/out.txt"; die "${what}：--strict 应退 5，实得 ${c}"; }
  grep -Fq '"exit_code":5' "${WORK}/out.txt" || die "${what}：信封 exit_code 应为 5"
  grep -Fq '"status":"failed"' "${WORK}/out.txt" || die "${what}：信封 status 应为 failed"
  grep -Fq '"code":"E15"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "${what}：应携 E15"; }
  grep -Fq '"code":"W26"' "${WORK}/out.txt" && die "${what}：precheck 命中在 txn 之前，绝不该发 W26（无事可回滚）"
  [ "$(authority_sha "${v}")" = "${sha_before}" ] || die "${what}：--strict 命中必须零权威写入（Markdown 逐字不变）"
  [ "$(commits "${v}")" = "${commit_before}" ] || die "${what}：--strict 命中不得产生 commit"
  [ "$(txn_present "${v}")" = "${txn_before}" ] ||
    die "${what}：--strict 命中不得分配新 txn_id（.index/txn/ 事务目录数应不变：${txn_before} → $(txn_present "${v}")）"
  [ "$(gitv "${v}" status --porcelain)" = "" ] || { gitv "${v}" status --porcelain; die "${what}：命中后 vault 应干净"; }
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 eg
step "构建 eg（CGO_ENABLED=0 静态）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "CGO_ENABLED=0 编译失败"
ok "eg 就绪：${EG}"

# ---------------------------------------------------------------- 2. rel add：strict 命中零权威写入
step "rel add（reason=关系名 → W2）：--strict 命中退 5 / E15 / 零权威写入 / 无 txn / 无 commit / 无 W26"
VA="${WORK}/vault.add"
seed_vault "${VA}"
assert_strict_blocked "${VA}" "rel add" rel add "${CARD_A}" supports "${CARD_B}" --reason supports
ok "rel add --strict：退 5 / E15 / 权威逐字不变 / 无 txn 分配 / 零 commit / 无 W26 / vault 干净"

# 反证 plan 本身合法：同一 rel add 去掉 --strict 就能落盘（证明是 strict 拦下、不是 plan 有硬 error）。
step "反证：同一 rel add 去掉 --strict 退 0、关系落盘、恰一次 commit（唯 --strict 拦下合法 plan）"
COMMIT_BEFORE="$(commits "${VA}")"
C="$(run "${VA}" rel add "${CARD_A}" supports "${CARD_B}" --reason supports --json)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "非 strict rel add 应退 0，实得 ${C}"; }
grep -Fq '"exit_code":0' "${WORK}/out.txt" || die "非 strict 信封 exit_code 应为 0"
grep -Fq "\"type\":\"supports\",\"target\":\"${CARD_B}\"" "${WORK}/out.txt" ||
  grep -Fq "\"target\":\"${CARD_B}\"" "${WORK}/out.txt" || die "非 strict 应写入 supports → ${CARD_B} 关系"
grep -Fq "supports" "${VA}/domains/ai/knowledge/${CARD_A}.md" || die "关系应落到 ${CARD_A}.md"
[ "$(commits "${VA}")" = "$((COMMIT_BEFORE + 1))" ] || die "非 strict rel add 应恰一次 commit"
ok "非 strict rel add：退 0 / 关系落盘 / commit +1 —— 证 plan 合法，strict 拦的是升级面而非硬 error"

# ---------------------------------------------------------------- 3. rel remove：strict 命中零权威写入
step "rel remove（reason=关系名 → W2）：先备一条真实关系，再验 --strict 命中零权威写入"
VR="${WORK}/vault.remove"
seed_vault "${VR}"
# 用足够长的 reason 先真实写入一条关系（非 strict、无 W2）；随后对其做 rel remove。
C="$(run "${VR}" rel add "${CARD_A}" supports "${CARD_B}" --reason "${LONG_REASON}" --json)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "备关系：rel add 应退 0，实得 ${C}"; }
grep -Fq "supports" "${VR}/domains/ai/knowledge/${CARD_A}.md" || die "备关系：关系未落盘"
# 现在对已存在的关系做 rel remove，reason 等于关系名 → W2；--strict 命中应零权威写入。
assert_strict_blocked "${VR}" "rel remove" rel remove "${CARD_A}" supports "${CARD_B}" --reason supports
# 关系仍在（strict 命中零写入，不能真把它删了）。
grep -Fq "supports" "${VR}/domains/ai/knowledge/${CARD_A}.md" ||
  die "rel remove --strict 命中后关系必须仍在（零权威写入）"
ok "rel remove --strict：退 5 / E15 / 关系仍在 / 无 txn / 零 commit / 无 W26 / vault 干净"

# 反证：同一 rel remove 去掉 --strict 就能物理移除该关系。
step "反证：同一 rel remove 去掉 --strict 退 0、关系被物理移除、commit +1"
COMMIT_BEFORE="$(commits "${VR}")"
C="$(run "${VR}" rel remove "${CARD_A}" supports "${CARD_B}" --reason supports --json)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "非 strict rel remove 应退 0，实得 ${C}"; }
grep -Fq "supports" "${VR}/domains/ai/knowledge/${CARD_A}.md" &&
  die "非 strict rel remove 后关系应被物理移除"
[ "$(commits "${VR}")" = "$((COMMIT_BEFORE + 1))" ] || die "非 strict rel remove 应恰一次 commit"
ok "非 strict rel remove：退 0 / 关系已移除 / commit +1 —— 证 plan 合法，strict 拦的是升级面"

# ---------------------------------------------------------------- 4. 退出码 5 语义收口（三者一致）
step "退出码 5 收口：两条写命令 --strict 命中的进程退出码 / 信封 exit_code / status 三者一致且恰为 5/failed"
VZ="${WORK}/vault.exit5"
seed_vault "${VZ}"
C="$(run "${VZ}" rel add "${CARD_A}" supports "${CARD_B}" --reason supports --strict --json)"
[ "${C}" = "5" ] || die "退出码收口：进程退出码应为 5"
grep -Fq '"exit_code":5' "${WORK}/out.txt" && grep -Fq '"status":"failed"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "退出码收口：信封 exit_code=5 且 status=failed 应同时成立"; }
ok "进程退出码=5、信封 exit_code=5、status=failed 三者一致（E15 面退出码 5 合同成立）"

# ---------------------------------------------------------------- 5. 真实仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] precheck_exit5.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"

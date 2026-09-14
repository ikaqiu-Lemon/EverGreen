#!/usr/bin/env bash
# `--strict` 写前强校验语义 + `eg check --strict` 恒等端到端脚本
# （M6 · T-evergreen.s1_main_flow-158614-074）。
#
# 判据来源：docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md
#   §9 A-56（默认宽松；显式 --strict 把升级面 W1/W2/W3/W4/W6 升 severity 为 error，**码逐字不变**；
#           W5 明确不在升级面；只改 severity 不改 code）
#   §16.x（强校验命中 → 携 E15、退 5、本次请求事务零权威写入）
# 以及 eg check 只判 R3/R4（finding 码段 E11–E14 / W13–W20，与升级面 W1/W2/W3/W4/W6 不相交）。
#
# 本脚本以**真实 eg 二进制 + 真实写命令**反证，只守「strict 语义面」（原子性 / 无 txn 面走
# m6_precheck_exit5.sh，两本互不覆盖）：
#   ① 默认宽松：rel add（reason 等于关系名 → W2）默认退 0、W2 为 warning、关系照常写入；
#   ② 显式 --strict：同一 plan 退 5、携 E15，且 **W2 码逐字仍在**（升级只改结局不改码）、
#      并留一条点名 W2 的 strict 拦截说明 —— 证「升 error / 码不变」这条合同；
#   ③ eg check --strict 恒等：与 eg check **退出码逐位相同**、**永不产 E15 / 永不退 5**，
#      两次运行的 finding 码集合（剔除 info 说明）逐字一致 —— strict 对 check finding 是恒等变换，
#      唯一增量是一条透明说明（CheckStrictNotice），不改任何 finding 的码与 severity。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复；无 jq / sqlite3 外部依赖（只用 bash/coreutils/awk/git/go）；
#   一切写与构建只发生在 mktemp -d 沙箱内，真实仓库工作区一个字节都不碰（末尾自查）；
#   任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/ops-diagnostics/strict_check.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-strict.XXXXXX")"
EG="${WORK}/eg"

CARD_A='k-20270101-aaa'   # active
CARD_B='k-20270101-bbb'   # active

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

# run <vault> <args...>：跑 eg（stdin 接 /dev/null），stdout+stderr 落 out.txt，回显退出码。
run() {
  local v="$1"; shift
  local c=0
  "${EG}" --vault "${v}" "$@" </dev/null >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?
  echo "${c}"
}
gitv()    { git -C "$1" -c user.email=eg@example.com -c user.name=eg "${@:2}"; }
commits() { gitv "$1" log --oneline | wc -l | tr -d ' '; }
cardsha() { sha256sum "$1/domains/ai/knowledge/${CARD_A}.md" | awk '{print $1}'; }
# codes <文件>：warnings[] 里所有非空 code 的序列（原序，剔除空串），供逐字比对。
codes() { grep -oE '"code":"[^"]+"' "$1" | sed 's/"code":"//; s/"$//'; }
# find_codes <文件>：仅保留 check finding 码段（E11–E14 / W13–W20），剔除 info(I*) 说明与其它。
find_codes() { codes "$1" | grep -E '^(E1[1-4]|W1[3-9]|W20)$' || true; }

# seed_vault <dir>：造一个带两张 active 卡的干净 vault 并提交，回显 baseline commit 数。
seed_vault() {
  local v="$1" k
  "${EG}" --vault "${v}" init --domain ai </dev/null >/dev/null 2>&1 || die "eg init 失败"
  "${EG}" --vault "${v}" config set default_domain ai </dev/null >/dev/null 2>&1 || die "config set 失败"
  k="${v}/domains/ai/knowledge"; mkdir -p "${k}"
  local id
  for id in "${CARD_A}" "${CARD_B}"; do
    printf '%s\n' '---' "id: ${id}" 'status: active' "created_at: '2027-01-01'" \
      "updated_at: '2027-01-01T10:00:00+08:00'" "title: ${id}" 'tags: [ai]' 'sources: []' \
      '---' '' '## 知识内容' '' '正文占位。' '' >"${k}/${id}.md"
  done
  gitv "${v}" add -A >/dev/null
  gitv "${v}" commit -q -m "seed: m6 strict 语料（两张 active 卡）"
  [ "$(gitv "${v}" status --porcelain)" = "" ] || die "前置：seed 后 vault 应干净"
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 eg
step "构建 eg（CGO_ENABLED=0 静态）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "CGO_ENABLED=0 编译失败"
ok "eg 就绪：${EG}"

# ---------------------------------------------------------------- 2. 默认宽松：W2 照写、退 0
step "默认（无 --strict）：rel add（reason 等于关系名 → W2）退 0、W2 为 warning、关系照常写入"
VA="${WORK}/vault.default"
seed_vault "${VA}"
BASE_A="$(commits "${VA}")"
SHA_A_BEFORE="$(cardsha "${VA}")"
C="$(run "${VA}" rel add "${CARD_A}" supports "${CARD_B}" --reason supports --json)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "默认 rel add 应退 0，实得 ${C}"; }
grep -Fq '"exit_code":0' "${WORK}/out.txt" || die "默认信封 exit_code 应为 0"
grep -Fq '"status":"completed"' "${WORK}/out.txt" || die "默认信封 status 应为 completed"
grep -Eq '"code":"W2","level":"warning"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "默认应含 W2(warning)"; }
grep -Fq '"code":"E15"' "${WORK}/out.txt" && die "默认路径绝不该出现 E15"
[ "$(cardsha "${VA}")" != "${SHA_A_BEFORE}" ] || die "默认 rel add 应真实写入 ${CARD_A}（字节应变化）"
[ "$(commits "${VA}")" = "$((BASE_A + 1))" ] || die "默认 rel add 应恰产一次 commit"
ok "默认：退 0 / W2=warning / 关系已写入 / commit +1 / 无 E15"

# ---------------------------------------------------------------- 3. --strict：升 error、码不变、退 5
step "--strict：同一 plan 退 5、携 E15、W2 码逐字仍在（升级只改结局不改码），零权威写入"
VB="${WORK}/vault.strict"
seed_vault "${VB}"
BASE_B="$(commits "${VB}")"
SHA_B_BEFORE="$(cardsha "${VB}")"
C="$(run "${VB}" rel add "${CARD_A}" supports "${CARD_B}" --reason supports --strict --json)"
[ "${C}" = "5" ] || { cat "${WORK}/out.txt"; die "--strict rel add 应退 5，实得 ${C}"; }
grep -Fq '"exit_code":5' "${WORK}/out.txt" || die "--strict 信封 exit_code 应为 5"
grep -Fq '"status":"failed"' "${WORK}/out.txt" || die "--strict 信封 status 应为 failed"
grep -Fq '"code":"E15"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "--strict 应携 E15（写前强校验失败）"; }
# 合同核心：升级只改 severity 不改 code —— W2 这个码在 strict 结局里逐字仍在。
grep -Fq '"code":"W2"' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "--strict 结局里 W2 码必须逐字仍在（码不变）"; }
# 且留一条点名 W2 的 strict 拦截说明（人类可读交代升级面命中了哪个码）。
grep -Fq '升级面命中：W2' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "--strict 应说明升级面命中 W2"; }
[ "$(cardsha "${VB}")" = "${SHA_B_BEFORE}" ] || die "--strict 命中应零权威写入（${CARD_A} 字节须不变）"
[ "$(commits "${VB}")" = "${BASE_B}" ] || die "--strict 命中不得产生 commit"
[ "$(gitv "${VB}" status --porcelain)" = "" ] || { gitv "${VB}" status --porcelain; die "--strict 命中后 vault 应干净"; }
ok "--strict：退 5 / E15 / W2 码逐字仍在 / 零权威写入 / 零 commit / vault 干净"

# ---------------------------------------------------------------- 4. eg check --strict 恒等
step "eg check --strict 恒等：与 eg check 退出码逐位相同、永不 E15 / 永不退 5、finding 码集合一致"
VC="${WORK}/vault.check"
seed_vault "${VC}"
C1="$(run "${VC}" check --json)"; cp "${WORK}/out.txt" "${WORK}/check.plain.json"
C2="$(run "${VC}" check --strict --json)"; cp "${WORK}/out.txt" "${WORK}/check.strict.json"
[ "${C1}" = "${C2}" ] || die "check 与 check --strict 退出码应逐位相同（${C1} ≠ ${C2}）"
grep -Fq '"code":"E15"' "${WORK}/check.strict.json" && die "eg check --strict 绝不该产 E15"
[ "${C2}" != "5" ] || die "eg check --strict 绝不该退 5"
# finding 码集合（E11–E14 / W13–W20）逐字一致：strict 对 check finding 是恒等变换。
diff <(find_codes "${WORK}/check.plain.json") <(find_codes "${WORK}/check.strict.json") >/dev/null ||
  { diff <(find_codes "${WORK}/check.plain.json") <(find_codes "${WORK}/check.strict.json") || true
    die "check --strict 改动了 R3/R4 finding 码集合（应恒等）"; }
# 唯一允许的增量：一条 CheckStrictNotice 透明说明。
grep -Fq 'eg check --strict 对本命令 finding 是恒等变换' "${WORK}/check.strict.json" ||
  { cat "${WORK}/check.strict.json"; die "check --strict 应留一条 CheckStrictNotice 说明"; }
grep -Fq 'eg check --strict 对本命令 finding 是恒等变换' "${WORK}/check.plain.json" &&
  die "无 --strict 的 check 不应出现 CheckStrictNotice"
ok "check(${C1}) 与 check --strict(${C2}) 退出码相同；finding 码集合逐字一致；无 E15、非 5；仅多一条透明说明"

# ---------------------------------------------------------------- 5. 真实仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] strict_check.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"

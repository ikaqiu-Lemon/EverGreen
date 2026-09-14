#!/usr/bin/env bash
# `run.lock` 跨进程互斥端到端脚本（M6 · T-evergreen.s1_main_flow-158614-071）。
#
# 判据来源：docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md
#   §3（run.lock 唯一位置 vault/.index/run.lock；内核 flock 为互斥真源；忙时绝不强抢 / 删锁；
#      持锁期锁文件 inode 零替换；Release 只 LOCK_UN+Close，不删文件；正文纯诊断可容错）
#   §16.1（取锁与 txn_id 分配 / intent 写入解耦：B 类维护命令可只取锁不开事务）
#   §16.3 / R8（run.lock 是 symlink / 非普通文件时写前 fail closed，携 E15，目标字节零触碰）
#   §12（锁不可用超时携 E16；等待期 W28 留痕）
#
# 本脚本以**真实两进程**反证互斥（而非同进程二次 flock，那样拿不到跨 fd 的真互斥）：
#   ① 互斥 + E16：A 持锁期间 B 限时取锁必超时得 E16，A 释放后 B 立即取到；
#   ② 陈旧锁接管：持锁进程被 kill -9（不走 Release），内核自动释放 flock，
#      新进程无需强抢 / 删锁即可接管；且 run.lock 文件在全过程始终在盘（inode 不变）；
#   ③ Release 不删文件 + inode 零替换：一轮取用后 run.lock 仍在、inode 与初次一致；
#   ④ symlink fail closed：run.lock 预置为指向 vault 外文件的 symlink 时取锁得 E15，
#      外部目标字节零改动；
#   ⑤ 锁与 txn 解耦：默认取锁不带 txn_id（holder_txn 空），--record-txn 后正文补写 txn_id。
#
# 不钉具体退出码：一切语义判定读 helper 的 stdout 标记（acquired= / code=E16 / code=E15 / holder_*），
# 不断言进程退出码的具体数值（合同不钉退出码，退码映射属 T-…-074）。
#
# 约束：离线、零交互、可重复；无 jq / sqlite3 外部依赖（只用 bash/coreutils/go）；
#   一切写与构建只发生在 mktemp -d 沙箱内，真实仓库工作区一个字节都不碰（末尾自查）；
#   任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/run_lock.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-run-lock.XXXXXX")"
VAULT="${WORK}/vault"
TXNCTL="${WORK}/txnctl"
LOCK="${VAULT}/.index/run.lock"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

# ctl <args...>：跑 helper，stdout 落到 out.txt 并回显（供 grep 断言）。
ctl() { "${TXNCTL}" "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt"; cat "${WORK}/out.txt"; }
# inode <path>：稳定取 inode 号（缺文件即空）。
inode() { eg_stat_inode "$1" 2>/dev/null || true; }

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 helper + 备好 vault
step "构建 txnctl（test-only helper）并备好 vault/.index/"
(cd "${REPO_ROOT}" && go build -o "${TXNCTL}" ./tests/lib/txnctl) || die "txnctl 构建失败"
mkdir -p "${VAULT}/.index"
ok "helper 就绪：${TXNCTL}"

# ---------------------------------------------------------------- 2. 互斥 + E16 超时
step "两进程互斥：A 持锁期间 B 限时取锁得 E16；A 释放后 B 立即取到"
READY="${WORK}/ready.a"
rm -f "${READY}"
# A：后台持锁 2s，取到即写就绪文件。
"${TXNCTL}" lock-hold --vault "${VAULT}" --hold-ms 2000 --ready "${READY}" \
  >"${WORK}/a.out" 2>"${WORK}/a.err" &
A_PID=$!
# 等 A 真正取到锁（就绪文件出现），最多等 5s。
for _ in $(seq 1 100); do [ -f "${READY}" ] && break; sleep 0.05; done
[ -f "${READY}" ] || { cat "${WORK}/a.out" "${WORK}/a.err"; die "A 未能在时限内取到锁"; }
grep -q 'acquired=1' "${WORK}/a.out" || die "A 应报 acquired=1"
# B：A 仍持锁时限时 200ms 取锁 —— 必超时得 E16。
ctl lock-try --vault "${VAULT}" --timeout-ms 200 || true
grep -q 'code=E16' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "占用期 B 应得 code=E16"; }
grep -q 'acquired=1' "${WORK}/out.txt" && die "占用期 B 绝不该取到锁"
# 等 A 释放。
wait "${A_PID}"
grep -q 'released=1' "${WORK}/a.out" || die "A 应正常释放"
# B 再取 —— 现在应立即取到。
ctl lock-try --vault "${VAULT}" --timeout-ms 2000
grep -q 'acquired=1' "${WORK}/out.txt" || die "A 释放后 B 应取到锁"
ok "占用期 B 得 E16 且未取到；A 释放后 B 立即取到"

# ---------------------------------------------------------------- 3. 陈旧锁接管（kill -9，不走 Release）
step "持锁进程被 kill -9：内核自动释放 flock，新进程无需强抢 / 删锁即可接管"
INODE_BEFORE="$(inode "${LOCK}")"
[ -n "${INODE_BEFORE}" ] || die "前置：run.lock 应已在盘"
READY2="${WORK}/ready.b"
rm -f "${READY2}"
# 持锁 60s（远超测试时长），取到即写就绪文件，随后我们 kill -9 它（模拟崩溃）。
"${TXNCTL}" lock-hold --vault "${VAULT}" --hold-ms 60000 --ready "${READY2}" \
  >"${WORK}/c.out" 2>"${WORK}/c.err" &
C_PID=$!
for _ in $(seq 1 100); do [ -f "${READY2}" ] && break; sleep 0.05; done
[ -f "${READY2}" ] || { cat "${WORK}/c.out" "${WORK}/c.err"; die "崩溃前进程未能取到锁"; }
# 硬杀：不给它机会跑 Release。
kill -9 "${C_PID}" 2>/dev/null || true
wait "${C_PID}" 2>/dev/null || true
# 接管：新进程限时取锁应成功（内核在被杀进程 fd 关闭时已释放 flock）。
ctl lock-try --vault "${VAULT}" --timeout-ms 3000
grep -q 'acquired=1' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "陈旧锁应可被接管"; }
INODE_AFTER="$(inode "${LOCK}")"
[ -n "${INODE_AFTER}" ] || die "接管后 run.lock 应仍在盘（绝不删锁）"
[ "${INODE_BEFORE}" = "${INODE_AFTER}" ] ||
  die "接管未替换 inode：${INODE_BEFORE} → ${INODE_AFTER}（不得 tmp+rename / unlink）"
ok "崩溃后新进程无强抢接管；run.lock 全程在盘且 inode 不变（${INODE_AFTER}）"

# ---------------------------------------------------------------- 4. Release 不删文件 + inode 零替换
step "一轮取用后 run.lock 仍在盘、inode 与初次一致"
ctl lock-try --vault "${VAULT}" --timeout-ms 2000
grep -q 'released=1' "${WORK}/out.txt" || die "lock-try 成功路径应含 released=1"
ctl lock-inspect --vault "${VAULT}"
grep -q 'exists=1' "${WORK}/out.txt" || die "Release 之后 run.lock 必须仍在盘"
INODE_NOW="$(inode "${LOCK}")"
[ "${INODE_NOW}" = "${INODE_BEFORE}" ] ||
  die "run.lock inode 被替换：${INODE_BEFORE} → ${INODE_NOW}"
ok "run.lock 仍在盘、inode 恒为 ${INODE_NOW}（Release 只解锁不删文件）"

# ---------------------------------------------------------------- 5. symlink fail closed（E15）
step "run.lock 预置为指向 vault 外文件的 symlink：取锁 fail closed 得 E15，外部目标零改动"
SVAULT="${WORK}/svault"
mkdir -p "${SVAULT}/.index"
EXTERNAL="${WORK}/external.secret"
printf 'DO-NOT-TOUCH\n' >"${EXTERNAL}"
EXT_SHA_BEFORE="$(sha256sum "${EXTERNAL}")"
ln -s "${EXTERNAL}" "${SVAULT}/.index/run.lock"
ctl lock-try --vault "${SVAULT}" --timeout-ms 500 || true
grep -q 'code=E15' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "symlink run.lock 应得 code=E15" ; }
grep -q 'acquired=1' "${WORK}/out.txt" && die "symlink run.lock 绝不该取到锁"
EXT_SHA_AFTER="$(sha256sum "${EXTERNAL}")"
[ "${EXT_SHA_BEFORE}" = "${EXT_SHA_AFTER}" ] ||
  die "symlink 目标字节被动过：O_NOFOLLOW 应保证零触碰"
ok "symlink run.lock → E15 且未取锁；外部目标字节零改动"

# ---------------------------------------------------------------- 6. 锁与 txn_id 解耦
step "取锁与 txn 分配解耦：默认取锁 holder_txn 空；--record-txn 后正文补写 txn_id"
DVAULT="${WORK}/dvault"
mkdir -p "${DVAULT}/.index"
# 默认取锁不开事务（B 类维护命令语义）：短持锁即可，holder_txn 应为空。
READY3="${WORK}/ready.d"
rm -f "${READY3}"
"${TXNCTL}" lock-hold --vault "${DVAULT}" --hold-ms 800 --ready "${READY3}" \
  >"${WORK}/d.out" 2>"${WORK}/d.err" &
D_PID=$!
for _ in $(seq 1 100); do [ -f "${READY3}" ] && break; sleep 0.05; done
  [ -f "${READY3}" ] || die "解耦用例：进程未取到锁"
  ctl lock-inspect --vault "${DVAULT}"
  grep -q 'holder_ok=1' "${WORK}/out.txt" || die "解耦用例：holder 应可读"
  TXNVAL="$(grep -oE 'holder_txn=[^ ]*' "${WORK}/out.txt" | sed 's/holder_txn=//')"
  [ -z "${TXNVAL}" ] || die "默认取锁不应带 txn_id（取锁不开事务），实得 ${TXNVAL}"
  wait "${D_PID}"
# 带 --record-txn：正文补写 txn_id，holder 可读且带该 txn。
# 先删就绪文件再启动后台进程 —— 否则 helper 可能已 touch，主脚本随后的 rm 会造成竞态误删。
rm -f "${READY3}.2"
"${TXNCTL}" lock-hold --vault "${DVAULT}" --hold-ms 800 --ready "${READY3}.2" \
  --record-txn t000000000000abcd >"${WORK}/d2.out" 2>"${WORK}/d2.err" &
D2_PID=$!
for _ in $(seq 1 100); do [ -f "${READY3}.2" ] && break; sleep 0.05; done
ctl lock-inspect --vault "${DVAULT}"
grep -q 'holder_txn=t000000000000abcd' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "--record-txn 后 holder 应带该 txn_id"; }
wait "${D2_PID}"
ok "默认取锁 holder_txn 空；--record-txn 后正文补写 txn_id 且可读"

# ---------------------------------------------------------------- 7. S0 锁外候选发现期间他人可取锁
step "锁外阶段（S0 长时候选提示，Acquire 之前）另一进程可立即取锁 / 释放；随后前者才取锁"
OVAULT="${WORK}/ovault"
mkdir -p "${OVAULT}/.index"
SCANREADY="${WORK}/scan.ready"
DONE_A="${WORK}/a.slow.out"
# 先删就绪文件，再启动慢进程 A：A 先发 scan-ready、锁外停留 1.5s，之后才 Acquire 并短持锁。
rm -f "${SCANREADY}"
"${TXNCTL}" lock-hold --vault "${OVAULT}" --before-lock-ms 1500 --scan-ready "${SCANREADY}" \
  --hold-ms 300 --argv 'txnctl slow-s0' >"${DONE_A}" 2>"${WORK}/a.slow.err" &
SLOW_PID=$!
# 等 A 进入锁外阶段（scan-ready 出现，但此刻 A 尚未 Acquire）。
for _ in $(seq 1 100); do [ -f "${SCANREADY}" ] && break; sleep 0.05; done
[ -f "${SCANREADY}" ] || { cat "${DONE_A}"; die "A 未进入锁外候选阶段"; }
# 此窗口内 B 应能**立即**取锁并释放（因为 A 还没取锁）。用较短超时反证「立即可取」而非「等 A 超时」。
ctl lock-try --vault "${OVAULT}" --timeout-ms 300
grep -q 'acquired=1' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt" "${DONE_A}"; die "锁外候选期间 B 应能立即取锁（S0 不在临界区）"; }
grep -q 'released=1' "${WORK}/out.txt" || die "B 应随即释放"
# 待 A 走完（它在锁外停留后才 Acquire、短持、释放），确认 A 最终也成功取到并释放。
wait "${SLOW_PID}"
grep -q 'acquired=1' "${DONE_A}" || { cat "${DONE_A}"; die "A 在锁外阶段结束后应成功取锁"; }
grep -q 'released=1' "${DONE_A}" || die "A 应正常释放"
ok "S0 锁外候选期间 B 立即取到锁；A 锁外结束后再取锁，二者互不阻塞"

# ---------------------------------------------------------------- 8. 真实仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] run_lock.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"

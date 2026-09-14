#!/usr/bin/env bash
# 多文件原子提交端到端脚本（M6 · T-evergreen.s1_main_flow-158614-072）。
#
# 判据来源：docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md
#   §4（临时文件 + fsync + 原子 rename；commit 标记 = Markdown 事务提交点；发布屏障先于首个权威 rename）
#   §5.1（四级可见性：L1 任意观察者永不见撕裂半写文件；L2 协作 eg 写者之间永不见跨文件中间态；
#         L3 恢复屏障语义——半应用态可跨进程 / 重启长期留盘并被只读命令看到，S2 完成后回到前像；
#         L4 非协作观察者只享 L1，不承诺跨文件瞬时同时可见、也不声称半应用态不被持久保留）
#
# 反证清单（accepted write-set「全成或全不成」以**协作 eg 写者视角**与**恢复屏障 S2 完成后的持久态**为判据）：
#   ① N ∈ {1, 2, 5} 三档：atomic-commit 成功后每档全部文件同时到目标态（全成）、commit 标记在盘；
#   ② 提交幂等：对已提交事务再跑一次 Commit，结果不变、内容字节逐字不变、无 .tmp 残留；
#   ③ 无撕裂（L1）：分阶段提交（rename 之间人为拉开窗口）期间并发读循环采样，任一文件内容恒 ∈ {前像, 目标态}，
#      绝不出现半写字节 —— 即便此刻跨文件处于「部分新、部分旧」的中间态（L4 允许非协作观察者看到）；
#   ④ 半应用态持久留盘（L3 前半）：崩溃在 partial / all_renamed 且**不跑写命令**时，半应用态仍在盘、被只读观察者看到；
#   ⑤ 恢复屏障回到前像（L3 后半 + 全不成）：跑一次写命令（recover 屏障）后，accepted write-set 全部回到前像、产 W26。
#
# 不钉具体退出码：语义判定读 helper 的 stdout 标记（committed= / txn_id= / outcome= / w26=），
#   不断言进程退出码的具体数值（退码映射属 T-…-074）。
#
# 约束：离线、零交互、可重复；无 jq / sqlite3 外部依赖（只用 bash/coreutils/go）；
#   一切写与构建只发生在 mktemp -d 沙箱内，真实仓库工作区一个字节都不碰（末尾自查）；
#   任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/atomic_multifile.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-atomic.XXXXXX")"
TXNCTL="${WORK}/txnctl"

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
# new_vault：造一个带 .index/ 的干净 vault，回显其路径。
new_vault() { local v; v="$(mktemp -d "${WORK}/vault.XXXXXX")"; mkdir -p "${v}/.index"; echo "${v}"; }

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 helper
step "构建 txnctl（test-only helper）"
(cd "${REPO_ROOT}" && go build -o "${TXNCTL}" ./tests/lib/txnctl) || die "txnctl 构建失败"
ok "helper 就绪：${TXNCTL}"

# ---------------------------------------------------------------- 2. N ∈ {1,2,5} 全成
step "N ∈ {1, 2, 5}：atomic-commit 成功后每档全部文件同时到目标态、commit 标记在盘"
for N in 1 2 5; do
  V="$(new_vault)"
  FILES=()
  for i in $(seq 1 "${N}"); do FILES+=(--file "f${i}.md|true||TARGET-${i}"); done
  ctl atomic-commit --vault "${V}" "${FILES[@]}"
  grep -q 'committed=true' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "N=${N} 提交应成功"; }
  grep -q "files_written=${N}" "${WORK}/out.txt" || die "N=${N} 应写 ${N} 个文件"
  for i in $(seq 1 "${N}"); do
    [ "$(cat "${V}/f${i}.md")" = "TARGET-${i}" ] || die "N=${N} f${i}.md 未到目标态"
  done
  TXN="$(grep -oE 'txn_id=t[0-9a-f]{16}' "${WORK}/out.txt" | sed 's/txn_id=//')"
  [ -f "${V}/.index/txn/${TXN}/commit" ] || die "N=${N} commit 标记应在盘"
  ctl scan --vault "${V}"
  grep -qE "^STATE ${TXN} committed" "${WORK}/out.txt" || die "N=${N} 事务应判 committed"
done
ok "N=1 / 2 / 5 三档均全成：全部文件到目标态、commit 标记在盘、Scan 判 committed"

# ---------------------------------------------------------------- 3. 提交幂等
step "提交幂等：对已提交事务再跑一次 Commit，结果不变、内容字节逐字不变、无 .tmp 残留"
V="$(new_vault)"
ctl atomic-commit --vault "${V}" --file "a.md|false|OLD-A|NEW-A" --file "b.md|false|OLD-B|NEW-B"
TXN="$(grep -oE 'txn_id=t[0-9a-f]{16}' "${WORK}/out.txt" | sed 's/txn_id=//')"
SHA_A="$(sha256sum "${V}/a.md" | awk '{print $1}')"
SHA_B="$(sha256sum "${V}/b.md" | awk '{print $1}')"
# 再跑一次 Commit（同 txn、同写集）——应幂等成功、内容逐字不变。
ctl commit --vault "${V}" --txn "${TXN}" --file "a.md|false|OLD-A|NEW-A" --file "b.md|false|OLD-B|NEW-B"
grep -q 'committed=true' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "二次 Commit 应幂等成功"; }
[ "$(sha256sum "${V}/a.md" | awk '{print $1}')" = "${SHA_A}" ] || die "幂等提交改动了 a.md 字节"
[ "$(sha256sum "${V}/b.md" | awk '{print $1}')" = "${SHA_B}" ] || die "幂等提交改动了 b.md 字节"
[ -z "$(find "${V}" -name '*.crashtmp' -o -name '*.tmp' 2>/dev/null)" ] || die "幂等提交不应残留 .tmp"
ok "二次 Commit 幂等成功、a/b 字节逐字不变、无临时产物残留"

# ---------------------------------------------------------------- 4. 无撕裂（L1）+ 跨文件中间态（L4）
step "分阶段提交期间并发读循环：任一文件内容恒 ∈ {前像, 目标态}，绝不出现撕裂（半写字节）"
V="$(new_vault)"
# 5 文件、rename 之间停留 150ms，给读者足够窗口采样。首个 rename 前 touch READY 让读者精准起跑。
READY="${WORK}/rdy"
rm -f "${READY}"
declare -a PRE=(OLD-0 OLD-1 OLD-2 OLD-3 OLD-4)
declare -a TGT=(TARGET-0 TARGET-1 TARGET-2 TARGET-3 TARGET-4)
"${TXNCTL}" atomic-commit --vault "${V}" --pause-ms 150 --ready "${READY}" \
  --file "s0.md|false|OLD-0|TARGET-0" \
  --file "s1.md|false|OLD-1|TARGET-1" \
  --file "s2.md|false|OLD-2|TARGET-2" \
  --file "s3.md|false|OLD-3|TARGET-3" \
  --file "s4.md|false|OLD-4|TARGET-4" >"${WORK}/stage.out" 2>"${WORK}/stage.err" &
STAGE_PID=$!
# 等待首个 rename 前的就绪信号。
for _ in $(seq 1 200); do [ -f "${READY}" ] && break; sleep 0.01; done
[ -f "${READY}" ] || { cat "${WORK}/stage.out" "${WORK}/stage.err"; die "分阶段提交未发出就绪信号"; }
SAW_MID=0
# 并发读循环：反复采样每个文件，断言每次读到的都是完整的 {前像 or 目标}，绝不撕裂。
for _ in $(seq 1 400); do
  MIX_NEW=0; MIX_OLD=0
  for i in 0 1 2 3 4; do
    f="${V}/s${i}.md"
    [ -f "${f}" ] || continue
    c="$(cat "${f}")"
    if [ "${c}" = "${TGT[$i]}" ]; then MIX_NEW=1
    elif [ "${c}" = "${PRE[$i]}" ]; then MIX_OLD=1
    else die "读到撕裂内容：s${i}.md = ${c}（既非前像 ${PRE[$i]} 也非目标 ${TGT[$i]}）"; fi
  done
  # 同时出现「已新」与「仍旧」= 捕获到跨文件中间态（L4 允许非协作观察者看到）。
  [ "${MIX_NEW}" = 1 ] && [ "${MIX_OLD}" = 1 ] && SAW_MID=1
  kill -0 "${STAGE_PID}" 2>/dev/null || break
  sleep 0.01
done
wait "${STAGE_PID}"
grep -q 'committed=true' "${WORK}/stage.out" || { cat "${WORK}/stage.out"; die "分阶段提交应成功收尾"; }
for i in 0 1 2 3 4; do
  [ "$(cat "${V}/s${i}.md")" = "${TGT[$i]}" ] || die "收尾后 s${i}.md 应到目标态"
done
[ "${SAW_MID}" = 1 ] || die "分阶段窗口内应至少捕获一次跨文件中间态（否则窗口太窄，判据无效）"
ok "并发读全程无撕裂；且捕获到跨文件中间态（L4）—— 收尾后全部到目标态"

# ---------------------------------------------------------------- 5. 半应用态持久留盘（L3 前半）+ 恢复回到前像（全不成）
step "崩溃在 partial：不跑写命令时半应用态仍在盘（L3）；跑一次写命令（recover）后全部回到前像（全不成）+ W26"
V="$(new_vault)"
READY2="${WORK}/rdy2"
rm -f "${READY2}"
# 5 文件事务在 partial 崩溃：首个已 rename 到目标、其余仍前像，随后被真实 kill -9。
"${TXNCTL}" crash-commit --vault "${V}" --crash-at partial --ready "${READY2}" \
  --file "c0.md|false|OLD-0|TARGET-0" \
  --file "c1.md|false|OLD-1|TARGET-1" \
  --file "c2.md|false|OLD-2|TARGET-2" \
  --file "c3.md|false|OLD-3|TARGET-3" \
  --file "c4.md|false|OLD-4|TARGET-4" >"${WORK}/crash.out" 2>"${WORK}/crash.err" &
CRASH_PID=$!
for _ in $(seq 1 200); do [ -f "${READY2}" ] && break; sleep 0.02; done
[ -f "${READY2}" ] || { cat "${WORK}/crash.out" "${WORK}/crash.err"; die "崩溃进程未到达 partial 点"; }
kill -9 "${CRASH_PID}" 2>/dev/null || true
wait "${CRASH_PID}" 2>/dev/null || true
# L3 前半：不跑写命令时，半应用态（c0 已新、c1..c4 仍旧）持久留盘、被只读观察者看到。
[ "$(cat "${V}/c0.md")" = "TARGET-0" ] || die "半应用态：c0.md 应已是目标态并持久留盘"
for i in 1 2 3 4; do
  [ "$(cat "${V}/c${i}.md")" = "OLD-${i}" ] || die "半应用态：c${i}.md 应仍为前像"
done
ok "崩溃后不跑写命令：半应用态（c0 新、其余旧）持久留盘（L3 前半 / L4 事实）"
# L3 后半 + 全不成：跑一次写命令（recover 屏障）后，accepted write-set 全部回到前像、产 W26。
ctl recover --vault "${V}"
grep -q 'outcome=rolledback' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "recover 应回滚未闭合事务"; }
grep -q 'w26=1' "${WORK}/out.txt" || die "回滚应恰产一条 W26"
for i in 0 1 2 3 4; do
  [ "$(cat "${V}/c${i}.md")" = "OLD-${i}" ] || die "恢复屏障后 c${i}.md 应回到前像（全不成）"
done
ok "跑一次写命令后恢复屏障回到前像：accepted write-set 全不成、W26 留痕（L3 后半）"

# ---------------------------------------------------------------- 6. 真实仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] atomic_multifile.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"

#!/usr/bin/env bash
# 崩溃恢复端到端脚本（M6 · T-evergreen.s1_main_flow-158614-072）。
#
# 判据来源：docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md
#   §4.1（严格两遍恢复：Pass A 只读分类 B-R1/B-R2/B-R3 + 前像可用性校验，Pass B 幂等回滚后才写 abort）
#   §4.1.2（全局裁决：多 OpenTxn / 任一 CorruptTxn ⇒ 任何回滚写之前整体 fail closed、零写入）
#   §6（崩溃点矩阵：只有 P2/P3/P4 发 W26；commit 标记在盘 ⇒ 不回滚、不发 W26、目标态保留）
#   §4.2（无 intent.json ⇒ pre-intent residue：静默清理、不发 W26、不阻断；
#         intent.json 在盘但不可解析 ⇒ fail closed：原样保留、权威零写入、携 E15）
#
# ★ 崩溃全部用**真实进程 + 真实 kill -9** 复现（脚本内必须出现 kill -9，不接受 mock）：
#   crash-commit 用导出 API 把事务真实推进到指定崩溃点（真写 intent / 真备前像 / 真原子 rename /
#   真写 commit 标记）后长眠，由脚本执行真实 kill -9 杀死它——被杀后磁盘留态与「真实崩溃在该点」逐字一致。
#
# 反证清单：
#   8 个崩溃点逐点真杀（P1~P8），恢复后磁盘态与矩阵逐行相符、W26 分布逐行一致（只有 P2/P3/P4 发 W26）、
#   权威 Markdown 无一字节错写（回滚点逐字回到前像、committed 点逐字保持目标态）；恢复幂等（再跑结果不变）；
#   另覆盖三条非常规形态：
#     ① post-crash 外部编辑冲突（B-R3）⇒ 非零退出 + E15、该文件逐字节保持用户编辑、事务仍未闭合、无 W26、复跑仍阻断；
#     ② pre-intent residue（无 intent.json）⇒ 退 0、静默清理、无 W26、权威零改动；
#     ③ 损坏 intent.json（截断成非法 JSON）⇒ 非零退出 + E15、目录与文件逐字节原样保留、权威零写入、无 W26。
#
# 本 task 不断言退出码数字（既不断言退 5、也不断言恰 1——退 5 由 074/075 承接）：语义判定读
#   helper 的 stdout 标记（outcome= / w26= / code=E15 / residue_cleaned=），只断言「诊断码 + 权威零错写 + 事务/磁盘态」。
#
# 约束：离线、零交互、可重复；无 jq / sqlite3 外部依赖（只用 bash/coreutils/go）；
#   一切写与构建只发生在 mktemp -d 沙箱内，真实仓库工作区一个字节都不碰（末尾自查）；
#   任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/crash_recovery.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-crash.XXXXXX")"
TXNCTL="${WORK}/txnctl"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

ctl() { "${TXNCTL}" "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt"; cat "${WORK}/out.txt"; }
new_vault() { local v; v="$(mktemp -d "${WORK}/vault.XXXXXX")"; mkdir -p "${v}/.index"; echo "${v}"; }

# crash_kill <vault> <crash-at> <file-args...>：把事务真实推进到崩溃点后**真实 kill -9**。
# 全局 CRASH_TXN 回填被杀事务的 txn_id（供后续断言事务目录）。
crash_kill() {
  local vault="$1" crash_at="$2"; shift 2
  local ready="${WORK}/crash.ready"
  rm -f "${ready}"
  "${TXNCTL}" crash-commit --vault "${vault}" --crash-at "${crash_at}" --ready "${ready}" "$@" \
    >"${WORK}/crash.out" 2>"${WORK}/crash.err" &
  local pid=$!
  local i
  for i in $(seq 1 300); do [ -f "${ready}" ] && break; sleep 0.02; done
  [ -f "${ready}" ] || { cat "${WORK}/crash.out" "${WORK}/crash.err"; die "崩溃进程未到达 ${crash_at}"; }
  kill -9 "${pid}" 2>/dev/null || true    # ★ 真实 kill -9：非 mock，不给进程任何清理机会
  wait "${pid}" 2>/dev/null || true
  CRASH_TXN="$(grep -oE 'txn_id=t[0-9a-f]{16}' "${WORK}/crash.out" | head -1 | sed 's/txn_id=//')"
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 helper
step "构建 txnctl（test-only helper）"
(cd "${REPO_ROOT}" && go build -o "${TXNCTL}" ./tests/lib/txnctl) || die "txnctl 构建失败"
ok "helper 就绪：${TXNCTL}"

# ---------------------------------------------------------------- 2. P1：pre_intent（无 intent）
step "P1 pre_intent：真 kill -9 于「仅分配 txn 目录、未写 intent」⇒ residue 静默清理、无 W26、权威零改动"
V="$(new_vault)"
crash_kill "${V}" pre_intent --file "a.md|false|OLD-A|NEW-A"
SHA="$(sha256sum "${V}/a.md" | awk '{print $1}')"   # 崩溃留态（前像）作为基线：recover 绝不得改动权威
[ ! -e "${V}/.index/txn/${CRASH_TXN}/intent.json" ] || die "P1 不应有 intent.json"
ctl recover --vault "${V}"
grep -q 'outcome=noop' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "P1 residue 不应触发回滚"; }
grep -q 'w26=0' "${WORK}/out.txt" || die "P1 residue 绝不发 W26"
grep -q 'residue_cleaned=1' "${WORK}/out.txt" || die "P1 residue 应被静默清理"
[ "$(sha256sum "${V}/a.md" | awk '{print $1}')" = "${SHA}" ] || die "P1 权威 a.md 不应被改动"
ok "P1：residue 清理、无 W26、权威零改动"

# ---------------------------------------------------------------- 3. P2：after_intent（intent 已发布、任何 rename 之前）
step "P2 after_intent：真 kill -9 于「intent 已发布、无权威 rename」⇒ 回滚到前像、发 W26"
V="$(new_vault)"
crash_kill "${V}" after_intent --file "a.md|false|OLD-A|NEW-A" --file "b.md|false|OLD-B|NEW-B"
[ "$(cat "${V}/a.md")" = "OLD-A" ] && [ "$(cat "${V}/b.md")" = "OLD-B" ] || die "P2 崩溃留态：文件应仍为前像"
ctl recover --vault "${V}"
grep -q 'outcome=rolledback' "${WORK}/out.txt" || die "P2 应回滚"
grep -q 'w26=1' "${WORK}/out.txt" || die "P2 应发一条 W26"
[ "$(cat "${V}/a.md")" = "OLD-A" ] && [ "$(cat "${V}/b.md")" = "OLD-B" ] || die "P2 恢复后应为前像"
[ -f "${V}/.index/txn/${CRASH_TXN}/abort" ] || die "P2 回滚后应写 abort"
ok "P2：回滚到前像、W26=1、abort 落盘"

# ---------------------------------------------------------------- 4. P3：partial（部分权威已 rename）
step "P3 partial：真 kill -9 于「首个权威已 rename、其余未 rename」⇒ 回滚到前像、发 W26"
V="$(new_vault)"
crash_kill "${V}" partial --file "a.md|false|OLD-A|NEW-A" --file "b.md|false|OLD-B|NEW-B"
[ "$(cat "${V}/a.md")" = "NEW-A" ] || die "P3 崩溃留态：首个文件应已到目标态（半应用）"
[ "$(cat "${V}/b.md")" = "OLD-B" ] || die "P3 崩溃留态：次文件应仍前像"
ctl recover --vault "${V}"
grep -q 'outcome=rolledback' "${WORK}/out.txt" || die "P3 应回滚"
grep -q 'w26=1' "${WORK}/out.txt" || die "P3 应发一条 W26"
[ "$(cat "${V}/a.md")" = "OLD-A" ] && [ "$(cat "${V}/b.md")" = "OLD-B" ] || die "P3 恢复后应全回前像"
ok "P3：半应用态回滚到前像、W26=1"

# ---------------------------------------------------------------- 5. P4：all_renamed（全部已 rename、commit 标记之前）
step "P4 all_renamed：真 kill -9 于「全部权威已 rename、commit 标记之前」⇒ 回滚到前像、发 W26"
V="$(new_vault)"
crash_kill "${V}" all_renamed --file "a.md|false|OLD-A|NEW-A" --file "b.md|false|OLD-B|NEW-B"
[ "$(cat "${V}/a.md")" = "NEW-A" ] && [ "$(cat "${V}/b.md")" = "NEW-B" ] || die "P4 崩溃留态：应全为目标态但无 commit 标记"
[ ! -e "${V}/.index/txn/${CRASH_TXN}/commit" ] || die "P4 不应有 commit 标记"
ctl recover --vault "${V}"
grep -q 'outcome=rolledback' "${WORK}/out.txt" || die "P4 应回滚（commit 标记缺席）"
grep -q 'w26=1' "${WORK}/out.txt" || die "P4 应发一条 W26"
[ "$(cat "${V}/a.md")" = "OLD-A" ] && [ "$(cat "${V}/b.md")" = "OLD-B" ] || die "P4 恢复后应全回前像"
ok "P4：无 commit 标记 ⇒ 回滚到前像、W26=1"

# ---------------------------------------------------------------- 6. P5/P6/P7：committed（标记在盘）不回滚、不发 W26
step "P5/P6/P7 committed（N=1/2/5，commit 标记在盘）：真 kill -9 后不回滚、不发 W26、目标态逐字保留"
declare -a POINTS=(P5 P6 P7)
declare -a NS=(1 2 5)
for idx in 0 1 2; do
  N="${NS[$idx]}"; PT="${POINTS[$idx]}"
  V="$(new_vault)"
  FILES=()
  for i in $(seq 1 "${N}"); do FILES+=(--file "f${i}.md|false|OLD-${i}|TGT-${i}"); done
  crash_kill "${V}" committed "${FILES[@]}"
  [ -f "${V}/.index/txn/${CRASH_TXN}/commit" ] || die "${PT} commit 标记应在盘"
  for i in $(seq 1 "${N}"); do
    [ "$(cat "${V}/f${i}.md")" = "TGT-${i}" ] || die "${PT} 崩溃留态：f${i}.md 应已到目标态"
  done
  ctl recover --vault "${V}"
  grep -q 'outcome=noop' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "${PT} commit 在盘不应回滚"; }
  grep -q 'w26=0' "${WORK}/out.txt" || die "${PT} commit 在盘绝不发 W26"
  for i in $(seq 1 "${N}"); do
    [ "$(cat "${V}/f${i}.md")" = "TGT-${i}" ] || die "${PT} 恢复后 f${i}.md 应仍为目标态（不回滚）"
  done
done
ok "P5/P6/P7：commit 标记在盘 ⇒ 不回滚、W26=0、目标态逐字保留"

# ---------------------------------------------------------------- 7. P8：committed（create 新建文件）不回滚、不发 W26
step "P8 committed（create 新建文件）：真 kill -9 后不回滚、不发 W26、新建目标逐字保留"
V="$(new_vault)"
crash_kill "${V}" committed --file "new1.md|true||CREATED-1" --file "new2.md|true||CREATED-2"
[ -f "${V}/.index/txn/${CRASH_TXN}/commit" ] || die "P8 commit 标记应在盘"
[ "$(cat "${V}/new1.md")" = "CREATED-1" ] && [ "$(cat "${V}/new2.md")" = "CREATED-2" ] || die "P8 新建目标应在盘"
ctl recover --vault "${V}"
grep -q 'outcome=noop' "${WORK}/out.txt" || die "P8 commit 在盘不应回滚"
grep -q 'w26=0' "${WORK}/out.txt" || die "P8 commit 在盘绝不发 W26"
[ "$(cat "${V}/new1.md")" = "CREATED-1" ] && [ "$(cat "${V}/new2.md")" = "CREATED-2" ] || die "P8 恢复后新建目标应保留"
ok "P8：create 提交后 commit 标记在盘 ⇒ 不回滚、W26=0、新建目标保留"

# ---------------------------------------------------------------- 8. 恢复幂等
step "恢复幂等：对同一 P3 半应用崩溃连跑两次 recover，结果逐字相同（第二次 noop、磁盘不变）"
V="$(new_vault)"
crash_kill "${V}" partial --file "a.md|false|OLD-A|NEW-A" --file "b.md|false|OLD-B|NEW-B"
ctl recover --vault "${V}"
grep -q 'w26=1' "${WORK}/out.txt" || die "幂等用例首次 recover 应回滚 + W26"
SHA_A1="$(sha256sum "${V}/a.md" | awk '{print $1}')"
SHA_B1="$(sha256sum "${V}/b.md" | awk '{print $1}')"
ctl recover --vault "${V}"
grep -q 'outcome=noop' "${WORK}/out.txt" || die "第二次 recover 应为 noop（已闭合）"
grep -q 'w26=0' "${WORK}/out.txt" || die "第二次 recover 不应再发 W26"
[ "$(sha256sum "${V}/a.md" | awk '{print $1}')" = "${SHA_A1}" ] || die "第二次 recover 改动了 a.md"
[ "$(sha256sum "${V}/b.md" | awk '{print $1}')" = "${SHA_B1}" ] || die "第二次 recover 改动了 b.md"
ok "恢复幂等：二次 recover 为 noop、无重复 W26、磁盘逐字节不变"

# ---------------------------------------------------------------- 9. 非常规①：post-crash 外部编辑冲突（B-R3）
step "非常规① post-crash 外部编辑：kill -9 后用编辑器改写事务内文件 ⇒ 非零 + E15、逐字节保用户编辑、事务未闭合、无 W26、复跑仍阻断"
V="$(new_vault)"
crash_kill "${V}" after_intent --file "a.md|false|OLD-A|NEW-A"
printf 'USER-HAND-EDIT\n' >"${V}/a.md"    # 崩溃后外部编辑（既非前像也非目标态）
SHA_EDIT="$(sha256sum "${V}/a.md" | awk '{print $1}')"
ctl recover --vault "${V}" || true
grep -q 'code=E15' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "外部编辑冲突应携 E15"; }
grep -q 'w26=' "${WORK}/out.txt" && die "冲突路径不应有恢复回执（应在 E15 处早返）" || true
[ "$(sha256sum "${V}/a.md" | awk '{print $1}')" = "${SHA_EDIT}" ] || die "冲突下 a.md 应逐字节保持用户编辑"
[ ! -e "${V}/.index/txn/${CRASH_TXN}/abort" ] || die "冲突下事务应保持未闭合（无 abort）"
ctl scan --vault "${V}"
grep -qE "^STATE ${CRASH_TXN} open" "${WORK}/out.txt" || die "冲突下事务应仍判 open"
# 复跑仍被阻断。
ctl recover --vault "${V}" || true
grep -q 'code=E15' "${WORK}/out.txt" || die "复跑应仍被 E15 阻断"
[ "$(sha256sum "${V}/a.md" | awk '{print $1}')" = "${SHA_EDIT}" ] || die "复跑仍不得改动 a.md"
ok "外部编辑冲突：E15、用户编辑逐字节保留、事务未闭合、无 W26、复跑持续阻断"

# ---------------------------------------------------------------- 10. 非常规②：损坏 intent（截断成非法 JSON）
step "非常规② 损坏 intent：把 intent.json 截断成非法 JSON ⇒ 非零 + E15、目录与文件逐字节原样、权威零写入、无 W26"
V="$(new_vault)"
crash_kill "${V}" after_intent --file "a.md|false|OLD-A|NEW-A"
SHA_BEFORE="$(sha256sum "${V}/a.md" | awk '{print $1}')"
printf '{ "truncated' >"${V}/.index/txn/${CRASH_TXN}/intent.json"   # stat 成功但解析失败 ⇒ fail closed
SHA_INTENT="$(sha256sum "${V}/.index/txn/${CRASH_TXN}/intent.json" | awk '{print $1}')"
ctl recover --vault "${V}" || true
grep -q 'code=E15' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "损坏 intent 应 fail closed 携 E15"; }
[ "$(sha256sum "${V}/a.md" | awk '{print $1}')" = "${SHA_BEFORE}" ] || die "损坏 intent 下权威 a.md 应零写入"
[ "$(sha256sum "${V}/.index/txn/${CRASH_TXN}/intent.json" | awk '{print $1}')" = "${SHA_INTENT}" ] ||
  die "损坏 intent.json 应逐字节原样保留（不删、不改写、不参与清理）"
[ ! -e "${V}/.index/txn/${CRASH_TXN}/abort" ] || die "损坏 intent 下不应写 abort"
ctl scan --vault "${V}"
grep -qE "^STATE ${CRASH_TXN} corrupt" "${WORK}/out.txt" || die "损坏 intent 应判 corrupt"
ok "损坏 intent：E15、目录与文件逐字节原样、权威零写入、判 corrupt"

# ---------------------------------------------------------------- 11. 真实仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] crash_recovery.sh 全部 %d 组断言通过（共 %d 步，8 个崩溃点全部真实 kill -9）\n' "${PASS}" "${STEP}"

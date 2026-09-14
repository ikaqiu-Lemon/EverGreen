#!/usr/bin/env bash
# M6 统一验收端到端聚合器（M6 · T-evergreen.s1_main_flow-158614-075）。
#
# 判据来源：本 task deliverables「evergreen/test/e2e/m6_acceptance.sh」——
#   「11 个 m6_*.sh 全绿、M1–M5 的 50 个既有 e2e 全绿且一个不删、磁盘 e2e 总数 ≥ 61、
#     M6 现态边界断言（锁 / 事务日志 / 崩溃恢复 / 退出码 5 已落地且受限于授权本位）全部为真」；
#   milestone M-006 完成判据 17（11 支 M6 scripts + 历史门禁 + M6 final/mutation gate 全绿）。
#
# ── 与 m5_acceptance.sh 的分工（只增不改、避免重复跑）──
#   m5_acceptance.sh 是 M5 层聚合器，负责 M5 11 支 + 历史 39 支 = 50 支的逐个真跑。
#   本脚本是 **M6 层聚合器**，在其之上再叠一层：
#     · 全量模式下先逐个真跑 11 支 m6_*.sh；
#     · 再以「全量」委派 m5_acceptance.sh 跑完那 50 支（不重复实现历史清单，单一事实源）；
#     · 于是全集 = 11(M6) + 50(M5∪历史) = **恰 61 支**，逐个 exit 0 才算通过。
#   没有白名单 / 无签名豁免 / 无 skip / 不吞退出码：任一子脚本非 0 → 本聚合器立即非 0。
#
# ── M6 现态边界（§16.4 阶段化重钉的正面侧，与 m5_acceptance「M6 一格未提前」历史侧互补）──
#   M5 收口时是「M6 尚未落地」的历史事实（由 m5_acceptance 守）；本聚合器守的是 M6 **已落地** 的现态：
#     · run.lock 单机 flock、`.index/txn/` 事务日志、崩溃恢复、块级安全合并、写前强校验、退出码 5 均在场；
#     · 但一律受限于授权本位（internal/txn、index_lock.go、recover_hook.go、internal/index/），授权外零命中；
#     · 退出码全集恰 {0,1,2,3,4,5,6}，取值 5 常量恰 1 个且名为 ExitPrecheckOrLock，os.Exit(5) 字面量恰 0；
#     · 新增诊断码恰 5 条（E15 E16 W26 W27 W28），W21 仍不分配。
#
# 模式：
#   bash test/e2e/m6_acceptance.sh --list   只做清单面 + M6 静态边界断言（不跑被测脚本，秒级）
#   bash test/e2e/m6_acceptance.sh          全量：61 支 e2e 逐个真跑（重，视磁盘 / 机器可达数十分钟）
#
# 约束：离线、可重复；被测脚本各自在 mktemp 沙箱内工作，本脚本只在 mktemp 内存放日志；
#   真实仓库工作区零污染（末尾自查 git status）。全程零交互（stdin 接 /dev/null）。

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
E2E="${REPO_ROOT}/test/e2e"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-acc.XXXXXX")"

MODE="${1:-full}"
case "${MODE}" in
  --list|full) ;;
  *) printf '用法：bash test/e2e/m6_acceptance.sh [--list]\n' >&2; exit 1 ;;
esac

# M6 的 11 个脚本（封闭清单；含本聚合器自身与 m6_docs_commands.sh）。
M6_SCRIPTS=(
  m6_atomic_multifile.sh m6_block_merge.sh m6_ci_dep_gate.sh
  m6_concurrent_conflict.sh m6_crash_recovery.sh m6_precheck_exit5.sh
  m6_run_lock.sh m6_strict_check.sh m6_txn_journal.sh
  m6_docs_commands.sh m6_acceptance.sh
)
WANT_M6="${#M6_SCRIPTS[@]}"          # 恰 11
WANT_M5_UNION=50                     # M5 11 + 历史 39（由 m5_acceptance.sh 统一守清单）
WANT_TOTAL=$((WANT_M6 + WANT_M5_UNION))   # 恰 61

# M6 新增诊断码（恰 5 条；W21 刻意不在其中）。
M6_CODES=(E15 E16 W26 W27 W28)

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

BEFORE_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 清单面（M6 恰 11 / 总数 ≥ 61）
step "e2e 清单：m6_*.sh 恰 ${WANT_M6} 封闭 / 磁盘 e2e 总数 ≥ ${WANT_TOTAL}（M6 ${WANT_M6} + M5∪历史 ${WANT_M5_UNION}）"
N_M6_DISK="$(ls "${E2E}"/m6_*.sh 2>/dev/null | wc -l | tr -d ' ')"
N_TOTAL="$(ls "${E2E}"/*.sh 2>/dev/null | wc -l | tr -d ' ')"
[ "${#M6_SCRIPTS[@]}" = "${WANT_M6}" ] || die "M6_SCRIPTS 清单 ${#M6_SCRIPTS[@]} 条，期望 ${WANT_M6}"
[ "${N_M6_DISK}" = "${WANT_M6}" ] || die "磁盘上 m6_*.sh = ${N_M6_DISK}，与 M6_SCRIPTS 清单 ${WANT_M6} 不等（M6 清单须封闭）"
[ "${N_TOTAL}" -ge "${WANT_TOTAL}" ] || die "磁盘 e2e 总数 = ${N_TOTAL}，期望 ≥ ${WANT_TOTAL}（M-006 判据）"
for s in "${M6_SCRIPTS[@]}"; do
  [ -f "${E2E}/${s}" ] || die "M6 清单登记的脚本不在盘：${s}（一个不许缺）"
done
# m5_acceptance.sh 必须在盘：它是 M5∪历史 50 支的单一清单事实源，本聚合器委派它。
[ -f "${E2E}/m5_acceptance.sh" ] || die "缺 m5_acceptance.sh：M6 聚合器依赖它守 M5∪历史 50 支清单"
ok "M6 清单封闭且逐个在盘（${N_M6_DISK} 支）；磁盘 e2e 总数 ${N_TOTAL} ≥ ${WANT_TOTAL}"

# ---------------------------------------------------------------- 2. M6 现态边界断言（正面侧）
step "M6 现态边界：锁 / 事务日志符号仅落授权本位、退出码全集 {0,1,2,3,4,5,6}、取值 5 常量恰 ExitPrecheckOrLock、os.Exit(5) 字面量恰 0、新增诊断码恰 5、W21 不分配"
cd "${REPO_ROOT}"
# ① 锁 / 事务日志符号：M6 已落地，但只许落授权本位（授权外非注释代码零命中）。
IDX_AUTH_RE='^(internal/txn/|internal/cli/index_lock\.go|internal/cli/recover_hook\.go|internal/index/)'
M6SYM="$( { grep -rnE 'flock|run\.lock|/txn/' internal/ cmd/ --include='*.go' || true; } \
          | { grep -v '_test\.go' || true; } | { grep -vE ':[0-9]+:[[:space:]]*//' || true; } )"
UNAUTH="$(printf '%s\n' "${M6SYM}" | { grep -vE "${IDX_AUTH_RE}" || true; } | { grep -c . || true; })"
[ "${UNAUTH}" = "0" ] \
  || { printf '%s\n' "${M6SYM}" | grep -vE "${IDX_AUTH_RE}" >&2
       die "M6 锁 / 事务日志符号出现在授权本位之外（${UNAUTH} 处）"; }
AUTH_HIT="$(printf '%s\n' "${M6SYM}" | { grep -cE "${IDX_AUTH_RE}" || true; })"
[ "${AUTH_HIT}" -ge 1 ] || die "授权本位内未见 M6 锁 / 事务日志符号：M6 现态锁失去事实基础"
# ② 退出码全集恰扩张为 {0,1,2,3,4,5,6}；os.Exit(5) 字面量恰 0；取值 5 常量恰 1 且名 ExitPrecheckOrLock。
N5="$( { grep -rn 'os\.Exit(5)' internal/ cmd/ || true; } | wc -l | tr -d ' ')"
[ "${N5}" = "0" ] || die "出现 os.Exit(5) 字面量（os.Exit 单点须用 ExitCodeFor 变量出口）"
CODES="$( { grep -rhoE '^[[:space:]]*(const[[:space:]]+)?Exit[A-Za-z]*[[:space:]]*=[[:space:]]*[0-9]+' \
              internal/cli/exit.go internal/cli/exitcode.go || true; } \
            | grep -oE '[0-9]+$' | sort -un | tr '\n' ' ' | sed 's/ $//')"
[ "${CODES}" = "0 1 2 3 4 5 6" ] || die "退出码全集 = {${CODES}}，M6 现态期望逐字 {0 1 2 3 4 5 6}"
N5C="$( { grep -rnE '=[[:space:]]*5$' internal/cli/exit.go internal/cli/exitcode.go || true; } | wc -l | tr -d ' ')"
[ "${N5C}" = "1" ] || die "取值 5 的退出码常量应恰 1 个，实得 ${N5C}"
N5NAME="$( { grep -rhoE 'ExitPrecheckOrLock[[:space:]]*=[[:space:]]*5$' internal/cli/exit.go internal/cli/exitcode.go || true; } | wc -l | tr -d ' ')"
[ "${N5NAME}" = "1" ] || die "取值 5 的常量名不是逐字 ExitPrecheckOrLock，命中 ${N5NAME}"
# ③ 新增诊断码恰 5 条在诊断码定义处在册；W21 仍不分配。
for c in "${M6_CODES[@]}"; do
  { grep -rqF "\"${c}\"" internal/ || grep -rqF "${c}" internal/cli/diag*.go internal/cli/exitcode.go 2>/dev/null; } \
    || die "M6 诊断码 ${c} 未在 internal/ 定义在册"
done
N21="$( { grep -rn '"W21"' internal/ cmd/ || true; } | wc -l | tr -d ' ')"
[ "${N21}" = "0" ] || die "出现 \"W21\"（该号段刻意留白不分配）"
ok "M6 现态边界全部为真（授权本位 ${AUTH_HIT} 处、授权外 0；退出码全集 {${CODES}}=ExitPrecheckOrLock；os.Exit(5)=0；新增 5 码在册；W21=0）"

if [ "${MODE}" = "--list" ]; then
  printf '\n[--list] 清单面与 M6 静态边界断言完成（%d 组断言）；未运行任何被测脚本。\n' "${PASS}"
  AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
  [ "${BEFORE_STATUS}" = "${AFTER_STATUS}" ] || die "脚本运行后仓库脏文件集合发生变化"
  exit 0
fi

# ---------------------------------------------------------------- 3. 11 支 m6_*.sh 逐个真跑
step "M6 的 ${WANT_M6} 支脚本逐个真跑（本聚合器自身以 --list 自跑，避免递归全量）"
for s in "${M6_SCRIPTS[@]}"; do
  log="${WORK}/${s}.log"
  if [ "${s}" = "m6_acceptance.sh" ]; then
    if bash "${E2E}/${s}" --list </dev/null >"${log}" 2>&1; then rc=0; else rc=$?; fi
  else
    if bash "${E2E}/${s}" </dev/null >"${log}" 2>&1; then rc=0; else rc=$?; fi
  fi
  printf '  %-28s exit=%s\n' "${s}" "${rc}"
  [ "${rc}" = "0" ] || { tail -25 "${log}" >&2; die "M6 脚本 ${s} 退 ${rc}（M6 脚本必须全绿）"; }
done
ok "${WANT_M6} 支 m6_*.sh 全部退 0"

# ---------------------------------------------------------------- 4. 委派 m5_acceptance.sh 全量跑 50 支
step "委派 m5_acceptance.sh（全量）真跑 M5∪历史 ${WANT_M5_UNION} 支——单一清单事实源，避免重复实现"
log="${WORK}/m5_acceptance.full.log"
if bash "${E2E}/m5_acceptance.sh" </dev/null >"${log}" 2>&1; then rc=0; else rc=$?; fi
printf '  %-28s exit=%s\n' "m5_acceptance.sh(full)" "${rc}"
[ "${rc}" = "0" ] || { tail -40 "${log}" >&2; die "m5_acceptance.sh 全量退 ${rc}（M5∪历史 50 支须全绿）"; }
# 反证：确认委派确实跑了 50 支（日志末行汇总含「历史 39 个全绿」语义）。
grep -qE "历史 [0-9]+ 个全绿|全部 [0-9]+ 组断言通过" "${log}" \
  || die "m5_acceptance.sh 输出未见 50 支全绿汇总，疑似空跑"
ok "M5∪历史 ${WANT_M5_UNION} 支经 m5_acceptance.sh 全量真跑，全部退 0"

# ---------------------------------------------------------------- 5. 工作区零污染
step "真实仓库工作区零污染"
AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
if [ "${BEFORE_STATUS}" != "${AFTER_STATUS}" ]; then
  printf '  before:\n%s\n  after:\n%s\n' "${BEFORE_STATUS}" "${AFTER_STATUS}" >&2
  die "脚本运行后仓库脏文件集合发生变化"
fi
ok "git status --porcelain 前后一致"

printf '\n全部 %d 组断言通过（共 %d 步）；e2e 磁盘总数 %s：M6 %s 支全绿 + M5∪历史 %s 支全绿（零豁免、零冻结红），合计 %s 支。\n' \
  "${PASS}" "${STEP}" "${N_TOTAL}" "${WANT_M6}" "${WANT_M5_UNION}" "${WANT_TOTAL}"

#!/usr/bin/env bash
# M5 统一验收端到端脚本（T-evergreen.s1_main_flow-158614-069）。
#
# 判据来源：本 task deliverables「evergreen/test/e2e/m5_acceptance.sh」——
#   「11 个 m5_*.sh 全绿、M1–M4 的 39 个既有 e2e 全绿且一个不删、磁盘 e2e 总数 ≥ 50、
#     越界断言（无 flock / run.lock / /txn/、无 os.Exit(5)、退出码全集未扩张、无 "W21"）全部为真」；
#   milestone M-005 完成判据 17 / 18。
#
# ── 关于「M1–M4 的 39 个既有 e2e 全绿」这句话（**必须先读**）──
#   T-…-069 阶段 A 的第一版曾引入「冻结红项登记表」（FROZEN_RED / FROZEN_SIG / FROZEN_WHY）：
#   把 9 个已经红了的历史脚本按失败签名登记，签名命中即视为通过。owner 判定该机制**不成立**
#   —— 它把「历史门禁红」重新定义成「通过」，与 M-005 判据 17 和本 task Acceptance 直接冲突。
#   该机制已被**整体删除**，本脚本恢复这句话的字面语义：
#     · 39 个历史脚本逐个真实执行，**每一个都必须 exit 0**；
#     · 11 个 M5 脚本逐个真实执行，**每一个都必须 exit 0**；
#     · 任何一个子脚本非 0 → 本总控立即非 0。没有白名单、没有签名豁免、没有 skip、不吞退出码。
#   历史红项的正确清偿方式是**阶段化重钉**（已在 T-…-069 阶段 A 逐条完成）：
#   历史结论只读复算（旧判据一格不放宽），当期事实另立正面断言，两侧都钉住 ——
#   例如 m2_card_show 的「M2 三段次序」改由 `--include-deprecated` 复算、默认视图另钉 M4 可见集合
#   与两视图差集；诊断码封闭集合按包分域再加双侧等号；派生物删除按落点集合等号纳管。
#   净效果：约束比「全绿」更强（多了历史复算与新增等号），且一条红项都没有被洗白。
#
# 模式：
#   bash test/e2e/m5_acceptance.sh --list   只做清单面 + 静态越界断言（不跑任何被测脚本，秒级）
#   bash test/e2e/m5_acceptance.sh          全量：50 个 e2e 逐个真跑并分类判定（重，分钟级）
#
# 约束：离线、可重复；被测脚本各自在 mktemp 沙箱内工作，本脚本只在 mktemp 内存放日志；
#   真实仓库工作区零污染（末尾自查 git status）。全程零交互（stdin 接 /dev/null）。

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
E2E="${REPO_ROOT}/test/e2e"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-acc.XXXXXX")"

MODE="${1:-full}"
case "${MODE}" in
  --list|full) ;;
  *) printf '用法：bash test/e2e/m5_acceptance.sh [--list]\n' >&2; exit 1 ;;
esac

# M5 的 11 个脚本（封闭清单；含本脚本自身与 m5_docs_commands.sh）。
M5_SCRIPTS=(
  m5_index_build.sh m5_index_incremental.sh m5_index_consistency.sh
  m5_index_corrupt_rebuild.sh m5_read_path_index.sh m5_degrade_fallback.sh
  m5_sort_page.sh m5_replaced_by_reverse.sh m5_bench_p95.sh
  m5_docs_commands.sh m5_acceptance.sh
)
# M1–M4 的 39 个既有脚本（封闭清单，一个不删）。
HIST_SCRIPTS=(
  m1_real_article.sh
  m2_acceptance.sh m2_card_show.sh m2_context_polish.sh m2_convergence.sh
  m2_docs_commands.sh m2_ppe_replay.sh m2_rel_add.sh m2_rel_query.sh m2_search.sh
  m3_acceptance.sh m3_authorization.sh m3_docs_commands.sh m3_edit.sh
  m3_execution_failed.sh m3_lifecycle_state.sh m3_logical_delete.sh m3_markers.sh
  m3_ops_diagnostics.sh m3_proposal_cli.sh m3_proposal_layout.sh m3_proposal_state.sh
  m3_rel_remove.sh m3_reviewed.sh m3_superseded.sh
  m4_acceptance.sh m4_cmd_check.sh m4_cmd_reconcile.sh m4_docs_commands.sh
  m4_k041_proposal_clean.sh m4_r1_takeover.sh m4_r2_reviewed_backfill.sh
  m4_r3_relation.sh m4_r4_structure.sh m4_r5_domain_moved.sh m4_r6_recap_stale.sh
  m4_r7_support.sh m4_report_reconcile.sh m4_visibility_matrix.sh
)
# ── C2a·M6 现态重钉（§16.4「历史门禁阶段化与现态重钉」；保留 M5 历史事实 + 新增 M6 现态双侧锁）──
#   M5 历史事实（保留、不放宽）：M5 收口时磁盘 e2e 恰 50 支（M5 11 + 历史 39），M6 一支未落地；
#     反向封闭清单当时只需覆盖这 50 支。此事实继续机器可判：M5_SCRIPTS/HIST_SCRIPTS 计数与逐个在盘断言一格不动。
#   M6 现态（新增正面锁）：T-070~074 落地后磁盘新增 M6 e2e（m6_*.sh），是 §16.4 授权的现态漂移，
#     不是「悄悄加脚本」。反向清单从「M5∪历史」双侧扩为「M5∪历史∪M6」，并把 M6 清单也钉成封闭集合
#     （逐个在盘 + 反向零杂项），使「一个不删 / 一个不多」两侧仍然成立，只是白名单按里程碑并集扩张。
#   C2b·M6 现态补齐：T-075 交付 M6 文档面 e2e（m6_docs_commands.sh）与 M6 统一验收聚合器
#     （m6_acceptance.sh），M6 e2e 从 9 支补到 **恰 11 支**（milestone M-006「M6 e2e 恰 11 支、
#     磁盘 e2e 总数 ≥ 61」）。清单同步扩容并保持封闭（逐个在盘 + 反向零杂项）。
M6_SCRIPTS=(
  m6_atomic_multifile.sh m6_block_merge.sh m6_ci_dep_gate.sh
  m6_concurrent_conflict.sh m6_crash_recovery.sh m6_precheck_exit5.sh
  m6_run_lock.sh m6_strict_check.sh m6_txn_journal.sh
  m6_docs_commands.sh m6_acceptance.sh
)
WANT_M5=11
WANT_HIST=39
# M6 现态：磁盘上的 m6_*.sh 支数（C2b·T-075 补齐 docs/acceptance 后恰 11 支，与 M-006「M6 e2e 恰 11 支」同真）；只钉「M6 清单封闭且逐个在盘」。
WANT_M6="${#M6_SCRIPTS[@]}"
# M5 历史基线：磁盘 e2e 总数下界恒 ≥ 50（M5 收口口径，保留）；M6 现态：并集下界抬到 M5+历史+M6。
WANT_TOTAL=50
WANT_TOTAL_M6=$((WANT_M5 + WANT_HIST + WANT_M6))


STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

BEFORE_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 清单面
step "e2e 清单：m5_*.sh 恰 ${WANT_M5} / M1–M4 恰 ${WANT_HIST} 个一个不删 / M6 恰 ${WANT_M6} 封闭 / 磁盘总数 ≥ ${WANT_TOTAL}（M6 现态 ≥ ${WANT_TOTAL_M6}）"
N_M5_DISK="$(ls "${E2E}"/m5_*.sh 2>/dev/null | wc -l | tr -d ' ')"
N_M6_DISK="$(ls "${E2E}"/m6_*.sh 2>/dev/null | wc -l | tr -d ' ')"
N_TOTAL="$(ls "${E2E}"/*.sh 2>/dev/null | wc -l | tr -d ' ')"
[ "${#M5_SCRIPTS[@]}" = "${WANT_M5}" ] || die "M5_SCRIPTS 清单 ${#M5_SCRIPTS[@]} 条，期望 ${WANT_M5}"
[ "${#HIST_SCRIPTS[@]}" = "${WANT_HIST}" ] || die "HIST_SCRIPTS 清单 ${#HIST_SCRIPTS[@]} 条，期望 ${WANT_HIST}"
[ "${N_M5_DISK}" = "${WANT_M5}" ] || die "磁盘上 m5_*.sh = ${N_M5_DISK}，期望恰 ${WANT_M5}"
# M5 历史基线（保留）：磁盘 e2e 总数下界恒 ≥ 50。
[ "${N_TOTAL}" -ge "${WANT_TOTAL}" ] || die "磁盘 e2e 总数 = ${N_TOTAL}，期望 ≥ ${WANT_TOTAL}（M5 基线）"
# M6 现态双侧锁：M6 清单封闭且逐个在盘、磁盘 m6_*.sh 与清单等量、并集总数下界抬到 M5+历史+M6。
[ "${N_M6_DISK}" = "${WANT_M6}" ] || die "磁盘上 m6_*.sh = ${N_M6_DISK}，与 M6_SCRIPTS 清单 ${WANT_M6} 不等（M6 现态需清单封闭）"
[ "${N_TOTAL}" -ge "${WANT_TOTAL_M6}" ] || die "磁盘 e2e 总数 = ${N_TOTAL}，M6 现态期望 ≥ ${WANT_TOTAL_M6}（M5 ${WANT_M5} + 历史 ${WANT_HIST} + M6 ${WANT_M6}）"
for s in "${M5_SCRIPTS[@]}" "${HIST_SCRIPTS[@]}" "${M6_SCRIPTS[@]}"; do
  [ -f "${E2E}/${s}" ] || die "清单登记的脚本不在盘：${s}（历史脚本一个不许删；M6 脚本一个不许缺）"
done
# 反向：磁盘上不得有清单外的 e2e（防「悄悄加一个不被任何清单管的脚本」）。
# C2a·M6 现态重钉：白名单从「M5∪历史」按里程碑并集扩为「M5∪历史∪M6」，反向零杂项仍两侧成立。
for p in "${E2E}"/*.sh; do
  b="$(basename "${p}")"
  hit=0
  for s in "${M5_SCRIPTS[@]}" "${HIST_SCRIPTS[@]}" "${M6_SCRIPTS[@]}"; do [ "${s}" = "${b}" ] && hit=1 && break; done
  [ "${hit}" = "1" ] || die "磁盘脚本 ${b} 不在任何封闭清单内（M5/历史/M6 三侧并集）：请先登记再落地"
done
ok "清单三向恰等：m5 ${N_M5_DISK} / 历史 ${WANT_HIST} / M6 ${N_M6_DISK} / 总数 ${N_TOTAL}（≥ ${WANT_TOTAL} 基线、≥ ${WANT_TOTAL_M6} M6 现态）"

# ---------------------------------------------------------------- 2. 里程碑边界断言（C2a·M6 现态重钉，§16.4 + §17.2/§17.3）
# ── 重钉口径（唯一合法形态）：保留 M5 历史事实 + 新增 M6 现态双侧锁；禁删断言 / 禁 skip / 禁 allowlist / 禁 frozen-red ──
#   M5 历史事实（保留）：M5 收口时 M6 一格未提前——`flock/run.lock/txn` 只在注释里、无 `os.Exit(5)`、
#     退出码全集恰 {0,1,2,3,4,6}、无取值 5 的常量、无 "W21"。此判断作为里程碑事实原样在册。
#   M6 现态（新增正面锁）：T-070~074 落地后，锁 / 事务日志符号、退出码 5 常量已合法存在，但必须**受限落在
#     其架构本位**——符号只许出现在 internal/txn、internal/cli/index_lock.go、internal/cli/recover_hook.go、
#     internal/index/ 授权集合内（授权外零命中）；退出码全集恰扩张为 {0,1,2,3,4,5,6}（新增恰 5）；取值 5 的常量
#     恰 1 个且名逐字为 ExitPrecheckOrLock；`os.Exit(5)` 字面量仍恰 0（§17.2 R7 形态：os.Exit 单点在
#     cmd/eg/main.go 用 ExitCodeFor 变量出口，不写死 5）；"W21" 仍恒不分配。
step "里程碑边界：flock/run.lock/txn 仅落授权本位、退出码全集 M6 现态 {0,1,2,3,4,5,6}、取值 5 常量恰 ExitPrecheckOrLock、os.Exit(5) 字面量恰 0、无 \"W21\""
cd "${REPO_ROOT}"
# ① 锁 / 事务日志符号：M6 现态已落地，但只许落在授权本位（授权外非注释代码零命中）。
#    授权本位 = internal/txn/**、internal/cli/index_lock.go、internal/cli/recover_hook.go、internal/index/**。
IDX_AUTH_RE='^(internal/txn/|internal/cli/index_lock\.go|internal/cli/recover_hook\.go|internal/index/)'
M6SYM="$( { grep -rnE 'flock|run\.lock|/txn/' internal/ cmd/ --include='*.go' || true; } \
          | { grep -v '_test\.go' || true; } | { grep -vE ':[0-9]+:[[:space:]]*//' || true; } )"
UNAUTH="$(printf '%s\n' "${M6SYM}" | { grep -vE "${IDX_AUTH_RE}" || true; } | { grep -c . || true; })"
[ "${UNAUTH}" = "0" ] \
  || { printf '%s\n' "${M6SYM}" | grep -vE "${IDX_AUTH_RE}" >&2
       die "M6 锁 / 事务日志符号出现在授权本位之外（${UNAUTH} 处）：只许落 internal/txn、index_lock.go、recover_hook.go、internal/index/"; }
# 现态正面锁：授权本位内确实已落地这些符号（否则说明 M6 未接入，双侧锁失去事实基础）。
AUTH_HIT="$(printf '%s\n' "${M6SYM}" | { grep -cE "${IDX_AUTH_RE}" || true; })"
[ "${AUTH_HIT}" -ge 1 ] || die "授权本位内未见 M6 锁 / 事务日志符号：M6 现态锁失去事实基础（是否搬走/改名？）"
# ② 退出码全集：M5 基线 6 值一格不减（子集恒真）+ M6 现态全集恰扩张为 {0,1,2,3,4,5,6}（新增恰 5）。
N5="$( { grep -rn 'os\.Exit(5)' internal/ cmd/ || true; } | wc -l | tr -d ' ')"
[ "${N5}" = "0" ] || die "出现 os.Exit(5) 字面量（§17.2 R7 形态：os.Exit 单点用 ExitCodeFor 变量出口，字面量恒 0）"
CODES="$( { grep -rhoE '^[[:space:]]*(const[[:space:]]+)?Exit[A-Za-z]*[[:space:]]*=[[:space:]]*[0-9]+' \
              internal/cli/exit.go internal/cli/exitcode.go || true; } \
            | grep -oE '[0-9]+$' | sort -un | tr '\n' ' ' | sed 's/ $//')"
[ -n "${CODES}" ] || die "退出码常量表抽取为空：判据失去事实基础（常量文件是否被改名/搬走？）"
# M5 基线侧（保留、不放宽）：6 个历史退出码 {0,1,2,3,4,6} 一个不少。
for c in 0 1 2 3 4 6; do
  case " ${CODES} " in *" ${c} "*) ;; *) die "M5 历史退出码 ${c} 从常量表消失（历史事实被破坏，非授权漂移）" ;; esac
done
# M6 现态侧（新增正面锁）：全集逐字 {0,1,2,3,4,5,6}，相对 M5 新增恰 5。
[ "${CODES}" = "0 1 2 3 4 5 6" ] \
  || die "退出码全集 = {${CODES}}，M6 现态期望逐字 {0 1 2 3 4 5 6}（M5 6 值 + 新增恰 5；多一个少一个都红）"
# 常量名双侧锁：取值 5 的常量恰 1 个，且名逐字为 ExitPrecheckOrLock（防「加了另一个取值 5 的常量」）。
N5C="$( { grep -rnE '=[[:space:]]*5$' internal/cli/exit.go internal/cli/exitcode.go || true; } | wc -l | tr -d ' ')"
[ "${N5C}" = "1" ] || die "取值 5 的退出码常量应恰 1 个（M6 现态），实得 ${N5C}"
N5NAME="$( { grep -rhoE 'ExitPrecheckOrLock[[:space:]]*=[[:space:]]*5$' internal/cli/exit.go internal/cli/exitcode.go || true; } | wc -l | tr -d ' ')"
[ "${N5NAME}" = "1" ] || die "取值 5 的常量名不是逐字 ExitPrecheckOrLock（§17.3 唯一合法名），命中 ${N5NAME}"
# ③ W21 仍不分配（M5 = M6 现态两侧一致，恒 0）。
N21="$( { grep -rn '"W21"' internal/ cmd/ || true; } | wc -l | tr -d ' ')"
[ "${N21}" = "0" ] || die "出现 \"W21\"（该号段刻意留白不分配，M5/M6 两侧均恒 0）"
ok "里程碑边界双侧锁全部为真（M6 锁 / 事务符号仅落授权本位 ${AUTH_HIT} 处、授权外 0；退出码全集 = {${CODES}}，M5 6 值全保留 + 新增恰 5=ExitPrecheckOrLock；os.Exit(5) 字面量 0；W21 恒 0）"

if [ "${MODE}" = "--list" ]; then
  printf '\n[--list] 清单面与静态越界断言完成（%d 组断言）；未运行任何被测脚本。\n' "${PASS}"
  AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
  [ "${BEFORE_STATUS}" = "${AFTER_STATUS}" ] || die "脚本运行后仓库脏文件集合发生变化"
  exit 0
fi

# ---------------------------------------------------------------- 3. 11 个 m5_*.sh 全绿
step "M5 的 ${WANT_M5} 个脚本逐个真跑（本脚本自身以 --list 自跑，避免递归全量）"
for s in "${M5_SCRIPTS[@]}"; do
  log="${WORK}/${s}.log"
  if [ "${s}" = "m5_acceptance.sh" ]; then
    if bash "${E2E}/${s}" --list </dev/null >"${log}" 2>&1; then rc=0; else rc=$?; fi
  else
    if bash "${E2E}/${s}" </dev/null >"${log}" 2>&1; then rc=0; else rc=$?; fi
  fi
  printf '  %-28s exit=%s\n' "${s}" "${rc}"
  [ "${rc}" = "0" ] || { tail -20 "${log}" >&2; die "M5 脚本 ${s} 退 ${rc}（M5 自己的脚本必须全绿）"; }
done
ok "${WANT_M5} 个 m5_*.sh 全部退 0"

# ---------------------------------------------------------------- 4. 39 个历史脚本：逐个必须 exit 0
step "M1–M4 的 ${WANT_HIST} 个脚本逐个真跑：**每一个都必须 exit 0**（无白名单 / 无签名豁免 / 不吞码）"
GREEN=0
FAILED_LIST=()
for s in "${HIST_SCRIPTS[@]}"; do
  log="${WORK}/${s}.log"
  if bash "${E2E}/${s}" </dev/null >"${log}" 2>&1; then rc=0; else rc=$?; fi
  printf '  %-28s exit=%s（期望 0）\n' "${s}" "${rc}"
  if [ "${rc}" = "0" ]; then
    GREEN=$((GREEN + 1))
  else
    FAILED_LIST+=("${s}(exit=${rc})")
    tail -20 "${log}" >&2
  fi
done
[ "${#FAILED_LIST[@]}" = "0" ] ||
  die "历史脚本非零退出 ${#FAILED_LIST[@]} 个：${FAILED_LIST[*]}（历史门禁红必须真实清偿，不得登记豁免）"
[ "${GREEN}" = "${WANT_HIST}" ] || die "历史脚本退 0 条数 ${GREEN} != ${WANT_HIST}"
ok "历史 ${WANT_HIST} 个脚本逐个真实执行且全部 exit 0"

# ---------------------------------------------------------------- 5. 工作区零污染
step "真实仓库工作区零污染"
AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
if [ "${BEFORE_STATUS}" != "${AFTER_STATUS}" ]; then
  printf '  before:\n%s\n  after:\n%s\n' "${BEFORE_STATUS}" "${AFTER_STATUS}" >&2
  die "脚本运行后仓库脏文件集合发生变化"
fi
ok "git status --porcelain 前后一致"

printf '\n全部 %d 组断言通过（共 %d 步）；e2e 磁盘总数 %s：M5 %s 个全绿 + 历史 %s 个全绿（零豁免、零冻结红）。\n' \
  "${PASS}" "${STEP}" "${N_TOTAL}" "${WANT_M5}" "${GREEN}"

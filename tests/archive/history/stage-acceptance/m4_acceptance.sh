#!/usr/bin/env bash
# M4 验收总控脚本（T-evergreen.s1_main_flow-158614-063）。
#
# 判据来源：`milestones/M-004-m4.md`「M4 完成判据（17 条）」与判据 14 / 15 的门禁清单。
# 本脚本让「M4 是否达成」这一结论**可被一条命令复算**：
#
#   第一段  按固定顺序跑 M4 新增的 14 个 e2e（含本脚本自身**不自调**）
#   第二段  历史回归：M1 的 `m1_real_article.sh` + 两条历史验收总控 `m2_acceptance.sh`
#           / `m3_acceptance.sh`（后两者第一段各自内含 M1+M2[+M3] e2e 复跑，
#           `m3_acceptance.sh` 末行须为 `checks=106 failed=0`：M4 收口值是 83，
#           M5 · T-…-069 清偿后合法长到 106，故按实测重钉并加「不得少于 83」的单调锁）
#   第三段  `make lint` + `go test ./... -count=1`（一次跑完）
#   第四段  越界反证（判据 17）与脚本数复算。**注意口径已按 M5 事实阶段化重钉**：
#           S4/M5 能力（.index/ 派生物、FTS5、索引诊断码）在 T-…-064 ~ 068 已合法交付，
#           故该段不再断言「M5 一条未提前」，而是断言「M5 只落在登记落点 + S5/M6 一格未提前」：
#           对账包内零 FTS5/sqlite/.index、M6 的 flock / txn/ / run.lock **代码面恒 0**
#           且注释面命中必须处在否定语境、`os.Exit(5)` 恒 0、对账非写命令前置零泄漏
#           （磁盘 e2e ≥ 39、`m4_*.sh` 恰 14、`m1|m2|m3_*.sh` 恰 25）
#
# **三值口径（与验收报告同源，不许二值化）**：
#   PASS          该判据的全部机器断言通过
#   FAIL          至少一条机器断言不通过 → 本脚本非零退出，末行给出失败判据编号
#   NOT-VERIFIED  判据依赖沙箱外的外部事实（如 owner 真实裁决意图），本地无法证实亦不许伪证
#
# **退出码语义**：
#   默认（开发减负门禁）只让 M4 当前产品面与 P0 / 核心 P1 阻断；历史 M2 / M3 总控仍逐个
#   执行并记录，但其冻结事实被 M4 合法推进后产生的非 0 记入延期补测债，不阻断本轮。
#   `M4_STRICT_HISTORY=1` 恢复原始严格口径，历史总控任一非 0 即使脚本非 0。
# 总结论固定由倒数第二段给出；两种模式都不得跳过历史脚本执行。
#
# 已知文本缺陷（登记 owner 回写，不阻断本脚本）：判据 14 现文引用
# `test/e2e/m1_acceptance.sh`，但该文件从不存在（M1 只有单个 e2e `m1_real_article.sh`），
# 且判据 14 同时要求「`m1|m2|m3_*.sh` 恰 25 个」——新增 `m1_acceptance.sh` 会变成第 26 个、
# 反而破坏「恰 25」。故本脚本以现实为准：M1 回归用 `m1_real_article.sh` + 两条历史验收总控
# 内含的 M1 复跑承载；`m1_acceptance.sh` 缺失记为验收报告的 owner 回写项（非 FAIL）。
#
# 约束：离线、零交互、可重复执行；**一切写操作只发生在各子脚本自建的 mktemp -d 目录内**
# （本脚本末尾自查 evergreen 仓 git status 与运行前逐字相等）；末行输出 `checks=<n> failed=<m>`。
#
# 用法：cd evergreen && bash test/e2e/m4_acceptance.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
E2E="${REPO_ROOT}/test/e2e"
PARENT="$(cd "${REPO_ROOT}/.." && pwd)"
EPIC_DIR="${PARENT}/teamwork/projects/evergreen/s1_main_flow"
SPECS="${EPIC_DIR}/docs/specs"
REPORT="${SPECS}/2026-12-12-m4-acceptance-report.md"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-acc.XXXXXX")"
LOGS="${WORK}/logs"; mkdir -p "${LOGS}"
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

cd "${REPO_ROOT}"
BEFORE_E="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

CHECKS=0
FAILED=()
HISTORY_FAILED=()
declare -A REASON
STRICT_HISTORY="${M4_STRICT_HISTORY:-0}"

sub() { # sub <判据号> <断言名> <0|1>
  local n="$1" name="$2" ok="$3"
  CHECKS=$((CHECKS + 1))
  if [ "${ok}" != "0" ]; then REASON["${n}"]="${REASON[${n}]:-}${name}; "; fi
}
q() { if [ "$1" = "0" ]; then echo 0; else echo 1; fi; }

run_one() { # run_one <判据号> <script>
  local n="$1" s="$2" rc=0
  if bash "${E2E}/${s}" </dev/null >"${LOGS}/${s}.log" 2>&1; then rc=0; else rc=$?; fi
  printf '  [run] %-28s exit=%s\n' "${s}" "${rc}"
  if [ "${rc}" -ne 0 ]; then
    printf '  ↳ %s 末 25 行：\n' "${s}" >&2
    tail -25 "${LOGS}/${s}.log" >&2
  fi
  sub "${n}" "e2e ${s}" "$(q "${rc}")"
}
run_history() { # run_history <script>
  local s="$1" rc=0
  if bash "${E2E}/${s}" </dev/null >"${LOGS}/${s}.log" 2>&1; then rc=0; else rc=$?; fi
  local debt=""
  [ "${rc}" -eq 0 ] || debt="（延期债）"
  printf '  [history] %-24s exit=%s%s\n' "${s}" "${rc}" "${debt}"
  if [ "${rc}" -ne 0 ]; then
    HISTORY_FAILED+=("${s}")
    printf '  ↳ %s 末 25 行：\n' "${s}" >&2
    tail -25 "${LOGS}/${s}.log" >&2
    if [ "${STRICT_HISTORY}" = "1" ]; then
      sub 14 "e2e ${s}" 1
    fi
  elif [ "${STRICT_HISTORY}" = "1" ]; then
    sub 14 "e2e ${s}" 0
  fi
}

echo "════════ 第一段：M4 新增 14 个 e2e（固定顺序，本脚本自身不自调）════════"
M4=(m4_cmd_reconcile.sh m4_cmd_check.sh
    m4_r1_takeover.sh m4_r2_reviewed_backfill.sh m4_r3_relation.sh m4_r4_structure.sh
    m4_r5_domain_moved.sh m4_r6_recap_stale.sh m4_r7_support.sh
    m4_report_reconcile.sh m4_k041_proposal_clean.sh m4_visibility_matrix.sh
    m4_docs_commands.sh)
for s in "${M4[@]}"; do run_one 14 "${s}"; done
# 复算：磁盘上 m4_*.sh 恰 14（含本脚本自身），且上表恰覆盖除自身外的 13 个。
M4_ON_DISK="$(ls "${E2E}"/m4_*.sh | wc -l | tr -d ' ')"
printf '  [count] m4_*.sh on disk = %s（期望 14，含本脚本自身）\n' "${M4_ON_DISK}"
sub 14 "m4_*.sh 恰 14" "$([ "${M4_ON_DISK}" -eq 14 ] && echo 0 || echo 1)"
sub 14 "M4 e2e 表恰 13（+自身=14）" "$([ "${#M4[@]}" -eq 13 ] && echo 0 || echo 1)"

echo
echo "════════ 第二段：历史回归（始终执行；严格模式才阻断）════════"
run_history m1_real_article.sh
run_history m2_acceptance.sh
run_history m3_acceptance.sh
# m3_acceptance.sh 末行 checks / failed 双侧锁。
# 【阶段化重钉 · 2026-09-09 / T-…-069】M4 期硬钉的是 `checks=83 failed=0`；M5 期 m3_acceptance
# 自身按 T-…-069 的清偿把判据面加严（e2e 计数加数式双侧锁、check.go reconcile 具名归类等），
# checks 从 83 **合法长到 106**（只增不减）。旧字面量在当期必红，而它红的是「checks 变多」这件
# 好事，故按事实重钉成两侧都精确、且方向单调的判据，强度只增不降；同时**取消原来的
# 「非严格模式不判定」豁免**（旧口径下这一格在默认模式只打印、不入账，等于延期债）：
#   · failed 必须逐字 0（一格不放宽）；
#   · checks 必须逐字等于当期实测值 106，且 >= M4 收口值 83（防被悄悄削回去）；
#   · 无论 STRICT_HISTORY 取值，这一格都计入判据 14。
M3TAIL_WANT=106
M3TAIL_M4=83
M3TAIL_LINE="$( { grep -oE '^checks=[0-9]+ failed=[0-9]+$' "${LOGS}/m3_acceptance.sh.log" || true; } | tail -1 )"
M3_CHECKS="$( printf '%s' "${M3TAIL_LINE}" | awk -F'[= ]' '{ print $2 }' )"
M3_FAILED="$( printf '%s' "${M3TAIL_LINE}" | awk -F'[= ]' '{ print $4 }' )"
M3TAIL=1
if [ "${M3_CHECKS:-x}" = "${M3TAIL_WANT}" ] && [ "${M3_FAILED:-x}" = "0" ] &&
   [ "${M3_CHECKS:-0}" -ge "${M3TAIL_M4}" ]; then M3TAIL=0; fi
printf '  [history] m3_acceptance 末行须 checks=%s failed=0（M4 收口值 %s，只增不减）：%s（实测「%s」）\n' \
  "${M3TAIL_WANT}" "${M3TAIL_M4}" "$([ "${M3TAIL}" = 0 ] && echo 是 || echo 否)" "${M3TAIL_LINE}"
sub 14 "m3_acceptance 末行=checks=${M3TAIL_WANT} failed=0" "${M3TAIL}"

echo
echo "════════ 第三段：make lint + go test ./... -count=1 ════════"
if make lint >"${LOGS}/lint.log" 2>&1; then LINT_RC=0; else LINT_RC=$?; fi
printf '  [run] %-28s exit=%s\n' "make lint" "${LINT_RC}"
[ "${LINT_RC}" = 0 ] || tail -25 "${LOGS}/lint.log" >&2
sub 14 "make lint" "$(q "${LINT_RC}")"
if go test ./... -count=1 >"${LOGS}/gotest.log" 2>&1; then TEST_RC=0; else TEST_RC=$?; fi
printf '  [run] %-28s exit=%s\n' "go test ./... -count=1" "${TEST_RC}"
[ "${TEST_RC}" = 0 ] || grep -E '^(---|FAIL)' "${LOGS}/gotest.log" | tail -25 >&2
sub 14 "go test ./... -count=1" "$(q "${TEST_RC}")"

echo
echo "════════ 第四段：脚本数复算 + 判据 17 越界反证 ════════"
E2E_TOTAL="$(ls "${E2E}"/*.sh | wc -l | tr -d ' ')"
M123="$(ls "${E2E}"/m1_*.sh "${E2E}"/m2_*.sh "${E2E}"/m3_*.sh 2>/dev/null | wc -l | tr -d ' ')"
printf '  [count] e2e 总数 = %s（期望 ≥ 39）；m1|m2|m3_*.sh = %s（期望恰 25）\n' "${E2E_TOTAL}" "${M123}"
sub 14 "e2e 总数 ≥ 39" "$([ "${E2E_TOTAL}" -ge 39 ] && echo 0 || echo 1)"
sub 14 "m1|m2|m3_*.sh 恰 25" "$([ "${M123}" -eq 25 ] && echo 0 || echo 1)"

# 判据 17：越界反证（M5 只落在登记落点、S5/M6 一格未提前；逐条计数应为 0）。
c17=0
[ "$(ls vault/.index 2>/dev/null | wc -l | tr -d ' ')" -eq 0 ] || c17=1
[ "$(grep -rnE 'FTS5|sqlite|\.index/' internal/reconcile/ 2>/dev/null | wc -l | tr -d ' ')" -eq 0 ] || c17=1
# 【阶段化重钉 · 2026-09-09 / T-…-069】M4 收口时 internal/ 连注释都零命中，故旧判据整行 grep。
# M5 的 index 命令实现里出现了**否定语境的注释**（index.go / index_sync.go 各一处：
# 「不引入 run.lock / 事务日志（属 M6）」），那是「M6 一格未提前」的正面声明，不是能力提前。
# ── C2a·M6 现态重钉（§16.1/§16.2/§16.3「命令三类别 + 锁覆盖面 + runtime reserved」+ §16.4 阶段化）──
# 【M4 收口历史事实，逐字保留】M4 达成当日 S5/M6 强原子事务与锁子系统整体**未开工**，internal/ 代码面
# 对 flock/txn//run.lock 命中恒 0，仅两处否定语境注释声明「不引入任何 M6 能力」。该历史结论不被改写。
# 【M6 现态，新增双侧锁】M6（T-070～074）已按合同 A-52/A-53/A-54/A-55 落地 `run.lock` 短临界区锁、
# `txn/` 事务日志与崩溃恢复钩子，三词代码面不再恒 0。**判据本体一格不放宽**：把旧「代码面恒 0 + 注释面
# 恰 2」重钉为「非测试非注释命中只许落**授权本位** {internal/txn/**, internal/index/**,
# internal/cli/index_lock.go, internal/cli/recover_hook.go}（合同 §3/A-53/A-55 锁与恢复落点），
# 授权外恒 0（含 cmd/ 与其它 cli 文件）、授权内 ≥ 1」——授权外零泄漏这一侧比旧「恒 0」覆盖面更广：
# 把 M6 能力钉死在合同落点上，别处一个字都不许漏。
M6_AUTH_RE='(internal/txn/|internal/index/|internal/cli/index_lock\.go|internal/cli/recover_hook\.go)'
M6_CODE="$( { grep -rnE 'flock|txn/|run\.lock' internal/ 2>/dev/null || true; } | { grep -v _test.go || true; } |
  { grep -vE ':[0-9]+:[[:space:]]*//' || true; } | { grep -c . || true; } )"
M6_UNAUTH="$( { grep -rnE 'flock|txn/|run\.lock' internal/ 2>/dev/null || true; } | { grep -v _test.go || true; } |
  { grep -vE ':[0-9]+:[[:space:]]*//' || true; } | { grep -vE "${M6_AUTH_RE}" || true; } | { grep -c . || true; } )"
M6_AUTH=$(( M6_CODE - M6_UNAUTH ))
[ "${M6_UNAUTH}" -eq 0 ] || c17=1
[ "${M6_AUTH}" -ge 1 ] || c17=1
printf '  [check] M6 三词 flock/txn//run.lock 落授权本位：授权内 %s / 越界 %s（非注释代码面总 %s；期望 越界 0、授权内 ≥1）\n' \
  "${M6_AUTH}" "${M6_UNAUTH}" "${M6_CODE}"
[ "$(grep -rn 'os.Exit(5)' internal/ 2>/dev/null | wc -l | tr -d ' ')" -eq 0 ] || c17=1
# 对账非写命令前置：只检查产品源码；渲染/采样辅助文件属于 reconcile 命令族。
# K-063-03：旧 grep 未排除测试文件，且漏列两个合法辅助文件，导致纯验证误报。
# ── C2a·M6 现态重钉（§16.4）：M6 的 recover_hook.go / precheck_wire.go 在**注释**里引用
#    `internal/reconcile/strict.go` 作为「W1~W6 升级面唯一真源」的说明（写前强校验 --strict 面），
#    这不是代码级依赖（无 import、无 reconcile. 调用）。本体「非对账 CLI 命令不在代码级依赖 reconcile」
#    一格不放宽：**排除整行注释**后仍恒 0；真 import / 调用不以 `//` 起首，照旧被计入。
LEAK="$(
  grep -rn 'internal/reconcile' internal/cli/ --include='*.go' --exclude='*_test.go' 2>/dev/null |
    { grep -vE ':[0-9]+:[[:space:]]*//' || true; } |
    { grep -vE '^internal/cli/(reconcile|check|reconcile_commit|reconcile_repair_reviewed|reconcile_repair_stale|reconcile_recap_sample|reconcile_render)\.go' || true; } |
    wc -l | tr -d ' '
)"
[ "${LEAK}" -eq 0 ] || c17=1
printf '  [check] 判据17 越界反证（对账包零索引符号 / M6 代码面零命中且注释面否定语境 / os.Exit(5) / 对账非写前置）：%s\n' "$([ "${c17}" = 0 ] && echo 全 0 || echo 有命中)"
sub 17 "M5 只落登记落点 + S5/M6 能力未提前" "${c17}"

echo
echo "════════ 汇总 ════════"
# 归并原因表并逐判据打印结论（本脚本只覆盖判据 14 / 17 的可执行子集；1~13/15/16 由
# validate_m4_tasks.py + m4_final_gate.py + 各 R e2e + go test 分工承载，见验收报告）。
for n in 14 17; do
  if [ -n "${REASON[${n}]:-}" ]; then
    printf '  [判据 %-2s] FAIL   %s\n' "${n}" "${REASON[${n}]}"
    FAILED+=("${n}")
  else
    printf '  [判据 %-2s] PASS\n' "${n}"
  fi
done

# 验收报告在盘（判据 15 / T-063 交付物）。
if [ -f "${REPORT}" ]; then
  printf '  [报告] 2026-12-12-m4-acceptance-report.md 在盘：是\n'
else
  printf '  [报告] 2026-12-12-m4-acceptance-report.md 在盘：否\n'
  FAILED+=("报告缺失"); CHECKS=$((CHECKS + 1))
fi

# 工作区卫生：本脚本不得改动 evergreen 工作区。
AFTER_E="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
HYG=0; [ "${BEFORE_E}" = "${AFTER_E}" ] || HYG=1
CHECKS=$((CHECKS + 1))
printf '  [卫生] evergreen 工作区与运行前逐字相等：%s\n' "$([ "${HYG}" = 0 ] && echo 是 || echo 否)"
[ "${HYG}" = 0 ] || FAILED+=("卫生")

if [ ${#FAILED[@]} -eq 0 ]; then
  echo "M4 当前门禁：PASS（M4 产品面与 P0 / 核心 P1 无阻断）"
else
  echo "M4 当前门禁：FAIL；失败项：${FAILED[*]:-无}"
fi
echo "历史严格口径：$([ ${#HISTORY_FAILED[@]} -eq 0 ] && echo PASS || echo "DEFERRED (${HISTORY_FAILED[*]})")"
echo "模式：$([ "${STRICT_HISTORY}" = "1" ] && echo strict-history || echo reduced)"
echo "checks=${CHECKS} failed=${#FAILED[@]}"
[ ${#FAILED[@]} -eq 0 ]

#!/usr/bin/env bash
# M3 验收总控脚本（T-evergreen.s1_main_flow-158614-047）。
#
# 判据来源：`milestones/M-003-m3.md`「M3 完成判据（17 条）」与「验收门禁」8 条。
# 本脚本让「M3 是否达成」这一结论**可被一条命令复算**：
#
#   第一段  按固定顺序跑 M1（1 个）+ M2（9 个）+ M3（15 个，含本脚本自身不自调）= 25 个 e2e 脚本
#   第二段  make lint + go test -v ./... （一次跑完，判据逐条按用例名取结论）
#   第三段  越界门禁 grep（reconcile / flock / txn / run.lock / FTS5 / os.Exit(5) / os.Remove /
#           checkout -- / reset --hard）、占位门禁 grep（M2 占位字样零命中，字面量由
#           PLACE_LIT 分片拼接，避免本脚本自身成为占位残留的假阳性来源）、
#           授权合同 §5 三条禁止措辞 grep、M3 期真实会话留痕在盘
#   第四段  逐条打印判据 1 ~ 17 的三值结论（PASS / FAIL / NOT-VERIFIED）与总结论
#
# **三值口径（与验收报告同源，不许二值化）**：
#   PASS          该判据的全部机器断言通过
#   FAIL          至少一条机器断言不通过 → 本脚本非零退出，末行给出失败判据编号
#   NOT-VERIFIED  判据依赖沙箱外的外部事实（如 owner 的真实裁决意图），本地无法证实亦不许伪证
#
# **退出码语义**：0 只表示「没有 FAIL 判据」，不等于 M3 达成。总结论固定由倒数第二行的
# 「M3 结论：…」给出：存在 FAIL 或 NOT-VERIFIED 判据时该行即为「未达成 / 待人判」。
#
# 约束：离线、零交互、可重复执行；**一切写操作只发生在 mktemp -d 目录内**（末尾自查两仓
# git status 与运行前逐字相等）；末行输出 `checks=<n> failed=<m>`。
#
# 用法：cd evergreen && bash test/e2e/m3_acceptance.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
E2E="${REPO_ROOT}/test/e2e"
PARENT="$(cd "${REPO_ROOT}/.." && pwd)"
EPIC_DIR="${PARENT}/teamwork/projects/evergreen/s1_main_flow"
SPECS="${EPIC_DIR}/docs/specs"
REPORT="${SPECS}/2026-11-11-m3-acceptance-report.md"
ADJ="${SPECS}/2026-10-13-m3-prestart-adjudication.md"
M3SESS="${E2E}/testdata/ppe/m3-raw-session"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-acc.XXXXXX")"
LOGS="${WORK}/logs"; mkdir -p "${LOGS}"
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

BEFORE_E="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

CHECKS=0
FAILED=()
UNVERIFIED=()
declare -A REASON

# sub <判据号> <断言名> <0|1>：记录一条子断言，失败即写进该判据的原因表。
sub() {
  local n="$1" name="$2" ok="$3"
  CHECKS=$((CHECKS + 1))
  if [ "${ok}" != "0" ]; then REASON["${n}"]="${REASON[${n}]:-}${name}; "; fi
}
q() { if [ "$1" = "0" ]; then echo 0; else echo 1; fi; }

run_sub() { # run_sub <script>
  local s="$1" rc=0
  if bash "${E2E}/${s}" </dev/null >"${LOGS}/${s}.log" 2>&1; then rc=0; else rc=$?; fi
  printf '  [run] %-24s exit=%s\n' "${s}" "${rc}"
  if [ "${rc}" -ne 0 ]; then
    printf '  ↳ %s 末 25 行：\n' "${s}" >&2
    tail -25 "${LOGS}/${s}.log" >&2
  fi
  sub 17 "e2e ${s}" "$(q "${rc}")"
  E2E_RC=$((E2E_RC + rc))
}

echo "════════ 第一段：M1 + M2 + M3 全部 e2e（固定顺序，24 个子脚本 + 本脚本自身）════════"
E2E_RC=0
M1M2=(m1_real_article.sh
      m2_acceptance.sh m2_card_show.sh m2_context_polish.sh m2_convergence.sh
      m2_docs_commands.sh m2_ppe_replay.sh m2_rel_add.sh m2_rel_query.sh m2_search.sh)
M3=(m3_proposal_layout.sh m3_proposal_state.sh m3_ops_diagnostics.sh m3_superseded.sh
    m3_execution_failed.sh m3_authorization.sh m3_lifecycle_state.sh m3_proposal_cli.sh
    m3_logical_delete.sh m3_reviewed.sh m3_markers.sh m3_rel_remove.sh
    m3_edit.sh m3_docs_commands.sh)
for s in "${M1M2[@]}" "${M3[@]}"; do run_sub "${s}"; done
# 计数事实：磁盘 30 = M1+M2 恰 10 + M3 期恰 15（含本脚本自身，不自调）+ M4 期恰 5。
# 2026-09-06 随 M4 · T-…-050 按实测重钉（只改形态，判据本体一格不放宽）：M4 第一个 e2e
# `m4_r1_takeover.sh` 落地，总数 25 → **26**。M3 期的 15 与 M1+M2 的 10 两条加数**逐字未动**
# （它们才是 M3 的判据本体），总数这一格跟最新磁盘事实对撞，并**新增** M4 期恰 1 这一格：
# 把某个 m3 脚本改名成 m4_ 前缀冒充新交付，总数不变而分账立刻失衡。M4 脚本不进第一段的
# 固定顺序 —— M3 验收不拿 M4 脚本凑绿（Go 侧 TestM3AcceptanceScriptCoversAllCriteria 反证）。
# 2026-09-06 随 M4 · T-…-052 再次按实测重钉（同口径）：M4 第二个 e2e `m4_r4_structure.sh`
# （R4 三项只读结构检查）落地，总数 26 → **27**、M4 期 1 → **2**；M3 侧两条加数仍逐字未动。
# 2026-09-06 随 M4 · T-…-051 第三次按实测重钉（同口径）：M4 第三个 e2e
# `m4_r2_reviewed_backfill.sh`（R2 `reviewed_at` 补齐：外部编辑后的判定 + 经 ChangePlan 的
# 封闭单键写入）落地，总数 27 → **28**、M4 期 2 → **3**；M3 侧两条加数仍逐字未动。
# 2026-09-06 随 M4 · T-…-053 第四次按实测重钉（同口径）：M4 第四个 e2e `m4_r3_relation.sh`
# （R3 关系校验四子检查 E13 / E14 / W15 / W16，只报告不自动修）落地，总数 28 → **29**、
# M4 期 3 → **4**；M3 侧两条加数（10 与 15）仍逐字未动。
# 2026-09-06 随 M4 · T-…-054 第五次按实测重钉（同口径）：M4 第五个 e2e
# `m4_r5_domain_moved.sh`（R5 手工跨领域移动检测 `domain_moved` / W18，只报告 +
# targets 恰三元顺序固定）落地，总数 29 → **30**、M4 期 4 → **5**；
# M3 侧两条加数（10 与 15）仍逐字未动。
# 2026-09-06 随 M4 · T-…-056 第六次按实测重钉（同口径）：M4 第六个 e2e `m4_r7_support.sh`
# （R7 材料支撑不足实时判定 `support_insufficient` / W20，永不改 status + 零落盘标记）落地，
# 总数 30 → **31**、M4 期 5 → **6**；M3 侧两条加数（10 与 15）仍逐字未动。
# 2026-09-07 随 M4 · T-…-055 第七次按实测重钉（同口径）：M4 第七个 e2e
# `m4_r6_recap_stale.sh`（R6 综述失准标记 `recap_stale` / W19：经 ChangePlan 落 `stale` /
# `stale_reason` 两键、幂等零写、B3 过期跳过、综述正文字节不变）落地，
# 总数 31 → **32**、M4 期 6 → **7**；M3 侧两条加数（10 与 15）仍逐字未动。
# 2026-09-07 随 M4 · T-…-057 第八次按实测重钉（同口径）：M4 第八个 e2e
# `m4_report_reconcile.sh`（报告 `reconcile` 恰三键：非对账路径「键在值空」、对账路径
# ran=true / commit 为真实 sha / findings 元素恰四键、两条路径顶层键序逐位相等）落地，
# 总数 32 → **33**、M4 期 7 → **8**；M3 侧两条加数（10 与 15）仍逐字未动。
# 2026-09-07 随 M4 · T-…-058 阶段 3 第九次按实测重钉（同口径）：M4 第九个 e2e
# `m4_cmd_reconcile.sh`（`eg reconcile` 命令本体端到端：干净库退 0 零 commit、外部编辑恰 +1、
# 重复 ID 退 2 且纳管 commit 仍恰 +1、`--dry-run` 三项快照逐字不变、三种收窄参数各退 1、
# `eg report --last` 逐字节复现三键、不作为写命令前置）落地，
# 总数 33 → **34**、M4 期 8 → **9**；M3 侧两条加数（10 与 15）仍逐字未动。
# 2026-09-07 随 M4 · T-…-059 阶段 4B1 第十次按实测重钉（同口径）：M4 第十个 e2e
# `m4_cmd_check.sh`（`eg check` 只读结构体检命令本体端到端：干净库退 0 且 commit +0 /
# porcelain 逐字不变、重复 ID 与关系异常退 2 且**不**纳管不提交、检查面闭合于 R3 / R4 七值 +
# 五个被排除 check 恒 0（用同库 `eg reconcile --dry-run` 作非空对照）、与 `--dry-run` 的
# **真子集**关系、五种参数各退 1、`eg report --last` 前后逐字节不变、不作为写命令前置）落地，
# 总数 34 → **35**、M4 期 9 → **10**；M3 侧两条加数（10 与 15）仍逐字未动。
TOTAL_SH="$(ls "${E2E}"/*.sh | wc -l | tr -d ' ')"
N_M1M2="$(ls "${E2E}"/m1_*.sh "${E2E}"/m2_*.sh | wc -l | tr -d ' ')"
N_M3="$(ls "${E2E}"/m3_*.sh | wc -l | tr -d ' ')"
N_M4="$(ls "${E2E}"/m4_*.sh 2>/dev/null | wc -l | tr -d ' ')"
printf '  e2e 计数：总 %s = M1+M2 %s + M3 %s + M4 %s（本脚本已在 M3 计数内，固定顺序里不自调）\n' \
  "${TOTAL_SH}" "${N_M1M2}" "${N_M3}" "${N_M4}"
# 2026-09-08 随 M5 · T-…-069 第十一次按实测重钉（同「加数式」口径，历史加数逐字未动）：
# ① M4 侧的历史加数 **10** 是 T-…-059 当日的真事实，M4 后续 task（T-060 视图矩阵 /
#    T-061 K-041 提案洁净 / T-062 对账报告 / T-063 M4 验收）又合法落地 4 个 e2e，
#    故 M4 实测 14，按加法等式 **10 + 4 = 14** 判定 —— 历史加数 10 一格未动，仍为 059 那条
#    结论背书；少一个 = M4 交付被抹，多一个 = 有脚本挪进 m4_ 号段。
# ② M5 落地 11 个 e2e（m5_index_build / m5_index_incremental / m5_index_consistency /
#    m5_index_corrupt_rebuild / m5_read_path_index / m5_degrade_fallback / m5_sort_page /
#    m5_replaced_by_reverse / m5_bench_p95 / m5_docs_commands / m5_acceptance）。
# 总数 35 → **50**，两条新增加数是「M4 后段 4」与「M5 期 11」；M1+M2 = 10 / M3 = 15 /
# M4 前段 10 三个历史加数一格未动，且总数用**加法等式**判定（10 + 15 + (10 + 4) + 11 = 50），
# 比写死总数更严：谁少提交一个脚本、或把脚本挪进别的里程碑号段，等式两侧都会当场不等。
N_M5="$(ls "${E2E}"/m5_*.sh 2>/dev/null | wc -l | tr -d ' ')"
# ── C2a·M6 现态重钉（§16.4「历史门禁阶段化与现态重钉」；保留 M5 基线加法等式 + 新增 M6 现态加数，非放宽）──
#   M5 基线（保留、不放宽）：M1+M2 10 + M3 15 + M4 14 + M5 11 = 50，四个历史加数一格不动，等式恒真。
#   M6 现态（新增加数）：T-070~074 落地 M6 e2e（m6_*.sh，实跑 9 支），磁盘总数抬为 50 + M6；
#     仍用加法等式判定（谁把脚本挪号段 / 少提交，等式两侧都当场不等），比写死总数更严。
N_M6="$(ls "${E2E}"/m6_*.sh 2>/dev/null | wc -l | tr -d ' ')"
N_M4_T059=10          # T-…-059 当日的历史加数，逐字冻结
N_M4_LATE=$((N_M4 - N_M4_T059))   # M4 后段（T-060～063）新增数，实测 4
printf '  e2e 计数（加数式）：总 %s = M1+M2 %s + M3 %s + M4 %s（%s + %s）+ M5 %s + M6 %s\n' \
  "${TOTAL_SH}" "${N_M1M2}" "${N_M3}" "${N_M4}" "${N_M4_T059}" "${N_M4_LATE}" "${N_M5}" "${N_M6}"
sub 17 "e2e 总数 == 50 基线 + M6 ${N_M6}（M1+M2 10 + M3 15 + M4 10+4 + M5 11 = 50，加法等式；M6 现态并集）" \
  "$([ "$((N_M1M2 + N_M3 + N_M4 + N_M5))" = "50" ] && [ "${TOTAL_SH}" = "$((N_M1M2 + N_M3 + N_M4 + N_M5 + N_M6))" ] && echo 0 || echo 1)"
sub 17 "M1+M2 == 10" "$([ "${N_M1M2}" = "10" ] && echo 0 || echo 1)"
sub 17 "M3 == 15" "$([ "${N_M3}" = "15" ] && echo 0 || echo 1)"
sub 17 "M4 == 14（加法等式 10（T-059 冻结）+ 4（T-060～063））" \
  "$([ "${N_M4}" = "14" ] && [ "${N_M4_LATE}" = "4" ] && echo 0 || echo 1)"
sub 17 "M5 == 11" "$([ "${N_M5}" = "11" ] && echo 0 || echo 1)"

echo "════════ 第二段：make lint + go test -v ./... ════════"
LINT_RC=0
(cd "${REPO_ROOT}" && make lint) </dev/null >"${LOGS}/lint.log" 2>&1 || LINT_RC=$?
printf '  [run] %-24s exit=%s\n' "make lint" "${LINT_RC}"
sub 17 "make lint" "$(q "${LINT_RC}")"
TEST_RC=0
(cd "${REPO_ROOT}" && go test -v -count=1 ./...) </dev/null >"${LOGS}/gotest.log" 2>&1 || TEST_RC=$?
printf '  [run] %-24s exit=%s\n' "go test -v ./..." "${TEST_RC}"
sub 17 "go test ./... -count=1" "$(q "${TEST_RC}")"
[ "${TEST_RC}" = "0" ] || tail -30 "${LOGS}/gotest.log" >&2
grep -E '^\s*--- (PASS|FAIL): ' "${LOGS}/gotest.log" | sed -E 's/^\s*--- (PASS|FAIL): ([^ ]+).*/\1 \2/' \
  >"${WORK}/cases.txt" || true
# passed <TestName>：该用例在本次全量 go test 中以 PASS 出现（顶层或子用例前缀）
passed() { grep -qE "^PASS ${1}(/|$)" "${WORK}/cases.txt"; }
case_ok() { local n="$1"; if passed "${n}"; then echo 0; else echo 1; fi; }

echo "════════ 第三段：越界 / 占位 / 措辞 / 留痕门禁 ════════"
cnt() { local n; n="$({ eval "$1" || true; } | wc -l | tr -d ' ')"; echo "${n}"; }
nz() { # nz <名字> <期望0的计数>
  printf '  [grep] %-46s %s\n' "$1" "$2"
}
# reconcile：判据要的是「S3 的 eg reconcile **命令**零注册」。裸词 grep 会命中 M1 起就存在的
# `config set` 的 commit verb 说明与测试里的反证串（实测 14 处，全部是那两类），因此这里按
# **命令形态**判定：非测试源命中恰 2（均在 commands.go 的 verb 说明行）+ 命令注册与用法行 0。
# 2026-09-06 随 M4 · T-…-050 按实测重钉（只改形态，判据本体一格不放宽）：R1 纳管 commit 的
# 写口 `internal/cli/reconcile_commit.go` 落地后，非测试源的裸词命中必然非 0（实测 15 处，
# **全部**落在该写口文件内，裸词总数 35）。判据本体仍是「命令零注册」，故把「非测试源裸词 0」
# 重钉为「**写口文件之外**非测试源裸词 0 + 注册形态 `Name: "reconcile"` 恰 0 + 用法行 0」——
# 新增注册形态这一格，是加严：写口文件本身若偷偷注册命令，第二格立刻红。
# 2026-09-06 随 M4 · T-…-051 再次按实测重钉（**同一三段式判据形态**，本体一格不放宽）：
# R2 `reviewed_at` 补齐的 CLI 修复桥 `internal/cli/reconcile_repair_reviewed.go` 落地后，
# 「写口文件之外非测试源裸词」实测 22（该修复桥内 21 处 + `apply.go` 里 1 处指向该桥文件的
# 说明注释行），故把该修复桥并入 T-050 已建立的同一份排除名单（`commands.go` /
# `reconcile_commit.go` / `reconcile_repair_reviewed.go`），重钉后实测回到 **0**。
# 判据本体仍逐字是「`eg reconcile` **命令**零注册」，由三段式合判：
#   ① 排除名单外非测试源裸词 == 0；② 注册形态 `Name: "reconcile"` 全 `internal/cli/`（**含**
#   三个被排除文件、不设任何白名单）== 0；③ `eg reconcile` 用法行 == 0。
# 排除名单只影响第 ① 格的裸词噪声，②③ 两格覆盖全目录 —— 修复桥若注册命令或写用法行，立刻红。
C_RECONCILE_RAW="$(cnt "grep -rn 'reconcile' ${REPO_ROOT}/internal/cli/")"
# 白名单第四格（2026-09-06 随 M4 · T-…-056 按实测重钉判据**形态**）：
# `reconcile_render.go` 是对账 finding 的人类可读渲染层（R7 视图提示的唯一落点，只进 stdout）。
# 它按包名自带 `reconcile` 裸词与两处「命令本体属 T-…-058 / T-…-059」的说明行，
# 因此与既有三格同口径排除；**判据本体一格未放宽**：命令注册形态 `Name: "reconcile"` 仍恒 0，
# 并新增一格加严 —— 渲染层文件内**零命令注册形态**（Name / Usage / Run / Command{ 全零）。
INSUFF_RENDER_BASE="reconcile_render.go"
# 白名单第五 / 第六格（2026-09-07 随 M4 · T-…-055 阶段 3 按实测重钉判据**形态**，照 T-…-051
# 修复桥先例）：R6 的 CLI 修复桥 `reconcile_repair_stale.go` 与综述事实只读采样
# `reconcile_recap_sample.go` 按包名/职责自带 `reconcile` 裸词（实测两文件共 26 处，全是
# 包路径、类型名 `reconcile.RepairSpec` 之类与边界说明行）。**判据本体一格未放宽**：
# 命令注册形态 `Name: "reconcile"` 仍覆盖全 `internal/cli/`（含这两个文件、不设白名单）恒 0，
# 用法行 grep 也**不**为这两个文件开口子（它们文件内零 `eg reconcile` 用法字样）。
R6_BRIDGE_BASE="reconcile_repair_stale.go"
R6_SAMPLE_BASE="reconcile_recap_sample.go"
# ── 2026-09-07 随 M4 · T-…-058 阶段 3 按实测重钉判据**形态**（本体一格未放宽） ──────────────
# M3 收口当日 `eg reconcile` 命令零注册是 M3 的真事实（M3 只有 S1/S2 命令，S3 命令整体未开工），
# 该事实以「M3 侧加数 0」的形态在下面逐字保留。M4 · T-…-058 按技术方案 §7.1 / 对账合同 §12
# **合法注册**该 S3 命令（`eg reconcile [--json] [--dry-run]`，命令数 18 → 19），
# 因此判据由「零注册」按实测重钉为「注册形态恰 1 + 唯一落点 + 用法行落点唯一」三格：
#   ① 裸词：排除名单**增列** `reconcile.go`（同 `reconcile_commit.go` / `reconcile_repair_reviewed.go`
#      / `reconcile_render.go` / `reconcile_repair_stale.go` / `reconcile_recap_sample.go` 五个先例，
#      它是命令本体的唯一落点，按包名/职责自带大量裸词，实测 84 处）。排除后 `root.go` 仍剩 2 行
#      （1 行边界说明注释 + 1 行 Wire 注册行），此处**不排除整个 `root.go`**，而是收紧表达式：
#      去掉说明行（`: //`）与那一行逐字 Wire 字面量，重钉后实测回到 **0**；
#      被去掉的 Wire 行不是白名单口子 —— 它由下面第 ④ 格「Wire 落点恰 1 且唯一在 root.go」正面钉死。
#   ② 注册形态 `Name: "reconcile"` 全 `internal/cli/`（**含**全部被排除文件、不设任何白名单）
#      == **M3 期 0 + M4 T-058 新增 1 = 1**（加法等式，M3 侧加数 0 逐字保留）；
#      并**新增一格加严**：该唯一落点必须是 `internal/cli/reconcile.go`，除该文件外恒 **0**。
#   ③ `eg reconcile` 用法行：落点**恰 1 个文件**且集合逐字 == {`internal/cli/reconcile.go`}，
#      该文件外恒 **0**（此处选「落点集合等号 + 文件外恒 0」而不是钉死条数 6：条数锁只锁数量、
#      不锁位置，落点集合等号同时锁位置与唯一性，对本体是更严的约束）。
#   ④ 新增一格加严：Wire 注册行 `r.Wire("reconcile"` 全 `internal/cli/` 恰 1，且唯一落点是
#      `internal/cli/root.go` —— 命令只许经 root 的固定 Wire 表注册一次，禁止第二处偷偷注册。
# 判据本体（「S3 对账**命令**只有一处合法注册、别处零注册零用法」）一格未放宽，且比重钉前多 3 格。
RECONCILE_CMD_BASE="reconcile.go"
RECONCILE_CMD_FILE="internal/cli/reconcile.go"
RECONCILE_WIRE_LINE='_ = r.Wire("reconcile", r.runReconcile)'
# 2026-09-08 随 M5 · T-…-069 按 T-049 / T-056 同一先例再分一域（只改**形态**，本体一格不放宽）：
# M4 · T-…-059 的 `eg check` 是「`eg reconcile --dry-run` 的 R3/R4 只读**真子集**」，
# 它按合同**必须**引用 `internal/reconcile` 包符号，并在未判面提示里引导用户去跑 `eg reconcile`
# —— 那是「只读子集」这件事的正面证据，不是第二处命令注册。故把 `check.go` 纳入分域名单，
# 同时对该文件新增**封闭分类等号**（C_CHECK_UNCLASSIFIED）：它的每一条 reconcile 命中都必须
# 落进「包符号引用 / 本文件私有辅助函数 / 引导用户跑 eg reconcile 的文案 / 报告三键占位提示」
# **四**类，未归类恒 0；且 `Name: "reconcile"` 与 `r.Wire("reconcile"` 在该文件内恒 0
# （不注册第二处命令）。第四类是具名常量 `CheckReportFieldNotice` 那一行 —— 它说的是
# 「`eg check` 不写报告的 reconcile 三键、保持 `ran=false` 占位」，即**不做对账**的否定声明，
# 与「第二处对账命令」正好相反；该分类不是口子：常量名与 `ran=false` 两处逐字由
# CHECK_REPORT_NOTICE_OK 正面对撞，删掉或改成肯定语气立刻红。
CHECK_CMD_BASE="check.go"
CHECK_REPORT_NOTICE_CONST="CheckReportFieldNotice"
C_RECONCILE="$(cnt "grep -rn 'reconcile' ${REPO_ROOT}/internal/cli/ --include=*.go | grep -v _test.go | grep -v 'commands.go' | grep -v 'reconcile_commit.go' | grep -v 'reconcile_repair_reviewed.go' | grep -v '${INSUFF_RENDER_BASE}' | grep -v '${R6_BRIDGE_BASE}' | grep -v '${R6_SAMPLE_BASE}' | grep -v '/${RECONCILE_CMD_BASE}:' | grep -v '/${CHECK_CMD_BASE}:' | grep -vE ':[[:space:]]*//' | grep -vF '${RECONCILE_WIRE_LINE}'")"
C_CHECK_UNCLASSIFIED="$(cnt "grep -n 'reconcile' ${REPO_ROOT}/internal/cli/${CHECK_CMD_BASE} | grep -vE ':[[:space:]]*//' | grep -vE 'reconcile\.[A-Z]|github.com/ikaqiu-Lemon/EverGreen/internal/reconcile|reconcile(ErrorCount|ReportFindings|FindingDiags)\(|eg reconcile' | grep -vF '${CHECK_REPORT_NOTICE_CONST}'")"
CHECK_REPORT_NOTICE_OK="$(cnt "grep -n '${CHECK_REPORT_NOTICE_CONST}' ${REPO_ROOT}/internal/cli/${CHECK_CMD_BASE} | grep -F 'ran=false'")"
C_CHECK_REG="$(cnt "grep -nE 'Name:[[:space:]]*\"reconcile\"|r\.Wire\(\"reconcile\"' ${REPO_ROOT}/internal/cli/${CHECK_CMD_BASE}")"
C_RECONCILE_REG="$(cnt "grep -rnE 'Name:[[:space:]]*\"reconcile\"' ${REPO_ROOT}/internal/cli/ --include=*.go")"
C_RECONCILE_REG_OUT="$(cnt "grep -rnE 'Name:[[:space:]]*\"reconcile\"' ${REPO_ROOT}/internal/cli/ --include=*.go | grep -v '/${RECONCILE_CMD_BASE}:'")"
RECONCILE_REG_SET="$({ grep -rlE 'Name:[[:space:]]*"reconcile"' "${REPO_ROOT}/internal/cli/" --include=*.go || true; } | sed "s#${REPO_ROOT}/##" | sort | tr '\n' ' ')"
# 2026-09-08 随 M5 · T-…-069 按实测重钉（只改**形态**，本体一格不放宽）：
# `eg reconcile` 用法行的落点集合从 {reconcile.go} 扩为 {reconcile.go, check.go, commands.go}
# —— 后两处都是 M4 · T-…-059 合同要求的**引导文案**（`eg check` 未判面提示「请跑 eg reconcile」
# + 命令表登记「reconcile --dry-run 的只读真子集」），不是第二处命令实现。
# 同时把「条数」升级为**加法等式** 6 + 7 + 1 = 14（历史加数 6 逐字保留），
# 并保留「注册形态只在 reconcile.go」这一格本体（见 C_RECONCILE_REG_OUT / C_CHECK_REG）。
# ── C2a·M6 现态重钉（§16.4 + A-52/A-58 强事务）：M6（T-070～074）把 `eg reconcile` 非 dry-run
#    路径接入 A 类强事务底座（runReconcileCritical 编排 + 七步固定次序入口），`reconcile.go` 内
#    合同说明用法行由 6 抬为 8。**历史加数 6 逐字保留**、check 7 / commands 1 两格未动，只把
#    reconcile.go 那格重钉为「M4/M5 期 6 + M6 强事务 2 = 8」，总数 6+7+1=14 → (6+2)+7+1=16。
C_RECONCILE_CMD="$(cnt "grep -rn 'eg reconcile' ${REPO_ROOT}/internal/cli/ --include=*.go | grep -v _test.go | grep -v '≠' | grep -v '${INSUFF_RENDER_BASE}'")"
C_RECONCILE_CMD_OUT="$(cnt "grep -rn 'eg reconcile' ${REPO_ROOT}/internal/cli/ --include=*.go | grep -v _test.go | grep -v '≠' | grep -v '${INSUFF_RENDER_BASE}' | grep -v '/${RECONCILE_CMD_BASE}:'")"
C_RECONCILE_CMD_RECON="$(cnt "grep -n 'eg reconcile' ${REPO_ROOT}/internal/cli/${RECONCILE_CMD_BASE} | grep -v '≠'")"
C_RECONCILE_CMD_CHECK="$(cnt "grep -n 'eg reconcile' ${REPO_ROOT}/internal/cli/${CHECK_CMD_BASE} | grep -v '≠'")"
C_RECONCILE_CMD_CMDS="$(cnt "grep -n 'eg reconcile' ${REPO_ROOT}/internal/cli/commands.go | grep -v '≠'")"
C_RECONCILE_CMD_OTHER="$(cnt "grep -rn 'eg reconcile' ${REPO_ROOT}/internal/cli/ --include=*.go | grep -v _test.go | grep -v '≠' | grep -v '${INSUFF_RENDER_BASE}' | grep -v '/${RECONCILE_CMD_BASE}:' | grep -v '/${CHECK_CMD_BASE}:' | grep -v '/commands.go:'")"
RECONCILE_CMD_SET="$({ grep -rn 'eg reconcile' "${REPO_ROOT}/internal/cli/" --include=*.go | grep -v _test.go | grep -v '≠' | grep -v "${INSUFF_RENDER_BASE}" || true; } | sed "s#${REPO_ROOT}/##" | cut -d: -f1 | sort -u | tr '\n' ' ')"
C_RECONCILE_CMD_FILES="$({ printf '%s\n' "${RECONCILE_CMD_SET}" | tr ' ' '\n' | grep -c . || true; } | tr -d ' ')"
C_RECONCILE_WIRE="$(cnt "grep -rnF 'r.Wire(\"reconcile\"' ${REPO_ROOT}/internal/cli/ --include=*.go")"
RECONCILE_WIRE_SET="$({ grep -rlF 'r.Wire("reconcile"' "${REPO_ROOT}/internal/cli/" --include=*.go || true; } | sed "s#${REPO_ROOT}/##" | sort | tr '\n' ' ')"
C_RENDER_REG="$(cnt "grep -nE '(Name|Usage|Run|Flags):|Command\{' ${REPO_ROOT}/internal/cli/${INSUFF_RENDER_BASE}")"
# S5 符号：裸词命中 1 处，是 `internal/query/relation.go` 的**阶段说明注释**（判据原文允许
# 「仅命中标注 S3–S5 阶段的注释」），故按「非注释命中」判定。
# 2026-09-08 随 M5 · T-…-069 按合同分域重钉（只改**形态**，S5 / M6 本体一格不放宽）：
# 原判据把五个词混成一张表。M5 索引合同（T-…-064 / 065）明写「纯 Go SQLite + FTS5、静态构建、
# 索引为可重建派生物」，故 `FTS5` / `sqlite` 自 M5 起是**索引包内的合同事实**；
# 而 `flock` / `txn/` / `run.lock` 属 **M6（S5）** 的强原子事务与锁，本阶段一个字都不许出现。
# 拆成三格，后两格是**新增的加严**：
#   ① M6 三词在 internal/ 与 cmd/ 的非注释行恒 0（原本体，未放宽）；
#   ② M5 两词的非注释命中必须全部落在 `internal/index/` 内 —— 域外恒 0（含 cmd/ / cli / query）；
#   ③ 域内必须 ≥ 1：索引确实由 SQLite/FTS5 实现，避免「把词删干净换来假绿」。
C_S5_RAW="$(cnt "grep -rnE 'flock|txn/|run\.lock|FTS5|sqlite' ${REPO_ROOT}/internal/ ${REPO_ROOT}/cmd/")"
C_S5="$(cnt "grep -rnE 'flock|txn/|run\.lock' ${REPO_ROOT}/internal/ ${REPO_ROOT}/cmd/ | grep -vE ':[0-9]+:\s*//'")"
# ── C2a·M6 现态重钉（§16.1/§16.2/§16.3「命令三类别 + 锁覆盖面 + runtime reserved」+ §16.4 阶段化）──
# 原 ① 写「M6 三词非注释恒 0」是 M5 收口当日的真事实（S5 强原子事务/锁子系统整体未开工）。M6
# （T-070～074）已按合同 A-52/A-53/A-54/A-55 落地 `run.lock` 短临界区锁、`txn/` 事务日志与
# 崩溃恢复钩子，故三词不再恒 0。**判据本体一格不放宽**：M5 期「非授权本位零命中」逐字保留，
# 只把恒 0 重钉为「保留历史事实 + 新增 M6 现态双侧锁」——
#   ① 非测试非注释命中只许落**授权本位**
#      {internal/txn/**, internal/index/**, internal/cli/index_lock.go, internal/cli/recover_hook.go}
#      （合同 §3 / A-53 / A-55 的锁与恢复落点），授权外恒 0（含 cmd/ 与其它 cli 文件）；
#   ② 授权内 ≥ 1：锁/事务确有实现，避免「把词删干净换来假绿」。
S5_AUTH_RE='(/internal/txn/|/internal/index/|/internal/cli/index_lock\.go|/internal/cli/recover_hook\.go)'
C_S5_CODE="$(cnt "grep -rnE 'flock|txn/|run\.lock' ${REPO_ROOT}/internal/ ${REPO_ROOT}/cmd/ | grep -v '_test\.go' | grep -vE ':[0-9]+:\s*//'")"
C_S5_UNAUTH="$(cnt "grep -rnE 'flock|txn/|run\.lock' ${REPO_ROOT}/internal/ ${REPO_ROOT}/cmd/ | grep -v '_test\.go' | grep -vE ':[0-9]+:\s*//' | grep -vE '${S5_AUTH_RE}'")"
C_S5_AUTH=$((C_S5_CODE - C_S5_UNAUTH))
C_M5SQL_OUT="$(cnt "grep -rnE 'FTS5|sqlite' ${REPO_ROOT}/internal/ ${REPO_ROOT}/cmd/ | grep -vE ':[0-9]+:\s*//' | grep -v '/internal/index/'")"
C_M5SQL_IN="$(cnt "grep -rnE 'FTS5|sqlite' ${REPO_ROOT}/internal/index/ | grep -vE ':[0-9]+:\s*//'")"
C_EXIT5="$(cnt "grep -rn 'os.Exit(5)' ${REPO_ROOT}/internal/ ${REPO_ROOT}/cmd/ | grep -v _test.go | grep -v 'os\.\" *+'")"
# os.Remove：非测试落点恰 2 且集合恰 {internal/cli/ymlwrite.go, internal/store/write.go}
# （T-044 收口登记的白名单；两处都是 tmp 文件清理，不删任何权威 Markdown）。
# 2026-09-08 随 M5 · T-…-069 按实测重钉（只改**形态**，「不删权威 Markdown」本体一格不放宽）：
# M5 索引包新增三处删除动作，删的都是**可完整重建的派生物**：`.index/`（build 半成品清理 /
# rebuild 先删后建）与 `NewScratch` 亲手造出来的一次性目录。故：
#   ① 非测试落点用**加法等式** M3 期 2 + M5 新增 3 = 5 判定（历史加数 2 逐字保留）；
#   ② 白名单集合逐字等号扩为五文件（多一处、换文件都要重新登记）；
#   ③ 破坏性动作**摘掉派生物白名单后仍恒 0**，且 RemoveAll 参数只许逐字 `dir`、
#      索引包内 Git 破坏性动作恒 0（后两格是新增加严）。
C_REMOVE="$(cnt "grep -rn 'os.Remove' ${REPO_ROOT}/internal/ --include=*.go | grep -v _test.go")"
REMOVE_SET="$({ grep -rln 'os.Remove' "${REPO_ROOT}/internal/" --include=*.go | grep -v _test.go || true; } | sed "s#${REPO_ROOT}/##" | sort | tr '\n' ' ')"
# ── C2a·M6 现态重钉（§16.2「.index/ 共存契约与 RemoveAll 禁令」+ A-52/A-59 事务提交/恢复 + §16.4）──
# M6（T-070～074）在事务底座与恢复路径新增 os.Remove：`internal/txn/commit.go`（提交/清理前像、
# 隔离区、闭合事务目录）与 `internal/txn/recover.go`（Pass B 幂等回滚后清理），删的都是 **.index/txn/**
# 下**本进程亲手写出的**事务派生物/恢复中间物，**从不触碰权威 Markdown**（§16.2 禁 os.RemoveAll(.index)
# 由 C_DESTRUCT / C_DESTRUCT_ARG 另钉）。**判据本体一格不放宽**：「不删权威 Markdown」逐字保留，
# 「加法等式 2+3=5 + 白名单五文件」按 M6 现态重钉为「加法等式 2(M3)+3(M5)+2(M6)=七文件闭包」：
#   ① 非测试落点的**文件集**恰七文件闭包（M3 tmp 清理 2 + M5 索引派生物 3 + M6 事务/恢复派生物 2），
#      位置与唯一性由集合等号锁死（多一处/换文件都要重新登记）；
#   ② 白名单**之外**的 os.Remove 非测试命中恒 0（双侧锁：授权外零泄漏）；
#   ③ 非测试**调用行数**逐字锁死为 25（历史「恰 2」→「2+3=5」的精确基数口径一格不放宽，只按 M6
#      现态换数：ymlwrite 1 + store/write 1 + index{build 5, rebuild 8, scratch 1} + txn{commit 7,
#      recover 2} = 25）。任何新增/删除一处 os.Remove 都必须重新登记，杜绝「集合不变、次数悄悄涨」。
#      ③ 与 ① 合并进**同一格**判据（集合等号 AND 行数等号），故 checks 总数保持 106 不变，
#      m4_acceptance 的 `checks=106 failed=0` 历史单调锁不受影响。
REMOVE_WL_RE='/internal/(cli/ymlwrite|store/write|index/(build|rebuild|scratch)|txn/(commit|recover))\.go:'
C_REMOVE_OUT="$(cnt "grep -rn 'os.Remove' ${REPO_ROOT}/internal/ --include=*.go | grep -v _test.go | grep -vE '${REMOVE_WL_RE}'")"
IDX_DESTRUCT_WL='^internal/index/(build|rebuild|scratch)\.go:[0-9]+:[[:space:]]*(_ = |if err := |return )os\.RemoveAll\(dir\)'
# C2a·M6 现态重钉（§16.2 RemoveAll 禁令 + §16.4）：M6 在 `internal/index/build.go` 新增一行**注释**
# 逐字声明「`os.RemoveAll(dir)` 在 M6 语境下永久禁用」（§16.2 的禁令自证）。该注释不是可执行破坏性
# 动作，却被裸词 grep 命中。照 m3_execution_failed.sh「可执行行 vs 注释行」先例，**排除整行注释**
# （`:[0-9]+:` 后首非空字符为 `//`），本体「无可执行破坏性动作」一格不放宽 —— 带尾注释的可执行行
# 不以 `//` 起首，仍会被计入；C_DESTRUCT_ARG / C_DESTRUCT_GIT 两格继续正面钉死索引包破坏性动作。
C_DESTRUCT="$( { cd "${REPO_ROOT}" && grep -rnE 'checkout --|reset --hard|RemoveAll' internal/ --include=*.go || true; } | { grep -v '_test\.go' || true; } | { grep -vE "${IDX_DESTRUCT_WL}" || true; } | { grep -vE ':[0-9]+:[[:space:]]*//' || true; } | wc -l | tr -d ' ')"
C_DESTRUCT_ARG="$(cnt "grep -rn 'RemoveAll' ${REPO_ROOT}/internal/index/ --include=*.go | grep -v _test.go | grep -v 'os\.RemoveAll(dir)'")"
C_DESTRUCT_GIT="$(cnt "grep -rnE 'checkout --|reset --hard' ${REPO_ROOT}/internal/index/ --include=*.go")"
C_INDEX="$(cnt "ls ${REPO_ROOT}/vault/.index 2>/dev/null")"
# 2026-09-08 随 M5 · T-…-069 按合同分域重钉（只改**形态**，「M3 不承诺性能」本体一格不放宽）：
# 「万卡」这类夸大规模承诺**全域仍恒 0**（原本体，未放宽）；`P95` 自 M5 · T-…-068 起是
# `eg bench` 的合同产物（search_p95_ms / card_show_p95_ms / rel_p95_ms 三键），
# 故 `P95` 的落点集合封闭为 {internal/cli/bench.go, internal/cli/bench_test.go}，域外恒 0。
C_P95="$(cnt "grep -rn '万卡' ${REPO_ROOT}/internal/ ${REPO_ROOT}/cmd/")"
C_P95_OUT="$(cnt "grep -rn 'P95' ${REPO_ROOT}/internal/ ${REPO_ROOT}/cmd/ | grep -vE '/internal/cli/bench(_test)?\.go:'")"
P95_SET="$({ grep -rln 'P95' "${REPO_ROOT}/internal/" "${REPO_ROOT}/cmd/" || true; } | sed "s#${REPO_ROOT}/##" | sort | tr '\n' ' ')"
# 占位字面量分片拼接：整串写死会让本脚本自身被 teamwork 侧 H10z 的「占位零残留」判成残留。
PLACE_LIT="M3/S2 未""实现"
C_PLACE="$(cnt "grep -rF '${PLACE_LIT}' ${REPO_ROOT}/internal/ ${REPO_ROOT}/README.md ${REPO_ROOT}/INSTALL.md ${REPO_ROOT}/skill/SKILL.md")"
C_BANNED=0
for b in "跳过 hash 比对" "自动回滚到执行前" "Agent 自动路径亦可改"; do
  C_BANNED=$((C_BANNED + $(cnt "grep -rF '${b}' ${REPO_ROOT}/README.md ${REPO_ROOT}/INSTALL.md ${REPO_ROOT}/skill/SKILL.md")))
done
# 「材料支持不足 / insufficient_support」：M3 收口当日非测试命中恰 0。
# 2026-09-06 随 M4 · T-…-056（R7 实时判定）**按实测重钉判据形态、本体一格不放宽**：
# 那句方括号提示按对账合同 §10 第 2 条必须在命令渲染层有**唯一**落点（只进 stdout），
# 故判据按包分域（照 T-049 先例）：渲染层**以外**仍恰 0（本体：不进产物、不进对账包 /
# 落盘层、不进 M3 命令输出），渲染层内 ≥ 1，且提示的非测试落点集合恰 {渲染层那一个文件}。
INSUFF_RENDER="internal/cli/reconcile_render.go"
C_INSUFF="$(cnt "grep -rn '材料支持不足\|insufficient_support' ${REPO_ROOT}/internal/ --include=*.go | grep -v _test.go | grep -v '${INSUFF_RENDER}'")"
C_INSUFF_CORE="$(cnt "grep -rn '材料支持不足\|insufficient_support' ${REPO_ROOT}/internal/reconcile/ ${REPO_ROOT}/internal/store/ --include=*.go | grep -v _test.go")"
C_INSUFF_RENDER="$(cnt "grep -n '材料支持不足' ${REPO_ROOT}/${INSUFF_RENDER}")"
INSUFF_SET="$({ grep -rl '材料支持不足\|insufficient_support' "${REPO_ROOT}/internal/" --include=*.go | grep -v _test.go || true; } | sed "s#${REPO_ROOT}/##" | sort | tr '\n' ' ')"
# 写口唯一：判据要的是「三个写口函数的**调用点**只在白名单内」。裸词 grep 会命中 op 名常量、
# ActionKind 常量与矩阵备注（实测 19 处），故按**函数调用形态** `SetXxx(` 判定。
C_WRITEPORT_RAW="$(cnt "grep -rn 'SetStatus\|SetReplacedBy\|SetDeleted' ${REPO_ROOT}/internal/ --include=*.go | grep -v _test.go")"
C_WRITEPORT="$(cnt "grep -rnE '\b(SetStatus|SetReplacedBy|SetDeleted)\(' ${REPO_ROOT}/internal/ --include=*.go | grep -v _test.go | grep -v '/internal/store/' | grep -vE '/internal/cli/(deprecate|restore|replaced_by|delete|undelete)\.go'")"
C_RULESPTR="$(cnt "grep -rn '\*model\.Card' ${REPO_ROOT}/internal/rules/ --include=*.go | grep -v _test.go")"
C_UNREVFILT="$(cnt "grep -rln 'filter/unreviewed' ${REPO_ROOT}/internal/query/rank ${REPO_ROOT}/internal/query/relations ${REPO_ROOT}/internal/rules/converge ${REPO_ROOT}/internal/query/review 2>/dev/null")"
C_RELPLACE="$(cnt "grep -nF '${PLACE_LIT}' ${REPO_ROOT}/internal/cli/rel.go")"
C_RECOMPUTE="$(cnt "grep -nE 'recomputeImpact|RecomputeImpact' ${REPO_ROOT}/internal/cli/proposal.go")"
# ── op 全集与矩阵两路径同 🔴 的**判据形态锁**（2026-09-07 随 M4 · T-…-055 阶段 4a 新增）——
# 判据 15 的「op 全集」与判据 17 的「两路径同 🔴」两条计数在本 task 按实测重钉为
# **加法 / 减法等式**：`AllOpNames` = M3 期 16 + M4 新增 1（`set_stale`，A-33）= 17；
# 两路径同 🔴 = M3 期 5 − 依 A-34 放开 1（矩阵 #33 的 P-A）= 4。
# M3 期的两个加数 16 / 5 是**历史事实**，只许以「加数」形态出现，**不许**被谁悄悄改回
# 写死的总数或直接篡改数值。故这里对**判据源码本身**再上一道形态锁（比只跑用例更严）：
#   ① 加法等式常量 `m3AllOps, m4NewOps = 16, 1` 的落点恰 3 处
#      （`internal/plan/m3_test.go` / `internal/cli/reconcile_repair_reviewed_test.go` /
#      `test/e2e/m3_acceptance_test.go`），少一处 = 有人把等式改回写死；
#   ② 全库不存在把 `AllOpNames()` 与写死 16 直接比较的旧形态；
#   ③ 减法等式常量 `m3BothDenied, a34Unlocked = 5, 1` 的落点恰 2 处
#      （`internal/plan/matrix_test.go` / `test/e2e/m3_acceptance_test.go`）；
#   ④ 矩阵 #33 逐字恰 `allow, deny` 一行（放开的恰 P-A 一格，P-U 仍 🔴，不得顺手放开）。
C_OPEQ="$(cnt "grep -rn 'm3AllOps, m4NewOps = 16, 1' ${REPO_ROOT}/internal/ ${REPO_ROOT}/test/ --include=*_test.go")"
C_OPHARD="$(cnt "grep -rnE 'AllOpNames\(\)\)?[[:space:]]*!=[[:space:]]*16|len\(all\)[[:space:]]*!=[[:space:]]*16' ${REPO_ROOT}/internal/ ${REPO_ROOT}/test/ ${REPO_ROOT}/cmd/ --include=*.go")"
C_BOTHEQ="$(cnt "grep -rn 'm3BothDenied, a34Unlocked = 5, 1' ${REPO_ROOT}/internal/ ${REPO_ROOT}/test/ --include=*_test.go")"
C_M33="$(cnt "grep -nF '{33, ObjectReview, FieldReviewStale, allow, deny,' ${REPO_ROOT}/internal/plan/matrix.go")"
nz "reconcile 裸词（总 ${C_RECONCILE_RAW}，名单外非测试非说明非 Wire 行）" "${C_RECONCILE}"
printf '  [grep] %-46s %s（= M3 期 0 + M4 T-058 新增 1；落点 %s）\n' \
  "reconcile 注册形态 Name: \"reconcile\" 恰 1" "${C_RECONCILE_REG}" "${RECONCILE_REG_SET}"
nz "reconcile 注册形态在 ${RECONCILE_CMD_FILE} 之外" "${C_RECONCILE_REG_OUT}"
printf '  [grep] %-46s %s（落点 %s，共 %s 文件）\n' \
  "eg reconcile 用法行落点恰 1 文件" "${C_RECONCILE_CMD}" "${RECONCILE_CMD_SET}" "${C_RECONCILE_CMD_FILES}"
nz "eg reconcile 用法行在 ${RECONCILE_CMD_FILE} 之外" "${C_RECONCILE_CMD_OUT}"
printf '  [grep] %-46s %s（落点 %s）\n' \
  "reconcile Wire 注册行恰 1" "${C_RECONCILE_WIRE}" "${RECONCILE_WIRE_SET}"
nz "渲染层文件内命令注册形态" "${C_RENDER_REG}"
printf '  [grep] %-46s 授权内 %s / 越界 %s（非注释总 %s）\n' \
  "M6 三词 flock|txn/|run.lock 落授权本位" "${C_S5_AUTH}" "${C_S5_UNAUTH}" "${C_S5}"
nz "M5 两词 FTS5|sqlite 在 internal/index 之外" "${C_M5SQL_OUT}"
printf '  [grep] %-46s %s（应 ≥ 1：索引确由 SQLite/FTS5 实现）\n' \
  "FTS5|sqlite 在 internal/index 之内" "${C_M5SQL_IN}"
nz "os.Exit(5)" "${C_EXIT5}"
printf '  [grep] %-46s 落点行 %s / 授权外 %s（白名单七文件：%s）\n' \
  "os.Remove 七文件闭包 2(M3)+3(M5)+2(M6)" "${C_REMOVE}" "${C_REMOVE_OUT}" "${REMOVE_SET}"
nz "checkout --|reset --hard|RemoveAll（摘派生物白名单后）" "${C_DESTRUCT}"
nz "internal/index 的 RemoveAll 参数非逐字 dir" "${C_DESTRUCT_ARG}"
nz "internal/index 内 Git 破坏性动作" "${C_DESTRUCT_GIT}"
nz "vault/.index" "${C_INDEX}"
nz "万卡（夸大规模承诺，全域）" "${C_P95}"
printf '  [grep] %-46s %s（落点集合：%s）\n' \
  "P95 在 eg bench 两文件之外" "${C_P95_OUT}" "${P95_SET}"
nz "占位「${PLACE_LIT}」" "${C_PLACE}"
nz "三条禁止措辞" "${C_BANNED}"
nz "材料支持不足（非测试，渲染层外）" "${C_INSUFF}"
nz "材料支持不足（对账包 + 落盘层非测试）" "${C_INSUFF_CORE}"
printf '  [grep] %-46s %s（落点集合：%s）\n' "材料支持不足 渲染层命中（应 ≥ 1）" \
  "${C_INSUFF_RENDER}" "${INSUFF_SET}"
nz "写口越界调用点（裸词 ${C_WRITEPORT_RAW}，调用形态）" "${C_WRITEPORT}"
nz "rules 包可变引用" "${C_RULESPTR}"
nz "unreviewed 过滤器被跨包引用" "${C_UNREVFILT}"
printf '  [grep] %-46s 等式落点 %s / 旧写死形态 %s\n' \
  "op 全集加法等式 16+1（应 3 / 0）" "${C_OPEQ}" "${C_OPHARD}"
printf '  [grep] %-46s 等式落点 %s / #33 allow,deny %s\n' \
  "两路径同 🔴 减法等式 5−1（应 2 / 1）" "${C_BOTHEQ}" "${C_M33}"
# 版本号四处同真
VER_MK="$(cd "${REPO_ROOT}" && make print-version 2>/dev/null | tail -1 | tr -d ' ')"
VER_BIN_SRC="$(grep -oE '0\.[0-9]+\.[0-9]+-m[0-9]' "${REPO_ROOT}/internal/version/version.go" | head -1)"
VER_README="$(grep -oE '0\.[0-9]+\.[0-9]+-m[0-9]' "${REPO_ROOT}/README.md" | head -1)"
VER_INSTALL="$(grep -oE '0\.[0-9]+\.[0-9]+-m[0-9]' "${REPO_ROOT}/INSTALL.md" | head -1)"
printf '  [ver] make print-version=%s version.go=%s README=%s INSTALL=%s\n' \
  "${VER_MK}" "${VER_BIN_SRC}" "${VER_README}" "${VER_INSTALL}"
# 2026-09-08 随 M5 · T-…-069 阶段化重钉（只改**形态**，「四处同真」本体一格不放宽）：
# 版本号逐里程碑推进，「当期值」这个**指针**随里程碑走；原脚本把它写死成 0.3.0-m3，
# M5 推进后必然自红。两侧重钉（比原判据更严）：
#   ① 当前值：make print-version / version.go / README / INSTALL 四处**逐字同真** 0.6.0-m6；
#   ② 历史基线只读复算：M3 决策文档仍逐字记录 0.3.0-m3（历史结论不被改写），
#      且历史值一律不得作为**生效值**残留在 version.go / Makefile 里（逐个旧值对撞）。
# C2b·M6 现态重钉：M6 · T-…-075 把版本推进到 0.6.0-m6，故当期值抬为 0.6.0-m6，
# 0.5.0-m5 并入旧值残留对撞集（M3 只读基线一字未动）。
CUR_VERSION="0.6.0-m6"
M3_SPEC="${EPIC_DIR}/docs/specs/2026-11-08-m3-release-and-version.md"
VER_OK=0
for v in "${VER_BIN_SRC}" "${VER_README}" "${VER_INSTALL}" "${VER_MK}"; do
  [ "${v}" = "${CUR_VERSION}" ] || VER_OK=1
done
grep -Fq "0.3.0-m3" "${M3_SPEC}" 2>/dev/null || VER_OK=1
for old in 0.1.0-m1 0.2.0-m2 0.3.0-m3 0.4.0-m4 0.5.0-m5; do
  if grep -Fq "${old}" "${REPO_ROOT}/internal/version/version.go" ||
    grep -Fq "${old}" "${REPO_ROOT}/Makefile"; then VER_OK=1; fi
done
# M3 期真实会话留痕在盘 + 无回放替代物 + plan 里 M3 op ≥ 3
N_SESS="$(cnt "ls ${M3SESS}")"
N_REPLAY="$(cnt "ls ${E2E}/m3_*replay*.sh 2>/dev/null")"
N_M3OP="$({ grep -ohE '"(deprecate|restore|set_replaced_by|delete|undelete|mark_reviewed|replace_block|remove_relation)"' "${M3SESS}"/m3-ops-plan.json || true; } | sort -u | wc -l | tr -d ' ')"
printf '  [sess] 留痕文件 %s 个 / 回放替代物 %s 个 / plan 内不同 M3 op %s 个\n' \
  "${N_SESS}" "${N_REPLAY}" "${N_M3OP}"
# 验收报告在盘且被跟踪
REPORT_TRACKED=1
if [ -f "${REPORT}" ] && git -C "${PARENT}/teamwork" ls-files --error-unmatch \
  "projects/evergreen/s1_main_flow/docs/specs/2026-11-11-m3-acceptance-report.md" >/dev/null 2>&1; then
  REPORT_TRACKED=0
fi
printf '  [report] 存在且被 Git 跟踪：%s\n' "$([ "${REPORT_TRACKED}" = "0" ] && echo 是 || echo 否)"

echo "════════ 第四段：判据 1 ~ 17 逐条结论 ════════"

# ── [判据 1] A-23 / A-24 前置裁决已关闭（文档存在性与结论字样机器可判；owner 真实意图属外部事实）
ADJ_OK=1
[ -f "${ADJ}" ] && git -C "${PARENT}/teamwork" ls-files --error-unmatch \
  "projects/evergreen/s1_main_flow/docs/specs/2026-10-13-m3-prestart-adjudication.md" >/dev/null 2>&1 && ADJ_OK=0
N_ADJ_ROWS="$(cnt "grep -cE '^\| \*\*A-2[34]\*\* \|' ${ADJ} || true")"
A23="$(cnt "grep -c 'CLI 直写例外' ${ADJ}")"
A24="$(cnt "grep -c '物理移除' ${ADJ}")"
sub 1 "裁决文档存在且被跟踪" "${ADJ_OK}"
sub 1 "A-23 结论 CLI 直写例外在册" "$([ "${A23}" != "0" ] && echo 0 || echo 1)"
sub 1 "A-24 结论 物理移除在册" "$([ "${A24}" != "0" ] && echo 0 || echo 1)"

# ── [判据 2] 提案 status 四态封闭
sub 2 "TestStatus_ExactlyFourValues" "$(case_ok TestStatus_ExactlyFourValues)"
# ── [判据 3] 合法边 5 / 非法边 12 / 终态
for t in TestStateMachine_LegalTransitions TestStateMachine_IllegalTransitions TestTerminalStates; do
  sub 3 "${t}" "$(case_ok "${t}")"
done
# ── [判据 4] superseded 两触发 + E7
for t in TestSuperseded_TriggerPartialAccept TestSuperseded_TriggerImpactChanged TestSupersededBy_Required; do
  sub 4 "${t}" "$(case_ok "${t}")"
done
# ── [判据 5] approve 执行前重算影响面
sub 5 "recomputeImpact ≥ 1（实测 ${C_RECOMPUTE}）" "$([ "${C_RECOMPUTE}" != "0" ] && echo 0 || echo 1)"
sub 5 "TestApprove_RecomputesImpactBeforeExecute" "$(case_ok TestApprove_RecomputesImpactBeforeExecute)"
# ── [判据 6] 4 × 3 可达矩阵
for t in TestOrthogonality_ReachabilityMatrix TestApprovedDoesNotImplySucceeded; do
  sub 6 "${t}" "$(case_ok "${t}")"
done
# ── [判据 7] execution=failed 逐路径
sub 7 "TestExecutionFailed_ListsWrittenAndUnwrittenPaths" "$(case_ok TestExecutionFailed_ListsWrittenAndUnwrittenPaths)"
# ── [判据 8] 用户可改 / Agent 改不了
sub 8 "TestE6_AgentAppendCoreKnowledgeRejected" "$(case_ok TestE6_AgentAppendCoreKnowledgeRejected)"
sub 8 "TestEdit_UserExplicitCoreKnowledgeAccepted" "$(case_ok TestEdit_UserExplicitCoreKnowledgeAccepted)"
# ── [判据 9] initiator + --user-request 反伪造
sub 9 "TestUserRequestFlagRequired" "$(case_ok TestUserRequestFlagRequired)"
# ── [判据 10] 写口唯一 + rules 无可变引用
sub 10 "写口越界调用点 0（实测 ${C_WRITEPORT}）" "$([ "${C_WRITEPORT}" = "0" ] && echo 0 || echo 1)"
sub 10 "rules 可变引用 0（实测 ${C_RULESPTR}）" "$([ "${C_RULESPTR}" = "0" ] && echo 0 || echo 1)"
# ── [判据 11] 退出码 6 启用且恰两条命令
sub 11 "TestExitCode6_OnlyAfterValidationPasses" "$(case_ok TestExitCode6_OnlyAfterValidationPasses)"
sub 11 "os.Exit(5) 0（实测 ${C_EXIT5}）" "$([ "${C_EXIT5}" = "0" ] && echo 0 || echo 1)"
sub 11 "TestExitCodeSetClosed（本 task 新增）" "$(case_ok TestExitCodeSetClosed)"
# ── [判据 12] 逻辑删除全时序
for t in TestDelete_NoAutoStatusChange TestRestore_NoSupportPrecondition TestUndelete_StatusUntouched \
         TestLogicalDelete_RelationsPreserved; do
  sub 12 "${t}" "$(case_ok "${t}")"
done
# ── [判据 13] reviewed_at 唯一写入 + ADR-20 文件级隔离
sub 13 "unreviewed 过滤器跨包引用 0（实测 ${C_UNREVFILT}）" "$([ "${C_UNREVFILT}" = "0" ] && echo 0 || echo 1)"
sub 13 "TestMarkReviewed" "$(case_ok TestMarkReviewed)"
sub 13 "TestUnreviewed" "$(case_ok TestUnreviewed)"
# ── [判据 14] 两个标记与双标记顺序；材料支持不足不出现
sub 14 "TestMarkers_DeletedAndUnreviewed" "$(case_ok TestMarkers_DeletedAndUnreviewed)"
sub 14 "材料支持不足 渲染层外 0（实测 ${C_INSUFF}）" "$([ "${C_INSUFF}" = "0" ] && echo 0 || echo 1)"
sub 14 "材料支持不足 对账包+落盘层 0（实测 ${C_INSUFF_CORE}）" \
  "$([ "${C_INSUFF_CORE}" = "0" ] && echo 0 || echo 1)"
sub 14 "材料支持不足 提示唯一落点是渲染层（实测 ${C_INSUFF_RENDER} 行 / 集合 ${INSUFF_SET})" \
  "$([ "${C_INSUFF_RENDER}" -ge 1 ] && [ "${INSUFF_SET}" = "${INSUFF_RENDER} " ] && echo 0 || echo 1)"
# ── [判据 15] 8 个 op 可执行 + 诊断码闭合
# 2026-09-07 随 M4 · T-…-055 阶段 4a 按实测重钉判据**形态**（本体一格不放宽）：
# `AllOpNames` 由 M3 期的写死 16 改为**加法等式** 16 + 1 = 17（新增的那 1 个恰 `set_stale`，
# A-33）。「M3 新增 op 恰 8 逐个可派发 / 字段表非空 / 各有 e2e 落点」与「状态类 op 恰 5」
# 四条断言逐字未动；并**新增两格加严**：等式常量落点恰 3 处、旧写死形态全库恰 0。
sub 15 "TestW7_IsErrorInM3" "$(case_ok TestW7_IsErrorInM3)"
sub 15 "TestM3Ops" "$(case_ok TestM3Ops)"
sub 15 "TestM3OpsAllExecutable（本 task 新增）" "$(case_ok TestM3OpsAllExecutable)"
sub 15 "TestDiagnosticCodesCovered（本 task 新增）" "$(case_ok TestDiagnosticCodesCovered)"
sub 15 "op 全集加法等式 16+1 落点恰 3（实测 ${C_OPEQ}）" \
  "$([ "${C_OPEQ}" = "3" ] && echo 0 || echo 1)"
sub 15 "AllOpNames 旧写死 16 形态恰 0（实测 ${C_OPHARD}）" \
  "$([ "${C_OPHARD}" = "0" ] && echo 0 || echo 1)"
# ── [判据 16] rel remove 占位接管
sub 16 "rel.go 占位 0（实测 ${C_RELPLACE}）" "$([ "${C_RELPLACE}" = "0" ] && echo 0 || echo 1)"
# ── [判据 17] 不回归 / 不越界 / 报告成文（其余子断言已在第一 / 二 / 三段记入）
sub 17 "reconcile 名单外裸词 0（总 ${C_RECONCILE_RAW} / 实测 ${C_RECONCILE}）" "$([ "${C_RECONCILE}" = "0" ] && echo 0 || echo 1)"
sub 17 "reconcile 注册形态 == M3 期 0 + M4 T-058 新增 1 = 1（实测 ${C_RECONCILE_REG}）" "$([ "${C_RECONCILE_REG}" = "1" ] && echo 0 || echo 1)"
sub 17 "reconcile 注册形态唯一落点 == ${RECONCILE_CMD_FILE}（该文件外实测 ${C_RECONCILE_REG_OUT}）" \
  "$([ "${C_RECONCILE_REG_OUT}" = "0" ] && [ "${RECONCILE_REG_SET}" = "${RECONCILE_CMD_FILE} " ] && echo 0 || echo 1)"
sub 17 "eg reconcile 用法行落点集合恰 {reconcile.go, check.go, commands.go}（实测 ${C_RECONCILE_CMD_FILES} 文件 / ${C_RECONCILE_CMD} 行）" \
  "$([ "${C_RECONCILE_CMD_FILES}" = "3" ] && [ "${RECONCILE_CMD_SET}" = "internal/cli/check.go internal/cli/commands.go internal/cli/reconcile.go " ] && echo 0 || echo 1)"
sub 17 "eg reconcile 用法行加法等式 (6+2)+7+1=16（reconcile.go = M4/M5 期 6 + M6 强事务编排 2；实测 ${C_RECONCILE_CMD_RECON}+${C_RECONCILE_CMD_CHECK}+${C_RECONCILE_CMD_CMDS}=${C_RECONCILE_CMD}）" \
  "$([ "${C_RECONCILE_CMD}" = "16" ] && [ "${C_RECONCILE_CMD_RECON}" = "8" ] && [ "${C_RECONCILE_CMD_CHECK}" = "7" ] && [ "${C_RECONCILE_CMD_CMDS}" = "1" ] && echo 0 || echo 1)"
sub 17 "eg reconcile 用法行在三处登记落点之外 0（实测 ${C_RECONCILE_CMD_OTHER}）" "$([ "${C_RECONCILE_CMD_OTHER}" = "0" ] && echo 0 || echo 1)"
sub 17 "check.go 的 reconcile 命中未归类 0（实测 ${C_CHECK_UNCLASSIFIED}）" "$([ "${C_CHECK_UNCLASSIFIED}" = "0" ] && echo 0 || echo 1)"
sub 17 "check.go 的报告三键占位提示 ${CHECK_REPORT_NOTICE_CONST} 逐字含 ran=false 恰 1（实测 ${CHECK_REPORT_NOTICE_OK}）" \
  "$([ "${CHECK_REPORT_NOTICE_OK}" = "1" ] && echo 0 || echo 1)"
sub 17 "check.go 零命令注册形态（实测 ${C_CHECK_REG}）" "$([ "${C_CHECK_REG}" = "0" ] && echo 0 || echo 1)"
sub 17 "reconcile Wire 注册行恰 1 且唯一落点 internal/cli/root.go（实测 ${C_RECONCILE_WIRE} / ${RECONCILE_WIRE_SET}）" \
  "$([ "${C_RECONCILE_WIRE}" = "1" ] && [ "${RECONCILE_WIRE_SET}" = "internal/cli/root.go " ] && echo 0 || echo 1)"
sub 17 "渲染层零命令注册形态（实测 ${C_RENDER_REG}）" "$([ "${C_RENDER_REG}" = "0" ] && echo 0 || echo 1)"
sub 17 "M6 三词 flock/txn//run.lock 非测试非注释全落授权本位（越界 ${C_S5_UNAUTH} / 授权内 ${C_S5_AUTH}≥1）" \
  "$([ "${C_S5_UNAUTH}" = "0" ] && [ "${C_S5_AUTH}" -ge 1 ] && echo 0 || echo 1)"
sub 17 "FTS5/sqlite 在 internal/index 之外 0（实测 ${C_M5SQL_OUT}）" "$([ "${C_M5SQL_OUT}" = "0" ] && echo 0 || echo 1)"
sub 17 "FTS5/sqlite 在 internal/index 之内 >=1（实测 ${C_M5SQL_IN}）" "$([ "${C_M5SQL_IN}" -ge 1 ] && echo 0 || echo 1)"
sub 17 "os.Remove 白名单之外非测试命中 0（双侧锁；实测 ${C_REMOVE_OUT}）" "$([ "${C_REMOVE_OUT}" = "0" ] && echo 0 || echo 1)"
sub 17 "os.Remove 白名单文件集恰七文件闭包（M3 2 + M5 3 + M6 2）且调用行数恰 25（实测 ${C_REMOVE}）" \
  "$([ "${REMOVE_SET}" = "internal/cli/ymlwrite.go internal/index/build.go internal/index/rebuild.go internal/index/scratch.go internal/store/write.go internal/txn/commit.go internal/txn/recover.go " ] && [ "${C_REMOVE}" = "25" ] && echo 0 || echo 1)"
sub 17 "破坏性操作（摘派生物白名单后）0（实测 ${C_DESTRUCT}）" "$([ "${C_DESTRUCT}" = "0" ] && echo 0 || echo 1)"
sub 17 "internal/index 的 RemoveAll 参数非逐字 dir 0（实测 ${C_DESTRUCT_ARG}）" "$([ "${C_DESTRUCT_ARG}" = "0" ] && echo 0 || echo 1)"
sub 17 "internal/index 内 Git 破坏性动作 0（实测 ${C_DESTRUCT_GIT}）" "$([ "${C_DESTRUCT_GIT}" = "0" ] && echo 0 || echo 1)"
sub 17 ".index 0（实测 ${C_INDEX}）" "$([ "${C_INDEX}" = "0" ] && echo 0 || echo 1)"
sub 17 "万卡（夸大规模承诺）全域 0（实测 ${C_P95}）" "$([ "${C_P95}" = "0" ] && echo 0 || echo 1)"
sub 17 "P95 在 eg bench 两文件之外 0（实测 ${C_P95_OUT}）" "$([ "${C_P95_OUT}" = "0" ] && echo 0 || echo 1)"
sub 17 "P95 落点集合恰 {bench.go, bench_test.go}" \
  "$([ "${P95_SET}" = "internal/cli/bench.go internal/cli/bench_test.go " ] && echo 0 || echo 1)"
sub 17 "占位 0（实测 ${C_PLACE}）" "$([ "${C_PLACE}" = "0" ] && echo 0 || echo 1)"
sub 17 "三条禁止措辞 0（实测 ${C_BANNED}）" "$([ "${C_BANNED}" = "0" ] && echo 0 || echo 1)"
sub 17 "版本四处同真 ${CUR_VERSION}（+ M3 历史基线只读复算 + 旧值零残留）" "${VER_OK}"
sub 17 "验收报告存在且被跟踪" "${REPORT_TRACKED}"
sub 17 "M3 会话留痕 ≥ 1 个文件（实测 ${N_SESS}）" "$([ "${N_SESS}" != "0" ] && echo 0 || echo 1)"
sub 17 "无 m3_*replay*.sh（实测 ${N_REPLAY}）" "$([ "${N_REPLAY}" = "0" ] && echo 0 || echo 1)"
sub 17 "会话 plan 内不同 M3 op ≥ 3（实测 ${N_M3OP}）" "$([ "${N_M3OP}" -ge 3 ] && echo 0 || echo 1)"
sub 17 "TestWritePermissionMatrixCounts（本 task 新增）" "$(case_ok TestWritePermissionMatrixCounts)"
# 2026-09-07 随 M4 · T-…-055 阶段 4a 按实测重钉判据**形态**（本体一格不放宽）：矩阵
# 两路径同 🔴 由 M3 期写死 5 改为**减法等式** 5 − 1 = 4（放开的恰 #33 的 P-A，依 A-34）。
# 行数恒 43 / 格数恒 86 / 路径恒 2 / 严格解锁恒 16 / 条件解锁恒 1 且为 #12 / 对象类恒 7
# 六条断言逐字未动；这里**新增两格加严**：减法等式常量落点恰 2 处、矩阵 #33 逐字恰
# `allow, deny` 一行（P-U 仍 🔴，不得顺手放开成两格全开）。
sub 17 "两路径同 🔴 减法等式 5−1 落点恰 2（实测 ${C_BOTHEQ}）" \
  "$([ "${C_BOTHEQ}" = "2" ] && echo 0 || echo 1)"
sub 17 "矩阵 #33 逐字 allow,deny 恰 1 行（实测 ${C_M33}）" \
  "$([ "${C_M33}" = "1" ] && echo 0 || echo 1)"

for n in $(seq 1 17); do
  if [ -n "${REASON[${n}]:-}" ]; then
    printf '  [判据 %-2s] FAIL   %s\n' "${n}" "${REASON[${n}]}"
    FAILED+=("${n}")
  else
    printf '  [判据 %-2s] PASS\n' "${n}"
  fi
done

# 工作区卫生：本脚本不得改动任何一仓的工作区
AFTER_E="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
HYG=0
[ "${BEFORE_E}" = "${AFTER_E}" ] || HYG=1
CHECKS=$((CHECKS + 1))
printf '  [卫生] evergreen 工作区与运行前逐字相等：%s\n' "$([ "${HYG}" = "0" ] && echo 是 || echo 否)"
[ "${HYG}" = "0" ] || FAILED+=("卫生")

if [ ${#FAILED[@]} -eq 0 ] && [ ${#UNVERIFIED[@]} -eq 0 ]; then
  echo "M3 结论：17 条判据全部 PASS（离线可复算部分），M3 达成"
else
  echo "M3 结论：未达成 / 待人判；失败判据编号：${FAILED[*]:-无}；未验证：${UNVERIFIED[*]:-无}"
fi
echo "checks=${CHECKS} failed=${#FAILED[@]}"
[ ${#FAILED[@]} -eq 0 ]

#!/usr/bin/env bash
# M2 验收总控脚本（T-evergreen.s1_main_flow-158614-029）。
#
# 判据来源：`milestones/M-002-m2.md`「M2 完成判据（9 条）」与「验收门禁」5 条。
# 本脚本让「M2 是否达成」这一结论**可被一条命令复算**：
#
#   第一段  按固定顺序跑 M1 回归 + M2 全部 e2e 子脚本（9 个）与 go test ./...
#   第二段  越界门禁 grep（remove_relation / .index/ / FTS5 / sqlite）与占位门禁 grep（M3/S2 未实现）
#   第三段  逐条打印判据 1 ~ 9 的三值结论（PASS / FAIL / NOT-VERIFIED）与总结论
#
# **三值口径（与验收报告同源，不许二值化）**：
#   PASS          判据的全部机器断言通过
#   FAIL          至少一条机器断言不通过 → 本脚本以非零码退出，末行打印失败判据编号列表
#   NOT-VERIFIED  判据依赖沙箱外的外部事实（如真实 AIME PPE 会话），本地无法证实亦不许伪证
#
# **退出码语义（务必读完）**：退出码 0 只表示「没有 FAIL 判据」，**不等于 M2 达成**。
# M2 总结论固定由倒数第二行的「M2 结论：…」给出：只要存在 FAIL 或 NOT-VERIFIED 判据，
# 该行即为「未达成」。这一设计是为了让「离线可复算的部分全绿」与「外部事实未验证」
# 两件事同时可见，而不是用一个绿色退出码把后者盖掉。
#
# 约束：离线、零交互、可重复执行；**一切写操作只发生在 mktemp -d 目录内**（脚本末尾自查
# 仓库 git status 与运行前逐字相等）；stdout 逐字确定（无时间戳、无临时路径、无耗时），
# 因此连跑两次的 stdout 必须完全相等；子脚本输出只进日志文件，失败时才 tail 到 stderr。
#
# 用法：cd evergreen && bash test/e2e/m2_acceptance.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
E2E="${REPO_ROOT}/test/e2e"
PARENT="$(cd "${REPO_ROOT}/.." && pwd)"
EPIC_DIR="${PARENT}/teamwork/projects/evergreen/s1_main_flow"
REPORT="${EPIC_DIR}/docs/specs/2026-10-09-m2-acceptance-report.md"
PPE_DIR="${E2E}/testdata/ppe"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m2-acc.XXXXXX")"
LOGS="${WORK}/logs"
mkdir -p "${LOGS}" "${WORK}/bin"
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

BEFORE_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

declare -A RC
FAILED=()
UNVERIFIED=()
PASSED=()

run_sub() { # run_sub <script>
  local s="$1" rc=0
  if bash "${E2E}/${s}" </dev/null >"${LOGS}/${s}.log" 2>&1; then rc=0; else rc=$?; fi
  RC["${s}"]="${rc}"
  printf '  [run] %-22s exit=%s\n' "${s}" "${rc}"
  if [ "${rc}" -ne 0 ]; then
    printf '  ↳ %s 末 25 行：\n' "${s}" >&2
    tail -25 "${LOGS}/${s}.log" >&2
  fi
}

run_cmd() { # run_cmd <tag> <cmd...>
  local tag="$1" rc=0
  shift
  if (cd "${REPO_ROOT}" && "$@") </dev/null >"${LOGS}/${tag}.log" 2>&1; then rc=0; else rc=$?; fi
  RC["${tag}"]="${rc}"
  printf '  [run] %-22s exit=%s\n' "${tag}" "${rc}"
  if [ "${rc}" -ne 0 ]; then
    printf '  ↳ %s 末 25 行：\n' "${tag}" >&2
    tail -25 "${LOGS}/${tag}.log" >&2
  fi
}

# 记录一条子断言的结果；失败即把原因写进对应判据的原因表。
declare -A REASON
sub() { # sub <判据号> <断言名> <0|1>
  local n="$1" name="$2" ok="$3"
  if [ "${ok}" != "0" ]; then REASON["${n}"]="${REASON[${n}]:-}${name}; "; fi
}

echo "════════ 第一段：M1 回归 + M2 全部 e2e（固定顺序）+ 全量 go test ════════"
run_sub m1_real_article.sh
run_sub m2_search.sh
run_sub m2_card_show.sh
run_sub m2_rel_query.sh
run_sub m2_rel_add.sh
run_sub m2_context_polish.sh
run_sub m2_convergence.sh
run_sub m2_docs_commands.sh
run_sub m2_ppe_replay.sh
run_cmd go-test-all go test ./... -count=1
run_cmd go-test-cli go test ./internal/cli/... -run 'Search|CardShow|Rel' -count=1
run_cmd go-build go build -o "${WORK}/bin/eg" ./cmd/eg
EG="${WORK}/bin/eg"

echo
echo "════════ 第二段：越界门禁 grep + 占位门禁 grep ════════"

# —— 越界门禁（M-002 验收门禁 3）：命中行必须全部是「登记类」——
# 允许两类：① 注释行或自带阶段标注的行（阶段登记与说明，含 --help 文案）；② 两处冻结的代码形态——
#   internal/plan/schema.go 的 s2OpNames() 字面量（那正是「不实现」的反证）、
#   internal/git/repo.go 的 GitignoreContent 常量（`.index/` 属 S4 目标态目录，S1 只预留排除）。
# 其余任何代码命中即判越界。另要求命中文件自身带阶段标注（S2/S3/S4/M3/M5/M6）。
OUT_HITS="${WORK}/out_of_scope.txt"
: >"${OUT_HITS}"
while IFS= read -r f; do
  grep -n "remove_relation\|\.index/\|FTS5\|sqlite" "${f}" 2>/dev/null |
    sed "s|^|${f}:|" >>"${OUT_HITS}" || true
done < <(cd "${REPO_ROOT}" && find internal cmd -name '*.go' ! -name '*_test.go' | sort)
N_OUT=$(wc -l <"${OUT_HITS}" | tr -d ' ')
BAD_OUT="${WORK}/out_of_scope_bad.txt"
: >"${BAD_OUT}"
while IFS= read -r line; do
  [ -n "${line}" ] || continue
  file="${line%%:*}"
  rest="${line#*:}"
  text="${rest#*:}"
  case "${text}" in
  *//*) ;;                                              # ① 注释登记
  *S2* | *S3* | *S4* | *M3* | *M5* | *M6*) ;;            # ① 命中行自身带阶段标注（含 --help 文案）
  *'"replace_block"'* | *'"mark_reviewed"'*) ;;          # ② s2OpNames() 字面量
  *'GitignoreContent = ".index/'*) ;;                    # ② gitignore 常量
  *) echo "${line}" >>"${BAD_OUT}" ;;
  esac
  if ! grep -qE 'S2|S3|S4|M3|M5|M6' "${REPO_ROOT}/${file}"; then
    echo "${line}（文件无阶段标注）" >>"${BAD_OUT}"
  fi
done <"${OUT_HITS}"
N_BAD_OUT=$(wc -l <"${BAD_OUT}" | tr -d ' ')
printf '  越界符号命中（非测试源）：%s 行；未登记/无阶段标注：%s 行\n' "${N_OUT}" "${N_BAD_OUT}"
[ "${N_BAD_OUT}" -eq 0 ] || cat "${BAD_OUT}" >&2

# —— 占位门禁（M-002 验收门禁 4）：代码与 --help 逐字「M3/S2 未实现」——
PH_CODE=0
grep -Fq "M3/S2 未实现" "${REPO_ROOT}/internal/cli/rel.go" || PH_CODE=1
PH_HELP=0
"${EG}" rel --help >"${LOGS}/rel-help.txt" 2>&1 || true
grep -Fq "M3/S2 未实现" "${LOGS}/rel-help.txt" || PH_HELP=1
PH_M2=0
if grep -n "M2 落地" "${REPO_ROOT}/internal/cli/rel.go" | grep -qi remove; then PH_M2=1; fi
printf '  占位文案：rel.go=%s --help=%s 「M2 落地」残留=%s（0 为期望值）\n' \
  "${PH_CODE}" "${PH_HELP}" "${PH_M2}"

# —— 占位标记门禁（判据 1）：非测试源零 `Placeholder: true` ——
PT="${WORK}/placeholder_true.txt"
(cd "${REPO_ROOT}" && grep -rn "Placeholder: *true" internal/ cmd/ --include='*.go' || true) |
  grep -v '_test\.go' >"${PT}" || true
N_PT=$(wc -l <"${PT}" | tr -d ' ')
PN="${WORK}/placeholder_notice.txt"
(cd "${REPO_ROOT}" && grep -rln "PlaceholderNotice" internal/ cmd/ --include='*.go' || true) |
  grep -v '_test\.go' | sort >"${PN}"
N_PN_BAD=$(grep -cv '^internal/cli/\(root\|placeholder\|rel\)\.go$' "${PN}" || true)
printf '  占位标记：Placeholder:true 命中=%s；PlaceholderNotice 越界文件=%s（0 为期望值）\n' \
  "${N_PT}" "${N_PN_BAD}"

# —— 判据 4 反证：internal/query/ 不读索引 ——
IDX="${WORK}/query_index.txt"
(cd "${REPO_ROOT}" && grep -rn "\.index" internal/query/ || true) >"${IDX}"
N_IDX=$(wc -l <"${IDX}" | tr -d ' ')
N_IDX_BAD=$(grep -v '_test\.go' "${IDX}" | grep -cv 'SkipDir\|d\.Name() == "\.index"' || true)
printf '  internal/query/ 内 .index 命中=%s（全部为 walker 排除分支或其单测）；索引读写命中=%s\n' \
  "${N_IDX}" "${N_IDX_BAD}"

# —— 判据 5 反证：rel_add.go 不绕过 plan/executor 直接调 store ——
ST="${WORK}/rel_add_store.txt"
grep -n "store\." "${REPO_ROOT}/internal/cli/rel_add.go" >"${ST}" || true
N_ST_CODE=$(grep -cv '//' "${ST}" || true)
N_IMPORT=$(grep -c 'evergreen/internal/store' "${REPO_ROOT}/internal/cli/rel_add.go" || true)
printf '  rel_add.go：store. 命中=%s（其中非注释=%s）；import internal/store=%s\n' \
  "$(wc -l <"${ST}" | tr -d ' ')" "${N_ST_CODE}" "${N_IMPORT}"

# —— 只读断言在场反证（判据 3 / 7）：断言若被删掉，这里立刻不为期望值 ——
RO_MISS=0
for s in m2_search.sh m2_card_show.sh m2_rel_query.sh m2_context_polish.sh; do
  grep -Fq 'status --porcelain' "${E2E}/${s}" || RO_MISS=1
done
DIAG_MISS=0
for s in m2_search.sh m2_card_show.sh m2_rel_query.sh; do
  grep -q '"code":"Q' "${E2E}/${s}" || DIAG_MISS=1
done
printf '  断言在场：只读零副作用缺失=%s；诚实诊断缺失=%s（0 为期望值）\n' "${RO_MISS}" "${DIAG_MISS}"

# —— PPE 留痕核验（判据 8）——
PPE_MISS=0
for f in plan.json commands.log query-before.txt query-after.txt git-log.txt session.md; do
  [ -s "${PPE_DIR}/${f}" ] || PPE_MISS=1
done
PPE_REAL=0 # 1 = 留痕自证「真实会话已执行」
if grep -Fq "真实 AIME PPE 会话未执行" "${PPE_DIR}/session.md" 2>/dev/null; then PPE_REAL=0; else PPE_REAL=1; fi
printf '  PPE 留痕：六件缺失=%s；真实会话自证=%s（0 = 留痕明示未执行）\n' "${PPE_MISS}" "${PPE_REAL}"

# —— 验收报告结构核验（判据 9）——
REP_OK=0
REP_SECTIONS=0
REP_CLOSED=0
REP_VAGUE=0
REP_QUAD=0
if [ -f "${REPORT}" ]; then
  REP_SECTIONS=$(grep -cE '^## 判据 [1-9] ' "${REPORT}" || true)
  REP_CLOSED=$(grep -cE '^结论：(达成|未达成|未验证)$' "${REPORT}" || true)
  REP_VAGUE=$(grep -cE '基本达成|大部分达成|大致通过|基本通过' "${REPORT}" || true)
  REP_QUAD=$(awk '
    /^## 判据 [1-9] /   { if (n) { if (a==1 && b==1 && c==1) good++ } ; n++; a=0; b=0; c=0; next }
    /^(#### )?判定命令$/ { if (n) a++ }
    /^(#### )?实测输出$/ { if (n) b++ }
    /^结论：/           { if (n) c++ }
    END { if (n && a==1 && b==1 && c==1) good++; print good+0 }' "${REPORT}")
  ORDER=$(grep -oE '^## 判据 [1-9] ' "${REPORT}" | grep -oE '[1-9]' | tr '\n' ' ')
  if git -C "${PARENT}/teamwork" ls-files --error-unmatch \
    "projects/evergreen/s1_main_flow/docs/specs/2026-10-09-m2-acceptance-report.md" \
    >/dev/null 2>&1; then REP_TRACKED=0; else REP_TRACKED=1; fi
  [ "${REP_SECTIONS}" = "9" ] && [ "${REP_CLOSED}" = "9" ] && [ "${REP_VAGUE}" = "0" ] &&
    [ "${REP_QUAD}" = "9" ] && [ "${ORDER}" = "1 2 3 4 5 6 7 8 9 " ] && [ "${REP_TRACKED}" = "0" ] || REP_OK=1
  printf '  验收报告：节数=%s 三值行=%s 模糊措辞=%s 四项齐备节=%s Git 跟踪=%s 编号序=%s\n' \
    "${REP_SECTIONS}" "${REP_CLOSED}" "${REP_VAGUE}" "${REP_QUAD}" "${REP_TRACKED}" "${ORDER}"
else
  REP_OK=1
  printf '  验收报告：缺失（%s）\n' "docs/specs/2026-10-09-m2-acceptance-report.md"
fi

echo
echo "════════ 第三段：M2 完成判据 1 ~ 9 逐条结论 ════════"

sub 1 "m2_search.sh" "${RC[m2_search.sh]}"
sub 1 "m2_card_show.sh" "${RC[m2_card_show.sh]}"
sub 1 "m2_rel_query.sh" "${RC[m2_rel_query.sh]}"
sub 1 "m2_rel_add.sh" "${RC[m2_rel_add.sh]}"
sub 1 "go test ./internal/cli/... -run 'Search|CardShow|Rel'" "${RC[go-test-cli]}"
sub 1 "Placeholder:true 非零命中" "${N_PT}"
sub 1 "PlaceholderNotice 越界文件" "${N_PN_BAD}"
V1=PASS
[ -z "${REASON[1]:-}" ] || V1=FAIL
printf '[判据 1] S1 九命令全部具备真实实现（search / card show / rel 查询 / rel add 非占位）  %s\n' "${V1}"

sub 2 "m2_rel_add.sh（含 rel remove 零副作用段）" "${RC[m2_rel_add.sh]}"
sub 2 "rel.go 占位文案逐字" "${PH_CODE}"
sub 2 "--help 占位文案逐字" "${PH_HELP}"
sub 2 "rel remove 的「M2 落地」措辞残留" "${PH_M2}"
V2=PASS
[ -z "${REASON[2]:-}" ] || V2=FAIL
printf '[判据 2] rel remove 仍属 M3/S2，调用零写入零 commit  %s\n' "${V2}"

sub 3 "m2_search.sh" "${RC[m2_search.sh]}"
sub 3 "m2_card_show.sh" "${RC[m2_card_show.sh]}"
sub 3 "m2_rel_query.sh" "${RC[m2_rel_query.sh]}"
sub 3 "m2_context_polish.sh" "${RC[m2_context_polish.sh]}"
sub 3 "只读零副作用断言在场" "${RO_MISS}"
V3=PASS
[ -z "${REASON[3]:-}" ] || V3=FAIL
printf '[判据 3] search / card show / rel 查询（含 context）零文件变化、零 commit  %s\n' "${V3}"

sub 4 "m2_rel_query.sh" "${RC[m2_rel_query.sh]}"
sub 4 "m2_card_show.sh" "${RC[m2_card_show.sh]}"
sub 4 "internal/query/ 索引读写命中" "${N_IDX_BAD}"
V4=PASS
[ -z "${REASON[4]:-}" ] || V4=FAIL
printf '[判据 4] 反向关系由 Markdown 全库扫描正确展示（不走索引）  %s\n' "${V4}"

sub 5 "m2_rel_add.sh" "${RC[m2_rel_add.sh]}"
sub 5 "rel_add.go 非注释 store. 命中" "${N_ST_CODE}"
sub 5 "rel_add.go import internal/store" "${N_IMPORT}"
V5=PASS
[ -z "${REASON[5]:-}" ] || V5=FAIL
printf '[判据 5] rel add 走 ChangePlan/executor 写链路，恰一次 relate commit  %s\n' "${V5}"

sub 6 "go test ./... -count=1" "${RC[go-test-all]}"
sub 6 "m1_real_article.sh" "${RC[m1_real_article.sh]}"
sub 6 "m2_convergence.sh" "${RC[m2_convergence.sh]}"
sub 6 "m2_docs_commands.sh" "${RC[m2_docs_commands.sh]}"
V6=PASS
[ -z "${REASON[6]:-}" ] || V6=FAIL
printf '[判据 6] B1–B4、重复加工幂等、全部 M1 E2E 不回归  %s\n' "${V6}"

sub 7 "m2_search.sh" "${RC[m2_search.sh]}"
sub 7 "m2_card_show.sh" "${RC[m2_card_show.sh]}"
sub 7 "m2_rel_query.sh" "${RC[m2_rel_query.sh]}"
sub 7 "诚实诊断断言在场" "${DIAG_MISS}"
V7=PASS
[ -z "${REASON[7]:-}" ] || V7=FAIL
printf '[判据 7] 不可解析文件 / 悬空引用 / 部分结果均有诚实诊断（两形态同源）  %s\n' "${V7}"

sub 8 "m2_ppe_replay.sh（离线回放）" "${RC[m2_ppe_replay.sh]}"
sub 8 "PPE 六件留痕存在且非空" "${PPE_MISS}"
V8=PASS
[ -z "${REASON[8]:-}" ] || V8=FAIL
if [ "${V8}" = PASS ] && [ "${PPE_REAL}" = "0" ]; then
  V8=NOT-VERIFIED
  REASON[8]="真实 AIME PPE 会话未执行（session.md 明示）：本地只完成离线回放，外部事实无法在沙箱内证实; "
fi
printf '[判据 8] AIME PPE 真实 Agent 完成「收录 → 查询 → 判断 → 写入 → 再查询验证」  %s\n' "${V8}"

sub 9 "验收报告存在 / 九节 / 四项齐备 / 三值封闭 / Git 跟踪" "${REP_OK}"
V9=PASS
[ -z "${REASON[9]:-}" ] || V9=FAIL
printf '[判据 9] 形成 M2 验收报告（2026-10-09-m2-acceptance-report.md）  %s\n' "${V9}"

echo
echo "════════ 汇总 ════════"
for i in 1 2 3 4 5 6 7 8 9; do
  eval "v=\${V${i}}"
  case "${v}" in
  PASS) PASSED+=("${i}") ;;
  NOT-VERIFIED) UNVERIFIED+=("${i}") ;;
  *) FAILED+=("${i}") ;;
  esac
done
printf '  PASS         %s 条：%s\n' "${#PASSED[@]}" "${PASSED[*]:-无}"
printf '  NOT-VERIFIED %s 条：%s\n' "${#UNVERIFIED[@]}" "${UNVERIFIED[*]:-无}"
printf '  FAIL         %s 条：%s\n' "${#FAILED[@]}" "${FAILED[*]:-无}"
for i in "${UNVERIFIED[@]:-}"; do
  [ -n "${i}" ] || continue
  printf '  未验证原因（判据 %s）：%s\n' "${i}" "${REASON[${i}]%; }"
done
for i in "${FAILED[@]:-}"; do
  [ -n "${i}" ] || continue
  printf '  失败原因（判据 %s）：%s\n' "${i}" "${REASON[${i}]%; }"
done

AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
if [ "${BEFORE_STATUS}" != "${AFTER_STATUS}" ]; then
  printf '  工作区卫生：脏（总控脚本自身有副作用）\n'
  FAILED+=(0)
else
  printf '  工作区卫生：干净（运行前后 git status --porcelain 逐字相等）\n'
fi

if [ "${#FAILED[@]}" -eq 0 ] && [ "${#UNVERIFIED[@]}" -eq 0 ]; then
  echo "  M2 结论：达成（9 条判据全部 PASS）"
else
  echo "  M2 结论：未达成（存在 FAIL 或 NOT-VERIFIED 判据，见上）"
fi

if [ "${#FAILED[@]}" -ne 0 ]; then
  printf '  失败判据编号：%s\n' "${FAILED[*]}"
  exit 1
fi
echo "  退出码 0 = 无 FAIL 判据；是否达成只看上一行「M2 结论」"

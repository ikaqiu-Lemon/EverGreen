#!/usr/bin/env bash
# M6 文档 / 命令面 / 版本一致性端到端脚本（M6 · T-evergreen.s1_main_flow-158614-075）。
#
# 判据来源：本 task deliverables「evergreen/tests/e2e/docs-cli/docs_cli_current_state_and_limits.sh」——
#   「README / INSTALL / SKILL.md 中出现的命令与实际命令集合（仍恰 22 个）逐条对齐，版本号四处同真，
#     内嵌 SKILL.md 与 skill/SKILL.md 逐字相等，三项已知限制在 README / INSTALL 中在册」；
#   milestone M-006 完成判据 16（版本 0.6.0-m6 多处同真 + 静态构建保全，本脚本承载其文档 / 命令 / 版本面）。
#
# 与既有文档门禁的分工（互不覆盖、只增不改）：
#   m2/m3/m4/m5_docs_commands.sh 各守其里程碑增量；本脚本**只守 M6 增量**：命令数仍恰 22（M6 不新增命令），
#   版本推进到 0.6.0-m6，并把 M5 期的「越界反证」（M6 术语只许出现在否定 / 未做 / 属 M6 语境）**现态重钉**为
#   **正面在场断言**——M6·T-070~074 已把强原子事务 / 锁 / 事务日志 / 崩溃恢复 / 块级安全合并 / 写前强校验 /
#   退出码 5 全部落地，三份文档必须把它们写成**已落地的 M6 能力**，且退出码 5 现态**绝不再**标「未启用」。
#     ① `eg --help` 顶层命令名集合恰 22 个，与收口清单**逐字双向恰等**（多一个 / 少一个都红）；
#     ② 三份文档里出现的 `eg <名>` 形态命令名集合**恰等于**上面这 22 个（方向 A 文档 ⊆ 实际、方向 B 实际 ⊆ 文档）；
#     ③ 版本号**五处**同真：version.go 字面量 == `make print-version` == `eg --version` == README == INSTALL，
#        且 M6 发布口径决策文档逐字声明同一个值（第五处 = 唯一决策出处）；上一里程碑版本 0.5.0-m5 零残留；
#     ④ 内嵌 SKILL.md（`eg init` 落盘副本）与 `skill/SKILL.md` 逐字相等（go:embed 未过期）；
#     ⑤ M6 能力面在文档中逐条**正面在场**：run.lock / 事务日志(`.index/txn/`) / 崩溃恢复(W26) /
#        块级安全合并(W27) / 并发冲突(W28) / `--strict` 写前强校验 / 退出码 5（E15 / E16）；
#        新增诊断码恰 5 条 `E15` `E16` `W26` `W27` `W28` 在三份文档在册；`W21` 仍不分配；
#     ⑥ 退出码 5 现态**绝不再**声称「未启用」（M6·T-074 已启用），且 INSTALL 退出码 5 行含 `E15` / `E16` 两类成因；
#     ⑦ **三项已知限制**在 README + INSTALL 在册：macOS 未真机验证 / 网络盘 · 同步盘不保证原子性 /
#        存在未闭合事务时不可删 `.index/`。
#
# 约束：离线、可重复、无外部依赖（bash / coreutils / git / make / go）；
#   **一切写与构建只发生在 mktemp -d 沙箱内**，真实仓库工作区零污染（脚本末尾自查 git status）。
#   全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/docs-cli/docs_cli_current_state_and_limits.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
# 唯一决策出处 = M6 发布口径文档（docs_test.go 的 docsReleaseSpec 指针目标）。
SPEC_REL="projects/evergreen/s1_main_flow/docs/specs/2027-02-21-m6-release-and-version.md"
DECISION_DOC="${EG_CONTRACTS}/${SPEC_REL}"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-docs.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

# 当期期望版本号（写死；唯一决策出处见 ${SPEC_REL} §1.1）。M6 现态：0.5.0-m5 → 0.6.0-m6。
WANT_VERSION="0.6.0-m6"
PREV_VERSION="0.5.0-m5"
# M6 收口值：M6 不新增顶层命令，故与 M5 收口清单逐字相同（历史锚点，恒不放宽）。
WANT_COMMANDS=22

# M6 收口的 22 条顶层命令名（与 M5 收口清单逐字相同；历史结论「M6 恰 22」由此逐条复算）。
CANON_CMDS=(
  init config capture context apply search card rel report
  deprecate restore replaced-by proposal delete undelete mark-reviewed unreviewed edit
  reconcile check index bench
)

# 读路径拆分批次（**post-M5 / post-M6**）在 M6 收口清单之上**追加**的顶层命令：观点子系统
# `eg opinion`。它不属于任何历史里程碑（M4=20 / M5=22 / M6=22 均在它之前收口），因此如实登记为
# 「后续新增项」并在复算历史结论时**摘掉**：M6 的历史结论「恰 22 条」一个字不放宽，当前真实顶层
# 命令集合抬为 23（= 22 + opinion）。追加项必须真在 `eg --help` 里（登记了没注册 = 名单造假）。
POST_M6_ADDED_CMDS=(opinion)
# 当前真实顶层命令集合（23 条）= M6 收口 22 + 读路径拆分批次追加的 opinion。
CURRENT_CMDS=("${CANON_CMDS[@]}" "${POST_M6_ADDED_CMDS[@]}")
WANT_COMMANDS_NOW=$((WANT_COMMANDS + ${#POST_M6_ADDED_CMDS[@]}))

# M6 新增诊断码（恰 5 条；W21 刻意不在其中：仍不分配）。
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

DOCS=("${REPO_ROOT}/README.md" "${REPO_ROOT}/INSTALL.md" "${REPO_ROOT}/skill/SKILL.md")

# ---------------------------------------------------------------- 1. 构建 eg（输出到沙箱，零污染）
step "从源码构建 eg 到沙箱（不落 bin/ dist/）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -trimpath \
  -ldflags "-X github.com/ikaqiu-Lemon/EverGreen/internal/version.Version=$(make -s print-version | tail -1 | tr -d ' ') -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Commit=e2e -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Date=e2e" \
  -o "${EG}" ./cmd/eg) || die "编译失败"
ok "eg 已构建：${EG}"

# ---------------------------------------------------------------- 2. 顶层命令集合与当前 23 条清单双向恰等
step "eg --help 顶层命令名集合与当前清单双向恰等（恰 ${WANT_COMMANDS_NOW} 条 = M6 收口 ${WANT_COMMANDS} + 读路径拆分追加 ${#POST_M6_ADDED_CMDS[@]}）"
ACTUAL="$("${EG}" --help </dev/null 2>/dev/null | awk '
  /^命令/ {inblk=1; next}
  /^全局 flag/ {inblk=0}
  inblk && /^  [a-z]/ {print $1}' | sort -u)"
WANT="$(printf '%s\n' "${CURRENT_CMDS[@]}" | sort -u)"
N_ACTUAL="$(printf '%s\n' "${ACTUAL}" | grep -c .)"
[ "${N_ACTUAL}" = "${WANT_COMMANDS_NOW}" ] || die "实际顶层命令数 = ${N_ACTUAL}，期望 ${WANT_COMMANDS_NOW}"
[ "$(printf '%s\n' "${CURRENT_CMDS[@]}" | sort -u | wc -l | tr -d ' ')" = "${WANT_COMMANDS_NOW}" ] \
  || die "CURRENT_CMDS 去重后不是 ${WANT_COMMANDS_NOW} 条（期望集合自身有重复 = 名单造假）"
if [ "${ACTUAL}" != "${WANT}" ]; then
  printf '  仅实际有：\n%s\n  仅清单有：\n%s\n' \
    "$(comm -23 <(printf '%s\n' "${ACTUAL}") <(printf '%s\n' "${WANT}"))" \
    "$(comm -13 <(printf '%s\n' "${ACTUAL}") <(printf '%s\n' "${WANT}"))" >&2
  die "顶层命令集合与当前清单不恰等"
fi
# 历史结论复算：摘掉读路径拆分批次追加的命令后，恰是 M6 收口清单（22 条）——历史结论不放宽、不篡改。
HIST="$(comm -23 <(printf '%s\n' "${ACTUAL}") <(printf '%s\n' "${POST_M6_ADDED_CMDS[@]}" | sort -u))"
HIST_WANT="$(printf '%s\n' "${CANON_CMDS[@]}" | sort -u)"
N_HIST="$(printf '%s\n' "${HIST}" | grep -c .)"
[ "${N_HIST}" = "${WANT_COMMANDS}" ] || die "摘掉读路径拆分追加后 = ${N_HIST}，期望 M6 收口值 ${WANT_COMMANDS}"
[ "${HIST}" = "${HIST_WANT}" ] || die "摘掉读路径拆分追加后与 M6 收口清单不恰等（历史结论被篡改）"
# 追加项必须真在 --help 命令区（登记了却没注册 = 名单造假）。
for c in "${POST_M6_ADDED_CMDS[@]}"; do
  printf '%s\n' "${ACTUAL}" | grep -qx "${c}" || die "读路径拆分登记的命令 ${c} 不在 --help 命令区"
done
ok "顶层命令集合 == 当前清单，恰 ${WANT_COMMANDS_NOW} 条（双向恰等）；摘掉 opinion 后 = ${WANT_COMMANDS}（M6 收口值，历史保真）"

# ---------------------------------------------------------------- 3. 四份文档各自与当前 23 条双向恰等 + 逐条覆盖 + 登记总数
step "README / README.zh-CN / INSTALL / SKILL 各自覆盖全部 ${WANT_COMMANDS_NOW} 条命令并登记当前总数（杜绝聚合互相补漏）"
ALL_DOCS=("${REPO_ROOT}/README.md" "${REPO_ROOT}/README.zh-CN.md" "${REPO_ROOT}/INSTALL.md" "${REPO_ROOT}/skill/SKILL.md")
for doc in "${ALL_DOCS[@]}"; do
  b="$(basename "${doc}")"
  # 方向 A（文档 ⊆ 实际）：本文档出现的 `eg <名>` 不得有实际不存在的命令（示范无效命令）。
  DSET="$(grep -ohE '\beg [a-z][a-z-]*' "${doc}" | awk '{print $2}' | sort -u)"
  BOGUS="$(comm -23 <(printf '%s\n' "${DSET}") <(printf '%s\n' "${WANT}"))"
  [ -z "${BOGUS}" ] || die "${b} 出现实际不存在的命令：$(printf '%s' "${BOGUS}" | tr '\n' ' ')"
  # 方向 B（实际 ⊆ 文档）：当前 23 条命令名逐条在**本文档自身**在场（不靠其它文档补漏；README.zh-CN 同判）。
  miss=0
  for c in "${CURRENT_CMDS[@]}"; do
    grep -qE "\beg ${c}\b" "${doc}" || { printf '  MISSING in %s: eg %s\n' "${b}" "${c}"; miss=$((miss + 1)); }
  done
  [ "${miss}" = "0" ] || die "${b} 缺 ${miss} 条当前命令名（每份文档都须自证覆盖全部 ${WANT_COMMANDS_NOW} 条）"
  # 当前总数在命令语境里被登记（防被日期 / 章节号蒙对）。
  grep -qE "命令.*${WANT_COMMANDS_NOW}|${WANT_COMMANDS_NOW}[^0-9].*命令" "${doc}" \
    || die "${b} 未在命令语境里登记当前顶层命令总数 ${WANT_COMMANDS_NOW}"
done
ok "四份文档各自覆盖全部 ${WANT_COMMANDS_NOW} 条命令、无实际不存在命令、均登记当前总数 ${WANT_COMMANDS_NOW}"

# ---------------------------------------------------------------- 3b. Opinion 现态防回退：validate/reject 生命周期已随 T-007 落地
# 精确锁定 Opinion 语境（引用 opinion / 观点 的行 + 源码构建的 eg opinion --help）——不对全文件泛搜「骨架」
# 二字（vault 骨架等其它语境合法）。历史里程碑基线（M4=20 / M5=22 / M6=22）不在本步触碰。
step "Opinion 现态：三份口径文档与 eg opinion --help 不得再声称 validate/reject 是骨架 / 未挂载 / 未实现"
# 过时骨架措辞黑名单（中英双语）：出现在任何引用 opinion/观点 的行即判回退。
OPINION_STALE_RE='骨架|未挂载|未实现|尚未落地|尚未挂载|后续落地|命令表面|行为尚未|随 T-007 落地|随后续行为批次落地|skeleton|unimplemented|not[[:space:]]*wired|NotWired|lands in later|later batch'
OPINION_DOCS=("${REPO_ROOT}/README.md" "${REPO_ROOT}/README.zh-CN.md" "${REPO_ROOT}/skill/SKILL.md")
for doc in "${OPINION_DOCS[@]}"; do
  b="$(basename "${doc}")"
  while IFS= read -r ln; do
    if grep -qE "${OPINION_STALE_RE}" <<<"${ln}"; then
      die "${b} 的 Opinion 行仍含过时骨架措辞（validate/reject 已随 T-007 落地）：${ln}"
    fi
  done < <(grep -nE 'opinion|观点' "${doc}" || true)
done
# 源码构建的 eg opinion --help 与顶层 opinion 摘要行同样不得残留骨架措辞。
OPINION_HELP="$("${EG}" opinion --help </dev/null 2>&1 || true)"
if grep -qE "${OPINION_STALE_RE}" <<<"${OPINION_HELP}"; then
  die "eg opinion --help 仍含过时骨架措辞（validate/reject 已随 T-007 落地）"
fi
TOPLINE="$("${EG}" --help </dev/null 2>/dev/null | grep -E '^  opinion ' || true)"
if grep -qE "${OPINION_STALE_RE}" <<<"${TOPLINE}"; then
  die "eg --help 的 opinion 摘要行仍含过时骨架措辞：${TOPLINE}"
fi

# 现态正面在场：五条合法验证边 + 授权 / 复议 / 事务 / W29 口径，逐条锁在 eg opinion --help 与三份口径文档。
# 归一：去空白、把 ASCII 箭头 -> 统一成 →，兼容中英排版。归一化结果落文件后用**文件 grep**（规避
# set -o pipefail 下 printf 大字符串 | grep -q 早退触发 SIGPIPE / 141 的潜在脆弱性）。
onorm() { tr -d ' ' | sed 's/->/→/g'; }
NORMDIR="${WORK}/norm"; mkdir -p "${NORMDIR}"
LEGAL_EDGES=("pending→validated" "pending→rejected" "validated→rejected" "validated→pending" "rejected→pending")
HELP_NORM_F="${NORMDIR}/opinion_help.norm"
onorm <<<"${OPINION_HELP}" > "${HELP_NORM_F}"
for e in "${LEGAL_EDGES[@]}"; do
  grep -qF "${e}" "${HELP_NORM_F}" || die "eg opinion --help 缺合法验证边 ${e}（现态五边须逐条在场）"
done
# rejected→validated 是**非法**直跳：help 必须点明「须先 reopen 回 pending 再 validate」（正向锁定该规则在册）。
grep -qE "先.*reopen.*pending|reopen 回 pending|回 pending.*再.*validate" <<<"${OPINION_HELP}" \
  || die "eg opinion --help 未写明 rejected→validated 非法（须先 reopen 回 pending 再 validate）"
# 授权与参数现态：P-U / --user-request / E19；--reopen 仅 validate；成功单文件事务 + verb=process。
grep -qF -- "--user-request" <<<"${OPINION_HELP}" || die "eg opinion --help 未写明 --user-request（P-U 写路径佐证）"
grep -qF "E19" <<<"${OPINION_HELP}" || die "eg opinion --help 未写明缺 --user-request → E19"
grep -qE "仅[[:space:]]*validate|只.*validate|仅 validate" <<<"${OPINION_HELP}" || die "eg opinion --help 未写明 --reopen 仅 validate"
grep -qF "process" <<<"${OPINION_HELP}" || die "eg opinion --help 未写明成功提交 verb=process"
grep -qE "单文件|恰 1 个|恰一个|原子" <<<"${OPINION_HELP}" || die "eg opinion --help 未写明单文件原子事务"
# 三份口径文档（README / 中文 README / SKILL）承载完整用户与 Agent 现态口径：五边 + P-A 禁写 + W29。
for doc in "${OPINION_DOCS[@]}"; do
  b="$(basename "${doc}")"
  DOC_NORM_F="${NORMDIR}/${b}.norm"
  onorm < "${doc}" > "${DOC_NORM_F}"
  for e in "${LEGAL_EDGES[@]}"; do
    grep -qF "${e}" "${DOC_NORM_F}" || die "${b} 缺 Opinion 合法验证边 ${e}（现态五边须逐条在场）"
  done
  grep -qF -- "--user-request" "${doc}" || die "${b} 的 Opinion 口径未写明 --user-request"
  grep -qE "reopen" "${doc}" || die "${b} 的 Opinion 口径未写明 reopen 复议"
  grep -qE "opinion_unsupported_validated|W29" "${doc}" || die "${b} 的 Opinion 口径未写明 W29（validated 零有效 incoming supports）"
  # P-A / Agent 不得写 validation（现态硬约束，取代旧「命令表面、没有行为」表述）。
  # 双语：中文文档用「不得…validation/采纳/驳回/验证」，英文 README 用「Agent…must not/never/cannot…validat」。
  grep -qE "Agent.*不得.*(validation|采纳|驳回|验证)|P-A.*不得|不得.*代替用户.*(验证|采纳|驳回)|[Aa]gent.*(must not|never|cannot|may not).*validat|P-A.*(must not|never|cannot|may not)" "${doc}" \
    || die "${b} 的 Opinion 口径未写明 Agent/P-A 不得写 validation"
done
ok "Opinion 现态在场：五条合法验证边 + 授权 / --reopen / process 事务 / W29 / P-A 禁写；旧骨架措辞零残留"

# ---------------------------------------------------------------- 3b-bis. opinion 退出码 5 归因防回退（只对 eg opinion --help，不过拟合无关文档）
# 实现真值：opinionLifecycleCritical 的单文件原子域守卫走 blockedError(..., nil) → 兜底 E21，classifyExit5
#   不会把 E21 提升成 PrecheckFailedError，ExitCodeFor 最终 exit 1；且该函数从不调用 plan.Precheck / --strict。
#   因此 opinion 的退出码 5 只能归两大真实可达类：run.lock 不可用（E16）、enterTxnCritical 事务安全复核 /
#   恢复屏障 fail-closed（E15）。help **绝不**可把「单文件原子域（越界）」列作 E15/退 5 成因，也**绝不**可
#   宣称 opinion 执行 / 跑 plan 的 --strict 预检（正确的否定句「本命令不跑 --strict 预检」允许）。
step "opinion 退出码 5 归因防回退：单文件原子域不入 E15/exit5；不得宣称跑 --strict（仅对 eg opinion --help）"
HELP5="$(grep -E '^[[:space:]]*\| 5 ' <<<"${OPINION_HELP}" | head -1)"
[ -n "${HELP5}" ] || die "eg opinion --help 未找到退出码 5 行"
# ① exit-5 行须写真实可达大类：run.lock/E16 + enterTxnCritical 事务安全 / 恢复屏障（E15）。
grep -qE 'E16|run\.lock' <<<"${HELP5}" || die "opinion 退出码 5 行未写 run.lock / E16：${HELP5}"
grep -qF 'E15' <<<"${HELP5}" || die "opinion 退出码 5 行未写 E15：${HELP5}"
grep -qE '事务安全|恢复屏障|enterTxnCritical' <<<"${HELP5}" \
  || die "opinion 退出码 5 行未把 E15 归到 enterTxnCritical 事务安全 / 恢复屏障：${HELP5}"
# ② exit-5 行**不得**把「单文件原子域（越界）」错列为 E15/退 5 成因（真值：防御性 E21 → 退 1）。
grep -qE '单文件原子域|越界' <<<"${HELP5}" \
  && die "opinion 退出码 5 行把「单文件原子域越界」错误归入 E15/exit5（真值：防御性 E21 → 退 1）：${HELP5}"
# ③ exit-5 行**不得**把 E15 误写成 plan 的「(写前)强校验」（opinion 的 E15 源于事务安全 / 恢复屏障，非 strict 预检）。
grep -qE '强校验' <<<"${HELP5}" \
  && die "opinion 退出码 5 行把 E15 误写成「写前强校验」（opinion 不跑 plan strict 预检，E15 源于恢复屏障）：${HELP5}"
# ④ 全篇不得**正向**宣称 opinion 执行 --strict：凡含 --strict 的行必须是否定句（含「不」/never/cannot/does not）。
if grep -qF -- "--strict" <<<"${OPINION_HELP}"; then
  while IFS= read -r ln; do
    case "${ln}" in
      *--strict*)
        case "${ln}" in
          *不*--strict*|*never*--strict*|*cannot*--strict*|*"does not"*--strict*|*"not run"*--strict*) : ;;
          *) die "eg opinion --help 正向宣称执行 --strict 预检（opinion 不跑 plan 的 --strict）：${ln}" ;;
        esac ;;
    esac
  done <<<"${OPINION_HELP}"
fi
ok "opinion 退出码 5 只归 run.lock(E16) 与 enterTxnCritical 事务安全 / 恢复屏障(E15)；单文件原子域不入 E15/exit5；无 --strict 正向声称"

# ---------------------------------------------------------------- 3c. W29 supporter 真值五格逐项机械锁定 + 定向 mutation 反证
# 判据：W29（opinion_unsupported_validated）只算指向本观点的 **incoming** supports；supporter 必须存在且未删除；
#   deprecated 但未删除仍有效；本观点 outgoing supports 不计；同一 supporter 重复边去重。这五格必须在
#   README.md / README.zh-CN.md / skill/SKILL.md 与源码构建的 eg opinion --help **各自**逐项在场——**不许**只 grep
#   「W29」名称蒙混。断言在 3b 的归一化文件（去空白、-> → →）上跑，语言分域：README.md 走英文串，其余三目标走中文串。
step "W29 supporter 真值五格逐项锁定（四目标：README / README.zh-CN / SKILL / eg opinion --help），并做定向 mutation 反证"
# W29 真值散布在自动换行 + markdown 强调（** / `）里，故用**更强**归一化：额外去换行 / 反引号 / 星号，
# 再落文件 grep（与 3b 的行级 onorm 分开，避免影响既有边断言）。
w29norm() { tr -d '\n' | tr -d ' ' | tr -d '`*' | sed 's/->/→/g'; }
W29DIR="${WORK}/w29"; mkdir -p "${W29DIR}"
W29_EN_README="${W29DIR}/README.md.w29"
w29norm < "${REPO_ROOT}/README.md" > "${W29_EN_README}"
w29norm < "${REPO_ROOT}/README.zh-CN.md" > "${W29DIR}/README.zh-CN.md.w29"
w29norm < "${REPO_ROOT}/skill/SKILL.md"   > "${W29DIR}/SKILL.md.w29"
w29norm <<<"${OPINION_HELP}"              > "${W29DIR}/opinion_help.w29"
W29_ZH_TARGETS=("${W29DIR}/README.zh-CN.md.w29" "${W29DIR}/SKILL.md.w29" "${W29DIR}/opinion_help.w29")

# 五格机械断言（英文目标）：逐格锁定归一化后的定长子串，任何一格缺失即红。
assert_w29_en() {
  local f="$1" name="$2"
  grep -qF "pointingatthisopinion"                        "${f}" || die "${name}: W29 未锁定 incoming 方向（指向本观点）"
  grep -qF "outgoingsupportsdonot"                        "${f}" || die "${name}: W29 未锁定 outgoing 不计"
  grep -qF "existandbenot-deleted"                        "${f}" || die "${name}: W29 未锁定 supporter 存在且未删除"
  grep -qF "deletedsupportermakesthatsupportineffective"  "${f}" || die "${name}: W29 未锁定已删除 supporter 无效"
  grep -qF "deprecatedbutnotdeletedstillcounts"           "${f}" || die "${name}: W29 未锁定 deprecated 未删除仍有效"
  grep -qF "de-duplicatedandcountedonce"                  "${f}" || die "${name}: W29 未锁定重复 supporter 去重"
}
# 五格机械断言（中文目标）。
assert_w29_zh() {
  local f="$1" name="$2"
  grep -qF "指向本观点的incomingsupports"  "${f}" || die "${name}: W29 未锁定 incoming 方向（指向本观点）"
  grep -qF "outgoingsupports不计"          "${f}" || die "${name}: W29 未锁定 outgoing 不计"
  grep -qF "supporter必须存在且未删除"      "${f}" || die "${name}: W29 未锁定 supporter 存在且未删除"
  grep -qF "supporter已删除→该支持无效"     "${f}" || die "${name}: W29 未锁定已删除 supporter 无效"
  grep -qF "deprecated但未删除仍有效"       "${f}" || die "${name}: W29 未锁定 deprecated 未删除仍有效"
  grep -qF "重复supports边去重、只算一次"    "${f}" || die "${name}: W29 未锁定重复 supporter 去重"
}
assert_w29_en "${W29_EN_README}" "README.md"
for f in "${W29_ZH_TARGETS[@]}"; do assert_w29_zh "${f}" "$(basename "${f}")"; done

# 定向 mutation 反证：从每个目标的归一化副本删掉「已删除 supporter 无效」与「deprecated 未删除仍有效」两格，
#   证明对应断言必转红（若删掉后仍绿 = 断言只匹配 W29 名称、没真锁这两格 = 反证失败）。用 '#' 作 sed 分隔符，
#   目标子串不含 '#'；子串为定长字面量（无正则元字符），删除后与原文必须不同（否则说明本就没锁住 = 失败）。
mutate_del_two_cells() {  # $1 srcnorm $2 dst $3 del-cell-substr $4 dep-cell-substr
  sed -e "s#$3##g" -e "s#$4##g" "$1" > "$2"
  if cmp -s "$1" "$2"; then die "mutation 无效：$(basename "$1") 删两格后与原文无差异（本就未锁两格）"; fi
}
# EN README 反证。
MUT_EN="${NORMDIR}/README.md.mut"
mutate_del_two_cells "${W29_EN_README}" "${MUT_EN}" "deletedsupportermakesthatsupportineffective" "deprecatedbutnotdeletedstillcounts"
if grep -qF "deletedsupportermakesthatsupportineffective" "${MUT_EN}"; then die "反证失败：README.md 删「已删除 supporter 无效」格后仍匹配"; fi
if grep -qF "deprecatedbutnotdeletedstillcounts"          "${MUT_EN}"; then die "反证失败：README.md 删「deprecated 未删除仍有效」格后仍匹配"; fi
ok "反证：README.md 删两格后两条断言均转红（断言真锁在这两格，而非 W29 名称）"
# ZH 三目标反证。
for f in "${W29_ZH_TARGETS[@]}"; do
  b="$(basename "${f}")"
  mut="${NORMDIR}/${b}.mut"
  mutate_del_two_cells "${f}" "${mut}" "supporter已删除→该支持无效" "deprecated但未删除仍有效"
  if grep -qF "supporter已删除→该支持无效" "${mut}"; then die "反证失败：${b} 删「已删除 supporter 无效」格后仍匹配"; fi
  if grep -qF "deprecated但未删除仍有效"    "${mut}"; then die "反证失败：${b} 删「deprecated 未删除仍有效」格后仍匹配"; fi
done
ok "W29 五格在四目标逐项在场；定向 mutation 反证：删 deleted/deprecated 两格后对应断言均转红"

# ---------------------------------------------------------------- 4. 版本号五处同真
step "版本号五处同真：version.go == make print-version == eg --version == README == INSTALL == 决策文档（${WANT_VERSION}）"
GO_VER="$(grep -E 'DefaultVersion[[:space:]]*=' "${REPO_ROOT}/internal/version/version.go" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+[^" ]*' | head -1)"
MK_VER="$(cd "${REPO_ROOT}" && make -s print-version | tail -1 | tr -d ' ')"
CLI_VER="$("${EG}" --version </dev/null | awk '{print $2}')"
printf '  version.go=%s  make print-version=%s  eg --version=%s\n' "${GO_VER}" "${MK_VER}" "${CLI_VER}"
[ -n "${GO_VER}" ] && [ -n "${MK_VER}" ] && [ -n "${CLI_VER}" ] || die "版本号有一处抽取为空"
[ "${GO_VER}" = "${WANT_VERSION}" ] || die "version.go 版本号 = ${GO_VER}，期望 ${WANT_VERSION}"
[ "${MK_VER}" = "${WANT_VERSION}" ] || die "make print-version = ${MK_VER}，期望 ${WANT_VERSION}"
[ "${CLI_VER}" = "${WANT_VERSION}" ] || die "eg --version = ${CLI_VER}，期望 ${WANT_VERSION}"
grep -Fq "${WANT_VERSION}" "${REPO_ROOT}/README.md" || die "README 未登记版本号 ${WANT_VERSION}"
grep -Fq "${WANT_VERSION}" "${REPO_ROOT}/INSTALL.md" || die "INSTALL 未登记版本号 ${WANT_VERSION}"
[ -f "${DECISION_DOC}" ] || die "缺 M6 发布口径决策文档：teamwork/${SPEC_REL}"
DOC_VER="$(grep -oE '\*\*`[0-9]+\.[0-9]+\.[0-9]+[^`]*`\*\*' "${DECISION_DOC}" | head -1 | tr -d '*`')"
[ "${DOC_VER}" = "${WANT_VERSION}" ] || die "决策文档声明版本号 = ${DOC_VER}，期望 ${WANT_VERSION}"
grep -Fq "未知" "${DECISION_DOC}" || die "决策文档必须对「是否推送远端 / tag」标注「未知」"
# version.go / Makefile 内不得残留上一里程碑版本字面量（M-006 判据 16 第二格）。
PREV="$({ grep -F "${PREV_VERSION}" "${REPO_ROOT}/internal/version/version.go" "${REPO_ROOT}/Makefile" || true; } | wc -l | tr -d ' ')"
[ "${PREV}" = "0" ] || die "version.go / Makefile 仍残留 ${PREV_VERSION}（${PREV} 处）"
ok "五处版本号逐字一致：${WANT_VERSION}；${PREV_VERSION} 零残留；决策文档 tag/推送标「未知」"

# ---------------------------------------------------------------- 5. 内嵌 SKILL.md 与源文件同字节
step "eg init 落盘的 SKILL.md 与 skill/SKILL.md 逐字相等（go:embed 未过期）"
mkdir -p "${VAULT}"
"${EG}" --vault "${VAULT}" init --domain ai-infra </dev/null >/dev/null 2>&1 || die "eg init 失败"
[ -f "${VAULT}/SKILL.md" ] || die "eg init 未落盘 SKILL.md"
cmp -s "${VAULT}/SKILL.md" "${REPO_ROOT}/skill/SKILL.md" \
  || die "内嵌 SKILL.md（落盘副本）与源文件字节不一致——须重编译刷新 go:embed"
ok "内嵌 SKILL.md 与 skill/SKILL.md 逐字相等"

# ---------------------------------------------------------------- 6. M6 能力面在场（正面断言，现态重钉）
step "M6 能力面正面在场：run.lock / 事务日志 / 崩溃恢复 / 块级 / 并发 / --strict 写前强校验 / 退出码 5（E15/E16）"
# ① 五条 M6 能力术语在三份文档均**正面在场**（写成已落地能力，不再是否定 / 属 M6 未做语境）。
M6_LANDED_RE='M6|S5|启用|落地|已'
ctx2() { awk -v t="$2" 'index($0, t) > 0 { print prev " ⏎ " $0 } { prev = $0 }' "$1"; }
for doc in "${DOCS[@]}"; do
  for term in "run.lock" "事务日志" "崩溃恢复" "块级" "退出码 \`5\`"; do
    hit=0
    while IFS= read -r win; do
      grep -qE "${M6_LANDED_RE}" <<<"${win}" && hit=1
    done < <(ctx2 "${doc}" "${term}")
    [ "${hit}" = "1" ] || die "$(basename "${doc}") 未把 M6 术语「${term}」写成已落地的 M6 能力（现态需正面在场）"
  done
done
# ② 新增五诊断码逐条在三份文档在册。
for c in "${M6_CODES[@]}"; do
  for doc in "${DOCS[@]}"; do
    grep -qF "${c}" "${doc}" || die "$(basename "${doc}") 未登记 M6 诊断码 ${c}"
  done
done
# ③ --strict 写前强校验在场。
for doc in "${DOCS[@]}"; do
  grep -qF -- "--strict" "${doc}" || die "$(basename "${doc}") 未登记 --strict 强校验开关"
done
# ④ W21 仍不分配：若三份文档提到它，必须处在「不分配 / 留白 / 不使用」语境。
for doc in "${DOCS[@]}"; do
  while IFS= read -r line; do
    grep -qE "不分配|留白|不使用|未分配" <<<"${line}" \
      || die "$(basename "${doc}") 在非「不分配」语境提到 W21：${line}"
  done < <(grep -F "W21" "${doc}" || true)
done
ok "M6 能力面逐条正面在场（五术语 + 五诊断码 + --strict；W21 仍不分配）"

# ---------------------------------------------------------------- 7. 退出码 5 现态：绝不再标「未启用」
step "退出码 5 现态已启用：三份文档不得再声称「未启用」；INSTALL 退出码 5 行含 E15 / E16 两类成因"
for doc in "${DOCS[@]}"; do
  while IFS= read -r win; do
    if grep -qE "未启用|尚未启用|尚不启用|不启用" <<<"${win}"; then
      die "$(basename "${doc}") 仍声称退出码 5 未启用（M6·T-074 已启用，旧字样必须零残留）：${win}"
    fi
  done < <(ctx2 "${doc}" "退出码 \`5\`")
done
# INSTALL 退出码表 5 行（`| 5 | ... |`）现态必须含 E15 与 E16，且不含「未启用」。
INSTALL5="$(grep -nE '^\|[[:space:]]*5[[:space:]]*\|' "${REPO_ROOT}/INSTALL.md" | head -1)"
[ -n "${INSTALL5}" ] || die "INSTALL 未找到退出码表的 5 行"
grep -qF "E15" <<<"${INSTALL5}" || die "INSTALL 退出码 5 行缺成因码 E15：${INSTALL5}"
grep -qF "E16" <<<"${INSTALL5}" || die "INSTALL 退出码 5 行缺成因码 E16：${INSTALL5}"
grep -qE "未启用" <<<"${INSTALL5}" && die "INSTALL 退出码 5 行仍含「未启用」：${INSTALL5}"
ok "退出码 5 全文无「未启用」残留；INSTALL 5 行含 E15 / E16"

# ---------------------------------------------------------------- 8. 三项已知限制在册（README + INSTALL）
step "三项已知限制在册：macOS 未真机验证 / 网络盘 · 同步盘不保证原子性 / 存在未闭合事务时不可删 .index/"
for doc in "${REPO_ROOT}/README.md" "${REPO_ROOT}/INSTALL.md"; do
  b="$(basename "${doc}")"
  grep -qE "未真机|未经真机|真机(运行)?验证" "${doc}" || die "${b} 未登记「macOS 未真机验证」已知限制"
  grep -qF "网络盘" "${doc}" || die "${b} 未登记「网络盘」已知限制"
  grep -qF "同步盘" "${doc}" || die "${b} 未登记「同步盘」已知限制"
  grep -qE "未闭合事务" "${doc}" || die "${b} 未登记「未闭合事务」相关限制"
  grep -qE "不可删|不能删|不得删" "${doc}" || die "${b} 未登记「存在未闭合事务时不可删 .index/」限制"
done
# 反向反证：不得对外过度承诺「任何 exit 5 场景整个命令零磁盘变化」（合同 §9.1 / R5）。
OVERCLAIM="$({ grep -rnE '整个命令.*零磁盘变化|任何 exit 5.*零磁盘' "${DOCS[@]}" || true; } | wc -l | tr -d ' ')"
[ "${OVERCLAIM}" = "0" ] || die "文档出现「整个命令零磁盘变化 / 任何 exit 5 零磁盘」过强表述（${OVERCLAIM} 处）"
ok "三项已知限制在 README + INSTALL 在册；无 exit 5 过强承诺"

# ---------------------------------------------------------------- 9. 真实仓库工作区零污染
step "真实仓库工作区零污染（一切写与构建只发生在 mktemp 内）"
AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
if [ "${BEFORE_STATUS}" != "${AFTER_STATUS}" ]; then
  printf '  before:\n%s\n  after:\n%s\n' "${BEFORE_STATUS}" "${AFTER_STATUS}" >&2
  die "脚本运行后仓库脏文件集合发生变化"
fi
ok "git status --porcelain 前后一致，未新增任何脏文件"

printf '\n全部 %d 组断言通过（共 %d 步）。\n' "${PASS}" "${STEP}"

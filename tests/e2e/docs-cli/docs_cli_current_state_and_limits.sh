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
WANT_COMMANDS=22

# 收口的 22 条顶层命令名（与 `eg --help` 命令区首词逐条对应，**双向恰等**的期望集合）。
# M6 不新增命令，故与 M5 收口清单逐字相同。
CANON_CMDS=(
  init config capture context apply search card rel report
  deprecate restore replaced-by proposal delete undelete mark-reviewed unreviewed edit
  reconcile check index bench
)

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

# ---------------------------------------------------------------- 2. 顶层命令集合与 22 条清单双向恰等
step "eg --help 顶层命令名集合与收口清单双向恰等（M6 不新增命令，恰 ${WANT_COMMANDS} 条）"
ACTUAL="$("${EG}" --help </dev/null 2>/dev/null | awk '
  /^命令/ {inblk=1; next}
  /^全局 flag/ {inblk=0}
  inblk && /^  [a-z]/ {print $1}' | sort -u)"
WANT="$(printf '%s\n' "${CANON_CMDS[@]}" | sort -u)"
N_ACTUAL="$(printf '%s\n' "${ACTUAL}" | grep -c .)"
[ "${N_ACTUAL}" = "${WANT_COMMANDS}" ] || die "实际顶层命令数 = ${N_ACTUAL}，期望 ${WANT_COMMANDS}"
[ "$(printf '%s\n' "${CANON_CMDS[@]}" | sort -u | wc -l | tr -d ' ')" = "${WANT_COMMANDS}" ] \
  || die "CANON_CMDS 去重后不是 ${WANT_COMMANDS} 条（期望集合自身有重复 = 名单造假）"
if [ "${ACTUAL}" != "${WANT}" ]; then
  printf '  仅实际有：\n%s\n  仅清单有：\n%s\n' \
    "$(comm -23 <(printf '%s\n' "${ACTUAL}") <(printf '%s\n' "${WANT}"))" \
    "$(comm -13 <(printf '%s\n' "${ACTUAL}") <(printf '%s\n' "${WANT}"))" >&2
  die "顶层命令集合与收口清单不恰等"
fi
ok "顶层命令集合 == 收口清单，恰 ${WANT_COMMANDS} 条（M6 不新增命令，双向恰等）"

# ---------------------------------------------------------------- 3. 文档命令集合与实际双向恰等
step "README / INSTALL / SKILL.md 中的 \`eg <名>\` 集合与实际 ${WANT_COMMANDS} 条双向恰等"
DOC_CMDS="$(grep -ohE '\beg [a-z][a-z-]*' "${DOCS[@]}" | awk '{print $2}' | sort -u)"
if [ "${DOC_CMDS}" != "${WANT}" ]; then
  printf '  文档写了但实际不存在（示范无效命令）：\n%s\n  实际有但文档未写（漏文档）：\n%s\n' \
    "$(comm -23 <(printf '%s\n' "${DOC_CMDS}") <(printf '%s\n' "${WANT}"))" \
    "$(comm -13 <(printf '%s\n' "${DOC_CMDS}") <(printf '%s\n' "${WANT}"))" >&2
  die "文档命令集合与实际命令集合不恰等"
fi
for doc in "${REPO_ROOT}/README.md" "${REPO_ROOT}/skill/SKILL.md"; do
  miss=0
  for c in "${CANON_CMDS[@]}"; do
    grep -qE "\beg ${c}\b" "${doc}" || { printf '  MISSING in %s: eg %s\n' "$(basename "${doc}")" "${c}"; miss=$((miss + 1)); }
  done
  [ "${miss}" = "0" ] || die "$(basename "${doc}") 缺 ${miss} 条命令名"
done
for doc in "${REPO_ROOT}/README.md" "${REPO_ROOT}/skill/SKILL.md"; do
  grep -qE "命令.*${WANT_COMMANDS}|${WANT_COMMANDS}[^0-9].*命令" "${doc}" \
    || die "$(basename "${doc}") 未在命令语境里登记当期总数 ${WANT_COMMANDS}"
done
ok "三份文档的命令集合 == 实际集合；README / SKILL 逐条在场且登记总数 ${WANT_COMMANDS}"

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
      printf '%s' "${win}" | grep -qE "${M6_LANDED_RE}" && hit=1
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
    printf '%s' "${line}" | grep -qE "不分配|留白|不使用|未分配" \
      || die "$(basename "${doc}") 在非「不分配」语境提到 W21：${line}"
  done < <(grep -F "W21" "${doc}" || true)
done
ok "M6 能力面逐条正面在场（五术语 + 五诊断码 + --strict；W21 仍不分配）"

# ---------------------------------------------------------------- 7. 退出码 5 现态：绝不再标「未启用」
step "退出码 5 现态已启用：三份文档不得再声称「未启用」；INSTALL 退出码 5 行含 E15 / E16 两类成因"
for doc in "${DOCS[@]}"; do
  while IFS= read -r win; do
    if printf '%s' "${win}" | grep -qE "未启用|尚未启用|尚不启用|不启用"; then
      die "$(basename "${doc}") 仍声称退出码 5 未启用（M6·T-074 已启用，旧字样必须零残留）：${win}"
    fi
  done < <(ctx2 "${doc}" "退出码 \`5\`")
done
# INSTALL 退出码表 5 行（`| 5 | ... |`）现态必须含 E15 与 E16，且不含「未启用」。
INSTALL5="$(grep -nE '^\|[[:space:]]*5[[:space:]]*\|' "${REPO_ROOT}/INSTALL.md" | head -1)"
[ -n "${INSTALL5}" ] || die "INSTALL 未找到退出码表的 5 行"
printf '%s' "${INSTALL5}" | grep -qF "E15" || die "INSTALL 退出码 5 行缺成因码 E15：${INSTALL5}"
printf '%s' "${INSTALL5}" | grep -qF "E16" || die "INSTALL 退出码 5 行缺成因码 E16：${INSTALL5}"
printf '%s' "${INSTALL5}" | grep -qE "未启用" && die "INSTALL 退出码 5 行仍含「未启用」：${INSTALL5}"
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

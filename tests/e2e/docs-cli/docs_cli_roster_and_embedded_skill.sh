#!/usr/bin/env bash
# M5 文档 / 命令面 / 版本一致性端到端脚本（T-evergreen.s1_main_flow-158614-069）。
#
# 判据来源：本 task deliverables「evergreen/tests/e2e/docs-cli/docs_cli_roster_and_embedded_skill.sh」——
#   「README / INSTALL / SKILL.md 中出现的命令与实际命令集合（22 个）逐条对齐，版本号四处同真，
#     内嵌 SKILL.md 与 skill/SKILL.md 逐字相等」；milestone M-005 完成判据 16 / 17。
#
# 与既有文档门禁的分工（互不覆盖、只增不改）：
#   m2/m3/m4_docs_commands.sh 各守其里程碑的增量；本脚本**只守 M5 增量**，并把 M4 期
#   「文档覆盖命令」的单向包含关系**加严为双向恰等**：
#     ① `eg --help` 顶层命令名集合恰 22 个，与 M5 收口清单**逐字双向恰等**（多一个 / 少一个都红）；
#     ② 三份文档里出现的 `eg <名>` 形态命令名集合**恰等于**上面这 22 个 ——
#        方向 A（文档 ⊆ 实际）杜绝文档示范不存在的命令（M4 期无此判据）；
#        方向 B（实际 ⊆ 文档）杜绝新命令落地却没写进文档；
#     ③ 版本号**五处**同真：version.go 字面量 == `make print-version` == `eg --version`
#        == README == INSTALL，且 M5 发布口径决策文档逐字声明同一个值（第五处 = 唯一决策出处）；
#     ④ 内嵌 SKILL.md（`eg init` 落盘副本）与 `skill/SKILL.md` 逐字相等（go:embed 未过期）；
#     ⑤ M5 能力面在文档中逐条在场：`index` 四子命令 / `bench` 五键 / `W22`–`W25` + `Q5` /
#        `--limit` `--offset` 与默认 50 / `.index/` 为不入 Git 的可重建派生 / 纯 Go 驱动 +
#        `CGO_ENABLED=0`；
#     ⑥ **越界反证**：M6 术语（`run.lock` / 事务日志 / 崩溃恢复 / 块级合并 / 退出码 `5`）
#        在三份文档里只允许出现在**否定 / 未做 / 属 M6** 语境里 —— 文档不得预告成已具备能力。
#
# 约束：离线、可重复、无外部依赖（bash / coreutils / git / make / go）；
#   **一切写与构建只发生在 mktemp -d 沙箱内**，真实仓库工作区零污染（脚本末尾自查 git status）。
#   全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/docs-cli/docs_cli_roster_and_embedded_skill.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
# ── C2b·M6 现态重钉（延续「决策出处 = docsReleaseSpec 当期指针」的历史口径，非放宽）：
#    M6 · T-…-075 把版本推进到 0.6.0-m6，唯一决策出处随 docs_test.go 的 docsReleaseSpec 一并
#    切到 M6 发布口径文档；M5 文档只读历史、事实一字未改。第五处一致仍锁「决策文档逐字含当期版本号」。
SPEC_REL="projects/evergreen/s1_main_flow/docs/specs/2027-02-21-m6-release-and-version.md"
DECISION_DOC="${EG_CONTRACTS}/${SPEC_REL}"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-docs.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

# 当期期望版本号（写死；唯一决策出处见 ${SPEC_REL} §1.1）。C2b·M6 现态重钉：0.5.0-m5 → 0.6.0-m6。
WANT_VERSION="0.6.0-m6"
WANT_COMMANDS=22

# M5 收口的 22 条顶层命令名（与 `eg --help` 命令区首词逐条对应，**双向恰等**的期望集合）。
CANON_CMDS=(
  init config capture context apply search card rel report
  deprecate restore replaced-by proposal delete undelete mark-reviewed unreviewed edit
  reconcile check index bench
)

# `eg index` 的四个子命令（合同 §7.2：恰四个，封闭）。
INDEX_SUBS=(build rebuild status sync)
# `eg bench --json` 的五键（合同 §7.3：恰五个，封闭）。
BENCH_KEYS=(search_p95_ms card_show_p95_ms rel_p95_ms index_build_ms index_incremental_ms)
# M5 新增诊断码（W21 刻意不在其中：仍不分配）。
M5_CODES=(W22 W23 W24 W25 Q5)

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
step "eg --help 顶层命令名集合与 M5 收口清单双向恰等（恰 ${WANT_COMMANDS} 条）"
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
  die "顶层命令集合与 M5 收口清单不恰等"
fi
ok "顶层命令集合 == M5 清单，恰 ${WANT_COMMANDS} 条（双向恰等）"

# ---------------------------------------------------------------- 3. 文档命令集合与实际双向恰等
step "README / INSTALL / SKILL.md 中的 \`eg <名>\` 集合与实际 ${WANT_COMMANDS} 条双向恰等"
DOC_CMDS="$(grep -ohE '\beg [a-z][a-z-]*' "${DOCS[@]}" | awk '{print $2}' | sort -u)"
if [ "${DOC_CMDS}" != "${WANT}" ]; then
  printf '  文档写了但实际不存在（示范无效命令）：\n%s\n  实际有但文档未写（漏文档）：\n%s\n' \
    "$(comm -23 <(printf '%s\n' "${DOC_CMDS}") <(printf '%s\n' "${WANT}"))" \
    "$(comm -13 <(printf '%s\n' "${DOC_CMDS}") <(printf '%s\n' "${WANT}"))" >&2
  die "文档命令集合与实际命令集合不恰等"
fi
# 每条命令名必须在**每一份**文档里至少出现一次吗？—— 不是：INSTALL 是安装口径，
# 只要求 README + SKILL 逐条覆盖（与 M4 期口径一致，不放宽也不越界加严到 INSTALL）。
for doc in "${REPO_ROOT}/README.md" "${REPO_ROOT}/skill/SKILL.md"; do
  miss=0
  for c in "${CANON_CMDS[@]}"; do
    grep -qE "\beg ${c}\b" "${doc}" || { printf '  MISSING in %s: eg %s\n' "$(basename "${doc}")" "${c}"; miss=$((miss + 1)); }
  done
  [ "${miss}" = "0" ] || die "$(basename "${doc}") 缺 ${miss} 条命令名"
done
# 当期总数必须在「命令」语境里被登记（防被日期 / 章节号蒙对）。
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
# 第五处 = 唯一决策文档。teamwork/ 是同级独立仓；**不允许缺失时跳过**（M5 起加严：
# 该文档正是 docs_test.go 的 docsReleaseSpec 指针目标，缺了就说明版本口径无出处）。
[ -f "${DECISION_DOC}" ] || die "缺 M5 发布口径决策文档：teamwork/${SPEC_REL}"
DOC_VER="$(grep -oE '\*\*`[0-9]+\.[0-9]+\.[0-9]+[^`]*`\*\*' "${DECISION_DOC}" | head -1 | tr -d '*`')"
[ "${DOC_VER}" = "${WANT_VERSION}" ] || die "决策文档声明版本号 = ${DOC_VER}，期望 ${WANT_VERSION}"
grep -Fq "未知" "${DECISION_DOC}" || die "决策文档必须对「是否推送远端 / tag」标注「未知」"
# version.go / Makefile 内不得残留上一里程碑版本字面量（M-005 判据 16 第二格）。
# ── C2b·M6 现态重钉：上一里程碑随 M6 收口由 0.4.0-m4 变为 0.5.0-m5，逐字对撞目标同步推进（非放宽）。
PREV="$({ grep -F '0.5.0-m5' "${REPO_ROOT}/internal/version/version.go" "${REPO_ROOT}/Makefile" || true; } | wc -l | tr -d ' ')"
[ "${PREV}" = "0" ] || die "version.go / Makefile 仍残留 0.5.0-m5（${PREV} 处）"
ok "五处版本号逐字一致：${WANT_VERSION}；0.5.0-m5 零残留；决策文档 tag/推送标「未知」"

# ---------------------------------------------------------------- 5. 内嵌 SKILL.md 与源文件同字节
step "eg init 落盘的 SKILL.md 与 skill/SKILL.md 逐字相等（go:embed 未过期）"
mkdir -p "${VAULT}"
"${EG}" --vault "${VAULT}" init --domain ai-infra </dev/null >/dev/null 2>&1 || die "eg init 失败"
[ -f "${VAULT}/SKILL.md" ] || die "eg init 未落盘 SKILL.md"
cmp -s "${VAULT}/SKILL.md" "${REPO_ROOT}/skill/SKILL.md" \
  || die "内嵌 SKILL.md（落盘副本）与源文件字节不一致——须重编译刷新 go:embed"
ok "内嵌 SKILL.md 与 skill/SKILL.md 逐字相等"

# ---------------------------------------------------------------- 6. M5 能力面在文档中逐条在场
step "M5 能力面在场：index 四子命令 / bench 五键 / W22-W25+Q5 / 分页 / .index 派生 / 纯 Go 驱动"
for s in "${INDEX_SUBS[@]}"; do
  grep -qF "index ${s}" "${REPO_ROOT}/README.md" || die "README 未写 eg index ${s}"
  grep -qF "index ${s}" "${REPO_ROOT}/skill/SKILL.md" || die "SKILL.md 未写 eg index ${s}"
done
grep -qE "恰四个|四个子命令|四子命令" "${REPO_ROOT}/skill/SKILL.md" \
  || die "SKILL.md 未写明 eg index 子命令恰四个（封闭集合）"
for k in "${BENCH_KEYS[@]}"; do
  for doc in "${DOCS[@]}"; do
    grep -qF "${k}" "${doc}" || die "$(basename "${doc}") 未登记 bench 键 ${k}"
  done
done
for c in "${M5_CODES[@]}"; do
  for doc in "${DOCS[@]}"; do
    grep -qF "${c}" "${doc}" || die "$(basename "${doc}") 未登记 M5 诊断码 ${c}"
  done
done
# W21 仍不分配：三份文档里若提到它，必须处在「不分配 / 留白 / 不使用」语境。
for doc in "${DOCS[@]}"; do
  while IFS= read -r line; do
    printf '%s' "${line}" | grep -qE "不分配|留白|不使用|未分配" \
      || die "$(basename "${doc}") 在非「不分配」语境提到 W21：${line}"
  done < <(grep -F "W21" "${doc}" || true)
done
for doc in "${DOCS[@]}"; do
  grep -qF -- "--limit" "${doc}" || die "$(basename "${doc}") 未登记 --limit"
  grep -qF -- "--offset" "${doc}" || die "$(basename "${doc}") 未登记 --offset"
done
grep -qE -- "--limit[^0-9]*50|默认[^。]*50" "${REPO_ROOT}/README.md" \
  || die "README 未写明 --limit 默认 50"
grep -qE "可重建|派生" "${REPO_ROOT}/README.md" || die "README 未写明 .index/ 是可重建派生物"
grep -qE "\.gitignore|不入 Git|不进 Git" "${REPO_ROOT}/skill/SKILL.md" \
  || die "SKILL.md 未写明 .index/ 不入 Git"
grep -qF "CGO_ENABLED=0" "${REPO_ROOT}/INSTALL.md" || die "INSTALL 未写明 CGO_ENABLED=0 静态构建"
grep -qF "modernc.org/sqlite" "${REPO_ROOT}/README.md" || die "README 未登记纯 Go SQLite 驱动"
ok "M5 能力面逐条在场（index 四子命令 / bench 五键 / 五诊断码 / 分页 / 派生物 / 纯 Go 驱动）"

# ---------------------------------------------------------------- 7. M6 现态：术语作为已落地能力在场
# ── C2b·M6 现态重钉（延续 m3_docs_commands §5「绝不再出现未启用 / 必须标启用」的现态口径，加严非放宽）──
# 历史事实（M5 收口当日）：本步曾是「越界反证」——run.lock / 事务日志 / 崩溃恢复 / 块级合并 / 退出码 5
# 只许出现在否定 / 属 M6 语境，防止 M5 文档预告未落地能力。M6·T-070~074 已把这五项**全部落地**，
# 故现态重钉为**正面在场断言**：三份文档必须把这五项写成**已落地的 M6 能力**（其 2 行窗口须命中
# M6 / S5 / 启用 / 落地 之一，即「明确归属 M6 且非否定当期」），且退出码 5 现态**绝不再**标「未启用」。
step "M6 能力面在场：run.lock / 事务日志 / 崩溃恢复 / 块级合并 / 退出码 5 均写成已落地的 M6 能力"
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
# 退出码 5 现态已启用：三份文档不得再声称它「未启用 / 尚未 / 属 M6 未做」。
for doc in "${DOCS[@]}"; do
  while IFS= read -r win; do
    if printf '%s' "${win}" | grep -qE "未启用|尚未启用|尚不启用|不启用"; then
      die "$(basename "${doc}") 仍声称退出码 5 未启用（M6·T-074 已启用，旧字样必须零残留）：${win}"
    fi
  done < <(ctx2 "${doc}" "退出码 \`5\`")
done
ok "M6 术语与退出码 5 全部作为已落地 M6 能力在场，无「未启用」残留"

# ---------------------------------------------------------------- 8. 真实仓库工作区零污染
step "真实仓库工作区零污染（一切写与构建只发生在 mktemp 内）"
AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
if [ "${BEFORE_STATUS}" != "${AFTER_STATUS}" ]; then
  printf '  before:\n%s\n  after:\n%s\n' "${BEFORE_STATUS}" "${AFTER_STATUS}" >&2
  die "脚本运行后仓库脏文件集合发生变化"
fi
ok "git status --porcelain 前后一致，未新增任何脏文件"

printf '\n全部 %d 组断言通过（共 %d 步）。\n' "${PASS}" "${STEP}"

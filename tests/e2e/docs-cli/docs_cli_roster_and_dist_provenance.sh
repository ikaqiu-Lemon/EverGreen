#!/usr/bin/env bash
# M4 文档 / 版本 / dist 溯源端到端脚本（T-evergreen.s1_main_flow-158614-062）。
#
# 判据来源：本 task deliverables「tests/e2e/docs-cli/docs_cli_roster_and_dist_provenance.sh」与 Acceptance「命令数与文档一致 /
#   内嵌一致（A-F-02 关闭）/ dist 溯源（A-F-01 关闭）」，milestone M-004 完成判据 16。
#
# 与既有文档门禁的分工（互不覆盖、只增不改）：
#   m2_docs_commands.sh 守 M2 口径、m3_docs_commands.sh 守 M3 增量；本脚本**只守 M4 增量**：
#   ① eg 实际顶层命令数恰 20，且 README / SKILL.md 的命令清单与之逐字一致（20 条命令名全覆盖）；
#   ② 版本号三处一致：version.go 字面量 == make print-version == eg --version，且逐字含**当期版本号**；
#      README / INSTALL 两处文档消费面同样登记当期版本号；version.go / Makefile 内 0.3.0-m3 零残留；
#      **M5 · T-…-069 阶段化重钉（事实变了，判据形态不变、且加严一格）**：M4 期这一格把当期版本
#      写死为 0.4.0-m4。M5 按发布口径文档 §1.1 把版本推进到 0.5.0-m5 后，「写死 M4 值」会把一条
#      本应长青的一致性判据变成必红的历史快照。因此改为：当期期望值 = WANT_VERSION（本脚本按里程碑
#      显式声明，仍是写死的常量，**不是从被测对象反推**），并**新增**「M4 收口值不得作为可生效取值
#      残留」与「当期版本必须严格新于 M4 收口值」两条 —— M4 的四处逐字一致结论一格未放宽。
#   ③ 内嵌 SKILL.md 与源文件同字节（eg init 落盘副本逐字相等）——关闭 phaseA A-F-02；
#   ④ dist 四平台产物齐全、linux/amd64 --version 逐字含 0.4.0-m4、sha256sum -c 全部 OK——关闭 A-F-01；
#   ⑤ SKILL.md 覆盖 eg reconcile / eg check / --include-deprecated 与「对账不是写命令前置」。
#
# 约束：离线、可重复、无外部依赖（bash / coreutils / git / make / go）；
#   **一切写与构建只发生在 mktemp -d 沙箱内**，真实仓库工作区零污染（脚本末尾自查 git status）。
#   全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/docs-cli/docs_cli_roster_and_dist_provenance.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-docs.XXXXXX")"
SANDBOX="${WORK}/evergreen"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

# 当期期望版本号（唯一决策出处见 teamwork 的 knowledge_opinion_split Schema-v2 决策文档 §0.0）。
# 写死而非从 version.go 反推：否则「四处一致」会退化成自证（被测对象自己定义期望值）。
# ── 阶段化重钉（同「当期期望值 = WANT_VERSION，本脚本按里程碑更新」的历史口径，非放宽）：
#    本 Epic（knowledge_opinion_split）按发布口径把版本推进到 0.7.0-m7，故当期期望值由 0.6.0-m6 抬为 0.7.0-m7；
#    M4_VERSION 反证锚点一字未改（0.7.0-m7 仍严格新于它、且它仍不得出现在任何赋值行）。
WANT_VERSION="0.7.0-m7"
# M4 收口值：作为**历史锚点 + 反证基线**保留（M4 结论「当期曾是它」不删，但它不得再是当期取值）。
M4_VERSION="0.4.0-m4"
WANT_COMMANDS=20

# M4 收口的 20 条顶层命令名（与 eg --help 命令区逐条对应）。
CANON_CMDS=(
  init config capture context apply search "card show" rel "report --last"
  deprecate restore replaced-by proposal delete undelete mark-reviewed unreviewed edit
  reconcile check
)

# M5 期在 M4 收口值之上**追加**的顶层命令名（本脚本只守 M4 增量，因此这里如实登记
# 后来者并在计数时摘掉：M4 结论「恰 20 条、逐条在场」一个字不放宽）。
#   index —— T-…-065（M5 派生索引；`eg bench` 属 T-…-068，落地时在此追加一行）
#   bench —— T-…-068（M5 只读性能采样；按上一行的明写扩展点补登，T-…-069 阶段 A 执行）
# 追加只影响「当期实际命令数」这一个派生值：M4 的两条结论
#   ①「摘掉 M5 期追加后恰 20」②「20 条命令名逐条在场」
# 仍原样判定，且新增命令必须真在 --help 里（登记了没注册 = 名单造假，见下方 for 循环）。
M5_ADDED_CMDS=(index bench)
# ── addendum（读路径拆分批次 · post-M5 / post-M6 现态重钉，非放宽历史）：
#    观点子系统 `eg opinion` 是 M5 之后新增的顶层命令，同样如实登记为后来者。M4 结论
#    「恰 20 条」仍由「摘掉 M4 之后所有新增项（index / bench / opinion）」逐条复算，
#    一个字不放宽、不把当时的 20 篡成 23。
POST_M5_ADDED_CMDS=(opinion)
# M4 之后所有新增的顶层命令（M5 期 index/bench + 读路径拆分 opinion）；计数复算时整体摘掉。
POST_M4_ADDED_CMDS=("${M5_ADDED_CMDS[@]}" "${POST_M5_ADDED_CMDS[@]}")
WANT_COMMANDS_NOW=$((WANT_COMMANDS + ${#POST_M4_ADDED_CMDS[@]}))

# 当前真实的 23 条顶层命令**简单名**（= eg --help 命令区首词；用于四份文档现态覆盖检查）。
CURRENT_CMDS=(
  init config capture context apply search card rel report
  deprecate restore replaced-by proposal delete undelete mark-reviewed unreviewed edit
  reconcile check index bench opinion
)

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

BEFORE_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 eg（输出到沙箱，零污染）
step "从源码构建 eg 到沙箱（不落 bin/ dist/）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -trimpath \
  -ldflags "-X github.com/ikaqiu-Lemon/EverGreen/internal/version.Version=$(make -s print-version | tail -1 | tr -d ' ') -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Commit=e2e -X github.com/ikaqiu-Lemon/EverGreen/internal/version.Date=e2e" \
  -o "${EG}" ./cmd/eg) || die "编译失败"
ok "eg 已构建：${EG}"

# ---------------------------------------------------------------- 2. eg 实际命令数恰 23
step "eg --help 顶层命令数恰 ${WANT_COMMANDS_NOW}（= M4 收口 ${WANT_COMMANDS} + M4 之后追加 ${#POST_M4_ADDED_CMDS[@]}）"
CMD_COUNT="$("${EG}" --help </dev/null 2>/dev/null | awk '
  /^命令/ {inblk=1; next}
  /^全局 flag/ {inblk=0}
  inblk && /^  [a-z]/ {n++}
  END {print n+0}')"
[ "${CMD_COUNT}" = "${WANT_COMMANDS_NOW}" ] || die "eg --help 命令数 = ${CMD_COUNT}，期望 ${WANT_COMMANDS_NOW}"
# M4 结论复算：摘掉 M4 之后追加的命令（index / bench / opinion）后恰 20（历史结论不放宽）。
[ "$((CMD_COUNT - ${#POST_M4_ADDED_CMDS[@]}))" = "${WANT_COMMANDS}" ] \
  || die "摘掉 M4 之后追加命令后 = $((CMD_COUNT - ${#POST_M4_ADDED_CMDS[@]}))，期望 M4 收口值 ${WANT_COMMANDS}"
# M4 之后追加的命令必须真的在 --help 里（登记了却没注册 = 名单造假）。
for c in "${POST_M4_ADDED_CMDS[@]}"; do
  "${EG}" --help </dev/null 2>/dev/null | grep -qE "^  ${c}( |$)" || die "M4 之后登记的命令 ${c} 不在 --help 命令区"
done
ok "eg 实际顶层命令数 = ${CMD_COUNT}；摘掉 M4 之后追加后 = ${WANT_COMMANDS}（M4 收口值，历史保真）"

# ---------------------------------------------------------------- 3. README / SKILL.md 命令清单与实际一致
step "README.md 与 skill/SKILL.md 覆盖全部 ${WANT_COMMANDS} 条命令名（逐条 grep -F）"
for doc in "${REPO_ROOT}/README.md" "${REPO_ROOT}/skill/SKILL.md"; do
  miss=0
  for c in "${CANON_CMDS[@]}"; do
    grep -qF -- "eg ${c}" "${doc}" || { printf '  MISSING in %s: eg %s\n' "$(basename "${doc}")" "${c}"; miss=$((miss + 1)); }
  done
  [ "${miss}" = "0" ] || die "$(basename "${doc}") 缺 ${miss} 条命令名"
done
# 顶层命令**总数**在两份文档里逐字在场：M4 期这里 grep 的是「20」，但 M5 把总数推到 22 后，
# 裸 grep -F "20" 会被日期 / 章节号等无关串轻易蒙对（假阳性）。故重钉为：两份文档必须登记
# **当期总数 ${WANT_COMMANDS_NOW}**，且必须与「命令」二字同行出现（把断言钉在语义上下文里）。
# ── addendum（post-M5 现态）：读路径拆分批次追加 opinion 后，当期总数由 22 再抬为 23；
#    ${WANT_COMMANDS_NOW} 已随之为 23，本断言无需再改数字（口径长青）。
for doc in "${REPO_ROOT}/README.md" "${REPO_ROOT}/skill/SKILL.md"; do
  grep -qE "命令.*${WANT_COMMANDS_NOW}|${WANT_COMMANDS_NOW}[^0-9].*命令" "${doc}" \
    || die "$(basename "${doc}") 未在命令语境里登记当期顶层命令总数 ${WANT_COMMANDS_NOW}"
done
ok "README / SKILL.md 命令清单与 M4 的 ${WANT_COMMANDS} 条逐条一致，且登记当期总数 ${WANT_COMMANDS_NOW}"

# ---------------------------------------------------------------- 3b. 四份文档各自覆盖当前 23 条并登记总数（杜绝聚合互相补漏）
# post-M5 现态门禁：README / README.zh-CN / INSTALL / SKILL **每一份**都须自证覆盖当前 23 条
# 顶层命令（简单名）且在命令语境登记当前总数 23；README.zh-CN 与 INSTALL 不再豁免（历史 M4 步只查
# README+SKILL 的复合形态，此步补齐现态四文档面，二者互不覆盖）。
step "README / README.zh-CN / INSTALL / SKILL 各自覆盖当前 ${WANT_COMMANDS_NOW} 条命令并登记总数"
ALL_DOCS=("${REPO_ROOT}/README.md" "${REPO_ROOT}/README.zh-CN.md" "${REPO_ROOT}/INSTALL.md" "${REPO_ROOT}/skill/SKILL.md")
CUR_WANT="$(printf '%s\n' "${CURRENT_CMDS[@]}" | sort -u)"
[ "$(printf '%s\n' "${CURRENT_CMDS[@]}" | sort -u | wc -l | tr -d ' ')" = "${WANT_COMMANDS_NOW}" ] \
  || die "CURRENT_CMDS 去重后不是 ${WANT_COMMANDS_NOW} 条（期望集合自身有重复 = 名单造假）"
for doc in "${ALL_DOCS[@]}"; do
  b="$(basename "${doc}")"
  # 方向 A（文档 ⊆ 实际）：本文档出现的 `eg <名>` 不得有实际不存在的命令。
  DSET="$(grep -ohE '\beg [a-z][a-z-]*' "${doc}" | awk '{print $2}' | sort -u)"
  BOGUS="$(comm -23 <(printf '%s\n' "${DSET}") <(printf '%s\n' "${CUR_WANT}"))"
  [ -z "${BOGUS}" ] || die "${b} 出现实际不存在的命令：$(printf '%s' "${BOGUS}" | tr '\n' ' ')"
  # 方向 B（实际 ⊆ 文档）：当前 23 条命令名逐条在**本文档自身**在场（不靠其它文档补漏）。
  miss=0
  for c in "${CURRENT_CMDS[@]}"; do
    grep -qE "\beg ${c}\b" "${doc}" || { printf '  MISSING in %s: eg %s\n' "${b}" "${c}"; miss=$((miss + 1)); }
  done
  [ "${miss}" = "0" ] || die "${b} 缺 ${miss} 条当前命令名（每份文档都须自证覆盖全部 ${WANT_COMMANDS_NOW} 条）"
  grep -qE "命令.*${WANT_COMMANDS_NOW}|${WANT_COMMANDS_NOW}[^0-9].*命令" "${doc}" \
    || die "${b} 未在命令语境里登记当前顶层命令总数 ${WANT_COMMANDS_NOW}"
done
ok "四份文档各自覆盖当前 ${WANT_COMMANDS_NOW} 条命令、无实际不存在命令、均登记当前总数 ${WANT_COMMANDS_NOW}"

# ---------------------------------------------------------------- 4. 版本号三处一致 + 文档消费面
step "版本号一致：version.go == make print-version == eg --version，逐字含 ${WANT_VERSION}"
GO_VER="$(grep -E 'DefaultVersion[[:space:]]*=' "${REPO_ROOT}/internal/version/version.go" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+[^" ]*' | head -1)"
MK_VER="$(cd "${REPO_ROOT}" && make -s print-version | tail -1 | tr -d ' ')"
CLI_VER="$("${EG}" --version </dev/null | awk '{print $2}')"
printf '  version.go=%s  make print-version=%s  eg --version=%s\n' "${GO_VER}" "${MK_VER}" "${CLI_VER}"
[ -n "${GO_VER}" ] && [ -n "${MK_VER}" ] && [ -n "${CLI_VER}" ] || die "版本号有一处抽取为空"
[ "${GO_VER}" = "${WANT_VERSION}" ] || die "version.go 版本号 = ${GO_VER}，期望 ${WANT_VERSION}"
[ "${GO_VER}" = "${MK_VER}" ] || die "version.go(${GO_VER}) ≠ make print-version(${MK_VER})"
[ "${GO_VER}" = "${CLI_VER}" ] || die "version.go(${GO_VER}) ≠ eg --version(${CLI_VER})"
grep -Fq "${WANT_VERSION}" "${REPO_ROOT}/README.md" || die "README 未登记版本号 ${WANT_VERSION}"
grep -Fq "${WANT_VERSION}" "${REPO_ROOT}/INSTALL.md" || die "INSTALL 未登记版本号 ${WANT_VERSION}"
# version.go / Makefile 内不得残留 M3 版本字面量（M4 期原判据，一字未改）。
STALE="$({ grep -F '0.3.0-m3' "${REPO_ROOT}/internal/version/version.go" "${REPO_ROOT}/Makefile" || true; } | wc -l | tr -d ' ')"
[ "${STALE}" = "0" ] || die "version.go / Makefile 仍残留 0.3.0-m3（${STALE} 处）"
# 【加严 1】当期取值必须已脱离 M4 收口值，且 M4 值不得出现在任何**赋值行**（注释里的里程碑沿革允许保留）。
[ "${GO_VER}" != "${M4_VERSION}" ] || die "当期版本仍是 M4 收口值 ${M4_VERSION}（M5 未推进版本号）"
LIVE_M4="$({ grep -F "${M4_VERSION}" "${REPO_ROOT}/internal/version/version.go" "${REPO_ROOT}/Makefile" \
             | grep -vE ':[[:space:]]*(#|//)' || true; } | wc -l | tr -d ' ')"
[ "${LIVE_M4}" = "0" ] || die "version.go / Makefile 的非注释行仍残留 ${M4_VERSION}（${LIVE_M4} 处：旧值不得可生效）"
# 【加严 2】当期版本必须严格新于 M4 收口值（sort -V 语义序；杜绝把版本号改小/改回旧里程碑）。
NEWEST="$(printf '%s\n%s\n' "${M4_VERSION}" "${WANT_VERSION}" | sort -V | tail -1)"
[ "${NEWEST}" = "${WANT_VERSION}" ] || die "当期版本 ${WANT_VERSION} 未严格新于 M4 收口值 ${M4_VERSION}"
ok "四处版本号逐字一致：${GO_VER}（严格新于 M4 收口值 ${M4_VERSION}）；0.3.0-m3 零残留、${M4_VERSION} 无可生效残留"

# ---------------------------------------------------------------- 5. 内嵌 SKILL.md 与源文件同字节（A-F-02）
step "eg init 落盘的 SKILL.md 与源文件逐字相等（关闭 A-F-02）"
mkdir -p "${VAULT}"
"${EG}" --vault "${VAULT}" init --domain ai-infra </dev/null >/dev/null 2>&1 || die "eg init 失败"
[ -f "${VAULT}/SKILL.md" ] || die "eg init 未落盘 SKILL.md"
if ! cmp -s "${VAULT}/SKILL.md" "${REPO_ROOT}/skill/SKILL.md"; then
  die "内嵌 SKILL.md（落盘副本）与源文件字节不一致——须重编译刷新 go:embed"
fi
ok "内嵌 SKILL.md 与 skill/SKILL.md 逐字相等"

# ---------------------------------------------------------------- 6. SKILL.md 覆盖 M4 命令与「对账非写前置」
step "SKILL.md 覆盖 eg reconcile / eg check / --include-deprecated 与「对账不是写命令前置」"
SK="${REPO_ROOT}/skill/SKILL.md"
grep -qF "eg reconcile" "${SK}"       || die "SKILL.md 缺 eg reconcile"
grep -qF "eg check" "${SK}"           || die "SKILL.md 缺 eg check"
grep -qF -- "--include-deprecated" "${SK}" || die "SKILL.md 缺 --include-deprecated"
grep -qE '对账.*不是.*前置|不作为写命令的前置|不作为任何写命令的前置' "${SK}" \
  || die "SKILL.md 未写明「对账不是写命令前置」"
ok "SKILL.md 覆盖 M4 两命令、可见性 flag 与对账非写前置口径"

# ---------------------------------------------------------------- 7. dist 四平台 + --version + sha256sum（A-F-01）
step "沙箱 make dist：四平台齐全、linux/amd64 --version 含 ${WANT_VERSION}、sha256sum -c 全 OK"
mkdir -p "${SANDBOX}"
# 运行期产物（.tests-staging/、tests/_report/）与"目标目录在源树内"的递归
# 统一由 eg_snapshot_worktree 排除（tests/lib/common.sh）。
eg_snapshot_worktree "${SANDBOX}"
(cd "${SANDBOX}" && make -s dist >/dev/null 2>&1) || die "沙箱 make dist 失败"
CNT="$(ls "${SANDBOX}"/dist/eg_darwin_amd64 "${SANDBOX}"/dist/eg_darwin_arm64 \
        "${SANDBOX}"/dist/eg_linux_amd64 "${SANDBOX}"/dist/eg_linux_arm64 \
        "${SANDBOX}"/dist/SHA256SUMS 2>/dev/null | wc -l | tr -d ' ')"
[ "${CNT}" = "5" ] || die "dist 四平台 + SHA256SUMS 不齐（实得 ${CNT}/5）"
"${SANDBOX}/dist/eg_$(go env GOOS)_$(go env GOARCH)" --version </dev/null | grep -qF "${WANT_VERSION}" \
  || die "本机平台 dist 产物的 --version 不含 ${WANT_VERSION}"
(cd "${SANDBOX}/dist" && sha256sum -c SHA256SUMS >/dev/null 2>&1) \
  || (cd "${SANDBOX}/dist" && shasum -a 256 -c SHA256SUMS >/dev/null 2>&1) \
  || die "dist/SHA256SUMS 校验未全部 OK"
ok "dist 四平台齐全、linux/amd64 --version 含 ${WANT_VERSION}、SHA256SUMS 全部 OK"

# ---------------------------------------------------------------- 8. 真实仓库工作区零污染
step "真实仓库工作区零污染（一切写与构建只发生在 mktemp 内）"
AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
if [ "${BEFORE_STATUS}" != "${AFTER_STATUS}" ]; then
  printf '  before:\n%s\n  after:\n%s\n' "${BEFORE_STATUS}" "${AFTER_STATUS}" >&2
  die "脚本运行后仓库脏文件集合发生变化"
fi
ok "git status --porcelain 前后一致，未新增任何脏文件"

printf '\n全部 %d 组断言通过（共 %d 步）。\n' "${PASS}" "${STEP}"

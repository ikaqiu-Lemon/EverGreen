#!/usr/bin/env bash
# 文档可执行性端到端脚本（T-evergreen.s1_main_flow-158614-046）。
#
# 判据来源：
#   本 task Acceptance「文档命令可实跑 / 占位字样零命中 / 新命令全部出现在 README /
#   退出码文档与实现一致 / SKILL.md 覆盖 W7 升级 / 版本号三处一致 / 已知限制未被冒充解除」
#   版本决策文档 `2026-11-08-m3-release-and-version.md` §1（三处同源）/ §3（远端）/ §4（验证矩阵）
#   授权合同 §3（X1–X4 与「需确认」列）/ §5（三条禁止措辞）
#
# 四组断言（与 m2_docs_commands.sh 分工：那份守 M2 口径，这份守 M3 增量）：
#   ① 三份文档（README / INSTALL / SKILL.md）内的每条命令逐条实跑，退出码与文档标注一致
#      （行尾 `# expect: N` 表示预期退出码 N，未标注即 0）；
#   ② 版本号三处一致：`eg --version` == `make print-version` == `version.go` 字面量，且已脱离 M2 口径；
#   ③ 退出码 6 的文档与实测一致：INSTALL 退出码表有 6 行、白名单恰两条命令、`5` 标未启用，
#      实测「approved 提案 + 缺 --confirm」退 6 且权威 Markdown 完全不变；
#   ④ 占位字样与禁止措辞零命中（四组 grep）+ darwin 已知限制未被冒充解除。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / make / go）；
# **一切写操作只发生在 mktemp -d 目录内**，真实仓库工作区零污染（脚本末尾自查 git status）。
# 全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/docs-cli/docs_cli_authorization_and_limits.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
PARENT="$(cd "${REPO_ROOT}/.." && pwd)"
SPEC_REL_M2="projects/evergreen/s1_main_flow/docs/specs/2026-10-03-m2-release-and-version.md"
SPEC_REL_M3="projects/evergreen/s1_main_flow/docs/specs/2026-11-08-m3-release-and-version.md"
# 2026-09-08 随 M5 · T-…-069 **阶段化重钉**（只改形态，「版本号三处一致」本体一格不放宽）：
# 版本号逐里程碑推进（M2 `0.2.0-m2` → M3 `0.3.0-m3` → M4 `0.4.0-m4` → M5 `0.5.0-m5`），
# 「当期唯一出处」这个**指针**随里程碑走；原脚本把它写死成 M3 决策文档，M5 推进后必然自红。
# 两侧：① 历史基线只读复算（M2 / M3 决策文档仍逐字记录各自当期版本号，历史结论不许被改写）；
#      ② 当前值正面断言（version.go == make print-version == eg --version == 当期决策文档
#         == 当期版本，且历史值一律不得作为**生效值**残留 —— 逐个旧值对撞，比原来只挡
#         `0.2.0-m2` 一个值更严）。
# ── C2b·M6 现态重钉（同上「指针随里程碑走 + 历史值不得为生效值」口径，非放宽）：
#    M6 · T-…-075 把版本推进到 `0.6.0-m6`，当期决策指针切到 M6 发布口径文档，`0.5.0-m5` 并入历史值集合
#    （随即也必须「不得作为生效值残留」）；M2/M3 历史基线只读复算一格不动。
SPEC_REL="projects/evergreen/s1_main_flow/docs/specs/2027-02-21-m6-release-and-version.md"
DECISION_DOC="${EG_CONTRACTS}/${SPEC_REL}"
DECISION_DOC_M2="${EG_CONTRACTS}/${SPEC_REL_M2}"
DECISION_DOC_M3="${EG_CONTRACTS}/${SPEC_REL_M3}"
HIST_VERSIONS=("0.2.0-m2" "0.3.0-m3" "0.4.0-m4" "0.5.0-m5")
CUR_VERSION="0.6.0-m6"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-docs.XXXXXX")"
SANDBOX="${WORK}/sandbox"
SRC_DIR="${SANDBOX}/evergreen"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/ai-infra/knowledge'
NDIR_REL='domains/ai-infra/notes'
CARD='k-20260901-attention'
CARD2='k-20260901-positional'
NOTE='n-20260901-attention'
SEED_SRC='s-20260901-attention'

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

BEFORE_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 占位字样与禁止措辞零命中
step "占位字样零命中四组：M3/S2 占位 / SKILL 全文「未实现」/ 三条禁止措辞 / 冒充已解除的 darwin 声称"
DOCS=("${REPO_ROOT}/README.md" "${REPO_ROOT}/INSTALL.md" "${REPO_ROOT}/skill/SKILL.md")
PLACEHOLDER="M3/S2 $(printf '未实现')"
N1=$({ grep -rF "${PLACEHOLDER}" "${DOCS[@]}" "${REPO_ROOT}/internal" || true; } | wc -l | tr -d ' ')
[ "${N1}" = "0" ] || { grep -rnF "${PLACEHOLDER}" "${DOCS[@]}" "${REPO_ROOT}/internal"; die "占位字样残留 ${N1} 处"; }
N2=$({ grep -F "$(printf '未实现')" "${REPO_ROOT}/skill/SKILL.md" || true; } | wc -l | tr -d ' ')
[ "${N2}" = "0" ] || die "SKILL.md 仍有 ${N2} 处「未实现」（M3 起 18 命令全部真实可用）"
N3=0
for banned in "跳过 hash 比对" "自动回滚到执行前" "Agent 自动路径亦可改"; do
  c=$({ grep -rF "${banned}" "${DOCS[@]}" || true; } | wc -l | tr -d ' ')
  N3=$((N3 + c))
done
[ "${N3}" = "0" ] || die "文档命中被禁措辞 ${N3} 处（授权合同 §5 三条）"
N4=$({ grep -rn "已在 macOS 真机验证\|已验证 macOS\|已在 macOS 验证" "${DOCS[@]}" || true; } | wc -l | tr -d ' ')
[ "${N4}" = "0" ] || die "文档冒充 darwin 已解除限制 ${N4} 处（R-12 必须继续登记）"
grep -qE "交叉编译|未经真机" "${REPO_ROOT}/INSTALL.md" || die "INSTALL.md 未登记 darwin 交叉编译限制"
ok "四组占位/措辞断言全为 0，平台限制仍在场"

# ---------------------------------------------------------------- 2. README 新命令全覆盖
step "README 命令清单补齐 M3 新增命令（逐条 grep -F）"
MISSING=0
for c in "eg proposal new" "eg proposal list" "eg proposal show" "eg proposal approve" \
         "eg proposal reject" "eg deprecate" "eg restore" "eg replaced-by" "eg delete" \
         "eg undelete" "eg mark-reviewed" "eg unreviewed" "eg edit" "eg rel remove" \
         "--include-deleted"; do
  grep -qF -- "${c}" "${REPO_ROOT}/README.md" || { printf '  MISSING:%s\n' "${c}"; MISSING=$((MISSING + 1)); }
done
[ "${MISSING}" = "0" ] || die "README 缺 ${MISSING} 条 M3 命令"
ok "README 逐条命中 15 项（13 条新命令 + proposal show + --include-deleted）"

# ---------------------------------------------------------------- 3. SKILL.md 的 S2 规程明文
step "SKILL.md 覆盖「可提不可执」/ W7 升 error / 矩阵 #12 / 退出码 6"
grep -qF "可提不可执" "${REPO_ROOT}/skill/SKILL.md" || die "SKILL.md 缺「可提不可执」"
grep -q "S2 起\|自 S2\|error" "${REPO_ROOT}/skill/SKILL.md" || die "SKILL.md 未写明 W7 自 S2 起升 error"
grep -qF "**自 S2 起是 error**" "${REPO_ROOT}/skill/SKILL.md" || die "SKILL.md 缺 W7 升级的逐字结论"
grep -qF "不追加、不改写" "${REPO_ROOT}/skill/SKILL.md" || die "SKILL.md 缺矩阵 #12（知识内容分区）明文"
grep -qF "权威 Markdown 完全不变" "${REPO_ROOT}/skill/SKILL.md" || die "SKILL.md 缺退出码 6 的语义边界"
ok "SKILL.md 的 R-10 处置（可提不可执 / W7 升 error / 矩阵 #12 / 退出码 6）逐条在场"

# ---------------------------------------------------------------- 4. 版本号三处一致
step "版本号三处一致：eg --version == make print-version == version.go，且已脱离历史里程碑口径"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
GO_VER="$(grep -E 'DefaultVersion[[:space:]]*=' "${REPO_ROOT}/internal/version/version.go" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+[^" ]*' | head -1)"
MK_VER="$(cd "${REPO_ROOT}" && make -s print-version | tail -1 | tr -d ' ')"
CLI_VER="$("${EG}" --version </dev/null | awk '{print $2}')"
DOC_VER="$(grep -oE '\*\*`[0-9]+\.[0-9]+\.[0-9]+[^`]*`\*\*' "${DECISION_DOC}" | head -1 | tr -d '*`')"
printf '  version.go=%s  make print-version=%s  eg --version=%s  当期决策文档=%s\n' \
  "${GO_VER}" "${MK_VER}" "${CLI_VER}" "${DOC_VER}"
[ -n "${GO_VER}" ] && [ -n "${MK_VER}" ] && [ -n "${CLI_VER}" ] && [ -n "${DOC_VER}" ] || die "版本号有一处抽取为空"
[ "${GO_VER}" = "${MK_VER}" ] || die "version.go(${GO_VER}) ≠ make print-version(${MK_VER})"
[ "${GO_VER}" = "${CLI_VER}" ] || die "version.go(${GO_VER}) ≠ eg --version(${CLI_VER})"
[ "${GO_VER}" = "${DOC_VER}" ] || die "version.go(${GO_VER}) ≠ 当期决策文档(${DOC_VER})"
[ "${GO_VER}" = "${CUR_VERSION}" ] || die "当前生效版本号 = ${GO_VER}，应逐字 ${CUR_VERSION}"
[ "${GO_VER}" != "0.0.0-dev" ] || die "版本号仍是脚手架值"
# ① 历史基线只读复算：M2 / M3 决策文档仍逐字记录各自当期版本号（历史结论只读）。
[ -f "${DECISION_DOC_M2}" ] || die "缺 M2 历史版本出处：teamwork/${SPEC_REL_M2}"
[ -f "${DECISION_DOC_M3}" ] || die "缺 M3 历史版本出处：teamwork/${SPEC_REL_M3}"
DOC_VER_M2="$(grep -oE '\*\*`[0-9]+\.[0-9]+\.[0-9]+[^`]*`\*\*' "${DECISION_DOC_M2}" | head -1 | tr -d '*`')"
DOC_VER_M3="$(grep -oE '\*\*`[0-9]+\.[0-9]+\.[0-9]+[^`]*`\*\*' "${DECISION_DOC_M3}" | head -1 | tr -d '*`')"
[ "${DOC_VER_M2}" = "0.2.0-m2" ] || die "M2 历史决策文档版本号被改写为 ${DOC_VER_M2}（应恒 0.2.0-m2）"
[ "${DOC_VER_M3}" = "0.3.0-m3" ] || die "M3 历史决策文档版本号被改写为 ${DOC_VER_M3}（应恒 0.3.0-m3）"
# ② 历史值一律不得作为**生效值**残留（逐个旧值对撞，比原来只挡 0.2.0-m2 更严）。
for hv in "${HIST_VERSIONS[@]}"; do
  [ "${GO_VER}" != "${hv}" ] || die "生效版本号仍是历史值 ${hv}"
  [ "${CLI_VER}" != "${hv}" ] || die "eg --version 仍输出历史值 ${hv}"
  if grep -E 'DefaultVersion[[:space:]]*=' "${REPO_ROOT}/internal/version/version.go" | grep -Fq "${hv}"; then
    die "version.go 的 DefaultVersion 赋值行残留历史版本号 ${hv}"
  fi
done
grep -Fq "${GO_VER}" "${REPO_ROOT}/README.md" || die "README 未登记版本号 ${GO_VER}"
grep -Fq "${GO_VER}" "${REPO_ROOT}/INSTALL.md" || die "INSTALL 未登记版本号 ${GO_VER}"
ok "四处逐字相等：${GO_VER}（历史基线 0.2.0-m2 / 0.3.0-m3 只读复算通过，旧值零残留为生效值）"

# ---------------------------------------------------------------- 5. 退出码 6：文档表与实现
step "退出码表与实现一致：INSTALL 有 6 行、白名单恰两条、5 标 M6 已启用（ExitPrecheckOrLock + E15/E16）、ExitNeedConfirm 恰 3 个文件"
N6=$({ grep -c "^| *6 *|" "${REPO_ROOT}/INSTALL.md" || true; } | tail -1)
[ "${N6}" -ge 1 ] || die "INSTALL.md 退出码表缺 6 行"
ROW6="$(grep -m1 "^| *6 *|" "${REPO_ROOT}/INSTALL.md")"
for c in "eg proposal approve" "eg delete"; do
  printf '%s' "${ROW6}" | grep -qF "${c}" || die "退出码 6 行未写出白名单命令 ${c}"
done
ROW5="$(grep -m1 "^| *5 *|" "${REPO_ROOT}/INSTALL.md" || true)"
# ── C2a·M6 现态重钉（§17.1/§17.2 R7 定稿授权；保留历史事实 + 新增现态双侧锁，加严非放宽）──
# 历史事实（M4 及以前）：退出码 5「未启用」。M6·T-074（A-58 / §17）把 5 落地为常量 ExitPrecheckOrLock、
# ExitCode5Enabled() 由恒 false 改恒 true，故 INSTALL 退出码表第 5 行现态**必须**逐字标注「启用」，
# 逐字给出常量名 ExitPrecheckOrLock 与两类成因码 E15 / E16（写前强校验失败 / 锁不可用），
# 且**绝不再**出现「未启用」（旧字样零残留为生效值）。
[ -n "${ROW5}" ] || die "INSTALL.md 退出码表缺第 5 行（M6·T-074 已启用退出码 5）"
printf '%s' "${ROW5}" | grep -qF "启用" || die "退出码 5 行必须逐字标注 M6 已「启用」"
printf '%s' "${ROW5}" | grep -qF "ExitPrecheckOrLock" || die "退出码 5 行必须逐字给出常量 ExitPrecheckOrLock（§17.1）"
for code in E15 E16; do
  printf '%s' "${ROW5}" | grep -qF "${code}" || die "退出码 5 行必须逐字给出成因码 ${code}（§17）"
done
if printf '%s' "${ROW5}" | grep -qF "未启用"; then
  die "退出码 5 行不得再标「未启用」：M6·T-074 已启用（旧字样必须零残留）"
fi
N_FILES=$({ grep -rl "ExitNeedConfirm" "${REPO_ROOT}/internal/cli/" --include='*.go' || true; } | { grep -v '_test.go' || true; } | wc -l | tr -d ' ')
[ "${N_FILES}" = "3" ] || die "ExitNeedConfirm 落在 ${N_FILES} 个非测试文件，期望恰 3（delete.go / exitcode.go / proposal_approve.go）"
ok "文档表 6 行 + 白名单两条 + 5 标未启用；ExitNeedConfirm 恰 3 个非测试文件"

# ---------------------------------------------------------------- 6. 构建 sandbox
step "构建仓库副本 sandbox（文档命令只在副本内执行，真实工作区零污染）"
mkdir -p "${SRC_DIR}" "${VAULT}"
# 运行期产物（.tests-staging/、tests/_report/）与"目标目录在源树内"的递归
# 统一由 eg_snapshot_worktree 排除（tests/lib/common.sh）。
eg_snapshot_worktree "${SRC_DIR}"
mkdir -p "${SANDBOX}/teamwork/projects/evergreen/s1_main_flow/docs"
cp -a "${EG_CONTRACTS}/projects/evergreen/s1_main_flow/docs/specs" \
      "${SANDBOX}/teamwork/projects/evergreen/s1_main_flow/docs/specs"
[ -f "${SRC_DIR}/Makefile" ] && [ -f "${SRC_DIR}/skill/SKILL.md" ] || die "sandbox 副本不完整"
ok "sandbox 就绪：${SRC_DIR}"

# ---------------------------------------------------------------- 7. 抽取三份文档的 bash 命令
step '从 README.md / INSTALL.md / skill/SKILL.md 的 bash 代码块机械抽取命令'
extract() { # $1=文档 $2=输出
  awk '/^```bash$/ { inblk = 1; next } /^```/ { inblk = 0; next } inblk { print }' "$1" >>"$2"
}
: >"${WORK}/cmds.all"
extract "${REPO_ROOT}/README.md" "${WORK}/cmds.all"
extract "${REPO_ROOT}/INSTALL.md" "${WORK}/cmds.all"
extract "${REPO_ROOT}/skill/SKILL.md" "${WORK}/cmds.all"
N=$({ grep -cE '^(make |go |\./bin/eg |eg |gofmt|sha256sum)' "${WORK}/cmds.all" || true; } | tail -1)
N_EXPECT=$({ grep -cE '#[[:space:]]*expect:[[:space:]]*[0-9]+' "${WORK}/cmds.all" || true; } | tail -1)
printf '  抽取到 %d 条命令（其中 %d 条带 # expect: N 标注）\n' "${N}" "${N_EXPECT}"
[ "${N}" -ge 30 ] || die "抽取到的命令仅 ${N} 条（< 30）：代码块被删空？"
[ "${N_EXPECT}" -ge 1 ] || die "没有任何 # expect: N 标注：非 0 退出码的文档示例未被覆盖"
# 自指入口（make test* / tests/run.sh / tests/ci/pipeline.sh）单列：在场即验，不递归执行。
N_SELF=$({ grep -cE "${EG_DOC_SELFREF_RE}" "${WORK}/cmds.all" || true; } | tail -1)
while IFS= read -r sc; do
  [ -n "${sc}" ] || continue
  eg_doc_selfref_assert "${sc}" || die "自指入口校验失败：${sc}"
done < <(grep -E "${EG_DOC_SELFREF_RE}" "${WORK}/cmds.all" || true)
ok "抽取到 ${N} 条命令，${N_EXPECT} 条带预期退出码标注，${N_SELF} 条自指测试入口（只验在场）"

# ---------------------------------------------------------------- 8. 逐条实跑（README → INSTALL → SKILL）
step "逐条实跑：退出码与文档标注逐条一致（未标注即 0）"
{
  printf '%s\n' 'set -u'
  printf '%s\n' 'PASSED=0'
  printf '%s\n' 'run() { want="$2"; printf "  $ %s\n" "$1"; c=0; eval "$1" >/dev/null 2>&1 || c=$?;'
  printf '%s\n' '  if [ "$c" != "$want" ]; then printf "  [FAIL] 退出码 %s，文档标注 %s：%s\n" "$c" "$want" "$1" >&2; exit 1; fi;'
  printf '%s\n' '  PASSED=$((PASSED+1)); }'
  printf '%s\n' 'ctx() { eval "$1" >/dev/null 2>&1 || { printf "  [FAIL] 文档上下文行失败：%s\n" "$1" >&2; exit 1; }; }'
  while IFS= read -r line; do
    case "${line}" in
      ''|'#'*) continue ;;
    esac
    want=0
    case "${line}" in
      *'# expect:'*) want="$(printf '%s' "${line}" | sed -E 's/.*#[[:space:]]*expect:[[:space:]]*([0-9]+).*/\1/')" ;;
    esac
    esc=${line//\\/\\\\}
    esc=${esc//\"/\\\"}
    if printf '%s' "${line}" | grep -qE "${EG_DOC_SELFREF_RE}"; then
      continue  # 自指入口：已单独验过"在场"，不在沙箱内递归跑整套测试
    elif printf '%s' "${line}" | grep -qE '^(make |go |\./bin/eg |eg |gofmt|sha256sum)'; then
      printf 'run "%s" %s\n' "${esc}" "${want}"
    else
      printf 'ctx "%s"\n' "${esc}"
    fi
  done <"${WORK}/cmds.all"
  printf '%s\n' 'printf "  文档命令全部符合预期退出码，共 %d 条\n" "$PASSED"'
} >"${WORK}/run_docs.sh"

# SKILL.md §8.6 的前置：在同一个 vault 里补两张知识卡与一份材料笔记（INSTALL 第 3 节已 init）。
seed_vault() {
  mkdir -p "${VAULT}/${KDIR_REL}" "${VAULT}/${NDIR_REL}" "${VAULT}/sources"
  cat >"${VAULT}/sources/${SEED_SRC}.md" <<SRC_EOF
---
id: ${SEED_SRC}
url: https://example.com/attention
title: 注意力机制原文
saved_at: '2026-09-01T09:00:00+08:00'
---

# 注意力机制原文

正文占位（收录后不因加工改写）。
SRC_EOF
  cat >"${VAULT}/${NDIR_REL}/${NOTE}.md" <<NOTE_EOF
---
id: ${NOTE}
source: ${SEED_SRC}
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
---

# 注意力机制材料笔记

## 材料提炼

提炼占位。

## Agent 分析

分析占位。

## 用户补充

## 存疑与待验证

## 产出知识卡

- ${CARD}
NOTE_EOF
  for id in "${CARD}" "${CARD2}"; do
    cat >"${VAULT}/${KDIR_REL}/${id}.md" <<CARD_EOF
---
id: ${id}
title: 注意力机制
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: ${SEED_SRC}
    note: ${NOTE}
    rel: support
    reason: 该结论由这份材料支持
---

# 注意力机制

## 知识内容

正文占位。

## 解释与依据

依据占位。

## 条件与边界

边界占位。

## 用户补充

## 理解自检

- 自检问题占位？
CARD_EOF
  done
  git -C "${VAULT}" add -A >/dev/null
  git -C "${VAULT}" -c user.name=eg-e2e -c user.email=eg-e2e@example.com \
    commit -q -m "seed: m3 文档示例语料"
}

# 真正的执行：把 seed 插在 SKILL 段之前。
SKILL_MARK='run "eg --vault \"$VAULT\" card show \"$CARD\"" 0'
if ! grep -Fq "${SKILL_MARK}" "${WORK}/run_docs.sh"; then
  die "未在生成脚本里定位到 SKILL 段起点（文档示例首行应为 eg card show）"
fi
# 注意：不能用 awk -v 传 mark（-v 赋值会解释 \" 转义），改走 ENVIRON 逐字取值。
SKILL_MARK="${SKILL_MARK}" awk '
  $0 == ENVIRON["SKILL_MARK"] { print "seed_vault || { printf \"  [FAIL] seed_vault 失败\\n\" >&2; exit 1; }"; hit = 1 }
  { print }
  END { if (!hit) { print "未能插入 seed_vault（mark 未逐字命中）" > "/dev/stderr"; exit 1 } }
' "${WORK}/run_docs.sh" >"${WORK}/run_docs_seeded.sh" || die "seed 注入失败"

export -f seed_vault
( cd "${SRC_DIR}" && VAULT="${VAULT}" CARD="${CARD}" CARD2="${CARD2}" NOTE="${NOTE}" \
  KDIR_REL="${KDIR_REL}" NDIR_REL="${NDIR_REL}" SEED_SRC="${SEED_SRC}" \
  GIT_AUTHOR_NAME="${GIT_AUTHOR_NAME:-eg-e2e}" GIT_AUTHOR_EMAIL="${GIT_AUTHOR_EMAIL:-eg-e2e@example.com}" \
  GIT_COMMITTER_NAME="${GIT_COMMITTER_NAME:-eg-e2e}" GIT_COMMITTER_EMAIL="${GIT_COMMITTER_EMAIL:-eg-e2e@example.com}" \
  bash "${WORK}/run_docs_seeded.sh" </dev/null ) || die "文档命令未能全部按标注退出"
ok "README / INSTALL / SKILL.md 的每条命令均按文档标注的退出码执行"

# ---------------------------------------------------------------- 9. 退出码 6 的实测语义
step "实测退出码 6：approved 提案 + 缺 --confirm → 退 6，且权威 Markdown 完全不变"
egv() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
PID2="$(egv proposal new --type logical_delete --target "${CARD2}" --reason '重复卡，保留另一张' --json |
  grep -oE 'p-[0-9]{8}-[0-9]{3}' | head -1)"
[ -n "${PID2}" ] || die "proposal new 未返回提案 ID"
egv proposal approve "${PID2}" --confirm --user-request >/dev/null || die "approve 未退 0"
HASH_BEFORE="$(sha256sum "${VAULT}/${KDIR_REL}/${CARD2}.md" | cut -d' ' -f1)"
LOG_BEFORE="$(git -C "${VAULT}" log --oneline | wc -l | tr -d ' ')"
PORC_BEFORE="$(git -C "${VAULT}" status --porcelain | wc -l | tr -d ' ')"
CODE=0
egv delete --target "${CARD2}" --reason '重复卡，保留另一张' --proposal "${PID2}" --user-request >/dev/null 2>&1 || CODE=$?
[ "${CODE}" = "6" ] || die "缺 --confirm 的 delete 退出码 = ${CODE}，期望 6"
[ "$(sha256sum "${VAULT}/${KDIR_REL}/${CARD2}.md" | cut -d' ' -f1)" = "${HASH_BEFORE}" ] ||
  die "退 6 后目标字节变了：权威 Markdown 必须完全不变"
[ "$(git -C "${VAULT}" log --oneline | wc -l | tr -d ' ')" = "${LOG_BEFORE}" ] || die "退 6 不得产生 commit"
[ "$(git -C "${VAULT}" status --porcelain | wc -l | tr -d ' ')" = "${PORC_BEFORE}" ] || die "退 6 后 porcelain 计数变了"
# 参数错先于缺确认：同一条命令去掉 --target → 退 1（顺序 1 → 2 → 6）
CODE=0
egv delete --reason x --proposal "${PID2}" --confirm --user-request >/dev/null 2>&1 || CODE=$?
[ "${CODE}" = "1" ] || die "参数错应先于缺确认判定（期望 1，实得 ${CODE}）"
ok "退 6 时零写入零 commit、字节不变；参数错仍先判 1（顺序 1 → 2 → 6）"

# ---------------------------------------------------------------- 10. 工作区零污染
step "真实仓库工作区零污染（一切写操作只发生在 mktemp 目录内）"
AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
if [ "${BEFORE_STATUS}" != "${AFTER_STATUS}" ]; then
  printf '  before:\n%s\n  after:\n%s\n' "${BEFORE_STATUS}" "${AFTER_STATUS}" >&2
  die "脚本运行后仓库脏文件集合发生变化"
fi
ok "git status --porcelain 前后一致，未新增任何脏文件"

printf '\n全部 %d 组断言通过（共 %d 步；文档命令 %d 条）。\n' "${PASS}" "${STEP}" "${N}"

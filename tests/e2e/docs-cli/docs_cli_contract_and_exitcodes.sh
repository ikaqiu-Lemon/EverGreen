#!/usr/bin/env bash
# 文档可执行性端到端脚本（T-evergreen.s1_main_flow-158614-027）。
#
# 判据来源：
#   里程碑 `M-002-m2.md` 风险 R-2（README 误述与无效命令 `eg version`）
#                              R-3（版本仍 0.0.0-dev / 无 tag / 无远端 / 无安装说明）
#                              R-4（darwin 产物仅交叉编译、未经真机运行验证）
#   版本决策文档 `2026-10-03-m2-release-and-version.md` §1（版本号三处同源）/ §3（远端状态）/ §4（验证矩阵）
#   CLI 合同 `2026-09-01-eg-cli-contract.md` §1（九命令参数表）/ §4（退出码）
#
# 四组断言（Acceptance 逐条对应）：
#   ① 误述已清除：README 零「脚手架」、两份文档零 `eg version` 无效示例、`--version` 在场
#   ② 文档里每条命令可实跑：从 README / INSTALL 的 ```bash 代码块机械抽取命令，逐条执行，
#      退出码全为 0；打印「抽取到 N 条命令」并断言 N ≥ 8（防止删空代码块变绿）
#   ③ 版本号三处同源 + 无效命令反证 + --version 输出完整
#   ④ 产物校验步骤真实有效（四产物 + SHA256SUMS）+ darwin 限制已登记 + 远端 / tag 结论有据
#
# 约束：离线、可重复执行、无外部依赖（只用 bash / coreutils / git / make / go）；
# **一切写操作只发生在 mktemp -d 目录内**——文档命令在仓库副本（sandbox）里执行，
# 真实仓库工作区零污染（脚本末尾自查 git status）。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/docs-cli/docs_cli_contract_and_exitcodes.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
PARENT="$(cd "${REPO_ROOT}/.." && pwd)"
# 版本口径自 M3 起由 T-…-046 的决策文档接管（M2 的 2026-10-03-… 只读保留为历史出处）。
#
# 2026-09-08 随 M5 · T-…-069 **阶段化重钉**（只改形态，R-3 的结论一格不放宽、不删断言）：
# 版本号是**逐里程碑推进**的事实（M2 `0.2.0-m2` → M3 `0.3.0-m3` → M4 `0.4.0-m4` →
# M5 `0.5.0-m5`），每个里程碑的决策文档各自是**当期**唯一出处。原脚本把「M3 决策文档」
# 写死成「当前版本的唯一出处」，M5 推进后必然自红 —— 红的是**指针**，不是 R-3 的判据本体
# （「Makefile / version.go / 当期决策文档三处同源」）。故拆成两侧：
#   ① 历史基线（**只读**，一字未动）：M3 决策文档必须仍逐字记录 `0.3.0-m3`，M2 的历史出处
#      必须仍逐字记录 `0.2.0-m2` —— 历史结论不许被后续里程碑改写；
#   ② 当前值（正面断言，新增等号 = 加严）：Makefile / version.go / **当期**决策文档
#      三处同源且逐字 `0.5.0-m5`，README / INSTALL 均已登记；且历史版本号一律**不得**
#      作为当前生效值残留在 Makefile / version.go 里（逐个旧值对撞）；
#      再加版本链**严格单调递增**一格：历史基线 < 当前值（回退版本号即红）。
SPEC_REL_M2="projects/evergreen/s1_main_flow/docs/specs/2026-10-03-m2-release-and-version.md"
SPEC_REL_M3="projects/evergreen/s1_main_flow/docs/specs/2026-11-08-m3-release-and-version.md"
# 当期（knowledge_opinion_split）版本决策文档：本脚本比对的「当前版本号」唯一出处。
# 阶段化重钉（沿用「当期指针随里程碑走」口径，非放宽）：本 Epic 把版本推进到 0.7.0-m7，
# 故 SPEC_REL 指向本 Epic §0.0 版本口径出处、CUR_VERSION 抬为 0.7.0-m7、
# 0.6.0-m6 降为历史基线（须 < 当前且不得作为生效值残留）；M2/M3 历史基线只读一字未动。
SPEC_REL="projects/evergreen/knowledge_opinion_split/docs/specs/2026-09-15-knowledge-opinion-schema-v2-design.md"
DECISION_DOC="${EG_CONTRACTS}/${SPEC_REL}"
DECISION_DOC_M2="${EG_CONTRACTS}/${SPEC_REL_M2}"
DECISION_DOC_M3="${EG_CONTRACTS}/${SPEC_REL_M3}"
# 历史里程碑版本号基线（逐字，只读；顺序即里程碑顺序）与当期值。
HIST_VERSIONS=("0.2.0-m2" "0.3.0-m3" "0.4.0-m4" "0.5.0-m5" "0.6.0-m6")
CUR_VERSION="0.7.0-m7"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m2-docs.XXXXXX")"
SANDBOX="${WORK}/sandbox"
SRC="${SANDBOX}/evergreen"
VAULT="${WORK}/vault"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

BEFORE_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 误述已清除（R-2）
step "误述已清除：README 零「脚手架」、零无效命令 eg version、--version 在场"
[ -f "${REPO_ROOT}/INSTALL.md" ] || die "缺 INSTALL.md"
if grep -n "脚手架" "${REPO_ROOT}/README.md"; then die "README.md 仍出现「脚手架」"; fi
if grep -nE '(^|[^-])\beg version\b|bin/eg version' "${REPO_ROOT}/README.md" "${REPO_ROOT}/INSTALL.md"; then
  die "文档仍示范无效命令 eg version（真实入口是 eg --version）"
fi
N_VER=$(grep -c -- "--version" "${REPO_ROOT}/README.md" || true)
[ "${N_VER}" -ge 1 ] || die "README.md 未出现 --version"
ok "README 零「脚手架」、两份文档零 eg version、--version 命中 ${N_VER} 处"

# ---------------------------------------------------------------- 2. 版本号三处同源（R-3）
step "版本号三处同源：Makefile / internal/version/version.go / 当期版本决策文档（+ 历史基线只读复算）"
MK_VER="$(grep -E '^VERSION[[:space:]]*\?=' "${REPO_ROOT}/Makefile" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+[^ ]*' | head -1)"
GO_VER="$(grep -E 'DefaultVersion[[:space:]]*=' "${REPO_ROOT}/internal/version/version.go" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+[^" ]*' | head -1)"
[ -f "${DECISION_DOC}" ] || die "缺当期版本决策文档：teamwork/${SPEC_REL}"
DOC_VER="$(grep -oE '\*\*`[0-9]+\.[0-9]+\.[0-9]+[^`]*`\*\*' "${DECISION_DOC}" | head -1 | tr -d '*`')"
printf '  Makefile=%s\n  version.go=%s\n  当期决策文档=%s\n' "${MK_VER}" "${GO_VER}" "${DOC_VER}"
[ -n "${MK_VER}" ] && [ -n "${GO_VER}" ] && [ -n "${DOC_VER}" ] || die "三处版本号有一处抽取为空"
[ "${MK_VER}" = "${GO_VER}" ] || die "Makefile(${MK_VER}) 与 version.go(${GO_VER}) 不同源"
[ "${MK_VER}" = "${DOC_VER}" ] || die "Makefile(${MK_VER}) 与当期决策文档(${DOC_VER}) 不同源"
[ "${MK_VER}" = "${CUR_VERSION}" ] ||
  die "当前生效版本号 = ${MK_VER}，应逐字 ${CUR_VERSION}（当期决策文档 §5 是唯一出处）"
[ "${MK_VER}" != "0.0.0-dev" ] || die "版本号仍是 0.0.0-dev"
grep -Fq "${MK_VER}" "${REPO_ROOT}/README.md" || die "README 未登记版本号 ${MK_VER}"
grep -Fq "${MK_VER}" "${REPO_ROOT}/INSTALL.md" || die "INSTALL 未登记版本号 ${MK_VER}"
# ① 历史基线只读复算：M2 / M3 的决策文档必须仍逐字记录**当期各自**的版本号。
[ -f "${DECISION_DOC_M2}" ] || die "缺 M2 历史版本出处：teamwork/${SPEC_REL_M2}"
[ -f "${DECISION_DOC_M3}" ] || die "缺 M3 历史版本出处：teamwork/${SPEC_REL_M3}"
DOC_VER_M2="$(grep -oE '\*\*`[0-9]+\.[0-9]+\.[0-9]+[^`]*`\*\*' "${DECISION_DOC_M2}" | head -1 | tr -d '*`')"
DOC_VER_M3="$(grep -oE '\*\*`[0-9]+\.[0-9]+\.[0-9]+[^`]*`\*\*' "${DECISION_DOC_M3}" | head -1 | tr -d '*`')"
[ "${DOC_VER_M2}" = "0.2.0-m2" ] ||
  die "M2 历史决策文档的版本号被改写为 ${DOC_VER_M2}（历史结论只读，应恒 0.2.0-m2）"
[ "${DOC_VER_M3}" = "0.3.0-m3" ] ||
  die "M3 历史决策文档的版本号被改写为 ${DOC_VER_M3}（历史结论只读，应恒 0.3.0-m3）"
# ② 历史版本号不得作为**当前生效值**残留（逐个旧值对撞；文档里的历史叙述不受影响）。
for hv in "${HIST_VERSIONS[@]}"; do
  [ "${MK_VER}" != "${hv}" ] || die "Makefile 的生效版本号仍是历史值 ${hv}"
  [ "${GO_VER}" != "${hv}" ] || die "version.go 的生效版本号仍是历史值 ${hv}"
  if grep -E '^VERSION[[:space:]]*\?=' "${REPO_ROOT}/Makefile" | grep -Fq "${hv}"; then
    die "Makefile 的 VERSION 赋值行残留历史版本号 ${hv}"
  fi
  if grep -E 'DefaultVersion[[:space:]]*=' "${REPO_ROOT}/internal/version/version.go" | grep -Fq "${hv}"; then
    die "version.go 的 DefaultVersion 赋值行残留历史版本号 ${hv}"
  fi
done
# ③ 版本链严格单调递增：历史基线逐个 < 当前值（回退版本号即红）。
LOWEST="$(printf '%s\n%s\n' "${DOC_VER_M2}" "${CUR_VERSION}" | sort -V | head -1)"
[ "${LOWEST}" = "${DOC_VER_M2}" ] || die "版本链非递增：${DOC_VER_M2} 不小于当前 ${CUR_VERSION}"
LOWEST="$(printf '%s\n%s\n' "${DOC_VER_M3}" "${CUR_VERSION}" | sort -V | head -1)"
[ "${LOWEST}" = "${DOC_VER_M3}" ] || die "版本链非递增：${DOC_VER_M3} 不小于当前 ${CUR_VERSION}"
ok "三处逐字相等：${MK_VER}（历史基线 0.2.0-m2 / 0.3.0-m3 只读复算通过，旧值零残留为生效值，版本链递增）"

# ---------------------------------------------------------------- 3. 构建 sandbox
step "构建仓库副本 sandbox（文档命令只在副本内执行，真实工作区零污染）"
mkdir -p "${SRC}" "${VAULT}"
# 运行期产物（.tests-staging/、tests/_report/）与"目标目录在源树内"的递归
# 统一由 eg_snapshot_worktree 排除（tests/lib/common.sh）。
eg_snapshot_worktree "${SRC}"
# 单测会只读引用 ../../../teamwork/**/docs/specs/：按同级布局复制该子树。
mkdir -p "${SANDBOX}/teamwork/projects/evergreen/s1_main_flow/docs"
cp -a "${EG_CONTRACTS}/projects/evergreen/s1_main_flow/docs/specs" \
      "${SANDBOX}/teamwork/projects/evergreen/s1_main_flow/docs/specs"
[ -f "${SRC}/Makefile" ] && [ -f "${SRC}/README.md" ] || die "sandbox 副本不完整"
ok "sandbox 就绪：${SRC}（含只读 teamwork specs 子树）"

# ---------------------------------------------------------------- 4. 机械抽取文档命令
step "从 README.md / INSTALL.md 的 bash 代码块机械抽取命令"
# 抽取规则（逐字实现 Acceptance）：
#   - 只看 ```bash 围栏内的行；行内注释保留（原样执行）；
#   - **计入 N** 的是以 make / go / ./bin/eg / eg / gofmt / sha256sum 开头的命令行；
#   - 代码块内的其它行（变量赋值 / mkdir / cp / chmod / printf / cd / export）同样**照序执行**，
#     以保证文档给出的上下文（CWD、PATH、$VAULT）与使用者照抄时一致；它们不计入 N。
extract() { # $1=文档路径 $2=输出脚本
  awk -v out="$2" '
    /^```bash$/ { inblk = 1; next }
    /^```/      { inblk = 0; next }
    inblk       { print > out }
  ' "$1"
}
: >"${WORK}/cmds.README"
: >"${WORK}/cmds.INSTALL"
extract "${REPO_ROOT}/README.md" "${WORK}/cmds.README"
extract "${REPO_ROOT}/INSTALL.md" "${WORK}/cmds.INSTALL"
cat "${WORK}/cmds.README" "${WORK}/cmds.INSTALL" >"${WORK}/cmds.all"
N=$(grep -cE '^(make |go |\./bin/eg |eg |gofmt|sha256sum)' "${WORK}/cmds.all" || true)
# 自指入口（make test* / tests/run.sh / tests/ci/pipeline.sh）单列：在场即验，不在沙箱内递归执行
# （见 tests/lib/common.sh 的 EG_DOC_SELFREF_RE；否则文档测试会把全量测试再跑一遍）。
N_SELF=$({ grep -cE "${EG_DOC_SELFREF_RE}" "${WORK}/cmds.all" || true; } | tail -1)
[ "${N_SELF}" -ge 1 ] || die "文档里应至少给出 1 条测试入口命令（make test / tests/run.sh），实得 ${N_SELF}"
while IFS= read -r sc; do
  eg_doc_selfref_assert "${sc}" || die "自指入口校验失败：${sc}"
done < <(grep -E "${EG_DOC_SELFREF_RE}" "${WORK}/cmds.all")
TOTAL=$(grep -cve '^[[:space:]]*$' "${WORK}/cmds.all" || true)
printf '  抽取到 %d 条命令（代码块内可执行行共 %d 行）\n' "${N}" "${TOTAL}"
[ "${N}" -ge 8 ] || die "抽取到的命令仅 ${N} 条，少于 8 条（代码块被删空？）"
ok "抽取到 ${N} 条命令（≥ 8），其中 ${N_SELF} 条为自指测试入口（只验在场），另有 $((TOTAL - N)) 行上下文行一并执行"

# ---------------------------------------------------------------- 5. 逐条实跑
step "逐条实跑文档命令，断言退出码全为 0"
{
  printf '%s\n' 'set -u'
  printf '%s\n' 'PASSED=0'
  printf '%s\n' 'run() { printf "  $ %s\n" "$1"; if eval "$1"; then PASSED=$((PASSED+1)); else'
  printf '%s\n' '  printf "  [FAIL] 文档命令退出码非 0：%s\n" "$1" >&2; exit 1; fi; }'
  printf '%s\n' 'ctx() { eval "$1" || { printf "  [FAIL] 文档上下文行失败：%s\n" "$1" >&2; exit 1; }; }'
  while IFS= read -r line; do
    case "${line}" in
      ''|'#'*) continue ;;
    esac
    esc=${line//\\/\\\\}
    esc=${esc//\"/\\\"}
    if printf '%s' "${line}" | grep -qE "${EG_DOC_SELFREF_RE}"; then
      continue  # 自指入口：已在上一步单独验过"在场"，不在沙箱内递归执行
    elif printf '%s' "${line}" | grep -qE '^(make |go |\./bin/eg |eg |gofmt|sha256sum)'; then
      printf 'run "%s"\n' "${esc}"
    else
      printf 'ctx "%s"\n' "${esc}"
    fi
  done <"${WORK}/cmds.all"
  printf '%s\n' 'printf "  文档命令全部退 0，共 %d 条\n" "$PASSED"'
} >"${WORK}/run_docs.sh"

( cd "${SRC}" && VAULT="${VAULT}" \
  GIT_AUTHOR_NAME="${GIT_AUTHOR_NAME:-eg-e2e}" GIT_AUTHOR_EMAIL="${GIT_AUTHOR_EMAIL:-eg-e2e@example.com}" \
  GIT_COMMITTER_NAME="${GIT_COMMITTER_NAME:-eg-e2e}" GIT_COMMITTER_EMAIL="${GIT_COMMITTER_EMAIL:-eg-e2e@example.com}" \
  bash "${WORK}/run_docs.sh" </dev/null ) || die "文档命令未能全部退 0"
ok "README 与 INSTALL 的每条命令均实跑成功（退 0）"

# ---------------------------------------------------------------- 6. 无效命令反证
step "无效命令反证：./bin/eg version 退非 0，./bin/eg --version 退 0 且含版本号"
[ -x "${SRC}/bin/eg" ] || die "文档流程未产出 bin/eg"
if ( cd "${SRC}" && ./bin/eg version >/dev/null 2>&1 </dev/null ); then
  die "./bin/eg version 竟然退 0：它不是有效命令"
fi
OUT="$( cd "${SRC}" && ./bin/eg --version </dev/null )" || die "./bin/eg --version 退非 0"
printf '  %s\n' "${OUT}"
printf '%s' "${OUT}" | grep -Fq "${MK_VER}" || die "--version 输出不含版本号 ${MK_VER}"
printf '%s' "${OUT}" | grep -q "commit " || die "--version 输出缺 commit 段"
printf '%s' "${OUT}" | grep -q "built " || die "--version 输出缺构建时间段"
ok "eg version 退非 0；eg --version 退 0 且含版本号 / commit / 构建时间三段"

# ---------------------------------------------------------------- 7. 首次上手可用
step "安装说明可用：INSTALL「首次上手」在临时 vault 里真的跑通了"
[ -f "${VAULT}/evergreen.yml" ] || die "eg init 未在临时 vault 写出 evergreen.yml"
[ -f "${VAULT}/SKILL.md" ] || die "eg init 未落盘 SKILL.md"
cmp "${VAULT}/SKILL.md" "${REPO_ROOT}/skill/SKILL.md" || die "落盘 SKILL.md 与源文件字节不同"
grep -q "default_domain: ai-infra" "${VAULT}/evergreen.yml" || die "config set default_domain 未生效"
ls "${VAULT}/sources" >/dev/null 2>&1 || die "eg capture 未产出 sources/"
[ -z "$(git -C "${VAULT}" status --porcelain)" ] || die "首次上手后 vault 工作区应干净"
ok "eg init + config set default_domain + eg capture 全部生效（vault 干净）"

# ---------------------------------------------------------------- 8. 产物校验步骤真实有效
step "make release 产物：四个 + SHA256SUMS 校验退 0 + 无 0.0.0-dev"
( cd "${SRC}" && make release >/dev/null 2>&1 ) || die "make release 失败"
CNT=$(ls "${SRC}"/dist/eg_* | wc -l | tr -d ' ')
[ "${CNT}" = "4" ] || die "dist/eg_* 产物 ${CNT} 个，期望 4"
( cd "${SRC}/dist" && sha256sum -c SHA256SUMS ) || die "sha256sum -c SHA256SUMS 校验失败"
for f in "${SRC}"/dist/eg_*; do
  n=$(grep -c "0.0.0-dev" "${f}" || true)
  [ "${n}" = "0" ] || die "$(basename "${f}") 内仍含 0.0.0-dev"
done
"${SRC}/dist/eg_$(go env GOOS)_$(go env GOARCH)" --version </dev/null | grep -Fq "${MK_VER}" ||
  die "本机平台产物的 --version 不含 ${MK_VER}"
ok "四平台产物齐备、SHA256SUMS 全 OK、产物内零 0.0.0-dev"

# ---------------------------------------------------------------- 9. darwin 限制已登记（R-4）
step "darwin 限制已登记：未经真机运行验证 / 未做，且不得声称已验证 macOS"
grep -nE "未经真机运行验证|未做" "${REPO_ROOT}/INSTALL.md" >/dev/null ||
  die "INSTALL.md 未登记 darwin 运行验证限制"
awk '/darwin/ && (/未经真机运行验证/ || /未做/) { hit = 1 } END { exit hit ? 0 : 1 }' \
  "${REPO_ROOT}/INSTALL.md" || die "darwin 与「未经真机运行验证 / 未做」不在同一段落"
if grep -nE "已在 macOS (验证|测试)|已验证 macOS" "${REPO_ROOT}/INSTALL.md" "${REPO_ROOT}/README.md"; then
  die "文档不得声称已验证 macOS"
fi
ok "darwin 两平台登记为「仅交叉编译 / 运行验证未做」，无「已验证 macOS」措辞"

# ---------------------------------------------------------------- 10. 公开仓库文档自足
step "公开仓库文档不依赖私有计划仓，也不保留远端占位符"
REMOTES="$(git -C "${REPO_ROOT}" remote -v || true)"
TAGS="$(git -C "${REPO_ROOT}" tag || true)"
printf '  git remote -v -> %s\n' "${REMOTES:-<空输出>}"
printf '  git tag       -> %s\n' "${TAGS:-<空输出>}"
if grep -rnE '(\.\./)?teamwork/projects/|<owner>|<repository>' \
  "${REPO_ROOT}/README.md" "${REPO_ROOT}/INSTALL.md"; then
  die "README / INSTALL 含私有计划仓引用或未替换的远端占位符"
fi
ok "README / INSTALL 自足；是否已配置 remote 或 tag 不影响文档正确性"

# ---------------------------------------------------------------- 11. 工作区零污染
step "真实仓库工作区零污染（一切写操作只发生在 mktemp 目录内）"
AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
if [ "${BEFORE_STATUS}" != "${AFTER_STATUS}" ]; then
  printf '  before:\n%s\n  after:\n%s\n' "${BEFORE_STATUS}" "${AFTER_STATUS}" >&2
  die "脚本运行后仓库脏文件集合发生变化"
fi
ok "git status --porcelain 前后一致，未新增任何脏文件"

printf '\n全部 %d 组断言通过（共 %d 步；文档命令 %d 条）。\n' "${PASS}" "${STEP}" "${N}"

#!/usr/bin/env bash
# D4 审计：构建 / 分发 / 文档 / 平台与版本面（system_assurance · T-…-012）。
#
# 判据来源（声明面）：
#   `evergreen/Makefile`（build / dist / release / clean / print-version 五个目标的自述）；
#   `evergreen/INSTALL.md` §1 构建 / §2 产物与溯源口径 / §3 首次上手 / §4 用户闸门链 /
#     §4.1 对账与可见性 / §5 已知限制表（darwin 两支与 linux/arm64 的运行验证结论）/
#     「`.eg/` 状态目录与 vault 的关系」三条声明；
#   `evergreen/README.md` §"make 目标" 与 §"不引入 cgo" / 平台反向说明；
#   `2027-02-21-m6-release-and-version.md`（版本 0.6.0-m6 唯一决策出处）。
#
# 为什么需要这支 suite：D4 审计（审计方法合同 `2027-03-10-system-audit-method-contract.md`
# §2 三面对照）发现下列条目「声明面写了、实现面实测一致、但当前测试体系没有任何 suite
# 直接锁死」。既有 docs-cli 五支只守「命令名集合 / 版本号多处同真 / 内嵌 SKILL 同字节 /
# 四产物齐全 + sha256 -c」，**不覆盖**：commit 溯源机制、静态链接与 CGO 关闭的产物级证据、
# `make build ⊃ dist` 与 `make dist` 不产 bin、`make clean` 语义、离线（GOPROXY=off）可构建、
# INSTALL §3/§4/§4.1 三段脚本逐行照抄的退出码、`.eg/` 与 `.index/` 的忽略机制、
# 以及「平台支持范围只许表述为交叉编译」的措辞在册。按合同 §4「不得以差异代替修复」的
# 同款纪律，一致条目也必须落成可执行判据，否则下一轮回归无从判红。
#
# 本 suite 锁死的判据（真实构建 + 真实二进制 + 真实临时 vault，事实只回读文件字节 /
# git 自己 / `go version -m` / `file(1)`，不看实现自报）：
#   A 构建目标语义：沙箱 `make build` 同时产 `bin/eg` 与四平台 `dist/` + `SHA256SUMS`；
#     `make dist` 单独跑**不产** `bin/eg`；`make clean` 后 `bin/` 与四支产物均不在场。
#   B commit 溯源机制：注入的 `commit` 段逐字 == 沙箱 `git rev-parse --short HEAD`；
#     HEAD 前移后重建，`commit` 段随之改变；同源重建 SHA 变化而 `commit` 段不变
#     （INSTALL §2「跨次构建的一致性锚点是 commit 段，不是 SHA」的正面钉）。
#   C 产物形态：linux 两支为 ELF **statically linked**（x86-64 / aarch64 各自架构）、
#     darwin 两支为 Mach-O（x86_64 / arm64）；linux 两支 `ldd` 为 not a dynamic executable。
#   D 构建设置：四支产物 `go version -m` 均含 `CGO_ENABLED=0` / `-trimpath=true` 与对应
#     `GOOS` / `GOARCH`；`go list -deps ./cmd/eg` 含纯 Go 驱动 `modernc.org/sqlite`、
#     零 cgo 驱动（`mattn/go-sqlite3` 零命中）。
#   E 校验和与溯源：`SHA256SUMS` 恰四行、`sha256sum -c` 四行全 OK；`PROVENANCE.txt`
#     由 `make dist` 现场生成并内嵌同一份 artifact checksums，改动任一产物字节后校验必红。
#   F 离线可构建：预热模块缓存下 `GOPROXY=off CGO_ENABLED=0 go build ./cmd/eg` 成功。
#   G INSTALL §3 首次上手四条命令逐行照抄退 0；vault 侧忽略机制在位：
#     `.git/info/exclude` 含 `.eg/`、`.gitignore` 含 `.index/`、`.eg` 未被 git 跟踪。
#   H INSTALL §4 用户闸门链逐行照抄：mark-reviewed / unreviewed / proposal list / show /
#     approve 全退 0；`delete` 缺 `--confirm` 退 **6** 且目标字节与 commit 数**一格不动**；
#     四条件齐备退 0 并落 `deleted_at`；`search --include-deleted` 退 0；`undelete` 退 0。
#   I INSTALL §4.1 对账与可见性逐行照抄：`check` / `reconcile --dry-run --json` /
#     `reconcile` / `apply --plan -`（文档内联 ChangePlan）/ `rel --include-deprecated` /
#     `card show --include-deprecated` 全退 0。
#   J 版本命令面：`eg --version` 退 0 且逐字含当期版本号；`eg version` 子命令退 **1**
#     （INSTALL 明写「没有 `version` 子命令」）。
#   K 平台支持范围措辞：INSTALL §5 已知限制表逐字在册 darwin 两支「仅交叉编译产出，
#     未经真机运行验证」与 linux/arm64「运行验证未做」；README 保留「不声称 darwin 通过
#     真机验证」的反向说明；本环境 `uname` 唯一原生目标为 linux/amd64（其余三平台的
#     运行结论如实为「未做」，本 suite 不伪造）。
#
# 刻意**不**在本 suite 断言的两条（已登记 issue，判据随对应修复任务落地，避免把缺陷锁成基线）：
#   · 顶层 `eg --help` 退出码行只列 `0`–`4`、缺 `5` / `6` → I-…-012（D1 登记）；
#   · 顶层 `eg --help` 命令区标题写「S1 九命令」却列 22 条、未知命令话术同源 → I-…-013（D1 登记）。
#
# 已修复并转为硬断言的（defect_zeroing 批次 4）：
#   · `dist/PROVENANCE.txt` 由 `make dist` 生成并与 `SHA256SUMS` 绑定；
#   · 空模块缓存 + `GOPROXY=off` 在 CI 前置阶段给出 ENV 指引，不再拖到 make lint/go build 底层错误；
#   · INSTALL §3 把原文写到 vault 外，照抄后 vault 根零 `article.txt` 跟踪；
#   · SKILL §6.2 样例不再复用 PPE 验收文章 `Verification, The Key to AI`；
#   · README / INSTALL / skill/SKILL.md 面向用户的旧 `test/` 根路径零残留。
#
# 已修复并转为硬断言的（defect_zeroing 批次 1）：
#   · `eg bench` 语料提示曾指向已不存在的 `./test/perf/corpus_gen.go`（I-…-027，已 close）——
#     判据落在 `tests/contract/arch-boundary/product_tree_selfcontained.sh` 断言⑤⑥
#     （产品树非注释行零 `tests?/` 路径 + 全文零已删除 `test/` 根引用），本 suite 不重复覆盖。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go / make / file）；
# 一切写与构建只发生在 mktemp -d 目录内，真实仓库工作区零污染（脚本末尾自查）；
# 任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/docs-cli/d4_dist_docs_platform.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 2048

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-d4-dist-docs.XXXXXX")"
SBX="${WORK}/evergreen"        # 仓库副本（一切构建只在这里发生）
EG="${WORK}/eg"                # 从沙箱产物拷出的被测二进制
VAULT="${WORK}/notes/evergreen"

# 当期期望版本号（M6 收口值；唯一决策出处见 teamwork 的 2027-02-21-m6-release-and-version.md §1.1）。
# 写死而非从 version.go 反推，否则一致性判据退化成自证。
WANT_VERSION="0.6.0-m6"

STEP=0
PASS=0
trap 'rm -rf "${WORK}"' EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

command -v file >/dev/null || die "本脚本用 file(1) 判定产物格式与静态链接"
command -v make >/dev/null || die "本脚本驱动真实 make 目标"

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
BEFORE_DIST_SHA="$(cd "${REPO_ROOT}" && { [ -f dist/SHA256SUMS ] && sha256sum dist/SHA256SUMS || echo none; } \
                   && { [ -f dist/PROVENANCE.txt ] && sha256sum dist/PROVENANCE.txt || echo none; })"

# egv <args...>：一律显式传 vault 绝对路径；零交互
egv() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
# rcv <args...>：回显退出码，输出落 out.txt
rcv() { local c=0; egv "$@" >"${WORK}/out.txt" 2>&1 || c=$?; echo "${c}"; }
gv()  { git -C "${VAULT}" "$@"; }
sha() { sha256sum "$1" | awk '{print $1}'; }

# ────────────────────────────────────────────────────────── A / B：构建目标与 commit 溯源
step "沙箱仓库副本 + 独立 git 历史（一切构建只发生在 mktemp 内）"
eg_snapshot_worktree "${SBX}"
[ -f "${SBX}/Makefile" ] || die "沙箱缺 Makefile"
[ ! -e "${SBX}/dist" ] || die "快照不应带入 dist/（helper 已排除）"
(cd "${SBX}" && git init -q . && git add -A \
   && git -c user.name=d4 -c user.email=d4@example.invalid commit -qm "d4 sandbox snapshot") \
  || die "沙箱 git 初始化失败"
HEAD1="$(cd "${SBX}" && git rev-parse --short HEAD)"
NATIVE_DIST="eg_$(go env GOOS)_$(go env GOARCH)"
ok "沙箱就绪：$(cd "${SBX}" && git ls-files | wc -l | tr -d ' ') 个跟踪文件，HEAD=${HEAD1}"

step "make build：同时产 bin/eg 与四平台 dist/ + SHA256SUMS（Makefile 自述与 INSTALL §1）"
(cd "${SBX}" && make -s build >"${WORK}/build1.log" 2>&1) || { tail -5 "${WORK}/build1.log" >&2; die "沙箱 make build 失败"; }
[ -x "${SBX}/bin/eg" ] || die "make build 未产 bin/eg"
ok "bin/eg 在场且可执行"
MISSING=""
for f in eg_linux_amd64 eg_linux_arm64 eg_darwin_amd64 eg_darwin_arm64 SHA256SUMS; do
  [ -f "${SBX}/dist/${f}" ] || MISSING="${MISSING} ${f}"
done
[ -z "${MISSING}" ] || die "make build 未重建 dist 产物：${MISSING}"
ok "四平台产物 + SHA256SUMS 齐全（build ⊃ dist 成立）"

step "make print-version == version.go 字面量 == 注入产物的版本段（逐字 ${WANT_VERSION}）"
MK_VER="$(cd "${SBX}" && make -s print-version | tail -1 | tr -d ' ')"
GO_VER="$(grep -oE '"[0-9]+\.[0-9]+\.[0-9]+-[a-z0-9]+"' "${SBX}/internal/version/version.go" | head -1 | tr -d '"')"
CLI_VER="$("${SBX}/bin/eg" --version </dev/null | awk '{print $2}')"
[ "${MK_VER}" = "${WANT_VERSION}" ] || die "make print-version=${MK_VER} ≠ ${WANT_VERSION}"
[ "${GO_VER}" = "${WANT_VERSION}" ] || die "version.go=${GO_VER} ≠ ${WANT_VERSION}"
[ "${CLI_VER}" = "${WANT_VERSION}" ] || die "eg --version=${CLI_VER} ≠ ${WANT_VERSION}"
ok "三处逐字同值：${WANT_VERSION}"

step "commit 溯源机制：注入 commit 段 == 沙箱 HEAD 短 sha（INSTALL §2 A-F-01 关闭方式）"
VER1="$("${SBX}/bin/eg" --version </dev/null)"
printf '  %s\n' "${VER1}"
echo "${VER1}" | grep -qF "commit ${HEAD1}" || die "commit 段不等于 HEAD1=${HEAD1}：${VER1}"
DIST_VER1="$("${SBX}/dist/${NATIVE_DIST}" --version </dev/null)"
echo "${DIST_VER1}" | grep -qF "commit ${HEAD1}" || die "dist 产物 commit 段不等于 HEAD1"
ok "bin/eg 与 dist/${NATIVE_DIST} 的 commit 段均逐字 == ${HEAD1}"

SUM1="$(sha "${SBX}/dist/eg_linux_amd64")"
step "HEAD 前移后重建：commit 段随 HEAD 改变；同源重建 SHA 变而 commit 段不变"
(cd "${SBX}" && git -c user.name=d4 -c user.email=d4@example.invalid commit -q --allow-empty -m "d4 advance head")
HEAD2="$(cd "${SBX}" && git rev-parse --short HEAD)"
[ "${HEAD1}" != "${HEAD2}" ] || die "HEAD 未前移"
(cd "${SBX}" && make -s build >"${WORK}/build2.log" 2>&1) || die "第二次 make build 失败"
VER2="$("${SBX}/bin/eg" --version </dev/null)"
echo "${VER2}" | grep -qF "commit ${HEAD2}" || die "重建后 commit 段未随 HEAD 更新：${VER2}"
ok "commit 段随 HEAD 前移：${HEAD1} → ${HEAD2}"
SUM2="$(sha "${SBX}/dist/eg_linux_amd64")"
[ "${SUM1}" != "${SUM2}" ] || die "同源重建 SHA 未变（构建时间戳应导致字节不同，INSTALL §2 明写）"
ok "同源重建 SHA 变化（${SUM1:0:12}… → ${SUM2:0:12}…）而 commit 段是稳定锚点"

# ────────────────────────────────────────────────────────── C / D / E：产物形态与构建设置
step "产物形态：linux 两支 ELF statically linked、darwin 两支 Mach-O，架构逐一对应"
FI_LA="$(file -b "${SBX}/dist/eg_linux_amd64")"
FI_LR="$(file -b "${SBX}/dist/eg_linux_arm64")"
FI_DA="$(file -b "${SBX}/dist/eg_darwin_amd64")"
FI_DR="$(file -b "${SBX}/dist/eg_darwin_arm64")"
printf '  %s\n  %s\n  %s\n  %s\n' "${FI_LA}" "${FI_LR}" "${FI_DA}" "${FI_DR}"
case "${FI_LA}" in *ELF*x86-64*statically\ linked*) ;; *) die "linux/amd64 产物非 ELF 静态链接 x86-64" ;; esac
case "${FI_LR}" in *ELF*aarch64*statically\ linked*) ;; *) die "linux/arm64 产物非 ELF 静态链接 aarch64" ;; esac
case "${FI_DA}" in *Mach-O*x86_64*) ;; *) die "darwin/amd64 产物非 Mach-O x86_64" ;; esac
case "${FI_DR}" in *Mach-O*arm64*) ;; *) die "darwin/arm64 产物非 Mach-O arm64" ;; esac
ok "四支产物格式与架构逐一对应，linux 两支 statically linked"
if command -v ldd >/dev/null; then
  for f in eg_linux_amd64 eg_linux_arm64; do
    # 静态可执行文件上 ldd 本身以非零退出并把结论打到 stderr，故先收编退出码再判文本
    { ldd "${SBX}/dist/${f}" 2>&1 || true; } | grep -q "not a dynamic executable" \
      || die "${f} 被 ldd 判为动态可执行（应为静态）"
  done
  ok "ldd：linux 两支均 not a dynamic executable"
else
  printf '  [note] 本环境无 ldd，静态性仅由 file(1) 判定（不伪造第二条证据）\n'
fi

step "构建设置：四支产物 go version -m 均含 CGO_ENABLED=0 / -trimpath=true / 对应 GOOS+GOARCH"
for spec in "eg_linux_amd64 linux amd64" "eg_linux_arm64 linux arm64" \
            "eg_darwin_amd64 darwin amd64" "eg_darwin_arm64 darwin arm64"; do
  set -- ${spec}
  M="$(go version -m "${SBX}/dist/$1" 2>/dev/null)"
  echo "${M}" | grep -qE '^[[:space:]]+build[[:space:]]+CGO_ENABLED=0$' || die "$1 缺 CGO_ENABLED=0"
  echo "${M}" | grep -qE '^[[:space:]]+build[[:space:]]+-trimpath=true$' || die "$1 缺 -trimpath=true"
  echo "${M}" | grep -qE "^[[:space:]]+build[[:space:]]+GOOS=$2$"   || die "$1 GOOS≠$2"
  echo "${M}" | grep -qE "^[[:space:]]+build[[:space:]]+GOARCH=$3$" || die "$1 GOARCH≠$3"
done
ok "四支产物构建设置逐一合规（CGO 关闭 + trimpath + 目标平台）"

step "依赖面：纯 Go SQLite 驱动在场、cgo 驱动零命中"
DEPS="$(cd "${SBX}" && CGO_ENABLED=0 go list -deps ./cmd/eg 2>/dev/null)"
echo "${DEPS}" | grep -qx "modernc.org/sqlite" || die "依赖闭包缺纯 Go 驱动 modernc.org/sqlite"
echo "${DEPS}" | grep -qi "mattn/go-sqlite3" && die "依赖闭包出现 cgo 驱动 mattn/go-sqlite3"
ok "modernc.org/sqlite 在场；mattn/go-sqlite3 零命中"

step "校验和与溯源：SHA256SUMS 恰四行、sha256sum -c 四行全 OK，PROVENANCE 同源绑定"
LINES="$(wc -l < "${SBX}/dist/SHA256SUMS" | tr -d ' ')"
[ "${LINES}" = "4" ] || die "SHA256SUMS 应恰四行，实测 ${LINES}"
(cd "${SBX}/dist" && sha256sum -c SHA256SUMS >"${WORK}/sums.log" 2>&1) \
  || (cd "${SBX}/dist" && shasum -a 256 -c SHA256SUMS >"${WORK}/sums.log" 2>&1) \
  || { cat "${WORK}/sums.log" >&2; die "sha256sum -c 未全 OK"; }
[ "$(grep -c ': OK$' "${WORK}/sums.log")" = "4" ] || die "OK 行数不足四行"
[ -f "${SBX}/dist/PROVENANCE.txt" ] || die "make dist 未生成 dist/PROVENANCE.txt"
grep -qF "version: ${WANT_VERSION}" "${SBX}/dist/PROVENANCE.txt" || die "PROVENANCE 缺版本号 ${WANT_VERSION}"
grep -qF "commit: ${HEAD2}" "${SBX}/dist/PROVENANCE.txt" || die "PROVENANCE 缺当期 commit ${HEAD2}"
sed -n '/^artifact checksums:$/,$p' "${SBX}/dist/PROVENANCE.txt" | tail -n +2 >"${WORK}/prov.sums"
cmp -s "${SBX}/dist/SHA256SUMS" "${WORK}/prov.sums" || die "PROVENANCE 中的 artifact checksums 与 SHA256SUMS 不一致"
(cd "${SBX}" && make -s verify-dist-provenance >/dev/null 2>&1) || die "make verify-dist-provenance 正常产物应通过"
printf 'tamper' >> "${SBX}/dist/eg_linux_amd64"
if (cd "${SBX}" && make -s verify-dist-provenance >"${WORK}/prov_tamper.log" 2>&1); then
  die "篡改 dist/eg_linux_amd64 后 verify-dist-provenance 必须失败"
fi
(cd "${SBX}" && make -s dist >"${WORK}/dist_after_tamper.log" 2>&1) || die "篡改反证后 make dist 应可重建恢复"
ok "SHA256SUMS/PROVENANCE 同源绑定，篡改产物一字节必红，重建恢复通过"

step "离线可构建（预热模块缓存 + GOPROXY=off）"
(cd "${SBX}" && GOPROXY=off CGO_ENABLED=0 go build -o "${WORK}/eg_offline" ./cmd/eg >"${WORK}/offline.log" 2>&1) \
  || { tail -3 "${WORK}/offline.log" >&2; die "GOPROXY=off 构建失败（预热缓存下应成功）"; }
[ -x "${WORK}/eg_offline" ] || die "离线构建未产出可执行文件"
ok "GOPROXY=off CGO_ENABLED=0 go build ./cmd/eg 成功"

step "I-028 反证：空 GOMODCACHE + GOPROXY=off 在 CI 环境前置阶段退 3 并给出指引"
EMPTY_MODCACHE="${WORK}/empty-gomodcache"
mkdir -p "${EMPTY_MODCACHE}"
RC_CI=0
(cd "${SBX}" && env GOMODCACHE="${EMPTY_MODCACHE}" GOPROXY=off EG_CI_PROFILE=manifest bash tests/ci/pipeline.sh >"${WORK}/ci_empty_cache.log" 2>&1) || RC_CI=$?
[ "${RC_CI}" = "3" ] || { tail -20 "${WORK}/ci_empty_cache.log" >&2; die "空模块缓存 CI 前置应退 3，实测 ${RC_CI}"; }
grep -qF "预热 GOMODCACHE 或提交 vendor/" "${WORK}/ci_empty_cache.log" || die "空模块缓存失败未给出 GOMODCACHE/vendor 指引"
ok "空模块缓存反证在环境前置阶段失败（退 3），未落到 make lint/go build 底层错误"

# 被测二进制拷出后再验 clean（clean 会删掉 bin/ 与 dist/）
cp "${SBX}/bin/eg" "${EG}"

step "make dist 单独跑不产 bin/eg；make clean 后 bin/ 与四支产物均不在场"
rm -rf "${SBX}/bin"
(cd "${SBX}" && make -s dist >"${WORK}/dist2.log" 2>&1) || die "沙箱 make dist 失败"
[ ! -e "${SBX}/bin/eg" ] || die "make dist 不应产 bin/eg（Makefile 自述「不产 \$(BIN)」）"
ok "make dist 未产 bin/eg，四平台产物已重建"
(cd "${SBX}" && make -s clean >/dev/null 2>&1) || die "make clean 失败"
[ ! -e "${SBX}/bin" ] || die "make clean 未删 bin/"
for f in eg_linux_amd64 eg_linux_arm64 eg_darwin_amd64 eg_darwin_arm64 SHA256SUMS PROVENANCE.txt; do
  [ ! -e "${SBX}/dist/${f}" ] || die "make clean 未删 dist/${f}"
done
ok "make clean 后 bin/ 与 dist/ 派生产物均不在场；PROVENANCE 可由 make dist/release 再生成"

# ────────────────────────────────────────────────────────── G：INSTALL §3 首次上手
step "INSTALL §3 首次上手：四条命令逐行照抄退 0（素材落在 vault 之外）"
mkdir -p "${VAULT}"
[ "$(rcv init --domain ai-infra)" = "0" ]                     || die "§3 eg init 未退 0"
[ "$(rcv config set default_domain ai-infra)" = "0" ]         || die "§3 eg config set 未退 0"
BODY_FILE="${WORK}/article_external.txt"
printf '正文：Transformer 用并行注意力替代递归，降低了长序列训练成本。\n' > "${BODY_FILE}"
[ "$(rcv capture --url https://example.com/attention --title "注意力机制综述" \
        --body-file "${BODY_FILE}" --reason "首次上手冒烟")" = "0" ] || die "§3 eg capture 未退 0"
[ "$(rcv search 注意力)" = "0" ]                               || die "§3 eg search 未退 0"
ok "init / config set / capture / search 四条逐行照抄全退 0"

step "I-029 审计锁：vault 根不得出现 article.txt 跟踪（素材必须落在外部）"
[ ! -f "${VAULT}/article.txt" ] || die "vault 根不应存在 article.txt（应当使用外部路径）"
gv ls-files | grep -q "article.txt" && die "vault 根 article.txt 被意外跟踪（I-029 审计红）"
ok "vault 根干净，无意外跟踪素材"

step "vault 侧 .index/ 忽略在位（init 期即写入 .gitignore）"
grep -qx "\.index/" "${VAULT}/.gitignore"     || die ".gitignore 未登记 .index/"
ok '.gitignore 登记 .index/（`.eg/` 的忽略登记随首次生成发生，见后续 apply 之后的一步）'

step "内嵌 SKILL.md 与 skill/SKILL.md 逐字相等（go:embed 未过期）"
cmp -s "${VAULT}/SKILL.md" "${REPO_ROOT}/skill/SKILL.md" \
  || die "eg init 落盘的 SKILL.md 与源文件不同字节"
ok "SKILL.md 逐字相等（$(wc -c < "${VAULT}/SKILL.md" | tr -d ' ') 字节）"

step "I-004 / defect_zeroing-001 文档审计：SKILL 示例不复用 PPE 语料，交付文档零旧 test/ 根路径"
if grep -nE 'Verification, The Key to AI|s-20260901-verification|n-20260901-verification|k-20260901-verification' "${REPO_ROOT}/skill/SKILL.md"; then
  die "skill/SKILL.md 示例仍复用 PPE 验收语料（I-004 审计红）"
fi
if grep -nE '(^|[^a-zA-Z0-9_/])(\./)?test/' "${REPO_ROOT}/README.md" "${REPO_ROOT}/INSTALL.md" "${REPO_ROOT}/skill/SKILL.md"; then
  die "README / INSTALL / SKILL 仍引用已删除的 test/ 根路径（defect_zeroing-001 审计红）"
fi
ok "SKILL 示例与 PPE 验收语料零重叠；README/INSTALL/SKILL 零旧 test/ 根路径"

# ────────────────────────────────────────────────────────── H：INSTALL §4 用户闸门链
step "INSTALL §4 用户闸门链：过目 / 提案 / 批准逐行照抄退 0"
SRC="$(basename "$(ls "${VAULT}"/sources/*.md | head -1)" .md)"
PID="$(egv proposal new --type logical_delete --target "${SRC}" \
        --reason '重复收录，保留更完整的一份' --json | grep -oE 'p-[0-9]{8}-[0-9]{3}' | head -1)"
[ -n "${PID}" ] || die "proposal new 未产出提案号"
[ "$(rcv mark-reviewed --target "${SRC}")" = "0" ] || die "§4 mark-reviewed 未退 0"
[ "$(rcv unreviewed)" = "0" ]                      || die "§4 unreviewed 未退 0"
[ "$(rcv proposal list --status pending)" = "0" ]   || die "§4 proposal list 未退 0"
[ "$(rcv proposal show "${PID}")" = "0" ]           || die "§4 proposal show 未退 0"
[ "$(rcv proposal approve "${PID}" --confirm --user-request)" = "0" ] || die "§4 proposal approve 未退 0"
ok "SRC=${SRC} / PID=${PID}：五条命令逐行照抄全退 0"

step "INSTALL §4 第 109 行：delete 去掉 --confirm 退 6 且权威字节与 commit 数一格不动"
SRC_FILE="${VAULT}/sources/${SRC}.md"
BEFORE_SHA="$(sha "${SRC_FILE}")"
BEFORE_COMMITS="$(gv rev-list --count HEAD)"
RC6="$(rcv delete --target "${SRC}" --reason "重复收录，保留更完整的一份" --proposal "${PID}" --user-request)"
[ "${RC6}" = "6" ] || die "缺 --confirm 应退 6，实测 ${RC6}"
[ "$(sha "${SRC_FILE}")" = "${BEFORE_SHA}" ] || die "退 6 路径改动了权威 Markdown 字节"
[ "$(gv rev-list --count HEAD)" = "${BEFORE_COMMITS}" ] || die "退 6 路径产生了 commit"
[ -z "$(gv status --porcelain)" ] || die "退 6 路径留下工作区改动"
ok "退 6 + 零写入 + 零 commit + 工作区干净"

step "INSTALL §4 余下三条：delete 齐备退 0 并落 deleted_at；search --include-deleted / undelete 退 0"
[ "$(rcv delete --target "${SRC}" --reason "重复收录，保留更完整的一份" --proposal "${PID}" --confirm --user-request)" = "0" ] \
  || die "§4 delete 四条件齐备未退 0"
grep -q "^deleted_at:" "${SRC_FILE}" || die "delete 未落 deleted_at"
[ "$(rcv search 注意力 --include-deleted)" = "0" ] || die "§4 search --include-deleted 未退 0"
[ "$(rcv undelete --target "${SRC}" --reason "确认还需要这份原文")" = "0" ] || die "§4 undelete 未退 0"
grep -q "^deleted_at:" "${SRC_FILE}" && die "undelete 未清空 deleted_at"
ok "delete 落 deleted_at → undelete 清空，两条只读/撤回命令均退 0"

# ────────────────────────────────────────────────────────── I：INSTALL §4.1 对账与可见性
step "INSTALL §4.1：check / reconcile --dry-run / reconcile 逐行照抄退 0"
[ "$(rcv check)" = "0" ]                        || die "§4.1 eg check 未退 0"
[ "$(rcv reconcile --dry-run --json)" = "0" ]    || die "§4.1 reconcile --dry-run 未退 0"
[ "$(rcv reconcile)" = "0" ]                     || die "§4.1 reconcile 未退 0"
ok "三条对账命令逐行照抄全退 0"

step "INSTALL §4.1 内联 ChangePlan：apply --plan - 退 0，两条 --include-deprecated 只读 flag 退 0"
BASE="$(egv context --source "${SRC}" --json | grep -oE 'sha256:[0-9a-f]{64}' | head -1)"
[ -n "${BASE}" ] || die "context 未给出 unprocessed.md 的 base 哈希"
CARD="k-20260908-attention"
printf '{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"首次上手：把这篇材料沉淀成一张知识卡","requirement_ids":["EG-KNW-04"],"convergence":[],"base":{"unprocessed.md":"%s"},"ops":[{"op":"write_note","source":"%s","note_id":"n-20260908-attention","title":"注意力机制综述","sections":{"材料提炼":"- 原文主张：并行注意力替代递归，降低长序列训练成本。\\n","Agent 分析":"- 该主张限定在自注意力可并行的结构。\\n"},"output_cards":[{"card":"%s","mode":"新建"}]},{"op":"create_card","card_id":"%s","title":"并行注意力降低长序列训练成本","tags":["attention"],"sources":[{"source":"%s","note":"n-20260908-attention","rel":"support","reason":"原文直接给出该结论"}],"sections":{"知识内容":"并行注意力替代递归，降低了长序列训练成本。\\n","解释与依据":"- 依据原文：并行化消除了逐步递归的串行依赖。\\n","条件与边界":"- 仅在自注意力可并行的结构下成立。\\n","理解自检":"- 为什么递归结构难以并行？\\n"}}]}' \
  "${BASE}" "${SRC}" "${CARD}" "${CARD}" "${SRC}" > "${WORK}/plan.json"
RCA=0
"${EG}" --vault "${VAULT}" apply --plan - <"${WORK}/plan.json" >"${WORK}/apply.log" 2>&1 || RCA=$?
[ "${RCA}" = "0" ] || { tail -3 "${WORK}/apply.log" >&2; die "§4.1 文档内联 ChangePlan 未退 0（实测 ${RCA}）"; }
[ -f "${VAULT}/domains/ai-infra/knowledge/${CARD}.md" ] || die "apply 未落知识卡文件"
[ "$(rcv rel "${CARD}" --include-deprecated)" = "0" ]       || die "§4.1 rel --include-deprecated 未退 0"
[ "$(rcv card show "${CARD}" --include-deprecated)" = "0" ]  || die "§4.1 card show --include-deprecated 未退 0"
ok "apply --plan - 退 0 并落卡；两条只读 flag 均退 0"

step 'INSTALL「.eg/ 状态目录与 vault 的关系」三条声明：exclude 登记 / 零跟踪 / report --last 可复现'
[ -f "${VAULT}/.eg/last-report.json" ] || die ".eg/last-report.json 未生成（report --last 的真源）"
grep -qx "\.eg/" "${VAULT}/.git/info/exclude" || die ".git/info/exclude 未登记 .eg/"
[ "$(gv ls-files | grep -c '^\.eg' || true)" = "0" ] || die ".eg 被 git 跟踪（应恒不入 commit）"
[ "$(rcv report --last)" = "0" ] || die "eg report --last 未退 0"
ok ".eg/ 在 .git/info/exclude、零跟踪、report --last 退 0"

# ────────────────────────────────────────────────────────── J / K：版本命令面与平台措辞
step "版本命令面：eg --version 退 0 且含 ${WANT_VERSION}；eg version 子命令退 1"
RCV=0; "${EG}" --version </dev/null >"${WORK}/ver.txt" 2>&1 || RCV=$?
[ "${RCV}" = "0" ] || die "eg --version 未退 0"
grep -qF "${WANT_VERSION}" "${WORK}/ver.txt" || die "eg --version 未逐字含 ${WANT_VERSION}"
RCS=0; "${EG}" version </dev/null >"${WORK}/versub.txt" 2>&1 || RCS=$?
[ "${RCS}" = "1" ] || die "eg version 子命令应退 1（INSTALL 明写没有该子命令），实测 ${RCS}"
ok "--version 退 0 含当期版本号；version 子命令退 1"

step "平台支持范围措辞：三平台运行结论如实为「未做」，文档未冒充真机验收"
grep -qF "仅交叉编译产出，未经真机运行验证" "${REPO_ROOT}/INSTALL.md" \
  || die "INSTALL 已知限制表缺 darwin「仅交叉编译产出，未经真机运行验证」"
grep -qF "eg_darwin_amd64" "${REPO_ROOT}/INSTALL.md" || die "INSTALL 未点名 darwin 两支产物"
grep -qE "linux/arm64.*(未做|仅交叉编译)" "${REPO_ROOT}/INSTALL.md" \
  || die "INSTALL 缺 linux/arm64 运行验证「未做」的结论"
grep -qF "均**不**声称 darwin 产物通过了 macOS 真机验证" "${REPO_ROOT}/INSTALL.md" \
  || die "INSTALL 缺「不声称 darwin 真机验证」的反向说明"
grep -qF "未经真机运行验证" "${REPO_ROOT}/README.md" \
  || die "README 缺 darwin 产物「未经真机运行验证」的限定"
UNAME="$(uname -srm)"
printf '  本环境：%s（唯一原生目标 linux/amd64；其余三平台运行结论如实为未做）\n' "${UNAME}"
ok "支持范围措辞在册且与本环境事实一致（见 I-…-007，属如实登记的未验证项）"

# ────────────────────────────────────────────────────────── 零污染自查
step "真实仓库零污染：工作区状态与 dist 两个溯源文件字节均未变"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] || {
  diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") >&2 || true
  die "真实仓库工作区状态发生变化"
}
AFTER_DIST_SHA="$(cd "${REPO_ROOT}" && { [ -f dist/SHA256SUMS ] && sha256sum dist/SHA256SUMS || echo none; } \
                  && { [ -f dist/PROVENANCE.txt ] && sha256sum dist/PROVENANCE.txt || echo none; })"
[ "${BEFORE_DIST_SHA}" = "${AFTER_DIST_SHA}" ] || die "真实仓库 dist/ 溯源文件被本脚本改动"
ok "真实仓库零污染（构建与写入全在 ${WORK} 内）"

printf '\n[PASS] d4_dist_docs_platform.sh：%d 项断言全绿（步骤 %d）\n' "${PASS}" "${STEP}"

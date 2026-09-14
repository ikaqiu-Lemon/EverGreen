#!/usr/bin/env bash
# 合同快照自足性门禁（system_assurance · T-…-006，合同 D8；suite id: contract.contract-snapshot）
#
# 判据来源：合同 D8「`tests/` 必须能从干净源码分发包运行」＋「快照本身就是必需真源」。
# 背景（I-…-003）：历史上 4 支 skill_test.go 系合同断言直接读 sibling `../teamwork/**`，
# 于是单仓分发包里这些必需断言全部失败或跳过 —— 「测试通过」依赖仓库之外的东西在场。
# D8 的解法是把被引用的合同文档以**快照**形式打包进 `tests/fixtures/contracts/`，
# 并规定解析顺序 `EG_CONTRACTS_DIR`（显式覆盖）→ 包内快照（默认、必需、自足）。
#
# 本门禁把「快照可信且自足」变成机器判据：
#   S1 快照完整性：SHA256SUMS 在场、`sha256sum -c` 全 OK，且**条目数 == 盘面文件数**
#      （只校验和不校验条目数的话，漏登记一个文件同样能"全 OK"）。
#   S2 必需真源自足：测试资产里对合同文档的引用（精确相对路径 + 文件名两种形态）
#      逐条能在快照里解析到；两类引用都必须**非空**，否则判据是空的。
#   S3 默认解析顺序：common.sh 的 EG_CONTRACTS 默认落在包内快照（静态 + 运行时双验），
#      且 eg_contract_doc 对不在场的文档必须非零（不静默回退 sibling）。
#   反例四连（只在临时副本上构造，真实仓零改动）：改一个字节 / 删一个文件 /
#      伪造多余条目 / 引用不在场的文档 —— 对应判据必须逐个转红。
#
# 约束：离线、可重复、零交互；只读真实仓（反例一律在 mktemp 副本里做）。
# 用法：cd evergreen && bash tests/contract/contract-snapshot/contract_snapshot.sh
set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-contract-snapshot.XXXXXX")"
trap 'rm -rf "${WORK}"' EXIT

STEP=0
PASS=0
step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

SNAP="${REPO_ROOT}/tests/fixtures/contracts"
SUMS="${SNAP}/SHA256SUMS"

step "S1 快照完整性：SHA256SUMS 逐条校验 + 条目数与盘面一致"
[ -f "${SUMS}" ] || die "缺 ${SUMS}：快照即必需真源，没有校验和就没有可信度（D8）"
( cd "${SNAP}" && sha256sum -c SHA256SUMS >"${WORK}/sums.log" 2>&1 ) ||
  { cat "${WORK}/sums.log" >&2; die "快照校验和不匹配 → 按 D8 判 FAIL（不是 NOT_RUN）"; }
ENTRIES="$(grep -c . "${SUMS}")"
ONDISK="$(cd "${SNAP}" && find . -type f ! -name SHA256SUMS | wc -l | tr -d ' ')"
[ "${ENTRIES}" -eq "${ONDISK}" ] ||
  die "SHA256SUMS 条目 ${ENTRIES} ≠ 快照文件 ${ONDISK}：漏登记的文件不会被校验"
[ "${ENTRIES}" -ge 10 ] || die "快照只有 ${ENTRIES} 条，明显不是完整合同集（判据不得空转）"
ok "快照 ${ONDISK} 个文件逐条校验和一致，且无漏登记"

step "S2 必需真源自足：测试资产对合同文档的引用全部能在快照内解析"
# 只认**带引号的字面量**（= 代码里真的会去读的路径），不认注释里的「判据来源：」引用：
# 后者是给人看的出处说明，不是运行期读取，扫进来只会制造与 D8 无关的红
# （实测 product_tree_selfcontained.sh / isolation.sh 的文件头就只在注释里提到本合同）。
# 含插值标记（$ % + *）的字面量静态不可解析，按构造排除；下面两条 ≥1 的断言保证判据不空转。
SCAN_DIRS=("${REPO_ROOT}/tests/_staged" "${REPO_ROOT}/tests/e2e" "${REPO_ROOT}/tests/contract")
grep -rhoE '"[^"$%*+]*projects/evergreen/[^"$%*+]+\.md"' "${SCAN_DIRS[@]}" 2>/dev/null |
  tr -d '"' | sed 's|.*\(projects/evergreen/\)|\1|' | sort -u >"${WORK}/paths.txt" || true
{ grep -rhoE '"[0-9]{4}-[0-9]{2}-[0-9]{2}-[a-z0-9-]+\.md"' "${SCAN_DIRS[@]}" 2>/dev/null | tr -d '"'
  grep -rhoE 'eg_contract_doc +[A-Za-z0-9._/-]+' "${SCAN_DIRS[@]}" 2>/dev/null |
    awk '{print $2}'; } | sort -u >"${WORK}/names.txt" || true
NPATH="$(grep -c . "${WORK}/paths.txt" || true)"
NNAME="$(grep -c . "${WORK}/names.txt" || true)"
[ "${NPATH}" -gt 0 ] || die "扫不到任何精确路径引用：判据空转（S2 失去意义）"
[ "${NNAME}" -gt 0 ] || die "扫不到任何文档名引用：判据空转（S2 失去意义）"
MISS=0
while read -r rel; do
  [ -n "${rel}" ] || continue
  [ -f "${SNAP}/${rel}" ] || { printf '  缺快照：%s\n' "${rel}" >&2; MISS=$((MISS + 1)); }
done <"${WORK}/paths.txt"
while read -r name; do
  [ -n "${name}" ] || continue
  case "${name}" in */*) rel="${name}";; *) rel="";; esac
  if [ -n "${rel}" ]; then
    [ -f "${SNAP}/${rel}" ] || { printf '  缺快照：%s\n' "${rel}" >&2; MISS=$((MISS + 1)); }
    continue
  fi
  found="$(find "${SNAP}" -type f -name "${name}" | head -1)"
  [ -n "${found}" ] || { printf '  缺快照（按文件名）：%s\n' "${name}" >&2; MISS=$((MISS + 1)); }
done <"${WORK}/names.txt"
[ "${MISS}" -eq 0 ] ||
  die "${MISS} 个被引用的合同文档不在快照里 → 单仓分发时这些必需断言必然失败（D8）"
ok "精确路径 ${NPATH} 条 + 文档名 ${NNAME} 条引用全部在快照内解析成功"


step "S3 默认解析顺序：EG_CONTRACTS 默认指向包内快照，缺失即非零"
grep -Fq 'EG_CONTRACTS="${EG_CONTRACTS_DIR:-${EG_TESTS_ROOT}/fixtures/contracts}"' \
  "${REPO_ROOT}/tests/lib/common.sh" ||
  die "common.sh 的默认解析顺序被改：必须是 EG_CONTRACTS_DIR → 包内快照（D8）"
ACTUAL="$( REPO_ROOT="${REPO_ROOT}" bash -c '
  unset EG_CONTRACTS_DIR
  . "${REPO_ROOT}/tests/lib/common.sh" >/dev/null 2>&1
  printf "%s" "${EG_CONTRACTS}"' )"
[ "${ACTUAL}" = "${SNAP}" ] || die "运行时 EG_CONTRACTS=${ACTUAL}，期望包内快照 ${SNAP}"
# 反例用的「不在场文档」路径由拼接生成：本文件自己也在 S2 的扫描面内，
# 写成字面量就会被自己的判据当成"引用了未打包的文档"（实测第一版就这么自伤了）。
ABSENT_REL="projects/evergreen/s1_main_flow/docs/specs/9999-01-01-${ABSENT_STEM:=absent}.md"
code=0
( REPO_ROOT="${REPO_ROOT}" ABSENT_REL="${ABSENT_REL}" bash -c '
  . "${REPO_ROOT}/tests/lib/common.sh" >/dev/null 2>&1
  eg_contract_doc "${ABSENT_REL}"' \
  >/dev/null 2>&1 ) || code=$?
[ "${code}" -ne 0 ] || die "eg_contract_doc 对不在场的文档必须非零（不得静默回退 sibling）"
ok "默认解析落在包内快照；不在场的文档解析失败而非静默回退"

step "反例四连：改字节 / 删文件 / 伪造条目 / 引用不在场文档 —— 判据必须逐个转红"
cp -r "${SNAP}" "${WORK}/snap"
FIRST_REL="$(cd "${WORK}/snap" && find . -type f ! -name SHA256SUMS | head -1 | sed 's|^\./||')"

printf 'x' >>"${WORK}/snap/${FIRST_REL}"
( cd "${WORK}/snap" && sha256sum -c SHA256SUMS >/dev/null 2>&1 ) &&
  die "反例1 未被命中：改了一个字节，sha256sum -c 仍然通过"
git -C "${REPO_ROOT}" status --porcelain -- tests/fixtures/contracts >"${WORK}/dirty.txt"
[ ! -s "${WORK}/dirty.txt" ] || { cat "${WORK}/dirty.txt" >&2; die "反例构造污染了真实仓快照"; }
cp -r "${SNAP}" "${WORK}/snap2"
rm -f "${WORK}/snap2/${FIRST_REL}"
( cd "${WORK}/snap2" && sha256sum -c SHA256SUMS >/dev/null 2>&1 ) &&
  die "反例2 未被命中：删了一个文件，sha256sum -c 仍然通过"
cp -r "${SNAP}" "${WORK}/snap3"
GHOST_REL="projects/evergreen/${GHOST_STEM:=ghost}.md"
printf '%s  %s\n' "$(printf '' | sha256sum | cut -d' ' -f1)" "${GHOST_REL}" \
  >>"${WORK}/snap3/SHA256SUMS"
E3="$(grep -c . "${WORK}/snap3/SHA256SUMS")"
O3="$(cd "${WORK}/snap3" && find . -type f ! -name SHA256SUMS | wc -l | tr -d ' ')"
[ "${E3}" -ne "${O3}" ] || die "反例3 未被命中：伪造条目后条目数比对仍然自洽"
code=0
( cd "${WORK}/snap3" && sha256sum -c SHA256SUMS >/dev/null 2>&1 ) || code=$?
[ "${code}" -ne 0 ] || die "反例3 未被命中：登记了不在场的 ${GHOST_REL} 却仍然校验通过"
if [ -f "${SNAP}/${GHOST_REL}" ]; then die "反例只允许在副本上构造"; fi
ok "四组反例逐个被对应判据命中；真实仓 tests/fixtures/contracts/ 未被改动"

printf '\n[PASS] 合同快照自足性门禁通过（S1~S3 + 4 组反例；共 %d 步 / %d 条断言）\n' "${STEP}" "${PASS}"

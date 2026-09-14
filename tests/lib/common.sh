#!/usr/bin/env bash
# 测试体系公共 helper（tests/lib/common.sh）。
#
# 被 tests/e2e/**、tests/contract/**、tests/perf/** 的脚本在定义 REPO_ROOT 之后 source。
# 设计约束：
#   · 只定义 `EG_*` 前缀的变量与函数，**不定义** step/ok/die 等常见名，避免覆盖各脚本自有 helper；
#   · 兼容 `set -Eeuo pipefail`（不使用未定义变量、不返回非零副作用）；
#   · 不产生任何副作用（不写盘、不联网、不改仓库）。
#
# 提供：
#   EG_FIXTURES   e2e 语料根（原 test/e2e/testdata）
#   EG_CONTRACTS  合同快照根（D8：包内快照即必需真源；EG_CONTRACTS_DIR 可显式覆盖）
#   eg_require_disk <MiB>   磁盘前置检查（不足即以退出码 3 明确报环境不足，避免 ENOSPC 伪失败）
#   eg_scratch <前缀>       在 TMPDIR 下创建 run 级 scratch 目录
#   eg_contract_doc <相对路径>  解析一个合同文档的绝对路径（缺失即非零）

: "${REPO_ROOT:?tests/lib/common.sh 需要调用方先定义 REPO_ROOT}"

EG_TESTS_ROOT="${REPO_ROOT}/tests"
EG_FIXTURES="${EG_FIXTURES:-${EG_TESTS_ROOT}/fixtures/e2e}"
# D8：默认走包内快照，使单仓（无 sibling teamwork）也能完成全部必需合同验证。
EG_CONTRACTS="${EG_CONTRACTS_DIR:-${EG_TESTS_ROOT}/fixtures/contracts}"
export EG_TESTS_ROOT EG_FIXTURES EG_CONTRACTS

# eg_require_disk <最少可用 MiB>：默认 512 MiB。
# 判据：TMPDIR 与仓库所在文件系统的可用空间都要达标（本环境两者同盘）。
eg_require_disk() {
  local need="${1:-512}" where avail
  for where in "${TMPDIR:-/tmp}" "${REPO_ROOT}"; do
    avail="$(df -Pm "${where}" 2>/dev/null | awk 'NR==2{print $4}')"
    [ -n "${avail}" ] || continue
    if [ "${avail}" -lt "${need}" ]; then
      printf '[ENV] 磁盘可用空间不足：%s 仅剩 %s MiB < 需要 %s MiB；这是环境约束，不是产品缺陷。\n' \
        "${where}" "${avail}" "${need}" >&2
      exit 3
    fi
  done
}

# eg_scratch <前缀>：创建并回显 scratch 目录；调用方负责 trap 清理。
eg_scratch() {
  local prefix="${1:-eg-scratch}"
  mktemp -d "${TMPDIR:-/tmp}/${prefix}.XXXXXX"
}

# Portable file metadata helpers for GNU/Linux and BSD/macOS.
eg_stat_fingerprint() {
  if stat -c '%n %h %s %a' "$1" >/dev/null 2>&1; then
    stat -c '%n %h %s %a' "$1"
  else
    stat -f '%N %l %z %Lp' "$1"
  fi
}

eg_stat_inode() {
  if stat -c '%i' "$1" >/dev/null 2>&1; then
    stat -c '%i' "$1"
  else
    stat -f '%i' "$1"
  fi
}

eg_stat_links() {
  if stat -c '%h' "$1" >/dev/null 2>&1; then
    stat -c '%h' "$1"
  else
    stat -f '%l' "$1"
  fi
}

eg_stat_size_mtime() {
  if stat -c '%s %Y' "$1" >/dev/null 2>&1; then
    stat -c '%s %Y' "$1"
  else
    stat -f '%z %m' "$1"
  fi
}

eg_snapshot_markdown_metadata() {
  local root="$1" out="$2" file
  : >"${out}"
  while IFS= read -r file; do
    printf '%s ' "${file}" >>"${out}"
    eg_stat_size_mtime "${file}" >>"${out}"
  done < <(find "${root}" -name '*.md' -type f | LC_ALL=C sort)
}

# eg_contract_doc <projects/... 相对路径>：回显绝对路径；缺失即非零。
eg_contract_doc() {
  local rel="$1" abs="${EG_CONTRACTS}/$1"
  if [ ! -f "${abs}" ]; then
    printf '[FAIL] 合同快照缺失：%s（期望在 %s 下；快照即必需真源，见合同 D8）\n' "${rel}" "${EG_CONTRACTS}" >&2
    return 1
  fi
  printf '%s\n' "${abs}"
}

# ---------------------------------------------------------------------------
# 测试树外置（合同 ADR-T1 / D2 / D3）配套 helper。
#
# 背景：白盒 Go 测试的**权威存放位置**是 `tests/_staged/<运行期路径>/`，运行期由
# materializer 物化回包目录。因此 e2e 脚本里两类历史写法在外置后必然失效，
# 且**不能**靠改断言迁就目录：
#   ① `go test ./internal/... / ./test/e2e/`  —— 真实仓这些包已无 *_test.go，必须在物化树里跑；
#   ② 直接读某个 `*_test.go`（具名反证在场、落点集合封闭等静态判据）—— 路径要指向权威存放位。
# 下面两个 helper 把这两类解析统一收口，避免每支脚本各自拼路径又各自漂移。
# ---------------------------------------------------------------------------

# 测试文件权威存放根（静态读取用；不需要物化）。
EG_STAGED_TESTS="${EG_TESTS_ROOT}/_staged"
export EG_STAGED_TESTS

# eg_test_path <运行期相对路径>：解析一个测试文件的权威路径。
#   eg_test_path internal/index/scratch_test.go
#     → <repo>/tests/_staged/internal/index/scratch_test.go
# 缺失即非零（不静默回退到产品树，否则「文件不在场」会被误读成「断言不存在」）。
eg_test_path() {
  local rel="$1" abs="${EG_STAGED_TESTS}/$1"
  if [ ! -f "${abs}" ]; then
    printf '[FAIL] 测试文件不在权威存放位：%s（期望 %s；见合同 ADR-T1）\n' "${rel}" "${abs}" >&2
    return 1
  fi
  printf '%s\n' "${abs}"
}

# eg_staged_root：回显可直接 `go test` 的物化树根（<stage>/evergreen），幂等。
#   · 若调用方（如统一 runner）已注入 EG_STAGED_ROOT 且目录在场，则直接复用，不重复物化；
#   · 否则本脚本物化一次并把 run-id 记到 TMPDIR 下的 cache 文件——**必须落文件**，
#     因为调用点多写成 `cd "$(eg_staged_root)"`，命令替换是子 shell，export 的变量传不回父进程，
#     只靠变量缓存会导致每次调用都重新物化、且退出时清理不到（实测残留 90+ 个 run 目录）；
#   · run-id 唯一；stage 路径**从 materializer 报告的 `stage` 键读取**，不在 shell 里拼路径
#     （D3.7 输出字段合同：消费方只认 `stage`。拼路径＝把布局约定复制一份，早晚漂移）；
#   · --no-cleanup 保证不动别人的 run；同名残骸由 materializer 按 D3.7 自恢复，无需脚本兜底。
_eg_stage_cache() { printf '%s/eg-stage-runid.%s' "${TMPDIR:-/tmp}" "${EG_STAGE_KEY:-$$}"; }

eg_staged_root() {
  if [ -n "${EG_STAGED_ROOT:-}" ] && [ -d "${EG_STAGED_ROOT}" ]; then
    printf '%s\n' "${EG_STAGED_ROOT}"
    return 0
  fi
  local cache rid root out stage
  cache="$(_eg_stage_cache)"
  if [ -f "${cache}" ]; then
    root="$(cat "${cache}")"
    if [ -n "${root}" ] && [ -d "${root}" ]; then
      printf '%s\n' "${root}"
      return 0
    fi
  fi
  rid="e2e-$$-$(date +%s)-${RANDOM}"
  out="${TMPDIR:-/tmp}/materialize.${rid}.json"
  if ! python3 "${REPO_ROOT}/tests/runner/materialize.py" --run-id "${rid}" --no-cleanup --json >"${out}" 2>&1; then
    printf '[FAIL] 物化失败（run-id=%s）：\n' "${rid}" >&2
    tail -20 "${out}" >&2
    return 1
  fi
  # 只认 `stage` 键（D3.7）：路径由 materializer 给出，脚本不复刻布局。
  stage="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["stage"])' "${out}" 2>/dev/null || true)"
  if [ -z "${stage}" ] || [ ! -d "${stage}/evergreen" ]; then
    printf '[FAIL] 物化报告未给出可用的 stage（run-id=%s）：%s\n' "${rid}" "${out}" >&2
    return 1
  fi
  root="${stage}/evergreen"
  printf '%s' "${root}" >"${cache}"
  printf '%s\n' "${root}"
}

# eg_staged_cleanup：只清理本脚本自己物化出来的树（外部注入的 EG_STAGED_ROOT 不动）。
eg_staged_cleanup() {
  local cache root run
  cache="$(_eg_stage_cache)"
  [ -f "${cache}" ] || return 0
  root="$(cat "${cache}")"
  # cache 里存的是 <stage>/evergreen；要删的是 <stage> 本身，且必须仍在隔离根内（防误删）
  run="$(dirname "${root:-/nonexistent}")"
  case "${run}" in
    "${REPO_ROOT:?}/.tests-staging/"*) rm -rf "${run}" ;;
    *) printf '[warn] 跳过越界清理：%s\n' "${run}" >&2 ;;
  esac
  rm -f "${cache}"
}

# eg_snapshot_worktree <目标目录> [额外 tar --exclude 参数...]
#
# 把**当前工作树**（含未提交改动）复制到目标目录，供"注入违规 → 门禁必须转红"这类
# 反证脚本使用。源仓库只读，一个字节都不回写。
#
# 为什么必须走这个 helper，而不是各脚本自己写 tar：
#   运行期产物 `.tests-staging/`（物化副本，本身就是一整份仓库）和 `tests/_report/`
#   必须排除。统一 runner 会把 run 级 TMPDIR 放在 `.tests-staging/<run>/tmp/<suite>` 下
#   （D7 隔离），于是各脚本的沙箱目标目录**落在源树内部**：不排除的话，tar 会一边读
#   源树一边把自己刚写出的副本再打包进去——实测表现为单 suite 跑满 900s 超时
#   （dep_gate_double_sided.sh，T-…-006 集成时暴露）。这类 bug 单跑脚本时不会出现，
#   只在"进 runner + staging 在盘"时才现形，因此判据必须收在公共 helper 里。
#
# 同时排除目标目录自身（当它位于 REPO_ROOT 内时），彻底消掉自包含递归。
eg_snapshot_worktree() {
  local dst="$1"; shift
  local -a ex=(--exclude='./.git' --exclude='./bin' --exclude='./dist'
               --exclude='./.tests-staging' --exclude='./tests/_report')
  local abs rel
  abs="$(cd "${dst}" 2>/dev/null && pwd || printf '%s' "${dst}")"
  case "${abs}/" in
    "${REPO_ROOT}/"*) rel="${abs#"${REPO_ROOT}/"}"; ex+=(--exclude="./${rel}") ;;
  esac
  mkdir -p "${dst}"
  tar -C "${REPO_ROOT}" "${ex[@]}" "$@" -cf - . | tar -C "${dst}" -xf -
}

# ---------------------------------------------------------------- 文档命令的自指入口
#
# EG_DOC_SELFREF_RE：README / INSTALL / SKILL 里那些**会把整个测试体系再跑一遍**的命令
# （`make test*`、`bash tests/run.sh …`、`bash tests/ci/pipeline.sh`）。
#
# 为什么必须单列而不是照跑：docs-cli 的判据是"文档里的命令照抄能跑通"，它把命令逐条
# 塞进沙箱副本执行。而这些入口本身就是统一 runner，照跑等于"测试体系在自己的文档测试
# 里再跑一遍全量"——正是 D5 禁止的递归重跑（实测：`make test` 在 docs 沙箱里既耗时数分钟，
# 又因为沙箱副本没有 git 索引而必然红）。
#
# 单列**不等于不验**：eg_doc_selfref_assert 会验这条命令指向的入口真的存在
#   · `make <target>` → Makefile 里有该 target；
#   · `bash <path>`   → 该脚本在盘且非空。
# 也就是说文档里写错入口名照样红；只有"入口在场且由 runner 自己负责执行"这一种情形放过。
EG_DOC_SELFREF_RE='^(make[[:space:]]+test([[:alnum:]-]*)|bash[[:space:]]+tests/(run\.sh|ci/pipeline\.sh))([[:space:]]|$)'

# eg_doc_selfref_assert <命令行>：断言自指入口在场；不在场即非零。
eg_doc_selfref_assert() {
  local cmd="$1" tgt
  case "${cmd}" in
    make\ *)
      tgt="$(printf '%s' "${cmd}" | awk '{print $2}')"
      grep -qE "^${tgt}:" "${REPO_ROOT}/Makefile" ||
        { printf '  [FAIL] 文档引用的 make target 不存在：%s\n' "${tgt}" >&2; return 1; }
      ;;
    bash\ *)
      tgt="$(printf '%s' "${cmd}" | awk '{print $2}')"
      [ -s "${REPO_ROOT}/${tgt}" ] ||
        { printf '  [FAIL] 文档引用的脚本不在盘或为空：%s\n' "${tgt}" >&2; return 1; }
      ;;
    *) printf '  [FAIL] 非自指入口却走了自指分支：%s\n' "${cmd}" >&2; return 1 ;;
  esac
  printf '  [ok] 自指入口在场（不在文档沙箱内递归执行）：%s\n' "${cmd}"
}

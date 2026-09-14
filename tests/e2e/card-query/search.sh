#!/usr/bin/env bash
# `eg search` 端到端脚本（T-evergreen.s1_main_flow-158614-021）。
#
# 判据来源：M2 查询合同 `2026-09-19-m2-query-contract.md`
#   §1.2 hits[] 键表 / §1.4 四级全序 / §1.5 失效卡同等可见并标 [失效]
#   §5.1 Q1 诊断（不可解析文件绝不静默跳过）/ §5.3 计数守恒 / §6 只读零副作用。
#
# 约束：离线、可重复执行、无外部依赖（只用 bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/card-query/search.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m2-search.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }

# 快照：vault 内全部 .md 的「路径 + 字节数 + mtime + sha256」（合同 §6 的 find 快照口径）。
snapshot() {
  eg_snapshot_markdown_metadata "${VAULT}" "$1"
  find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort >>"$1"
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed 语料
step "seed 语料：三张知识卡（含一张 deprecated）+ 一个不可解析 .md"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
[ "$(eg_code config set domains ai-infra,ops)" = "0" ] || die "config set domains 失败"

seed_card() { # id title status created updated domain [tag]
  local dir="${VAULT}/domains/$6/knowledge"
  mkdir -p "${dir}"
  {
    printf '%s\n' '---' "id: $1" "status: $3" "created_at: '$4'" "updated_at: '$5'" \
      "title: $2" 'sources: []'
    [ -n "${7:-}" ] && printf 'tags:\n  - %s\n' "$7"
    printf '%s\n' '---' '' '## 知识内容' '' '正文占位：注意力这个词只写在这里。' ''
  } >"${dir}/$1.md"
}
seed_card k-20260901-attention "注意力机制的计算代价" active 2026-09-01 "2026-09-01T10:00:00+08:00" ai-infra
seed_card k-20260902-rnn "RNN 的长序列衰减" deprecated 2026-09-02 "2026-09-02T10:00:00+08:00" ai-infra
seed_card k-20260903-ops "运维值班注意力分配" active 2026-09-03 "2026-09-03T10:00:00+08:00" ops "注意力"
printf -- '---\n- 1\n---\n\n# 坏卡\n' >"${VAULT}/domains/ai-infra/knowledge/broken.md"

git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg add -A
git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg commit -q -m "seed: m2 search 语料"
[ -z "$(git -C "${VAULT}" status --porcelain)" ] || die "前置条件失败：工作区应干净"
ok "3 张卡 + 1 个坏文件入库，工作区干净"

# ---------------------------------------------------------------- 2. 零副作用前置快照
step "只读零副作用：取检索前的 git / 文件快照（合同 §6）"
BEFORE_STATUS="$(git -C "${VAULT}" status --porcelain)"
BEFORE_LOG="$(git -C "${VAULT}" log --oneline | wc -l)"
snapshot "${WORK}/before.txt"
ok "git status / git log 条数 / .md 字节与 mtime 快照就绪"

# ---------------------------------------------------------------- 3. 检索：JSON
step "eg search 注意力 --json：命中、键表、计数守恒、Q1 诊断"
[ "$(eg_code search 注意力 --json)" = "0" ] || { cat "${WORK}/err.txt"; die "search --json 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/hits1.json"
for k in '"hits"' '"total"' '"scanned_files"' '"skipped_files"' '"matched_fields"' '"score"' '"deprecated"'; do
  grep -Fq -- "${k}" "${WORK}/hits1.json" || die "--json 缺合同 §1.2 的键 ${k}"
done
grep -Fq '"total":3' "${WORK}/hits1.json" || die "total 应为 3（三张卡都含「注意力」）"
grep -Fq '"scanned_files":4' "${WORK}/hits1.json" || die "scanned_files 应为 4"
grep -Fq '"skipped_files":1' "${WORK}/hits1.json" || die "skipped_files 应为 1（坏卡）"
grep -Fq '"code":"Q1"' "${WORK}/hits1.json" || die "warnings[] 缺 Q1"
grep -Fq 'domains/ai-infra/knowledge/broken.md' "${WORK}/hits1.json" || die "Q1 缺坏文件路径"
grep -Fq '"code":"Q3"' "${WORK}/hits1.json" || die "warnings[] 缺 Q3 汇总"
grep -Fq '"exit_code":0' "${WORK}/hits1.json" || die "有 Q 类 warning 时仍必须退 0"
ok "total=3 / scanned=4 / skipped=1；Q1（带路径）+ Q3 均在 warnings[]，仍退 0"

# ---------------------------------------------------------------- 4. 排序稳定
step "排序稳定且确定：连续两次执行的 hits[].id 序列逐字相等（合同 §1.4）"
[ "$(eg_code search 注意力 --json)" = "0" ] || die "第二次检索失败"
ids() { tr ',' '\n' <"$1" | grep -o '"id":"[^"]*"' | sed 's/"id":"//; s/"$//' | tr '\n' ' '; }
A="$(ids "${WORK}/hits1.json")"
B="$(ids "${WORK}/out.txt")"
[ -n "${A}" ] || die "取不到 hits[].id 序列"
[ "${A}" = "${B}" ] || die "两次执行的 ID 序列不同：「${A}」vs「${B}」"
ok "两次执行 ID 序列相同：${A}"

# ---------------------------------------------------------------- 5. 失效卡可见并标记
step "失效卡同等可见：文本模式 [失效] 前缀 + JSON deprecated: true（合同 §1.5）"
[ "$(eg_code search 衰减)" = "0" ] || die "search 衰减 应退 0"
grep -q '^\[失效\]' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "文本模式缺 [失效] 前缀"; }
grep -Fq 'k-20260902-rnn' "${WORK}/out.txt" || die "失效卡未出现在结果里"
[ "$(eg_code search 衰减 --json)" = "0" ] || die "search 衰减 --json 应退 0"
grep -Fq '"deprecated":true' "${WORK}/out.txt" || die "--json 缺 deprecated: true"
for m in '[已删除]' '[未过目]' '[材料支持不足]'; do
  if grep -Fq -- "${m}" "${WORK}/out.txt"; then die "M2 不得输出 ${m}（S2 / S3）"; fi
done
ok "失效卡照常命中，文本标 [失效]、JSON 标 deprecated: true，无 S2/S3 标记"

# ---------------------------------------------------------------- 6. 过滤与退出码
step "过滤与退出码边界：--domain / --tag / --since / --until / 零命中 / 参数非法"
[ "$(eg_code search 注意力 --domain ops --json)" = "0" ] || die "--domain 检索失败"
grep -Fq '"total":1' "${WORK}/out.txt" || die "--domain ops 应只命中 1 张"
grep -Fq 'k-20260903-ops' "${WORK}/out.txt" || die "--domain ops 命中的不是 ops 领域的卡"
[ "$(eg_code search 注意力 --tag 注意力 --json)" = "0" ] || die "--tag 检索失败"
grep -Fq '"total":1' "${WORK}/out.txt" || die "--tag 过滤应只命中 1 张"
[ "$(eg_code search 注意力 --since 2026-09-03 --json)" = "0" ] || die "--since 检索失败"
grep -Fq '"total":1' "${WORK}/out.txt" || die "--since 边界（含当日）不对"
[ "$(eg_code search 注意力 --until 2026-09-01 --json)" = "0" ] || die "--until 检索失败"
grep -Fq '"total":1' "${WORK}/out.txt" || die "--until 边界（含当日）不对"
[ "$(eg_code search 完全没有的词 --json)" = "0" ] || die "零命中必须退 0"
grep -Fq '"total":0' "${WORK}/out.txt" || die "零命中的 total 应为 0"
[ "$(eg_code search "" )" = "1" ] || die "空检索词应退 1"
[ "$(eg_code search 注意力 --since 2026-9-3)" = "1" ] || die "非法日期应退 1"
[ "$(eg_code search 注意力 --since 2026-09-05 --until 2026-09-01)" = "1" ] || die "区间倒置应退 1"
[ "$(eg_code search 注意力 --domain 未登记)" = "1" ] || die "领域未登记应退 1"
ok "四类过滤生效；零命中退 0，四类参数非法退 1（无 2 / 3 / 4）"

# ---------------------------------------------------------------- 7. 零副作用复核
step "只读零副作用复核：git status / git log / 文件字节与 mtime 逐一相等（合同 §6）"
[ "${BEFORE_STATUS}" = "$(git -C "${VAULT}" status --porcelain)" ] || die "git status 前后不同"
[ "${BEFORE_LOG}" = "$(git -C "${VAULT}" log --oneline | wc -l)" ] || die "git log 条数变了"
snapshot "${WORK}/after.txt"
diff -u "${WORK}/before.txt" "${WORK}/after.txt" >"${WORK}/diff.txt" ||
  { cat "${WORK}/diff.txt"; die "vault 内 .md 的字节 / mtime / sha256 发生变化"; }
ok "零文件变化、零 commit（含 sha256 逐文件比对）"

printf '\n=== search.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

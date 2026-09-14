#!/usr/bin/env bash
# `[已删除]` / `[未过目]` 两个显著标记与双标记顺序的端到端脚本
# （T-evergreen.s1_main_flow-158614-043 的 verify.run 逐字调用本文件）。
#
# 判据来源：提案与状态合同 §6.2 标记表与「双标记口径 + 本合同新定的标记顺序」、
# §5.1 四象限真值表（默认检索 / 参与收敛 / 关系端点默认展示 / 综述取材 / 可显式查看）；
# M2 查询合同「同一语料两次执行输出逐字相同」的冻结口径。
#
# 四组断言：
#   A. 两个标记出现：`[已删除]` ← deleted_at 有值、`[未过目]` ← 缺 reviewed_at，
#      且 --json 的 deleted / unreviewed 两个布尔与文本标记一一对应。
#   B. 双标记顺序：deprecated + 已删除 + 未过目的卡，显式查看的标题行**逐字**以
#      `[失效][已删除][未过目]` 开头（字符串前缀比对，不是集合比对）。
#   C. 默认视图不返回已删除项：默认检索的 hits[] 里 deleted == true 的条数为 0；
#      显式开关 --include-deleted 才带回并标 `[已删除]`；显式查看已删除卡退 0。
#   D. 确定性：同一语料连续两次执行同一查询，两次输出 diff 为空。
#
# 越界反证（本层一律不做）：只读只渲染——零文件变化、零 commit；不写任何 frontmatter 键
# （deleted_at 归 T-…-041、reviewed_at 归 T-…-042）；S3 的第四个标记不判定不输出。
#
# 约束：离线、可重复执行、依赖仅 bash / coreutils / git / go / jq；
# 任何一条断言不成立立刻非零退出；全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/lifecycle/markers.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-markers.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/ai-infra/knowledge'
LIVE='k-20260901-live'
NEW='k-20260902-new'
DEL='k-20260903-del'
BOTH='k-20260904-both'

STEP=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }
gitv() { git -C "${VAULT}" "$@"; }
porcelain_count() { gitv status --porcelain | wc -l | tr -d ' '; }
logcount() { gitv log --oneline | wc -l | tr -d ' '; }
# titleline 取人类可读输出里那张卡的标题行（标记前缀就在这一行行首）。
titleline() { grep -m1 -- "$1  " "${WORK}/out.txt" || true; }

# seed_card 写一张卡：id / title / status / updated_at / reviewed_at / deleted_at / relations。
# reviewed_at 传空即「该键缺省」＝从未过目；deleted_at 传空即未删除。三个维度各自独立控制。
seed_card() {
  local id="$1" title="$2" status="$3" updated="$4" reviewed="$5" deleted="$6" relations="$7"
  {
    printf '%s\n' '---' "id: ${id}" "status: ${status}" "created_at: '2026-09-01'" \
      "updated_at: '${updated}'"
    [ -n "${reviewed}" ] && printf "reviewed_at: '%s'\n" "${reviewed}"
    if [ -n "${deleted}" ]; then
      printf "deleted_at: '%s'\n" "${deleted}"
      printf 'deleted_reason: %s\n' '用户判断这条内容不该再出现在当前知识库里'
    fi
    printf '%s\n' "title: ${title}" 'sources: []'
    [ -n "${relations}" ] && printf '%s' "${relations}"
    printf '%s\n' '---' '' '## 知识内容' '' '正文占位：注意力这个词只写在这里。' ''
  } >"${VAULT}/${KDIR_REL}/${id}.md"
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
command -v jq >/dev/null || die "本脚本用 jq 读 .data.hits[] 与 .data 的布尔字段"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed 四象限语料
step "seed 语料：§5.1 四象限各一张卡（三个维度在语料层就互不耦合）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}"

# ① active + 未删除 + 已过目：第一象限（reviewed_at == updated_at，严格大于才算未过目）。
#    三条正向关系分别指向另外三张卡，用来验「关系端点靠有效性过滤、记录不动」。
seed_card "${LIVE}" '在册的卡' active '2026-09-01T10:00:00+08:00' '2026-09-01T10:00:00+08:00' '' \
  "relations:
  - type: supports
    target: ${NEW}
    reason: 支持没过目的那张卡
  - type: limits
    target: ${DEL}
    reason: 限制已删除的那张卡
  - type: derives
    target: ${BOTH}
    reason: 由双标记的那张卡推出
"
# ② active + 未删除 + **缺 reviewed_at**：从未过目 → [未过目]。
seed_card "${NEW}" '没过目的卡' active '2026-09-02T10:00:00+08:00' '' '' ''
# ③ active + 已删除 + 已过目：第三象限 → 仅 [已删除]。
seed_card "${DEL}" '已删除的卡' active '2026-09-03T10:00:00+08:00' '2026-09-03T10:00:00+08:00' \
  '2026-09-05T10:00:00+08:00' ''
# ④ deprecated + 已删除 + 缺 reviewed_at：第四象限叠加过目维度 → 三标记齐出。
seed_card "${BOTH}" '双标记的卡' deprecated '2026-09-04T10:00:00+08:00' '' \
  '2026-09-06T10:00:00+08:00' ''

gitv -c user.email=eg@example.com -c user.name=eg add -A
gitv -c user.email=eg@example.com -c user.name=eg commit -q -m "seed: m3 markers 语料"
[ "$(porcelain_count)" = "0" ] || die "前置条件失败：工作区应干净"
BEFORE_LOG="$(logcount)"
BEFORE_SUM="$(find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort)"
ok "四象限语料入库（4 张卡），工作区干净，commit 数 = ${BEFORE_LOG}"

# ---------------------------------------------------------------- 2. A：两个标记出现
step "A 两个标记出现：[已删除] ← deleted_at 有值；[未过目] ← 缺 reviewed_at"
[ "$(eg_code card show "${DEL}" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "已删除卡显式查看应退 0（可显式查看列 ✅）"; }
jq -e '.data.deleted == true and .data.unreviewed == false' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "已删除但已过目：应 deleted == true 且 unreviewed == false"; }
jq -e '.data.markers == ["[已删除]"]' "${WORK}/out.txt" >/dev/null ||
  die "已删除卡的 markers 应恰为 [\"[已删除]\"]"
[ "$(eg_code card show "${DEL}")" = "0" ] || die "已删除卡（文本）应退 0"
case "$(titleline "${DEL}")" in
  '[已删除]'*) ;;
  *) cat "${WORK}/out.txt"; die "文本模式标题行未以 [已删除] 开头" ;;
esac

[ "$(eg_code card show "${NEW}" --json)" = "0" ] || die "未过目卡显式查看应退 0"
jq -e '.data.unreviewed == true and .data.deleted == false' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "缺 reviewed_at 且未删除：应 unreviewed == true 且 deleted == false"; }
jq -e '.data.markers == ["[未过目]"]' "${WORK}/out.txt" >/dev/null ||
  die "未过目卡的 markers 应恰为 [\"[未过目]\"]"
[ "$(eg_code card show "${NEW}")" = "0" ] || die "未过目卡（文本）应退 0"
case "$(titleline "${NEW}")" in
  '[未过目]'*) ;;
  *) cat "${WORK}/out.txt"; die "文本模式标题行未以 [未过目] 开头" ;;
esac

# 第一象限：三个维度都不命中 → markers 为空数组，三个布尔全 false。
[ "$(eg_code card show "${LIVE}" --json)" = "0" ] || die "在册卡显式查看应退 0"
jq -e '.data.markers == [] and .data.deleted == false and .data.unreviewed == false and .data.deprecated == false' \
  "${WORK}/out.txt" >/dev/null || { cat "${WORK}/out.txt"; die "第一象限不应有任何标记"; }
# 关系端点靠有效性过滤：指向已删除卡的两条条目退出展示面，源文件 relations[] 条目数不变。
jq -e '[.data.relations_out[].target] == ["'"${NEW}"'"]' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "关系端点已删除的条目应退出默认展示"; }
[ "$(grep -c '^  - type:' "${VAULT}/${KDIR_REL}/${LIVE}.md")" = "3" ] ||
  die "源文件 relations[] 条目数被改动了（过滤只在查询层，记录不动）"
# S3 的第四个标记在 M3 不输出（按判据用 jq 反证 markers 里只可能出现三个已启用标记）。
jq -e '[.data.markers[] | select(. != "[失效]" and . != "[已删除]" and . != "[未过目]")] | length == 0' \
  "${WORK}/out.txt" >/dev/null || die "输出了 M3 未启用的标记"
ok "两个标记各自独立成立；三个布尔与文本标记一一对应；关系记录一字未动"

# ---------------------------------------------------------------- 3. B：双标记顺序
step "B 双标记顺序：deprecated + 已删除 + 未过目 → 标题行逐字以 [失效][已删除][未过目] 开头"
[ "$(eg_code card show "${BOTH}" --json)" = "0" ] || die "双标记卡显式查看应退 0"
jq -e '.data.deprecated == true and .data.deleted == true and .data.unreviewed == true' \
  "${WORK}/out.txt" >/dev/null || { cat "${WORK}/out.txt"; die "三个布尔字段应全 true"; }
jq -e '.data.markers == ["[失效]","[已删除]","[未过目]"]' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "markers 顺序应为 [失效] → [已删除] → [未过目]"; }
[ "$(eg_code card show "${BOTH}")" = "0" ] || die "双标记卡（文本）应退 0"
LINE="$(titleline "${BOTH}")"
# 逐字前缀比对（不是集合比对）：顺序本身就是判据。
case "${LINE}" in
  '[失效][已删除][未过目]'*) ;;
  *) cat "${WORK}/out.txt"; die "标题行未逐字以 [失效][已删除][未过目] 开头：${LINE}" ;;
esac
ok "双标记 + 过目标记按固定顺序逐字输出；JSON 的 markers 与文本同源同事实"

# ---------------------------------------------------------------- 4. C：默认视图不返回已删除
step "C 默认视图不返回已删除项；--include-deleted 才带回并标 [已删除]"
[ "$(eg_code search 注意力 --json)" = "0" ] || { cat "${WORK}/err.txt"; die "search 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/default.json"
jq -e '[.data.hits[] | select(.deleted == true)] | length == 0' "${WORK}/default.json" >/dev/null ||
  { cat "${WORK}/default.json"; die "默认视图返回了已删除项"; }
jq -e '[.data.hits[].id] | sort == ["'"${LIVE}"'","'"${NEW}"'"]' "${WORK}/default.json" >/dev/null ||
  { cat "${WORK}/default.json"; die "默认视图应恰返回两张未删除的卡"; }
[ "$(eg_code search 注意力)" = "0" ] || die "search（文本）应退 0"
if grep -Fq -- '[已删除]' "${WORK}/out.txt"; then die "默认视图的文本输出不该出现 [已删除]"; fi
[ "$(eg_code search 注意力 --include-deleted --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "search --include-deleted 应退 0"; }
jq -e '[.data.hits[] | select(.deleted == true) | .id] | sort == ["'"${DEL}"'","'"${BOTH}"'"]' \
  "${WORK}/out.txt" >/dev/null || { cat "${WORK}/out.txt"; die "--include-deleted 应把两张已删除卡带回"; }
[ "$(eg_code search 注意力 --include-deleted)" = "0" ] || die "search --include-deleted（文本）应退 0"
grep -Fq -- "[已删除]${DEL}" "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "显式带回的已删除项应标 [已删除]"; }
# 检索是排序路径：ADR-20 的受限信号不得渗进来（清单入口只有 eg unreviewed）。
if grep -Fq -- '[未过目]' "${WORK}/out.txt"; then die "检索输出不得携带 [未过目]（ADR-20）"; fi
ok "默认检索恰两张未删除卡；显式开关带回两张已删除卡并标 [已删除]"

# ---------------------------------------------------------------- 5. D：确定性
step "D 确定性：同一语料连续两次执行同一查询，两次输出 diff 为空"
eg search 注意力 --include-deleted >"${WORK}/run1.txt" 2>&1
eg search 注意力 --include-deleted >"${WORK}/run2.txt" 2>&1
diff -u "${WORK}/run1.txt" "${WORK}/run2.txt" >"${WORK}/diff1.txt" ||
  { cat "${WORK}/diff1.txt"; die "两次检索输出不同（M2 冻结口径：逐字相同）"; }
eg card show "${BOTH}" >"${WORK}/show1.txt" 2>&1
eg card show "${BOTH}" >"${WORK}/show2.txt" 2>&1
diff -u "${WORK}/show1.txt" "${WORK}/show2.txt" >"${WORK}/diff2.txt" ||
  { cat "${WORK}/diff2.txt"; die "两次显式查看输出不同（标记与顺序必须逐字相同）"; }
eg card show "${BOTH}" --json >"${WORK}/show1.json" 2>&1
eg card show "${BOTH}" --json >"${WORK}/show2.json" 2>&1
diff -u "${WORK}/show1.json" "${WORK}/show2.json" >"${WORK}/diff3.txt" ||
  { cat "${WORK}/diff3.txt"; die "两次 --json 输出不同"; }
ok "三组「两次执行」diff 全空（文本检索 / 文本显式查看 / JSON 显式查看）"

# ---------------------------------------------------------------- 6. 只读零副作用
step "只读零副作用：本层只渲染——零文件变化、零 commit、frontmatter 一字不改"
[ "$(porcelain_count)" = "0" ] || { gitv status --porcelain; die "工作区应干净（本任务只读）"; }
[ "$(logcount)" = "${BEFORE_LOG}" ] || die "commit 数变了：本任务不产生任何提交"
AFTER_SUM="$(find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort)"
[ "${BEFORE_SUM}" = "${AFTER_SUM}" ] || die "vault 内 .md 的 sha256 发生变化（只读被破坏）"
# 三个维度的写入都不属本层：语料里的键仍是 seed 时那些，逐字未变。
grep -Fq "deleted_at: '2026-09-05T10:00:00+08:00'" "${VAULT}/${KDIR_REL}/${DEL}.md" ||
  die "deleted_at 那一行被改动了（写入归 T-…-041）"
[ "$(grep -c '^reviewed_at:' "${VAULT}/${KDIR_REL}/${BOTH}.md")" = "0" ] ||
  die "渲染路径给缺 reviewed_at 的卡补了键（写入归 T-…-042，且缺省不回填）"
ok "零文件变化、零 commit（含 sha256 逐文件比对）；三个维度的键一字未改"

printf '\n=== markers.sh 全部通过（%d 步）===\n' "${STEP}"

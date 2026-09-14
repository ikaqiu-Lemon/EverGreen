#!/usr/bin/env bash
# `eg edit`（A-13）的端到端脚本
# （T-evergreen.s1_main_flow-158614-045 的 verify.run 逐字调用本文件）。
#
# 判据来源：授权合同 `docs/specs/2026-10-10-m3-user-authorization-contract.md`
#   §1（两条路径 + N-1 反伪造）、§2 写权限矩阵 **#12「知识内容」P-A 🔴 / P-U ✅** 与
#   **#15 / #25「用户补充」两路径同 🔴**、§3 **X1**（`eg edit` 需确认 = **否**）、
#   §5（授权不是豁免：B1–B4）、§6 **U-03**、§7.2 判定行；§9 临时裁决 **A-13**
#   （判 `eg edit` 属 M3 范围，否则完成判据「用户显式命令可改核心内容」无命令载体）。
#
# 四段断言（与 task Acceptance 的四段逐条对应）：
#   A. 正例：`eg edit … --user-request` 改「知识内容」→ 退 `0`、**内容逐字生效**、
#      恰一次 `process` commit；「用户补充」与「存疑与待验证」逐字节不变（B2）；
#   B. 缺命令行 `--user-request` → 退 `2` + `E6` + 零写入零 commit（N-1：plan 内容不能自证）；
#   C. `--section 用户补充` → **带 / 不带** `--user-request` 均退 `2` + `E6` + 字节不变（U-03）；
#   D. `content_hash` 冲突（凭据过期）→ 跳过该文件 + 退 `3` + `skipped[].kind == "file_changed"`
#      + 字节不变（B3 **不因授权豁免**）。
#
# 越界反证（本层一律不做）：不改 status / deleted_at / reviewed_at 三个正交维度中的任何一个
# （`reviewed_at` 只由 `eg mark-reviewed` 写）；不物理删除任何文件（U-01）；
# 不启用退出码 6（X1「需确认 = 否」，`eg edit` 不收确认参数）；不生成 .index/。
#
# 约束：离线、可重复执行、依赖仅 bash / coreutils / git / go / jq；
# 任何一条断言不成立立刻非零退出；全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/edit/edit.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-edit.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/ai-infra/knowledge'
K='k-20260901-attention'
CARD="${VAULT}/${KDIR_REL}/${K}.md"

USER_LINE='	这里是用户手写的缩进块，B2 要求逐字保留。'
QUEST_LINE='- 用户手记的疑点一条'

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
gitv() { git -C "${VAULT}" "$@"; }
logcount() { gitv log --oneline | wc -l | tr -d ' '; }
porcelain() { gitv status --porcelain; }
sums() { find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort; }
mdcount() { find "${VAULT}" -name '*.md' -type f | wc -l | tr -d ' '; }
# stamp_gt <新> <旧>：两个带时区 RFC3339 时刻，严格晚于则退 0（与 c2_updated_at_refresh.sh 同源口径）。
stamp_gt() {
  python3 - "$1" "$2" <<'PY_STAMP'
import sys
from datetime import datetime
def p(v):
    return datetime.fromisoformat(v.replace("Z", "+00:00"))
sys.exit(0 if p(sys.argv[1]) > p(sys.argv[2]) else 1)
PY_STAMP
}
# card_hash 与 store.ContentHash 同一口径（`sha256:<hex>`，见 m3_authorization.sh）。
card_hash() { echo "sha256:$(sha256sum "${CARD}" | cut -d' ' -f1)"; }
# 三个正交维度的反证：编辑正文一格都不许碰。
assert_dims_untouched() {
  grep -q '^status: active$' "${CARD}" || die "status 维度不得被牵连（$1）"
  if grep -q '^deleted_at:' "${CARD}"; then die "删除维度不得被牵连（$1）"; fi
  if grep -q '^deleted_reason:' "${CARD}"; then die "删除维度不得被牵连（$1）"; fi
}
# B2：两段用户所有的内容必须逐字仍在。
assert_user_sections() {
  grep -Fq -e "${USER_LINE}" "${CARD}" || die "「用户补充」必须逐字保留（$1）"
  grep -Fq -e "${QUEST_LINE}" "${CARD}" || die "「存疑与待验证」必须逐字保留（$1）"
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
command -v jq >/dev/null || die "本脚本用 jq 读 .data.report 与 .data.errors[]"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed 语料
step "seed 语料：一张五分区知识卡 + 用户自建第六分区「存疑与待验证」"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}"
{
  printf '%s\n' '---' "id: ${K}" 'status: active' "created_at: '2026-09-01'" \
    "updated_at: '2026-09-12T10:00:00+08:00'" 'title: 注意力机制的计算代价' 'sources: []' \
    'tags:' '  - s1' '---' '' \
    '## 知识内容' '' '旧的结论一行。' '' '第二段旧正文。' '' \
    '## 解释与依据' '' '旧依据。' '' \
    '## 条件与边界' '' '仅 S1。' '' \
    '## 用户补充' '' "${USER_LINE}" '' '- 用户自己的列表' '    - 未知子结构' '' \
    '## 理解自检' '' '- [ ] 能说出固定次序' '' \
    '## 存疑与待验证' '' "${QUEST_LINE}" ''
} >"${CARD}"
gitv -c user.email=eg@example.com -c user.name=eg add -A
gitv -c user.email=eg@example.com -c user.name=eg commit -q -m "seed: m3 edit 语料"
[ -z "$(porcelain)" ] || die "前置条件失败：工作区应干净"
if grep -q '^reviewed_at:' "${CARD}"; then die "前置条件失败：seed 卡不应带 reviewed_at"; fi
BASE_LOG="$(logcount)"
MD_FILES="$(mdcount)"
ok "语料入库（1 张卡 / 6 个分区），工作区干净，commit 数 = ${BASE_LOG}"

# ---------------------------------------------------------------- 2. A 正例
step "A 正例：eg edit --section 知识内容 --user-request → 退 0、内容逐字生效、恰一次 process commit"
printf '%s\n' '用户显式改写的新结论。' '' '  第二段：缩进与空行都必须逐字保留。' >"${WORK}/new-body.md"
[ "$(eg_code --json edit --target "${K}" --section '知识内容' \
  --content "${WORK}/new-body.md" --user-request)" = "0" ] ||
  { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "用户显式改核心内容应退 0"; }
jq -e '.exit_code == 0 and .ok == true' "${WORK}/out.txt" >/dev/null || die "信封应 exit_code=0 / ok=true"
jq -e '(.data.report.skipped | length) == 0' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "成功编辑不应有 skipped[]"; }
jq -e '[.data.report.cards.updated[]] | index("'"${K}"'") != null' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "编辑结果必须计入既有报告结构 cards.updated"; }
# 内容逐字生效（三行原样落盘，旧正文整段消失 —— 是替换而不是追加）。
grep -Fq '用户显式改写的新结论。' "${CARD}" || die "新正文首行必须逐字生效"
grep -Fq '  第二段：缩进与空行都必须逐字保留。' "${CARD}" || die "新正文缩进必须逐字生效"
if grep -Fq '旧的结论一行。' "${CARD}"; then die "整段替换后旧正文不应残留"; fi
if grep -Fq '第二段旧正文。' "${CARD}"; then die "整段替换后旧正文不应残留"; fi
# 只换被点名的那一段：其余分区逐字不动。
grep -Fq '旧依据。' "${CARD}" || die "「解释与依据」不得被牵连"
grep -Fq '仅 S1。' "${CARD}" || die "「条件与边界」不得被牵连"
grep -Fq -e '- [ ] 能说出固定次序' "${CARD}" || die "「理解自检」不得被牵连"
assert_user_sections "A 正例"
assert_dims_untouched "A 正例"
if grep -q '^reviewed_at:' "${CARD}"; then die "编辑正文不得写 reviewed_at（只由 eg mark-reviewed 写）"; fi
# updated_at 由 CLI 在**实际写入**时刷新（授权合同 §2.1 矩阵第 8 行 / I-…-009）：
# 必须恰一行、合法带时区 RFC3339，且严格晚于 seed 值。
[ "$(grep -c '^updated_at:' "${CARD}")" = "1" ] || die "updated_at 必须恰一行"
NOW_AT="$(sed -n 's/^updated_at: *//p' "${CARD}" | tr -d "'\"")"
stamp_gt "${NOW_AT}" '2026-09-12T10:00:00+08:00' ||
  die "updated_at 必须随本次实际写入刷新（旧 2026-09-12T10:00:00+08:00 → 实得 ${NOW_AT}）"
[ "$(logcount)" = "$((BASE_LOG + 1))" ] || die "一次成功编辑应恰产生一次 commit"
gitv log -1 --pretty=%s | grep -q '^process(' || die "commit 主题应以 process( 开头（沿用既有 verb）"
[ -z "$(porcelain)" ] || die "写入必须一并提交（工作区干净）"
[ "$(mdcount)" = "${MD_FILES}" ] || die "文件数必须不变（U-01：不物理删除、不新建文件）"
ok "正例：退 0 + 内容逐字生效 + 恰一次 process commit + 用户两段与三维度全未牵连"

# ---------------------------------------------------------------- 3. B 缺 --user-request
step "B 反伪造：缺命令行 --user-request → 退 2 + E6 + 零写入零 commit（矩阵 #12 的 P-A 🔴）"
LOG_BEFORE="$(logcount)"
SUM_BEFORE="$(sums)"
[ "$(eg_code --json edit --target "${K}" --section '知识内容' --content '偷偷改核心内容。')" = "2" ] ||
  { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "缺 --user-request 必须退 2（不是 1、不是 0）"; }
jq -e '[.data.errors[].code] | index("E6") != null' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "拒绝必须是既有编号 E6（不新增诊断码）"; }
[ "$(logcount)" = "${LOG_BEFORE}" ] || die "被拒必须零 commit"
[ -z "$(porcelain)" ] || die "被拒必须零写入"
[ "${SUM_BEFORE}" = "$(sums)" ] || die "被拒必须字节不变"
if grep -Fq '偷偷改核心内容。' "${CARD}"; then die "被拒的正文一个字节都不许落盘"; fi
ok "缺佐证：退 2 + E6 + 零写入零 commit（plan 内容不能自证授权）"

# ---------------------------------------------------------------- 4. C 用户补充永不写
step "C U-03：--section 用户补充 → 带 / 不带 --user-request 均退 2 + E6 + 字节不变"
for MODE in without-flag with-flag; do
  LOG_BEFORE="$(logcount)"
  SUM_BEFORE="$(sums)"
  if [ "${MODE}" = "with-flag" ]; then
    CODE="$(eg_code --json edit --target "${K}" --section '用户补充' \
      --content '越界写用户补充。' --user-request)"
  else
    CODE="$(eg_code --json edit --target "${K}" --section '用户补充' --content '越界写用户补充。')"
  fi
  [ "${CODE}" = "2" ] || { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "${MODE}：必须退 2"; }
  jq -e '[.data.errors[].code] | index("E6") != null' "${WORK}/out.txt" >/dev/null ||
    { cat "${WORK}/out.txt"; die "${MODE}：拒绝必须是 E6"; }
  [ "$(logcount)" = "${LOG_BEFORE}" ] || die "${MODE}：必须零 commit"
  [ "${SUM_BEFORE}" = "$(sums)" ] || die "${MODE}：必须字节不变"
  if grep -Fq '越界写用户补充。' "${CARD}"; then die "${MODE}：用户补充一个字节都不许被写"; fi
  assert_user_sections "C ${MODE}"
  ok "${MODE}：退 2 + E6 + 字节不变（矩阵 #15 / #25 两路径同 🔴）"
done

# ---------------------------------------------------------------- 5. D B3 不豁免
step "D B3 不豁免：content_hash 过期 → 跳过该文件 + 退 3 + skipped[].kind = file_changed"
LOG_BEFORE="$(logcount)"
STALE_HASH="sha256:$(printf 'stale' | sha256sum | cut -d' ' -f1)"
cat >"${WORK}/stale-plan.json" <<PLAN
{"plan_version":1,"verb":"process","domain":"ai-infra",
 "reason":"用户显式修改分区「知识内容」（凭据已过期）","requirement_ids":[],
 "base":{"${KDIR_REL}/${K}.md":"${STALE_HASH}"},
 "ops":[{"op":"edit_section","target":"${K}","section":"知识内容",
         "content":"凭据过期时这段字节不许落盘。\\n","initiator":"user"}]}
PLAN
SUM_BEFORE="$(sums)"
[ "$(eg_code --json apply --plan "${WORK}/stale-plan.json" --user-request)" = "3" ] ||
  { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "凭据过期必须退 3（授权不放宽 B3）"; }
jq -e '[.data.report.skipped[].kind] | index("file_changed") != null' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "跳过必须进报告且 kind = file_changed"; }
jq -e '[.data.report.skipped[].target] | index("'"${K}"'") != null' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "skipped[] 必须点名被跳过的目标"; }
[ "${SUM_BEFORE}" = "$(sums)" ] || die "被跳过必须字节不变"
[ "$(logcount)" = "${LOG_BEFORE}" ] || die "零写入必须零 commit"
if grep -Fq '凭据过期时这段字节不许落盘。' "${CARD}"; then die "凭据过期时不得写入任何字节"; fi
assert_user_sections "D 凭据过期"
ok "凭据过期：退 3 + skipped[].kind=file_changed + 零写入零 commit（B3 不因授权豁免）"

# ---------------------------------------------------------------- 6. 收口复核
step "收口复核：不需二次确认（不收 --confirm、不出 6）；不越界实现 A-18；无 .index/"
eg edit --help >"${WORK}/help.txt" 2>&1  # 退出码不吞：--help 合同退 0，非 0 由 set -e 直接失败
grep -Fq -- '--user-request' "${WORK}/help.txt" || die "--help 必须说明 --user-request 是必填佐证"
if grep -Fq -- '--confirm' "${WORK}/help.txt"; then die "eg edit 不需二次确认，不得收 --confirm（X1）"; fi
CONFIRM_CODE="$(eg_code edit --target "${K}" --section '知识内容' --content 'x' --user-request --confirm)"
[ "${CONFIRM_CODE}" = "1" ] || die "未知参数 --confirm 应按参数非法退 1，实际 ${CONFIRM_CODE}"
# A-18：不顺手实现归属未定的命令。
eg --help >"${WORK}/tophelp.txt" 2>&1  # 退出码不吞：--help 合同退 0，非 0 由 set -e 直接失败
for FORBIDDEN in 'eg open' 'eg tag' 'eg recap' 'eg recent' 'eg review'; do
  CMD="${FORBIDDEN#eg }"
  if grep -Eq "^${CMD}( |$)" "${WORK}/tophelp.txt"; then die "${FORBIDDEN} 归属未定（A-18），不得注册"; fi
done
# ── C2a·M6 现态重钉（合同 §16.3 runtime-reserved / §16.4 授权；保留 M5 历史事实 + 新增 M6 现态双侧锁）──
# M5 历史事实（只读复算，一格不放宽）：写命令绝不替用户建出派生索引 DB（属 S4）。
[ "$(ls -1 "${VAULT}/.index"/eg.db* 2>/dev/null | wc -l | tr -d ' ')" = "0" ] ||
  die "越界：.index/ 出现派生索引 DB（M3/S1 写命令不替用户建索引，属 S4）"
# M6 现态（新增正面锁）：.index/ 若存在，仅含 M6 事务基础设施——run.lock（普通文件）/ txn（目录）。
if [ -d "${VAULT}/.index" ]; then
  _rx="$(ls -1A "${VAULT}/.index" 2>/dev/null | grep -vxE 'run\.lock|txn' || true)"
  [ -z "${_rx}" ] || die "越界：.index/ 出现非 runtime-reserved 条目：${_rx}"
fi
# 过目维度：全程只在这里出现一次写入路径 —— eg mark-reviewed；编辑再跑一次也不刷新它。
[ "$(eg_code mark-reviewed --target "${K}")" = "0" ] || { cat "${WORK}/err.txt"; die "mark-reviewed 应退 0"; }
REVIEWED_BEFORE="$(grep '^reviewed_at:' "${CARD}")"
[ "$(eg_code edit --target "${K}" --section '条件与边界' --content '收口后的边界一行。' --user-request)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "编辑「条件与边界」应退 0（矩阵 P-U ✅）"; }
[ "$(grep '^reviewed_at:' "${CARD}")" = "${REVIEWED_BEFORE}" ] ||
  die "reviewed_at 必须逐字不变（过目维度与内容维度正交）"
grep -Fq '收口后的边界一行。' "${CARD}" || die "「条件与边界」的新正文必须逐字生效"
assert_user_sections "收口"
assert_dims_untouched "收口"
PROCESS="$(gitv log --pretty=%s | grep -c '^process(' || true)"
[ "${PROCESS}" -ge "2" ] || die "两次成功编辑至少应有 2 个 process commit，实际 ${PROCESS}"
[ "$(mdcount)" = "${MD_FILES}" ] || die "全程文件数不变（U-01）"
ok "不收 --confirm / 不出 6；A-18 未扩权；无 .index/；reviewed_at 全程由唯一写入路径掌握"

printf '\n=== edit.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

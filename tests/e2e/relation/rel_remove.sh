#!/usr/bin/env bash
# `eg rel remove` 接管 M2 占位后的端到端脚本
# （T-evergreen.s1_main_flow-158614-044 的 verify.run 逐字调用本文件）。
#
# 判据来源：本 task 的 Acceptance；owner 正式裁决
#   `docs/specs/2026-10-13-m3-prestart-adjudication.md` §7.2 —— A-24 采用 `物理移除`
#   （5 条附加约束：匹配规范化三元组的全部记录 / opposing 先按字典序归一 / 不留墓碑不建
#   RelationID / 未命中按 W10 幂等零写入零 commit / 逻辑删除实体不得级联删关系）；
#   提案与状态合同 §8.1 op #8、§8.2.2 W10；授权合同写权限矩阵 #11「删关系」P-A 🔴 / P-U ✅。
#
# 四组断言（与 task Acceptance 的四段逐条对应）：
#   A. 命中删除：恰一次 `relate` commit，匹配三元组的**全部**记录物理消失、不留墓碑，
#      其余关系与正文逐字保留；
#   B. 未命中：记 W10、退 `0`、零写入、**零 commit**（不产生空 commit）；
#   C. Agent 自动路径（plan 里写 initiator: user 但**没有**命令行 `--user-request`）：
#      退 `2`、零写入、零 commit（矩阵 #11 的 P-A 🔴 由 E6 拦下，先于 W7 类 warning）；
#   D. 重跑幂等：同一条命令连跑第二次 → 退 `0` + W10 + 零新增 commit + 字节不变。
#
# 越界反证（本层一律不做）：不物理删除任何**文件**（U-01）；不改 status / deleted_at /
# reviewed_at 三个正交维度中的任何一个；不做批量删除 / 撤销删除 / 历史回放；不引索引。
#
# 约束：离线、可重复执行、依赖仅 bash / coreutils / git / go / jq；
# 任何一条断言不成立立刻非零退出；全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/relation/rel_remove.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-rel-remove.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/ai-infra/knowledge'
A='k-20260901-attention'
B='k-20260902-rnn'

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
relcount() { grep -c '^  - type:' "${VAULT}/${KDIR_REL}/$1.md" | tr -d ' '; }
sums() { find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort; }

# seed_card 写一张卡：$1=id $2=title $3=relations 片段（空即无 relations 键）。
seed_card() {
  local id="$1" title="$2" relations="$3"
  {
    printf '%s\n' '---' "id: ${id}" 'status: active' "created_at: '2026-09-01'" \
      "updated_at: '2026-09-12T10:00:00+08:00'" "title: ${title}" 'sources: []'
    [ -n "${relations}" ] && printf 'relations:\n%s' "${relations}"
    printf '%s\n' '---' '' '## 知识内容' '' '用户手写的正文段落，B2 要求逐字保留。' ''
  } >"${VAULT}/${KDIR_REL}/${id}.md"
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
command -v jq >/dev/null || die "本脚本用 jq 读 .data.plan 与 warnings[]"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed 语料
step "seed 语料：A 卡带三条正向关系（同三元组 limits 两条 + 一条 supports 不该被牵连）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}"
seed_card "${A}" '注意力机制的计算代价' \
"  - type: limits
    target: ${B}
    reason: 该限定已不成立
  - type: limits
    target: ${B}
    reason: 历史遗留的重复条目（同三元组第二条）
  - type: supports
    target: ${B}
    reason: 这条关系不该被牵连
"
seed_card "${B}" 'RNN 的长序列表现' ''
gitv -c user.email=eg@example.com -c user.name=eg add -A
gitv -c user.email=eg@example.com -c user.name=eg commit -q -m "seed: m3 rel remove 语料"
[ -z "$(porcelain)" ] || die "前置条件失败：工作区应干净"
[ "$(relcount "${A}")" = "3" ] || die "前置条件失败：A 卡应有 3 条关系"
BASE_LOG="$(logcount)"
MD_FILES="$(find "${VAULT}" -name '*.md' -type f | wc -l | tr -d ' ')"
ok "语料入库（2 张卡 / 3 条关系），工作区干净，commit 数 = ${BASE_LOG}"

# ---------------------------------------------------------------- 2. A 命中删除
step "A 命中删除：恰一次 relate commit；匹配三元组的全部记录物理消失、不留墓碑"
[ "$(eg_code --json rel remove "${A}" limits "${B}" --reason '该限定已不成立')" = "0" ] ||
  { cat "${WORK}/err.txt"; die "命中删除应退 0"; }
jq -e '.exit_code == 0 and .ok == true' "${WORK}/out.txt" >/dev/null || die "信封应 exit_code=0 / ok=true"
jq -e '.data.plan.verb == "relate"' "${WORK}/out.txt" >/dev/null || die "plan.verb 应为 relate"
jq -e '[.data.plan.ops[].op] == ["remove_relation"]' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "plan.ops 应恰一条 remove_relation"; }
jq -e '.data.plan.ops[0].initiator == "user"' "${WORK}/out.txt" >/dev/null ||
  die "命令行路径必须带 initiator=user（矩阵 #11 的 P-U）"
[ "$(logcount)" = "$((BASE_LOG + 1))" ] || die "一次成功删除应恰产生一次 commit"
gitv log -1 --pretty=%s | grep -q '^relate(' || die "commit 主题应以 relate( 开头"
[ -z "$(porcelain)" ] || die "写入必须一并提交（工作区干净）"
[ "$(relcount "${A}")" = "1" ] || die "3 条应降到 1 条（同三元组两条一起移除）"
CARD_A="${VAULT}/${KDIR_REL}/${A}.md"
if grep -Fq '该限定已不成立' "${CARD_A}"; then die "被移除条目的字节必须彻底消失"; fi
if grep -Fq '历史遗留的重复条目' "${CARD_A}"; then die "匹配的全部记录都应移除，不是只删首条"; fi
grep -Fq '这条关系不该被牵连' "${CARD_A}" || die "其他三元组不得被牵连"
for BAN in removed_at removed_by relation_id RelationID 已移除; do
  if grep -Fq "${BAN}" "${CARD_A}"; then die "不得留墓碑 / 新主键（命中 ${BAN}）"; fi
done
grep -Fq '用户手写的正文段落，B2 要求逐字保留。' "${CARD_A}" || die "正文必须逐字保留（B2）"
# Git diff 可见（物理移除的直接证据）：上一次 commit 里有 relations 行被删除。
gitv show --unified=0 HEAD -- "${KDIR_REL}/${A}.md" | grep -q '^-.*该限定已不成立' ||
  { gitv show HEAD; die "Git diff 应可见被删除的关系行"; }
# **文件**一个都没少（U-01 无物理删除：删的是 frontmatter 条目，不是文件）。
[ "$(find "${VAULT}" -name '*.md' -type f | wc -l | tr -d ' ')" = "${MD_FILES}" ] ||
  die "文件数必须不变（U-01：只删关系条目，不删文件）：期望 ${MD_FILES}"
ok "命中删除：退 0、恰一次 relate commit、两条匹配记录物理消失、无墓碑、正文逐字保留"

# ---------------------------------------------------------------- 3. B 未命中 W10
step "B 未命中：记 W10、退 0、零写入、零 commit（不产生空 commit）"
LOG_BEFORE="$(logcount)"
SUM_BEFORE="$(sums)"
[ "$(eg_code --json rel remove "${A}" derives "${B}" --reason '这条关系并不存在')" = "0" ] ||
  { cat "${WORK}/err.txt"; die "未命中必须退 0（W10 幂等，不影响退出码）"; }
jq -e '[.warnings[].code] | index("W10") != null' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "未命中必须记一条 W10"; }
jq -e '.exit_code == 0' "${WORK}/out.txt" >/dev/null || die "W10 不得改变退出码"
[ "$(logcount)" = "${LOG_BEFORE}" ] || die "未命中不得产生空 commit"
[ -z "$(porcelain)" ] || die "未命中必须零写入"
[ "${SUM_BEFORE}" = "$(sums)" ] || die "未命中必须字节不变"
ok "未命中：退 0 + W10 + 零写入 + 零 commit"

# ---------------------------------------------------------------- 4. C Agent 自动路径被拒
step "C Agent 自动路径：remove_relation 无命令行佐证 → 退 2、零写入、零 commit（矩阵 #11 P-A 🔴）"
LOG_BEFORE="$(logcount)"
SUM_BEFORE="$(sums)"
# base 的 content_hash 与 store.ContentHash 同一口径（`sha256:<hex>`，见 m3_authorization.sh）。
HASH_A="sha256:$(sha256sum "${VAULT}/${KDIR_REL}/${A}.md" | cut -d' ' -f1)"
cat >"${WORK}/agent-plan.json" <<PLAN
{"plan_version":1,"verb":"relate","domain":"ai-infra",
 "reason":"Agent 自己想删这条关系","requirement_ids":[],
 "base":{"${KDIR_REL}/${A}.md":"${HASH_A}"},
 "ops":[{"op":"remove_relation","from":"${A}","type":"supports","target":"${B}",
         "reason":"Agent 自己想删这条关系","initiator":"user"}]}
PLAN
# 关键：**没有** --user-request。plan 里写 initiator: user 不能自证授权（N-1 反伪造）。
[ "$(eg_code --json apply --plan "${WORK}/agent-plan.json")" = "2" ] ||
  { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "Agent 自动路径删关系必须退 2"; }
jq -e '[.data.errors[].code] | index("E6") != null' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "拒绝必须是既有编号 E6（不新增诊断码）"; }
[ "$(logcount)" = "${LOG_BEFORE}" ] || die "被拒必须零 commit"
[ -z "$(porcelain)" ] || die "被拒必须零写入"
[ "${SUM_BEFORE}" = "$(sums)" ] || die "被拒必须字节不变"
[ "$(relcount "${A}")" = "1" ] || die "被拒不得动关系条目数"
ok "Agent 自动路径：退 2 + E6 + 零写入零 commit（N-1 反伪造成立）"

# ---------------------------------------------------------------- 5. D 重跑幂等
step "D 重跑幂等：同一条命令连跑第二次 → 退 0 + W10 + 零新增 commit + 字节不变"
LOG_BEFORE="$(logcount)"
SUM_BEFORE="$(sums)"
[ "$(eg_code --json rel remove "${A}" limits "${B}" --reason '该限定已不成立')" = "0" ] ||
  { cat "${WORK}/err.txt"; die "重跑必须退 0"; }
jq -e '[.warnings[].code] | index("W10") != null' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "重跑必须记 W10（第一次已把匹配记录删完）"; }
[ "$(logcount)" = "${LOG_BEFORE}" ] || die "重跑不得产生新 commit"
[ "${SUM_BEFORE}" = "$(sums)" ] || die "重跑必须字节不变"
[ "$(relcount "${A}")" = "1" ] || die "重跑不得再动条目数"
ok "重跑幂等：退 0 + W10 + 零新增 commit + 字节不变"

# ---------------------------------------------------------------- 6. 收口复核
step "收口复核：占位零残留；三个正交维度未被牵连；无 .index/；关系记录不因删卡而消失"
eg rel --help >"${WORK}/help.txt" 2>&1  # 退出码不吞：--help 合同退 0，非 0 由 set -e 直接失败
grep -Fq 'rel remove' "${WORK}/help.txt" || die "--help 应给出 rel remove 的真实用法"
if grep -Fq '未实现' "${WORK}/help.txt"; then die "--help 不得再宣告阶段未实现"; fi
# ── C2a·M6 现态重钉（合同 §16.3 runtime-reserved / §16.4 授权；保留 M5 历史事实 + 新增 M6 现态双侧锁）──
# M5 历史事实（只读复算，一格不放宽）：写命令绝不替用户建出派生索引 DB（属 S4）。
[ "$(ls -1 "${VAULT}/.index"/eg.db* 2>/dev/null | wc -l | tr -d ' ')" = "0" ] ||
  die "越界：.index/ 出现派生索引 DB（M3/S1 写命令不替用户建索引，属 S4）"
# M6 现态（新增正面锁）：.index/ 若存在，仅含 M6 事务基础设施——run.lock（普通文件）/ txn（目录）。
if [ -d "${VAULT}/.index" ]; then
  _rx="$(ls -1A "${VAULT}/.index" 2>/dev/null | grep -vxE 'run\.lock|txn' || true)"
  [ -z "${_rx}" ] || die "越界：.index/ 出现非 runtime-reserved 条目：${_rx}"
fi
# 三个正交维度：本命令只动 relations[]，status / deleted_at / reviewed_at 一律不碰。
grep -q '^status: active$' "${CARD_A}" || die "status 维度不得被牵连"
if grep -q '^deleted_at:' "${CARD_A}"; then die "删除维度不得被牵连（deleted_at 归 eg delete）"; fi
if grep -q '^reviewed_at:' "${CARD_A}"; then die "过目维度不得被牵连（reviewed_at 归 eg mark-reviewed）"; fi
# 「关系记录不被物理删除」的正确适用面（A-24 第 5 条）：改**对端卡的状态维度**不级联清关系。
# 这里用 eg deprecate（用户显式、低风险、无需提案）取证：对端失效后记录仍在，只是端点无效——
# 逻辑删除（deleted_at）侧的同款反证在 m3_logical_delete.sh 与 store 的
# TestLogicalDelete_RelationsPreserved 里，本脚本不重复造那条链路。
[ "$(eg_code deprecate --target "${B}" --reason '该结论已被新版综述取代')" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg deprecate 应退 0（本步只借它反证不级联）"; }
[ "$(relcount "${A}")" = "1" ] || die "对端状态变更不得级联删除关系记录（A-24 第 5 条）"
grep -Fq "target: ${B}" "${CARD_A}" || die "端点失效只影响可见性，记录不动（§5.2）"
RELATE="$(gitv log --pretty=%s | grep -c '^relate(' || true)"
[ "${RELATE}" = "1" ] || die "本脚本恰一次有效删除，relate commit 应恰 1 个，实际 ${RELATE}"
ok "占位零残留；三维度未牵连；无 .index/；关系不因删卡而消失；relate commit 恰 1 个"

printf '\n=== rel_remove.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"

#!/usr/bin/env bash
# `superseded` 两个触发端到端脚本（T-evergreen.s1_main_flow-158614-035）。
#
# 判据来源：提案合同 `2026-10-10-m3-proposal-state-contract.md`
#   §3 全节（两触发原文 + 三步动作表「缺一不可」+ S-② 硬要求「先批准后重算是唯一实现顺序」）
#   §2.3 的 T3 / T4 两行、§5.3 第 8 / 9a / 9b 步、§5.4 影响面四项、§8.2.1 的 E7 行、
#   §9 的四行（触发① / 触发② / 两触发都必写 superseded_by / approve 必须重算影响面）
# 以及 `2026-10-13-m3-prestart-adjudication.md` §7 的 A-23（proposals/** 直写例外仍走 guarded store）
# 与 `2026-10-10-m3-user-authorization-contract.md` §6 的 U-09（提案**不支持部分应用**）。
#
# 三组断言（task deliverable 逐字要求）：
#   ① **部分接受链路**：新提案存在 + 原提案 status = superseded + 原提案
#      decision.superseded_by = 新提案 ID（三步缺一不可，用例逐条断言落盘字节）；
#   ② **批准前改语料致影响面变化链路**：`approve` 先重算再落盘（注入计数器反证顺序）、
#      **不执行删除**（目标卡 deleted_at 仍为空）、生成新提案、原提案 superseded，
#      报告逐字含「不执行删除」；
#   ③ **superseded_by 缺失退 2**：真实 `eg apply` 三种断链形态（为空 / 指向自己 /
#      指向不存在的提案）逐条退 2 + 报告出 E7 + vault 逐字节不变 + 零 commit。
# 另加收口反证：Git 历史里**没有** `delete(` commit（本 task 不落盘任何删除）、
# 知识数据零改动、重算确定性、`stale_reviews` 只按 source_cards 给提示。
#
# 为什么两个触发的链路用 `go test` 驱动而不是 `eg proposal approve`：
#   `eg proposal` 子命令外壳、`--confirm` 与退出码 6 属 T-…-040，本 task 只交付被它调用的
#   **能力**（approveProposal / Supersede）。脚本因此在真实 vault 上跑 CLI 能覆盖的部分
#   （E7 三例 + 只读复核 + 零副作用），链路本身交给同样在真实临时 vault 上跑的用例，
#   并逐个断言子用例名 PASS —— 不靠「测试整体绿」这种粗判据。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/lifecycle/superseded.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-superseded.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
PLAN="${WORK}/plan.json"

KDIR_REL='domains/ai-infra/knowledge'
CARD_A="k-20260901-attention"   # 被删目标卡
CARD_B="k-20260815-rnn"         # 唯一有效 support 落在被删材料上的卡
P_EMPTY='p-20260701-001'        # superseded 但 superseded_by 为空
P_SELF='p-20260701-002'         # superseded_by 指向自己
P_GHOST='p-20260701-003'        # superseded_by 指向不存在的提案
SENTINEL='SENTINEL-SUPERSEDED-BODY-MUST-NOT-LEAK'

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}";  eg_staged_cleanup; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }
gitv() { git -C "${VAULT}" "$@"; }
snapshot() { find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort >"$1"; }
commits() { gitv log --oneline | wc -l | tr -d ' '; }
# 本脚本模拟「用户在命令行上敲命令」，故一律带 --user-request：
# plan 里的 initiator: user 只有配上它才构成用户显式路径 P-U（反伪造条款 N-1）。
apply() { cat >"${PLAN}"; eg_code apply --plan "${PLAN}" --json --user-request; }
has_code() { grep -Fq "\"code\":\"$1\"" "${WORK}/out.txt"; }
unchanged() { snapshot "${WORK}/now.txt"; diff -u "$1" "${WORK}/now.txt" >"${WORK}/diff.txt" ||
  { cat "${WORK}/diff.txt"; die "$2：vault 内 .md 发生变化"; }; }
# gotest：跑指定包的指定用例，落 -v 输出供逐子用例断言。
gotest() { local pkg="$1" run="$2" outfile="$3"
  (cd "$(eg_staged_root)" && go test "${pkg}" -run "${run}" -count=1 -v </dev/null) \
    >"${outfile}" 2>&1 || { tail -30 "${outfile}"; die "${pkg} 的 ${run} 未全绿"; }; }
want_pass() { grep -Fq -- "--- PASS: $1" "$2" || { tail -30 "$2"; die "子用例 $1 未 PASS"; }; }
count0() { # $1 说明 / 其余：grep 参数
  local name="$1"; shift
  local n
  n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 语料
step "eg init 建库 + 收录一篇原文 + 两张卡 + 三个断链提案夹具（提案子命令属 T-…-040，用夹具直落）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
printf '注意力机制的正文占位，供 M3 superseded 用例使用。\n' >"${WORK}/body.txt"
[ "$(eg_code capture --url https://example.com/attention --title '注意力机制入门' \
  --body-file "${WORK}/body.txt" --reason 'T-035 语料' --domain ai-infra \
  --captured-at 2026-09-01T09:00:00+08:00 --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "capture 失败"; }
SRC="$(sed 's/.*"source_id":"\([^"]*\)".*/\1/' "${WORK}/out.txt")"
[ -n "${SRC}" ] || die "capture 未返回 source_id"

mkdir -p "${VAULT}/${KDIR_REL}" "${VAULT}/proposals"
card() { # $1 id / $2 关系目标（可空）
  {
    printf -- '---\nid: %s\ntitle: 卡 %s\nstatus: active\n' "$1" "$1"
    printf "created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n"
    if [ -n "${2:-}" ]; then
      printf 'relations:\n  - type: limits\n    target: %s\n    reason: 限定原结论的适用范围\n' "$2"
    fi
    printf -- '---\n\n## 知识内容\n\n正文占位。\n'
  } >"${VAULT}/${KDIR_REL}/$1.md"
}
card "${CARD_A}" "${CARD_B}"
card "${CARD_B}" ""
proposal() { # $1 id / $2 decision.superseded_by
  {
    printf -- '---\nid: %s\ntype: logical_delete\nstatus: superseded\n' "$1"
    printf "created_at: '2026-07-01'\n"
    printf 'targets:\n  - %s\n' "${CARD_A}"
    printf 'impact:\n  exits_default_view:\n    - %s\n  cards_losing_support: []\n' "${CARD_A}"
    printf '  affected_material_rels: 0\n  affected_relations: 1\n  stale_reviews: []\n'
    printf "decision:\n  result: 'superseded'\n  reason: '已被新提案取代'\n  superseded_by: '%s'\n" "$2"
    printf "execution:\n  status: not_started\n  attempted_at:\n  reason:\n  git_commit:\n"
    printf '  written_paths: []\n  unwritten_paths: []\n'
    printf -- '---\n\n# 提案：逻辑删除一张过时卡\n'
    for sec in 推荐修改 理由与证据 '影响的文件、领域与关系' 执行后状态 不执行的影响 替代方案 可应用内容; do
      printf '\n## %s\n\n%s\n' "${sec}" "${SENTINEL}"
    done
  } >"${VAULT}/proposals/$1.md"
}
proposal "${P_EMPTY}" ''
proposal "${P_SELF}" "${P_SELF}"
proposal "${P_GHOST}" 'p-20260701-999'
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m3 superseded 语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
BASE_COMMITS="$(commits)"
snapshot "${WORK}/before.txt"
ok "库就绪：2 张卡 + 3 个断链提案 + 原文 ${SRC}；已取基线快照（commit 数 ${BASE_COMMITS}）"

# ---------------------------------------------------------------- 2. ① 部分接受链路
step "① 触发① 部分接受：新提案存在 + 原提案 superseded + 原提案 decision.superseded_by = 新提案 ID"
gotest ./internal/proposal 'TestSuperseded_TriggerPartialAccept' "${WORK}/t1.txt"
want_pass 'TestSuperseded_TriggerPartialAccept' "${WORK}/t1.txt"
# 三步动作在实现里逐条留痕（① / ② / ③），缺一不可。
SRCF="${REPO_ROOT}/internal/proposal/superseded.go"
for mark in '① 已生成新提案' '② 原提案' '③ 原提案'; do
  grep -Fq -- "${mark}" "${SRCF}" || die "superseded.go 缺三步动作留痕：${mark}"
done
# U-09：提案**不支持部分应用** —— 全库零 partial_apply 落点。
count0 'U-09 部分应用零落点' -rn 'partial_apply\|PartialApply' "${REPO_ROOT}/internal/"
ok "① 部分接受三步动作全绿；三步留痕在册；部分应用 0 命中（U-09）"

# ---------------------------------------------------------------- 3. ② 前提变化链路
step "② 触发② 前提变化：不执行删除（目标 deleted_at 仍为空）+ 生成新提案 + 原提案 superseded"
gotest ./internal/proposal 'TestSuperseded_TriggerImpactChanged' "${WORK}/t2.txt"
want_pass 'TestSuperseded_TriggerImpactChanged' "${WORK}/t2.txt"
# 报告用语逐字：「不执行删除」是 S-② 的结论文本（§5.3 第 9a 步）。
grep -Fq '不执行删除' "${SRCF}" || die "superseded.go 必须逐字含「不执行删除」"
ok "② 前提变化链路全绿：不执行删除、新提案承载当前影响面、原提案标 superseded"

step "② 「先批准后重算」是唯一实现顺序：CLI 侧注入计数器反证重算在写盘之前"
N="$( { grep -n 'recomputeImpact\|RecomputeImpact' "${REPO_ROOT}/internal/cli/proposal.go" || true; } |
  wc -l | tr -d ' ')"
[ "${N}" -ge 1 ] || die "internal/cli/proposal.go 必须调用影响面重算（实得 ${N} 处）"
gotest ./internal/cli 'TestApprove_RecomputesImpactBeforeExecute' "${WORK}/t3.txt"
for sub in '前提成立仍必须先重算' '前提变化时不执行删除' '重算失败即零写盘'; do
  want_pass "TestApprove_RecomputesImpactBeforeExecute/${sub}" "${WORK}/t3.txt"
done
ok "② 重算调用 ${N} 处；三个子用例逐个 PASS（重算序号 < 任一写盘序号）"

# ---------------------------------------------------------------- 4. ③ superseded_by 缺失退 2
step "③ superseded_by 断链三形态：eg apply 逐条退 2 + 报告出 E7 + vault 逐字节不变 + 零 commit"
e7_case() { # $1 提案 ID / $2 说明
  local pid="$1" name="$2" c
  c="$(apply <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"E7 断链反证","base":{},
 "ops":[{"op":"delete","target":"${CARD_A}","reason":"与新卡重复","initiator":"user",
         "proposal":"${pid}"}]}
JSON
)"
  [ "${c}" = "2" ] || { cat "${WORK}/out.txt"; die "${name} 应退 2，实退 ${c}"; }
  has_code E7 || { cat "${WORK}/out.txt"; die "${name}：报告里缺 E7"; }
  grep -Fq 'superseded_by' "${WORK}/out.txt" ||
    { cat "${WORK}/out.txt"; die "${name}：报告必须指出断链的键"; }
  unchanged "${WORK}/before.txt" "${name}"
  [ "${BASE_COMMITS}" = "$(commits)" ] || die "${name} 不得产生 commit"
}
e7_case "${P_EMPTY}" 'E7：superseded 但 superseded_by 为空'
e7_case "${P_SELF}"  'E7：superseded_by 指向自己'
e7_case "${P_GHOST}" 'E7：superseded_by 指向不存在的提案'
gotest ./internal/proposal 'TestSupersededBy_Required' "${WORK}/t4.txt"
want_pass 'TestSupersededBy_Required' "${WORK}/t4.txt"
gotest ./internal/plan 'TestE7SupersededChainBroken' "${WORK}/t5.txt"
want_pass 'TestE7SupersededChainBroken' "${WORK}/t5.txt"
ok "③ 三种断链形态逐条退 2、零写入、零 commit；判定层与发码层用例同时全绿"

# ---------------------------------------------------------------- 5. 影响面重算口径
step "影响面四项：重算确定性 + stale_reviews 只按 source_cards 给提示（自动判定属 S3）"
gotest ./internal/proposal 'TestRecomputeImpact|TestImpact_' "${WORK}/t6.txt"
for name in TestRecomputeImpact_FourItems TestRecomputeImpact_Deterministic \
  TestImpact_StaleReviewsHintOnly TestRecomputeImpact_SkipsProposalsAndDeleted; do
  want_pass "${name}" "${WORK}/t6.txt"
done
count0 'stale_reviews 自动判定零落点' -rn 'stale_review_auto\|autoStale' "${REPO_ROOT}/internal/"
# S2 不依赖索引：重算只经 store 的只读口（ScanIDs / Read）。
grep -q 'ScanIDs()' "${REPO_ROOT}/internal/proposal/impact.go" ||
  die "impact.go 必须直接扫描（store.ScanIDs）"
ok "四项口径 + 确定性 + 只提示三条全绿；自动判定 0 命中；直接扫描在册"

# ---------------------------------------------------------------- 6. 收口：零删除、零知识改动
step "收口反证：Git 历史无 delete( commit、知识数据零改动、工作区干净"
N="$( { gitv log --oneline || true; } | { grep -c 'delete(' || true; } | tr -d ' ')"
[ "${N}" = "0" ] || { gitv log --oneline; die "Git 历史出现 delete( commit（${N} 个）：本 task 不落盘删除"; }
for c in "${CARD_A}" "${CARD_B}"; do
  if grep -Fq 'deleted_at' "${VAULT}/${KDIR_REL}/${c}.md"; then
    die "${c} 出现 deleted_at：本 task 全程不执行删除"
  fi
done
unchanged "${WORK}/before.txt" "收口"
[ -z "$(gitv status --porcelain)" ] || die "收口时工作区必须干净"
[ "${BASE_COMMITS}" = "$(commits)" ] || die "全程不得产生新 commit（实得 $(commits)）"
ok "零 delete( commit、零 deleted_at、vault 逐字节不变、commit 数恒 ${BASE_COMMITS}"

# ---------------------------------------------------------------- 7. 越界与纪律
step "越界反证：不发编号、不裸写文件、不引 M4–M6 能力"
count0 'internal/proposal 诊断码字面量' -rhoE '"(E|W|I)[0-9]+"' "${REPO_ROOT}/internal/proposal/"
count0 'M4–M6 能力字样' -rnE '\.index/|FTS5|sqlite|reconcile|flock' \
  "${REPO_ROOT}/internal/proposal/" "${REPO_ROOT}/internal/cli/proposal.go"
# 重钉（M4 · T-…-049，只改形态不放宽本体）：E11–E14 / W13–W20 已由对账合同 §3 发放给
# internal/reconcile，越界扫描按包分域——reconcile 外仍 0 命中（原判据一格不放宽），
# reconcile 内 M5–M6 号段（E15+ / W21+ / I2+）也仍 0 命中（新增一条，是加严）。
# C2a·M6 现态重钉（原子性合同 §16.3/§16.4/§17.2 授权）：M6 把恰 5 个新码发放给两个专属包——
#   internal/txn 恰 {E15,E16,W26,W28}、internal/mdfile 恰 {W27}。E15/E16 命中旧「reconcile 外」grep，
#   沿用 M4→reconcile 的剔除先例把 txn/mdfile 一并剔除（历史事实一格不放宽：其余包越界仍恒 0），
#   并新增两条封闭等号把 M6 两域逐字锁死（双侧：多一码/少一码都当场红，是加严不是放宽）。
# ── C2 · I-…-015 现态重钉（CLI 合同 §5 现态脚注授权；**零弱化**：base 域一格不放宽）──
# 命令层补码把恰 9 个新码 E17–E25 发放给**一个文件** internal/cli/codes.go（其余文件一律引用常量名、
# 零码字面量，已由 exit_codes_and_diagnostics.sh ⑧ 双侧锁死）。E17–E19 命中旧「reconcile/txn/mdfile 外」
# grep，沿用 M4→reconcile、M6→txn/mdfile 的同一剔除先例把该**单个文件**（不是整个 internal/cli 目录）
# 剔除 —— internal/cli 其余文件的越界仍恒 0，历史事实逐字保留；并新增一条封闭双侧等号把该文件
# 逐字锁成 {E17…E25}（多一码 / 少一码当场红，是加严不是放宽）。
count0 '越界编号（reconcile/txn/mdfile 外，codes.go 除外）' -rnE '"(E1[1-9]|W1[3-9]|I[2-9])"' \
  --exclude-dir=reconcile --exclude-dir=txn --exclude-dir=mdfile --exclude=codes.go \
  "${REPO_ROOT}/internal/"
# 命令层域封闭双侧等号：internal/cli/codes.go 恰 9 值 {E17…E25}。
grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/cli/codes.go" | tr -d '"' | sort -u >"${WORK}/codes_cmd.txt"
printf 'E17\nE18\nE19\nE20\nE21\nE22\nE23\nE24\nE25\n' | sort -u >"${WORK}/want_codes_cmd.txt"
diff -u "${WORK}/want_codes_cmd.txt" "${WORK}/codes_cmd.txt" ||
  die "internal/cli/codes.go 的命令层码集合应恰 {E17…E25}（CLI 合同 §5 现态脚注）"
count0 '越界编号（reconcile 内 M5–M6 号段 W21–W28 / W30+；A-62 只解冻 W29）' \
  -rnE '"(E1[5-9]|E[2-9][0-9]|W2[1-8]|W[3-9][0-9]|I[2-9])"' "${REPO_ROOT}/internal/reconcile/"
# M6 域封闭双侧等号：internal/txn 恰 4 值（E15 写前复核 / E16 锁忙 / W26 恢复留痕 / W28 锁重试）。
grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/txn/" | tr -d '"' | sort -u >"${WORK}/codes_m6_txn.txt"
printf 'E15\nE16\nW26\nW28\n' | sort -u >"${WORK}/want_m6_txn.txt"
diff -u "${WORK}/want_m6_txn.txt" "${WORK}/codes_m6_txn.txt" ||
  die "internal/txn 的 M6 码集合应恰 {E15,E16,W26,W28}（原子性合同 §12/§16.3）"
# M6 域封闭双侧等号：internal/mdfile 恰 1 值（W27 块级合并冲突）。
grep -rhoE '"(E|W|I|Q)[0-9]+"' "${REPO_ROOT}/internal/mdfile/" | tr -d '"' | sort -u >"${WORK}/codes_m6_mdfile.txt"
printf 'W27\n' >"${WORK}/want_m6_mdfile.txt"
diff -u "${WORK}/want_m6_mdfile.txt" "${WORK}/codes_m6_mdfile.txt" ||
  die "internal/mdfile 的 M6 码集合应恰 {W27}（块级合并合同 §7）"
count0 '块冲突预留 kind' -rn 'block_conflict\|block_hash_changed' "${REPO_ROOT}/internal/"
# A-23：提案控制面的落盘一律经 guarded store，本包无裸写文件调用。
WR="$( { grep -rnE 'os\.(WriteFile|Create|OpenFile|Rename|Remove)' \
  "${REPO_ROOT}/internal/proposal/" || true; } | { grep -v '_test\.go' || true; } |
  wc -l | tr -d ' ')"
[ "${WR}" = "0" ] || die "internal/proposal 出现裸写文件调用（${WR} 处），违反 A-23"
for port in ApplyProposalCreate ApplyProposalUpdate; do
  grep -Fq "${port}" "${REPO_ROOT}/internal/store/proposal.go" || die "store 缺提案 guarded 写口 ${port}"
  grep -Fq "${port}" "${REPO_ROOT}/internal/proposal/superseded.go" ||
    die "superseded.go 必须经 guarded 写口 ${port} 落盘"
done
(cd "$(eg_staged_root)" && go test ./internal/store -run 'TestB1WriteFormsAreExactlyThree|TestProposalWritePort' \
  -count=1 </dev/null >"${WORK}/t7.txt" 2>&1) || { tail -20 "${WORK}/t7.txt";
  die "B1 写形态 / 提案写口用例未全绿"; }
ok "零编号字面量、零裸写、零越界能力；两个 guarded 写口在册且 B1 三形态不变"

# ---------------------------------------------------------------- 8. 只读复核
step "只读复核：提案仍不进知识检索、context 契约不回归（033 / 034 / 037 不回归）"
[ "$(eg_code search 注意力 --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg search 应退 0"; }
if grep -Fq "${P_EMPTY}" "${WORK}/out.txt"; then die "search 命中提案"; fi
if grep -Fq "${SENTINEL}" "${WORK}/out.txt"; then die "search 泄漏提案正文"; fi
[ "$(eg_code context --json)" = "1" ] || die "eg context 缺 --source/--note 必须按用法错误退 1"
grep -Fq -- "--source 与 --note 二选一" "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "缺参数的用法错误文案与合同 §1.4 不一致"; }
CTX="$(eg_code context --source "${SRC}" --json)"
[ "${CTX}" = "0" ] || { cat "${WORK}/err.txt" "${WORK}/out.txt"; die "eg context --source 应退 0，实退 ${CTX}"; }
grep -Fq '"exit_code":0' "${WORK}/out.txt" || die "context 信封的 exit_code 必须为 0"
unchanged "${WORK}/before.txt" "只读复核"
[ "${BASE_COMMITS}" = "$(commits)" ] || die "只读命令不得产生 commit"
ok "提案不进检索、正文零泄漏、context 契约不回归、只读零副作用"

printf '\n=== superseded.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
